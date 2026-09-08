// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/google/uuid"
)

func TestAssertionsRunOverHTTPWithAttributionScopedRetriesAndErasure(t *testing.T) {
	h := newHarness(t, "api_assertions")
	ctx := context.Background()
	request := pg.RecordAssertion{IdempotencyKey: uuid.NewString(), DataSubjectID: "subject-1", Subject: "I", Predicate: "works_at", Object: "Ensera", Statement: "I work at Ensera"}
	body := map[string]any{"records": []pg.RecordAssertion{request}}
	var batch pg.AssertionBatch
	h.do(t, http.MethodPost, "/v1/records/assert", body, 200, &batch)
	first := batch.Records[0]
	var citation pg.Citation
	h.do(t, http.MethodPost, "/v1/citations/resolve", map[string]any{"id": first.ID}, 200, &citation)
	if citation.Evidence[0].AuthoredBy == nil || citation.Evidence[0].ExtractorVersion != pg.CuratedExtractorVersion || citation.Statement != request.Statement {
		t.Fatal("assertion has no authored evidence")
	}
	h.do(t, http.MethodPost, "/v1/records/assert", body, 200, &batch)
	if !batch.Records[0].Replayed || batch.Records[0].ID != first.ID {
		t.Fatal("HTTP retry created new memory")
	}
	if err := migrate.ProvisionScope(ctx, h.pool, h.schema.String(), "p2"); err != nil {
		t.Fatal(err)
	}
	other, _, err := credential.NewStore(h.pool, string(migrate.ControlSchema)).Issue(ctx, "assertion-other", "p2")
	if err != nil {
		t.Fatal(err)
	}
	if status, _ := h.raw(t, http.MethodPost, "/v1/records/assert", body, other); status != 200 {
		t.Fatalf("independent project key collision: %d", status)
	}
	if status, _ := h.raw(t, http.MethodPost, "/v1/citations/resolve", map[string]any{"id": first.ID}, other); status != 404 {
		t.Fatal("foreign assertion visible")
	}
	request.Statement = "Different authored statement"
	h.do(t, http.MethodPost, "/v1/records/assert", map[string]any{"records": []pg.RecordAssertion{request}}, 409, nil)
	h.do(t, http.MethodPost, "/v1/erasures", map[string]any{"data_subject_id": "subject-1", "reason": "requested"}, 200, nil)
	h.do(t, http.MethodPost, "/v1/records/assert", body, 409, nil)
	var otherSources int
	if err := h.pool.QueryRow(ctx, h.schema.SQL(`SELECT count(*) FROM {schema}.observation WHERE scope='p2'`)).Scan(&otherSources); err != nil || otherSources != 1 {
		t.Fatal("erasure crossed project boundary")
	}
}

func TestAssertionHTTPRefusalsLeaveNoSourcesOrPrivateDiagnostics(t *testing.T) {
	h := newHarness(t, "api_assertion_refusals")
	ctx := context.Background()
	for _, body := range []string{`{}`, `{"records":[]}`, `{"records":[{"idempotency_key":"bad"}]}`, `{"records":[],"principal_id":"forged"}`, `{"records":[{"confidence":1}]}`, `{"records":[{"source_role":"user"}]}`, `{"records":[],"scope":"p2"}`, `{"records":[],"records":[]}`} {
		if status, _ := h.rawBody(t, http.MethodPost, "/v1/records/assert", body, h.token); status != 400 {
			t.Fatalf("invalid assertion accepted: %d", status)
		}
	}
	request := pg.RecordAssertion{IdempotencyKey: uuid.NewString(), DataSubjectID: "subject-1", Subject: "I", Predicate: "lives_in", Object: "Dublin", Statement: "I live in Dublin", ValidFrom: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)}
	if _, err := h.pool.Exec(ctx, h.schema.SQL(`ALTER TABLE {schema}.curated_claim ADD CONSTRAINT private_refusal CHECK(false)`)); err != nil {
		t.Fatal(err)
	}
	status, raw := h.raw(t, http.MethodPost, "/v1/records/assert", map[string]any{"records": []pg.RecordAssertion{request}}, h.token)
	if status != 500 || strings.Contains(string(raw), "private_refusal") || strings.Contains(string(raw), request.Statement) {
		t.Fatalf("private diagnostic exposed: %d", status)
	}
	if _, err := h.pool.Exec(ctx, h.schema.SQL(`ALTER TABLE {schema}.curated_claim DROP CONSTRAINT private_refusal`)); err != nil {
		t.Fatal(err)
	}
	h.do(t, http.MethodPost, "/v1/records/assert", map[string]any{"records": []pg.RecordAssertion{request}}, 200, nil)
	request.IdempotencyKey = uuid.NewString()
	request.Object = "Oslo"
	request.Statement = "I live in Oslo"
	request.ValidFrom = request.ValidFrom.AddDate(0, -1, 0)
	h.do(t, http.MethodPost, "/v1/records/assert", map[string]any{"records": []pg.RecordAssertion{request}}, 409, nil)
	request.IdempotencyKey = uuid.NewString()
	request.DataSubjectID = ""
	h.do(t, http.MethodPost, "/v1/records/assert", map[string]any{"records": []pg.RecordAssertion{request}}, 400, nil)
	var count int
	if err := h.pool.QueryRow(ctx, h.schema.SQL(`SELECT count(*) FROM {schema}.observation`)).Scan(&count); err != nil || count != 1 {
		t.Fatalf("failed assertion left sources: %d %v", count, err)
	}
}

func TestAssertionAtBacklogCapacityDoesNotReserveWorkOrSkipFreshness(t *testing.T) {
	h := newHarness(t, "api_assertion_capacity")
	ctx := context.Background()
	if _, err := h.pool.Exec(ctx, h.schema.SQL(`UPDATE {schema}.ingestion_budget SET max_pending=1,max_pending_per_project=1`)); err != nil {
		t.Fatal(err)
	}
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": "subject-1", "messages": []map[string]any{{"role": "user", "content": "Awaiting extraction"}}}, 201, nil)
	request := pg.RecordAssertion{IdempotencyKey: uuid.NewString(), DataSubjectID: "subject-1", Subject: "I", Predicate: "works_at", Object: "Ensera", Statement: "I work at Ensera"}
	h.do(t, http.MethodPost, "/v1/records/assert", map[string]any{"records": []pg.RecordAssertion{request}}, 200, nil)
	var pending, reservations int
	if err := h.pool.QueryRow(ctx, h.schema.SQL(`SELECT pending,(SELECT count(*) FROM {schema}.ingestion_reservation) FROM {schema}.ingestion_budget`)).Scan(&pending, &reservations); err != nil || pending != 1 || reservations != 1 {
		t.Fatalf("assertion spent backlog capacity: %d %d %v", pending, reservations, err)
	}
	var freshness struct {
		Stored int64  `json:"stored"`
		Formed *int64 `json:"formed"`
	}
	h.do(t, http.MethodGet, "/v1/freshness", nil, 200, &freshness)
	if freshness.Stored != 1 || freshness.Formed != nil {
		t.Fatal("assertion skipped an earlier pending observation")
	}
}
