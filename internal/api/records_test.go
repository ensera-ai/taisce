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
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/google/uuid"
)

func TestRecordInspectionWorksOverHTTPWithoutCrossProjectDisclosure(t *testing.T) {
	h := newHarness(t, "api_records")
	ctx := context.Background()
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": "subject-1", "messages": []map[string]any{{"role": "user", "content": "I work at Ensera and I live in Dublin."}}}, 201, nil)
	h.form(t)
	var records pg.RecordPage
	h.do(t, http.MethodPost, "/v1/records/list", map[string]any{"data_subject_id": "subject-1", "limit": 1}, 200, &records)
	if len(records.Records) != 1 || records.Next == nil {
		t.Fatalf("missing inventory %+v", records)
	}
	id := records.Records[0].ID
	var next pg.RecordPage
	h.do(t, http.MethodPost, "/v1/records/list", map[string]any{"after": records.Next, "limit": 1}, 200, &next)
	if len(next.Records) != 1 || next.Records[0].ID <= id {
		t.Fatal("cursor did not advance")
	}
	var history pg.RecordHistoryPage
	h.do(t, http.MethodPost, "/v1/records/history", map[string]any{"id": id, "limit": 1}, 200, &history)
	if history.ID != id || history.Current.Known.From == nil || history.Previous == nil {
		t.Fatalf("missing history %+v", history)
	}
	var citation pg.Citation
	h.do(t, http.MethodPost, "/v1/citations/resolve", map[string]any{"id": id}, 200, &citation)
	if len(citation.Evidence) == 0 || !strings.HasPrefix(citation.Statement, records.Records[0].StatementPreview) {
		t.Fatal("inventory does not lead to evidence")
	}
	if err := migrate.ProvisionScope(ctx, h.pool, h.schema.String(), "p2"); err != nil {
		t.Fatal(err)
	}
	token, _, err := credential.NewStore(h.pool, string(migrate.ControlSchema)).Issue(ctx, "other-record-project", "p2")
	if err != nil {
		t.Fatal(err)
	}
	status, foreign := h.raw(t, http.MethodPost, "/v1/records/history", map[string]any{"id": id}, token)
	if status != 404 {
		t.Fatalf("foreign history: %d", status)
	}
	status, missing := h.raw(t, http.MethodPost, "/v1/records/history", map[string]any{"id": uuid.NewString()}, token)
	if status != 404 || string(missing) != string(foreign) {
		t.Fatal("record existence leaked")
	}
	status, body := h.raw(t, http.MethodPost, "/v1/records/list", map[string]any{"after": records.Next}, token)
	var empty pg.RecordPage
	if status != 200 || json.Unmarshal(body, &empty) != nil || len(empty.Records) != 0 {
		t.Fatal("foreign cursor widened project authority")
	}
	for _, tc := range []struct{ path, body string }{
		{"list", `{"limit":0}`}, {"list", `{"limit":101}`}, {"list", `{"after":{"id":"bad"}}`}, {"list", `{"data_subject_id":"  "}`},
		{"list", `{"scope":"p2"}`}, {"list", `{"limit":1,"limit":2}`},
		{"history", `{"id":"bad"}`}, {"history", `{"id":"` + id + `","limit":101}`},
		{"history", `{"id":"` + id + `","before":{"history_id":"` + id + `","known_until":"0001-01-01T00:00:00Z"}}`},
		{"history", `{"id":"` + id + `","before":{"history_id":"bad","known_until":"2020-01-01T00:00:00Z"}}`},
		{"history", `{"id":"` + id + `","extra":true}`},
	} {
		if status, _ := h.rawBody(t, http.MethodPost, "/v1/records/"+tc.path, tc.body, h.token); status != 400 {
			t.Fatalf("bad %s request: %d", tc.path, status)
		}
	}
	entries, err := pg.NewAuditStore(h.pool, h.schema).Recent(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{}
	refused := map[string]bool{}
	for _, entry := range entries {
		if entry.Operation == domain.AuditRecordList || entry.Operation == domain.AuditRecordHistory {
			if entry.Outcome == domain.OutcomeAllowed {
				allowed[entry.Operation] = true
			} else {
				refused[entry.Operation] = true
				if entry.Magnitude != 0 {
					t.Fatal("refusal magnitude")
				}
			}
		}
	}
	if len(allowed) != 2 || len(refused) != 2 {
		t.Fatal("record operations missing audit")
	}
	encoded, _ := json.Marshal(entries)
	for _, private := range []string{id, "subject-1", citation.Statement, citation.Evidence[0].Quote} {
		if strings.Contains(string(encoded), private) {
			t.Fatal("record data entered audit")
		}
	}
	h.do(t, http.MethodPost, "/v1/erasures", map[string]any{"data_subject_id": "subject-1", "reason": "test"}, 200, nil)
	status, erased := h.raw(t, http.MethodPost, "/v1/records/history", map[string]any{"id": id}, h.token)
	if status != 404 || string(erased) != string(missing) {
		t.Fatal("erased history remains identifiable")
	}
	h.do(t, http.MethodPost, "/v1/records/list", map[string]any{}, 200, &empty)
	if len(empty.Records) != 0 {
		t.Fatal("erased record remained in inventory")
	}
}

func TestRecordInspectionReportsStorageFailureWithoutDatabaseDetails(t *testing.T) {
	h := newHarness(t, "api_record_failure")
	ctx := context.Background()
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": "subject-1", "messages": []map[string]any{{"role": "user", "content": "I work at Ensera and I live in Dublin."}}}, 201, nil)
	h.form(t)
	var id string
	if err := h.pool.QueryRow(ctx, h.schema.SQL(`SELECT fact_id::text FROM {schema}.fact LIMIT 1`)).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(ctx, h.schema.SQL(`ALTER TABLE {schema}.fact_history RENAME TO temporarily_unavailable_history`)); err != nil {
		t.Fatal(err)
	}
	status, body := h.raw(t, http.MethodPost, "/v1/records/history", map[string]any{"id": id}, h.token)
	if status != 500 || strings.Contains(string(body), h.schema.String()) || strings.Contains(string(body), id) {
		t.Fatalf("history failure: %d %s", status, body)
	}
	if _, err := h.pool.Exec(ctx, h.schema.SQL(`ALTER TABLE {schema}.fact RENAME TO temporarily_unavailable_fact`)); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"list", "history"} {
		request := map[string]any{}
		if path == "history" {
			request["id"] = id
		}
		status, body := h.raw(t, http.MethodPost, "/v1/records/"+path, request, h.token)
		if status != 500 || strings.Contains(string(body), h.schema.String()) {
			t.Fatalf("storage failure: %d %s", status, body)
		}
	}
}
