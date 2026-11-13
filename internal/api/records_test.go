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
	// The entity's page is a different statement in a transaction of its own, and fails the same way:
	// no database text, and not the id the caller sent.
	entity := uuid.NewString()
	status, body = h.raw(t, http.MethodPost, "/v1/records/list", map[string]any{"entity_id": entity}, h.token)
	if status != 500 || strings.Contains(string(body), h.schema.String()) || strings.Contains(string(body), entity) {
		t.Fatalf("storage failure on an entity's page: %d %s", status, body)
	}
}

// TestTheRecordInventoryNarrowsToOneEntityOverHTTP holds the entity filter at the boundary a caller
// meets it: the field is optional and a request without it is unchanged, a request with it returns
// the facts naming that entity and no others, a malformed id is the listing's own refusal, and a
// credential for another project that names this project's entity is told nothing — the same empty
// page an id nobody has would get.
func TestTheRecordInventoryNarrowsToOneEntityOverHTTP(t *testing.T) {
	h := newHarness(t, "api_record_entity")
	ctx := context.Background()
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": "subject-1", "messages": []map[string]any{{"role": "user", "content": "I work at Ensera and I live in Dublin."}}}, 201, nil)
	h.form(t)
	var whole pg.RecordPage
	h.do(t, http.MethodPost, "/v1/records/list", map[string]any{}, 200, &whole)
	var ensera string
	if err := h.pool.QueryRow(ctx, h.schema.SQL(`SELECT entity_id::text FROM {schema}.entity WHERE scope='p1' AND normalized_name='ensera'`)).Scan(&ensera); err != nil {
		t.Fatal(err)
	}
	naming := map[string]bool{}
	for _, record := range whole.Records {
		if (record.SubjectID != nil && *record.SubjectID == ensera) || (record.ObjectID != nil && *record.ObjectID == ensera) {
			naming[record.ID] = true
		}
	}
	if len(naming) == 0 || len(naming) == len(whole.Records) {
		t.Fatalf("%d of %d records name the entity; the fixture cannot tell a filter from none", len(naming), len(whole.Records))
	}
	var about pg.RecordPage
	h.do(t, http.MethodPost, "/v1/records/list", map[string]any{"entity_id": ensera}, 200, &about)
	if len(about.Records) != len(naming) || about.Next != nil {
		t.Fatalf("the entity's page held %d records; %d name it", len(about.Records), len(naming))
	}
	for _, record := range about.Records {
		if !naming[record.ID] || record.Status != "current" {
			t.Fatalf("the entity's page held %+v, which does not name it or does not hold now", record)
		}
	}
	// Both filters, and the cursor, are still the listing's.
	var narrowed, nobody, onward pg.RecordPage
	h.do(t, http.MethodPost, "/v1/records/list", map[string]any{"entity_id": ensera, "data_subject_id": "subject-1"}, 200, &narrowed)
	h.do(t, http.MethodPost, "/v1/records/list", map[string]any{"entity_id": ensera, "data_subject_id": "subject-2"}, 200, &nobody)
	h.do(t, http.MethodPost, "/v1/records/list", map[string]any{"entity_id": ensera, "after": map[string]any{"id": about.Records[len(about.Records)-1].ID}}, 200, &onward)
	if len(narrowed.Records) != len(naming) || len(nobody.Records) != 0 || len(onward.Records) != 0 {
		t.Fatalf("narrowed to its subject %d, to another subject %d, past its last record %d", len(narrowed.Records), len(nobody.Records), len(onward.Records))
	}
	// An empty field is an absent one, as an empty data subject is.
	var unfiltered pg.RecordPage
	h.do(t, http.MethodPost, "/v1/records/list", map[string]any{"entity_id": ""}, 200, &unfiltered)
	if len(unfiltered.Records) != len(whole.Records) {
		t.Fatalf("an empty entity id listed %d records, not the project's %d", len(unfiltered.Records), len(whole.Records))
	}
	// A malformed id is the listing's refusal. One that is not a string never reaches the listing:
	// the body is refused as a body, as it is for every field.
	for body, code := range map[string]string{
		`{"entity_id":"ensera"}`:                               "invalid_record_page",
		`{"entity_id":"00000000-0000-0000-0000-000000000000"}`: "invalid_record_page",
		`{"entity_id":" "}`:                                    "invalid_record_page",
		`{"entity_id":"` + ensera + `","limit":101}`:           "invalid_record_page",
		`{"entity_id":"` + ensera + `","after":{"id":"bad"}}`:  "invalid_record_page",
		`{"entity_id":7}`:                                      "invalid_body",
	} {
		status, raw := h.rawBody(t, http.MethodPost, "/v1/records/list", body, h.token)
		var refused struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if status != 400 || json.Unmarshal(raw, &refused) != nil || refused.Error.Code != code {
			t.Fatalf("%s: %d %s, wanted 400 %s", body, status, raw, code)
		}
	}
	if err := migrate.ProvisionScope(ctx, h.pool, h.schema.String(), "p2"); err != nil {
		t.Fatal(err)
	}
	token, _, err := credential.NewStore(h.pool, string(migrate.ControlSchema)).Issue(ctx, "other-entity-project", "p2")
	if err != nil {
		t.Fatal(err)
	}
	status, foreign := h.raw(t, http.MethodPost, "/v1/records/list", map[string]any{"entity_id": ensera}, token)
	_, unknown := h.raw(t, http.MethodPost, "/v1/records/list", map[string]any{"entity_id": uuid.NewString()}, token)
	if status != 200 || string(foreign) != string(unknown) || strings.Contains(string(foreign), ensera) {
		t.Fatalf("another project's credential was answered %d %s; an id nobody has is answered %s", status, foreign, unknown)
	}
	entries, err := pg.NewAuditStore(h.pool, h.schema).Recent(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(entries)
	if strings.Contains(string(encoded), ensera) {
		t.Fatal("the entity id a caller asked about entered the audit")
	}
	h.do(t, http.MethodPost, "/v1/erasures", map[string]any{"data_subject_id": "subject-1", "reason": "test"}, 200, nil)
	var erased pg.RecordPage
	h.do(t, http.MethodPost, "/v1/records/list", map[string]any{"entity_id": ensera}, 200, &erased)
	if len(erased.Records) != 0 {
		t.Fatalf("after its only source was erased the entity's page held %d records", len(erased.Records))
	}
}
