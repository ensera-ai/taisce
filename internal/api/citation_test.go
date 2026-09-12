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

func TestSavedCitationResolvesOverHTTPWithProjectPrivacyAndAudit(t *testing.T) {
	h := newHarness(t, "api_citation")
	ctx := context.Background()
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": "subject-1", "messages": []map[string]any{{"role": "user", "content": "I work at Ensera and I live in Dublin."}}}, http.StatusCreated, nil)
	h.form(t)
	var id string
	if err := h.pool.QueryRow(ctx, h.schema.SQL(`SELECT fact_id::text FROM {schema}.fact WHERE predicate='lives_in'`)).Scan(&id); err != nil {
		t.Fatal(err)
	}
	var citation pg.Citation
	h.do(t, http.MethodPost, "/v1/citations/resolve", map[string]any{"id": id}, http.StatusOK, &citation)
	if citation.ID != id || len(citation.Evidence) == 0 || citation.Evidence[0].Quote == "" {
		t.Fatalf("unresolved: %+v", citation)
	}
	if status, _ := h.raw(t, http.MethodPost, "/v1/citations/resolve", map[string]any{"id": id}, ""); status != 401 {
		t.Fatalf("anonymous: %d", status)
	}
	if err := migrate.ProvisionScope(ctx, h.pool, string(h.schema), "p2"); err != nil {
		t.Fatal(err)
	}
	token, _, err := credential.NewStore(h.pool, string(migrate.ControlSchema)).Issue(ctx, "citation-project-test", "p2")
	if err != nil {
		t.Fatal(err)
	}
	status, foreign := h.raw(t, http.MethodPost, "/v1/citations/resolve", map[string]any{"id": id}, token)
	if status != 404 {
		t.Fatalf("foreign: %d %s", status, foreign)
	}
	status, missing := h.raw(t, http.MethodPost, "/v1/citations/resolve", map[string]any{"id": uuid.NewString()}, token)
	if status != 404 || string(missing) != string(foreign) {
		t.Fatal("lookup reveals whether foreign fact exists")
	}
	for _, body := range []string{`{"id":"bad"}`, `{"id":"` + id + `","limit":0}`, `{"id":"` + id + `","scope":"p2"}`, `{"id":"` + id + `","after":{"source_observation_id":"bad"}}`, `{"id":"` + id + `","id":"` + id + `"}`} {
		if status, _ := h.rawBody(t, http.MethodPost, "/v1/citations/resolve", body, h.token); status != 400 {
			t.Fatalf("invalid request returned %d", status)
		}
	}
	if _, err := h.pool.Exec(ctx, h.schema.SQL(`UPDATE {schema}.fact_evidence SET quote='private corrupted content' WHERE fact_id=$1`), id); err != nil {
		t.Fatal(err)
	}
	status, body := h.raw(t, http.MethodPost, "/v1/citations/resolve", map[string]any{"id": id}, h.token)
	if status != 500 || strings.Contains(string(body), "private") {
		t.Fatalf("corrupt response: %d %s", status, body)
	}
	if _, err := h.pool.Exec(ctx, h.schema.SQL(`UPDATE {schema}.fact SET statement=repeat('x',65537) WHERE fact_id=$1`), id); err != nil {
		t.Fatal(err)
	}
	h.do(t, http.MethodPost, "/v1/citations/resolve", map[string]any{"id": id}, 422, nil)
	entries, err := pg.NewAuditStore(h.pool, h.schema).Recent(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	allowed, refused := 0, 0
	for _, entry := range entries {
		if entry.Operation == domain.AuditCitationResolve {
			if entry.Outcome == domain.OutcomeAllowed {
				allowed++
				if entry.Magnitude != len(citation.Evidence) {
					t.Fatal("incorrect audit magnitude")
				}
			} else {
				refused++
			}
		}
	}
	if allowed != 1 || refused < 4 {
		t.Fatalf("missing audit: %d allowed %d refused", allowed, refused)
	}
	encoded, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{id, "private corrupted content", citation.Statement, citation.Evidence[0].Quote} {
		if strings.Contains(string(encoded), private) {
			t.Fatal("citation content entered audit")
		}
	}
	if _, err := pg.NewEraser(h.pool).Erase(ctx, h.schema, "p1", "subject-1", "requested"); err != nil {
		t.Fatal(err)
	}
	status, erased := h.raw(t, http.MethodPost, "/v1/citations/resolve", map[string]any{"id": id}, h.token)
	if status != 404 || string(erased) != string(missing) {
		t.Fatal("erasure is distinguishable from unknown citation")
	}
}
