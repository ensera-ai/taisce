// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/extract"
	"github.com/ensera-ai/taisce/internal/formation"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/google/uuid"
)

// This model fails if called. The property is that authored source recovery never sends it text.
type forbiddenCuratedModel struct{ calls int }

func (m *forbiddenCuratedModel) Propose(context.Context, domain.Message, domain.Ontology) ([]extract.Proposal, error) {
	m.calls++
	return nil, errors.New("human input must not be reinterpreted")
}

func TestCorrectionHTTPReplaysAuthoredInputWithoutModelInference(t *testing.T) {
	h := newHarness(t, "api_correction")
	ctx := context.Background()
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": "subject-1", "messages": []map[string]any{{"role": "user", "content": "I work at Ensera."}}}, 201, nil)
	h.form(t)
	var page pg.RecordPage
	h.do(t, http.MethodPost, "/v1/records/list", map[string]any{}, 200, &page)
	old := page.Records[0]
	body := map[string]any{"records": []pg.RecordCorrection{{RecordMutation: pg.RecordMutation{ID: old.ID, ExpectedVersion: old.Version}, Object: "Atlas", Statement: "I work at Atlas."}}}
	var out pg.CorrectionBatch
	h.do(t, http.MethodPost, "/v1/records/correct", body, 200, &out)
	h.do(t, http.MethodPost, "/v1/records/correct", body, 409, nil)
	replacement := out.Records[0]
	var citation pg.Citation
	h.do(t, http.MethodPost, "/v1/citations/resolve", map[string]any{"id": replacement.ID}, 200, &citation)
	if citation.Statement != "I work at Atlas." || citation.Evidence[0].AuthoredBy == nil {
		t.Fatal("human provenance missing")
	}
	if _, err := h.pool.Exec(ctx, h.schema.SQL(`DELETE FROM {schema}.fact WHERE fact_id=$1`), replacement.ID); err != nil {
		t.Fatal(err)
	}
	model := &forbiddenCuratedModel{}
	former := formation.NewFormer(pg.NewObservationStore(h.pool), pg.NewFactStore(h.pool), extract.New(model, domain.Ontology{}))
	replay, err := former.Form(ctx, h.schema, domain.Observation{ID: replacement.SourceObservationID, Scope: "p1", DataSubjectID: "forged-replay-owner"})
	if err != nil || replay.FactsAsserted != 1 || model.calls != 0 {
		t.Fatalf("authored replay: %+v calls=%d %v", replay, model.calls, err)
	}
	h.do(t, http.MethodPost, "/v1/citations/resolve", map[string]any{"id": replacement.ID}, 200, &citation)
	h.do(t, http.MethodPost, "/v1/records/retract", map[string]any{"records": []pg.RecordMutation{{ID: replacement.ID, ExpectedVersion: citation.Version}}}, 200, nil)
	replay, err = former.Form(ctx, h.schema, domain.Observation{ID: replacement.SourceObservationID, Scope: "p1"})
	if err != nil || replay.ClaimsRetracted != 1 || model.calls != 0 {
		t.Fatalf("curated withdrawal: %+v calls=%d %v", replay, model.calls, err)
	}
	// Missing authoritative input must fail, never fall through to the model's interpretation.
	if _, err := h.pool.Exec(ctx, h.schema.SQL(`DELETE FROM {schema}.curated_claim WHERE source_observation_id=$1`), replacement.SourceObservationID); err != nil {
		t.Fatal(err)
	}
	if _, err := former.Form(ctx, h.schema, domain.Observation{ID: replacement.SourceObservationID, Scope: "p1"}); err == nil || model.calls != 0 {
		t.Fatal("missing human input was silently re-extracted")
	}
}

func TestCorrectionHTTPRefusesForeignReadersMalformedAndOversizedSources(t *testing.T) {
	h := newHarness(t, "api_correction_bounds")
	ctx := context.Background()
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": "subject-1", "messages": []map[string]any{{"role": "user", "content": "I work at Ensera. " + strings.Repeat("x", 256)}}}, 201, nil)
	h.form(t)
	var page pg.RecordPage
	h.do(t, http.MethodPost, "/v1/records/list", map[string]any{}, 200, &page)
	record := page.Records[0]
	correction := pg.RecordCorrection{RecordMutation: pg.RecordMutation{ID: record.ID, ExpectedVersion: record.Version}, Object: "Atlas", Statement: "I work at Atlas."}
	body := map[string]any{"records": []pg.RecordCorrection{correction}}
	if err := migrate.ProvisionScope(ctx, h.pool, h.schema.String(), "p2"); err != nil {
		t.Fatal(err)
	}
	credentials := credential.NewStore(h.pool, string(migrate.ControlSchema))
	foreign, _, err := credentials.Issue(ctx, "foreign", "p2")
	if err != nil {
		t.Fatal(err)
	}
	status, first := h.raw(t, http.MethodPost, "/v1/records/correct", body, foreign)
	if status != 404 {
		t.Fatal("foreign correction accepted")
	}
	missing := correction
	missing.ID = uuid.NewString()
	status, second := h.raw(t, http.MethodPost, "/v1/records/correct", map[string]any{"records": []pg.RecordCorrection{missing}}, foreign)
	if status != 404 || string(first) != string(second) {
		t.Fatal("foreign existence disclosed")
	}
	reader, _, err := credentials.IssueWithAccess(ctx, "reader", "p1", credential.ReadOnly)
	if err != nil {
		t.Fatal(err)
	}
	if status, _ := h.rawBody(t, http.MethodPost, "/v1/records/correct", `{"records":`, reader); status != 403 {
		t.Fatal("reader reached parser")
	}
	for _, raw := range []string{`{}`, `{"records":[]}`, `{"records":[{"id":"bad","object":"A","statement":"A"}]}`, `{"records":[],"principal_id":"forged"}`, `{"records":[],"scope":"p2"}`, `{"records":[],"records":[]}`} {
		if status, _ := h.rawBody(t, http.MethodPost, "/v1/records/correct", raw, h.token); status != 400 {
			t.Fatalf("invalid correction: %d", status)
		}
	}
	if _, err := h.pool.Exec(ctx, h.schema.SQL(`ALTER TABLE {schema}.curated_claim RENAME TO unavailable_curated`)); err != nil {
		t.Fatal(err)
	}
	status, raw := h.raw(t, http.MethodPost, "/v1/records/correct", body, h.token)
	if status != 500 || strings.Contains(string(raw), record.ID) || strings.Contains(string(raw), h.schema.String()) {
		t.Fatal("storage failure disclosed source details")
	}
	if _, err := h.pool.Exec(ctx, h.schema.SQL(`ALTER TABLE {schema}.unavailable_curated RENAME TO curated_claim`)); err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(ctx, h.schema.SQL(`INSERT INTO {schema}.fact_evidence(fact_id,source_observation_id,source_ordinal,quote,byte_start,byte_end,extractor_version,scope) SELECT e.fact_id,e.source_observation_id,0,'x',n,n+1,'fixture',e.scope FROM {schema}.fact_evidence e CROSS JOIN generate_series(30,157) n WHERE e.fact_id=$1`), record.ID); err != nil {
		t.Fatal(err)
	}
	h.do(t, http.MethodPost, "/v1/records/correct", body, 413, nil)
	var version string
	if err := h.pool.QueryRow(ctx, h.schema.SQL(`SELECT version::text FROM {schema}.fact WHERE fact_id=$1`), record.ID).Scan(&version); err != nil || version != record.Version {
		t.Fatal("failed correction changed version")
	}
}
