// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api_test

import (
	"context"
	"encoding/json"
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

func TestRetractionHTTPPreservesHistoryAndCannotBeUndoneByFormation(t *testing.T) {
	h := newHarness(t, "api_record_retraction")
	ctx := context.Background()
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": "subject-1", "messages": []map[string]any{{"role": "user", "content": "I work at Ensera."}}}, 201, nil)
	h.form(t)
	var inventory pg.RecordPage
	h.do(t, http.MethodPost, "/v1/records/list", map[string]any{}, 200, &inventory)
	if len(inventory.Records) != 1 || inventory.Records[0].Version == "" {
		t.Fatal("record lacks edit version")
	}
	record := inventory.Records[0]
	var original pg.Citation
	h.do(t, http.MethodPost, "/v1/citations/resolve", map[string]any{"id": record.ID}, 200, &original)
	request := map[string]any{"records": []pg.RecordMutation{{ID: record.ID, ExpectedVersion: record.Version}}}
	var result pg.RetractionBatch
	h.do(t, http.MethodPost, "/v1/records/retract", request, 200, &result)
	if len(result.Records) != 1 || result.Records[0].Version == record.Version {
		t.Fatal("withdrawal lacks new version")
	}
	h.do(t, http.MethodPost, "/v1/records/retract", request, 409, nil)
	var citation pg.Citation
	h.do(t, http.MethodPost, "/v1/citations/resolve", map[string]any{"id": record.ID}, 200, &citation)
	if citation.Status != "retracted" || citation.Retraction == nil || citation.Evidence[0].Quote != original.Evidence[0].Quote {
		t.Fatal("withdrawal lost retained citation")
	}
	var bundle struct {
		Facts []json.RawMessage `json:"facts"`
	}
	h.do(t, http.MethodPost, "/v1/recalls", map[string]any{"question": "Ensera"}, 200, &bundle)
	if len(bundle.Facts) != 0 {
		t.Fatal("withdrawn claim remained current")
	}
	h.do(t, http.MethodPost, "/v1/recalls", map[string]any{"question": "Ensera", "as_known_at": original.Known.From}, 200, &bundle)
	if len(bundle.Facts) != 1 {
		t.Fatal("withdrawal erased past knowledge")
	}
	vocabulary, err := pg.LoadOntology(ctx, h.pool, h.schema)
	if err != nil {
		t.Fatal(err)
	}
	former := formation.NewFormer(pg.NewObservationStore(h.pool), pg.NewFactStore(h.pool), extract.New(scriptedModel{}, vocabulary))
	replay, err := former.Form(ctx, h.schema, domain.Observation{ID: original.Evidence[0].ObservationID, Scope: "p1", DataSubjectID: "subject-1"})
	if err != nil || replay.ClaimsRetracted != 1 || replay.FactsAsserted != 0 {
		t.Fatalf("formation ignored withdrawal: %+v %v", replay, err)
	}
	entries, err := pg.NewAuditStore(h.pool, h.schema).Recent(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	allowed := 0
	for _, entry := range entries {
		if entry.Operation == domain.AuditRecordRetract && entry.Outcome == domain.OutcomeAllowed {
			allowed++
			if entry.Magnitude != 1 {
				t.Fatal("wrong audit magnitude")
			}
		}
	}
	if allowed != 1 {
		t.Fatalf("successful mutation audit count: %d", allowed)
	}
	encoded, _ := json.Marshal(entries)
	for _, private := range []string{record.ID, record.Version, "subject-1", "Ensera", original.Evidence[0].ObservationID} {
		if strings.Contains(string(encoded), private) {
			t.Fatal("record content or identity entered permanent audit")
		}
	}
	h.do(t, http.MethodPost, "/v1/erasures", map[string]any{"data_subject_id": "subject-1", "reason": "requested"}, 200, nil)
	h.do(t, http.MethodPost, "/v1/citations/resolve", map[string]any{"id": record.ID}, 404, nil)
}

func TestRetractionHTTPRejectsForeignIDsUnknownFieldsAndReaderMutations(t *testing.T) {
	h := newHarness(t, "api_retract_boundaries")
	ctx := context.Background()
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": "subject-1", "messages": []map[string]any{{"role": "user", "content": "I work at Ensera."}}}, 201, nil)
	h.form(t)
	var inventory pg.RecordPage
	h.do(t, http.MethodPost, "/v1/records/list", map[string]any{}, 200, &inventory)
	record := inventory.Records[0]
	body := map[string]any{"records": []pg.RecordMutation{{ID: record.ID, ExpectedVersion: record.Version}}}
	if err := migrate.ProvisionScope(ctx, h.pool, h.schema.String(), "p2"); err != nil {
		t.Fatal(err)
	}
	credentials := credential.NewStore(h.pool, string(migrate.ControlSchema))
	foreign, _, err := credentials.Issue(ctx, "foreign-retraction", "p2")
	if err != nil {
		t.Fatal(err)
	}
	status, foreignBody := h.raw(t, http.MethodPost, "/v1/records/retract", body, foreign)
	if status != 404 {
		t.Fatalf("foreign record: %d", status)
	}
	status, missing := h.raw(t, http.MethodPost, "/v1/records/retract", map[string]any{"records": []pg.RecordMutation{{ID: uuid.NewString(), ExpectedVersion: uuid.NewString()}}}, foreign)
	if status != 404 || string(foreignBody) != string(missing) {
		t.Fatal("record existence disclosed")
	}
	reader, _, err := credentials.IssueWithAccess(ctx, "retraction-reader", "p1", credential.ReadOnly)
	if err != nil {
		t.Fatal(err)
	}
	if status, _ := h.raw(t, http.MethodPost, "/v1/records/retract", body, reader); status != 403 {
		t.Fatal("reader mutated records")
	}
	if status, _ := h.rawBody(t, http.MethodPost, "/v1/records/retract", `{"records":`, reader); status != 403 {
		t.Fatal("permission depends on body parsing")
	}
	for _, raw := range []string{`{}`, `{"records":[]}`, `{"records":[{"id":"bad","expected_version":"bad"}]}`, `{"records":[],"principal_id":"forged"}`, `{"records":[],"scope":"p2"}`, `{"records":[],"records":[]}`} {
		if status, _ := h.rawBody(t, http.MethodPost, "/v1/records/retract", raw, h.token); status != 400 {
			t.Fatalf("bad request accepted: %d", status)
		}
	}
	if _, err := h.pool.Exec(ctx, h.schema.SQL(`ALTER TABLE {schema}.record_retraction RENAME TO temporarily_unavailable_retraction`)); err != nil {
		t.Fatal(err)
	}
	status, failed := h.raw(t, http.MethodPost, "/v1/records/retract", body, h.token)
	if status != 500 || strings.Contains(string(failed), record.ID) || strings.Contains(string(failed), h.schema.String()) {
		t.Fatalf("storage error disclosed details: %d", status)
	}
	var version string
	if err := h.pool.QueryRow(ctx, h.schema.SQL(`SELECT version::text FROM {schema}.fact WHERE fact_id=$1`), record.ID).Scan(&version); err != nil || version != record.Version {
		t.Fatalf("failure changed record: %v", err)
	}
}
