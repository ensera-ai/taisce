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

// Two sensitivity domains can share entity names and caller subject identifiers without sharing
// memory. Read-only is a mutation restriction, not a label-based content clearance.
func TestSeparateProjectsEnforceContentAccessAcrossTheMemoryLifecycle(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, "api_sensitivity")
	if err := migrate.ProvisionScope(ctx, h.pool, h.schema.String(), "restricted"); err != nil {
		t.Fatal(err)
	}
	credentials := credential.NewStore(h.pool, string(migrate.ControlSchema))
	restricted, _, err := credentials.Issue(ctx, "restricted-app", "restricted")
	if err != nil {
		t.Fatal(err)
	}
	reader, _, err := credentials.IssueWithAccess(ctx, "team-reader", "p1", credential.ReadOnly)
	if err != nil {
		t.Fatal(err)
	}
	var restrictedID, restrictedVersion string
	for _, tc := range []struct{ scope, token, company string }{{"p1", h.token, "OpenCo"}, {"restricted", restricted, "RestrictedCo"}} {
		text := "Atlas works at " + tc.company
		status, raw := h.raw(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": "same-subject", "messages": []map[string]any{{"role": "user", "content": text}}}, tc.token)
		if status != 201 {
			t.Fatalf("observe: %d", status)
		}
		var receipt struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &receipt); err != nil {
			t.Fatal(err)
		}
		id, err := pg.NewFactStore(h.pool).Assert(ctx, h.schema, tc.scope, receipt.ID, domain.RoleUser, "same-subject", domain.Claim{Subject: "Atlas", Predicate: "works_at", Object: tc.company, Statement: text, Quote: text, ByteEnd: len(text), Cardinality: domain.CardinalityMany})
		if err != nil {
			t.Fatal(err)
		}
		if tc.scope == "restricted" {
			restrictedID = id
			if err := h.pool.QueryRow(ctx, h.schema.SQL(`SELECT version::text FROM {schema}.fact WHERE scope=$1 AND fact_id=$2`), tc.scope, id).Scan(&restrictedVersion); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, token := range []string{h.token, reader} {
		for _, request := range []struct {
			path string
			body map[string]any
		}{
			{"/v1/recalls", map[string]any{"question": "Atlas", "hops": 2}},
			{"/v1/records/list", map[string]any{}},
			{"/v1/exports", map[string]any{"data_subject_id": "same-subject"}},
		} {
			status, raw := h.raw(t, http.MethodPost, request.path, request.body, token)
			if status != 200 || !strings.Contains(string(raw), "OpenCo") || strings.Contains(string(raw), "RestrictedCo") || strings.Contains(string(raw), "classification") {
				t.Fatalf("content boundary at %s: status %d", request.path, status)
			}
		}
		for _, path := range []string{"/v1/citations/resolve", "/v1/records/history"} {
			status, foreign := h.raw(t, http.MethodPost, path, map[string]any{"id": restrictedID}, token)
			missingStatus, missing := h.raw(t, http.MethodPost, path, map[string]any{"id": uuid.NewString()}, token)
			if status != 404 || missingStatus != 404 || string(foreign) != string(missing) {
				t.Fatalf("foreign identifier disclosed through %s", path)
			}
		}
	}
	// A project token cannot opt itself into a different domain by adding a label or project field.
	for _, field := range []string{"classification", "clearance", "project"} {
		status, _ := h.raw(t, http.MethodPost, "/v1/recalls", map[string]any{"question": "Atlas", field: "restricted"}, h.token)
		if status != 400 {
			t.Fatalf("unsupported access selector %s accepted", field)
		}
		status, _ = h.raw(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": "same-subject", field: "restricted", "messages": []map[string]any{{"role": "user", "content": "hello"}}}, h.token)
		if status != 400 {
			t.Fatalf("unsupported write selector %s accepted", field)
		}
	}
	status, _ := h.raw(t, http.MethodPost, "/v1/records/retract", map[string]any{"records": []map[string]any{{"id": restrictedID, "expected_version": restrictedVersion}}}, h.token)
	if status != 404 {
		t.Fatalf("foreign mutation: %d", status)
	}
	status, _ = h.raw(t, http.MethodPost, "/v1/erasures", map[string]any{"data_subject_id": "same-subject", "reason": "requested"}, reader)
	if status != 403 {
		t.Fatalf("read-only erasure: %d", status)
	}
	h.do(t, http.MethodPost, "/v1/erasures", map[string]any{"data_subject_id": "same-subject", "reason": "requested"}, 200, nil)
	status, raw := h.raw(t, http.MethodPost, "/v1/citations/resolve", map[string]any{"id": restrictedID}, restricted)
	if status != 200 || !strings.Contains(string(raw), "RestrictedCo") {
		t.Fatal("erasing one domain removed another's memory")
	}
	var labels int
	if err := h.pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema=$1 AND table_name IN ('observation','projection_dependency') AND column_name='classification'`, h.schema.String()).Scan(&labels); err != nil || labels != 0 {
		t.Fatalf("inert classification metadata survived: %d %v", labels, err)
	}
}
