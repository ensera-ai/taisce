// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package formation_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/extract"
	"github.com/ensera-ai/taisce/internal/formation"
	"github.com/ensera-ai/taisce/internal/infra/inference"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
)

// A real HTTP proposer and PostgreSQL carry the configuration through formation, exact citations,
// retry refusal, recovery and governance. The deterministic response isolates this wiring from
// provider quality; the live inference corpus remains the model-quality evidence.
func TestConfiguredExtractionIdentitySurvivesRecoveryAndRefusesMixedSourceRetries(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "formation_pipeline")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		payload := `{"claims":[{"subject":"I","predicate":"works_at","object":"Ensera","statement":"I work at Ensera","quote":"I work at Ensera","polarity":"asserted","tense":"present"},{"subject":"I","predicate":"unmapped","statement":"rejected","quote":"I work at Ensera"}]}`
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": payload}}}})
	}))
	defer server.Close()
	v, err := pg.LoadVocabulary(ctx, pool, schema)
	if err != nil {
		t.Fatal(err)
	}
	obs, facts := pg.NewObservationStore(pool), pg.NewFactStore(pool)
	makeFormer := func(name string) (*formation.Former, string) {
		e := extract.NewWith(inference.NewModel(inference.Config{Endpoint: server.URL, Model: name, APIKey: "synthetic-private-key"}), v)
		return formation.NewFormer(obs, facts, e), e.PipelineVersion()
	}
	a, versionA := makeFormer("model-a")
	b, versionB := makeFormer("model-b")
	if versionA == versionB || !strings.HasPrefix(versionA, "extract/v2:") {
		t.Fatal("runtime model change not represented")
	}
	source, err := obs.Append(ctx, schema, turnOf(domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "I work at Ensera"}))
	if err != nil {
		t.Fatal(err)
	}
	report, err := a.Form(ctx, schema, source)
	if err != nil || report.FactsAsserted != 1 || report.ClaimsRejected != 1 {
		t.Fatalf("formation: %+v %v", report, err)
	}
	var id, stamp string
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT fact_id::text,extractor_version FROM {schema}.fact_evidence WHERE source_observation_id=$1`), source.ID).Scan(&id, &stamp); err != nil || stamp != versionA {
		t.Fatal("evidence stamp is not configured identity", err)
	}
	var rejectedStamp string
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT extractor_version FROM {schema}.rejected_claim WHERE source_observation_id=$1`), source.ID).Scan(&rejectedStamp); err != nil || rejectedStamp != versionA {
		t.Fatal("rejection stamp is not configured identity", err)
	}
	before := calls.Load()
	if _, err := b.Form(ctx, schema, source); !errors.Is(err, pg.ErrExtractionChanged) || calls.Load() != before {
		t.Fatal("changed retry reached inference", err)
	}
	if _, err := a.Form(ctx, schema, source); err != nil {
		t.Fatal("identical retry refused", err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`DELETE FROM {schema}.fact WHERE fact_id=$1`), id); err != nil {
		t.Fatal(err)
	}
	page, err := facts.RecoverFacts(ctx, schema, "p1", source.ID, "", uuid.NewString(), 10)
	if err != nil || page.Restored != 1 {
		t.Fatal("recover", err)
	}
	citation, err := pg.NewCitationStore(pool, schema).Resolve(ctx, "p1", id, nil, 10)
	if err != nil || len(citation.Evidence) != 1 || citation.Evidence[0].ExtractorVersion != versionA || citation.Evidence[0].Quote != "I work at Ensera" {
		t.Fatal("recovery relabeled or broke citation", err)
	}
	exported, err := pg.NewExporter(pool, schema).Export(ctx, schema, "p1", source.DataSubjectID)
	if err != nil || len(exported.Sections["source_extraction"]) != 1 {
		t.Fatal("source pin not exported", err)
	}
	fresh, err := obs.Append(ctx, schema, turnOf(domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "I work at Ensera"}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Form(ctx, schema, fresh); err != nil {
		t.Fatal("new source cannot use new model", err)
	}
	erased, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", source.DataSubjectID, "requested")
	if err != nil || erased.Deleted["source_extraction"] != 2 || erased.Residual["source_extraction"] != 0 {
		t.Fatal("erasure retained source pin", err)
	}
}
