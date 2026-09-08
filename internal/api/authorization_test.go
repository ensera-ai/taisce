// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api_test

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"net/http"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/api"
	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
)

func TestReadOnlyCredentialsCanInspectButCannotMutateEveryMemoryRoute(t *testing.T) {
	h := newHarness(t, "api_readonly")
	ctx := context.Background()
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": "subject-1", "messages": []map[string]any{{"role": "user", "content": "I work at Ensera and I live in Dublin."}}}, 201, nil)
	h.form(t)
	var id string
	if err := h.pool.QueryRow(ctx, h.schema.SQL(`SELECT fact_id::text FROM {schema}.fact LIMIT 1`)).Scan(&id); err != nil {
		t.Fatal(err)
	}
	credentials := credential.NewStore(h.pool, string(migrate.ControlSchema))
	token, reader, err := credentials.IssueWithAccess(ctx, "read-only-client", "p1", credential.ReadOnly)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := pg.NewArtifactStore(h.pool, h.schema).Put(ctx, "p1", reader.CredentialID, pg.ArtifactPut{ID: uuid.NewString(), DataSubjectID: "artifact-owner", Kind: "file", Content: []byte("opaque")})
	if err != nil {
		t.Fatal(err)
	}
	bodies := map[string]map[string]any{
		domain.AuditArtifactPut:    {"private": "unauthorized mutation"},
		domain.AuditArtifactDelete: {"id": artifact.ID, "expected_version": artifact.Version},
		domain.AuditArtifactGet:    {"id": artifact.ID},
		domain.AuditArtifactList:   {},
		domain.AuditObserve:        {"data_subject_id": "subject-1", "messages": []map[string]any{{"role": "user", "content": "private unauthorized mutation"}}},
		domain.AuditErase:          {"data_subject_id": "subject-1", "reason": "private unauthorized mutation"},
		domain.AuditRecall:         {"question": "Where does the user live?", "data_subject_id": "subject-1"},
		domain.AuditExport:         {"data_subject_id": "subject-1"}, domain.AuditCitationResolve: {"id": id}, domain.AuditFreshness: nil,
		domain.AuditRecordList: {}, domain.AuditRecordHistory: {"id": id},
		domain.AuditRecordCorrect: {"records": []pg.RecordCorrection{{RecordMutation: pg.RecordMutation{ID: id, ExpectedVersion: id}, Object: "private unauthorized mutation", Statement: "private unauthorized mutation"}}},
		domain.AuditRecordAssert:  {"records": []map[string]any{{"statement": "private unauthorized mutation"}}},
		domain.AuditRecordRetract: {"records": []pg.RecordMutation{{ID: id, ExpectedVersion: id}}},
	}
	for _, op := range api.Operations() {
		body, ok := bodies[op.Name]
		if !ok {
			t.Fatalf("new route needs authorization coverage: %s", op.Name)
		}
		status, raw := h.raw(t, op.Method, op.Path, body, token)
		want := 200
		if op.Name == domain.AuditObserve || op.Name == domain.AuditErase || op.Name == domain.AuditRecordRetract || op.Name == domain.AuditRecordCorrect || op.Name == domain.AuditRecordAssert || op.Name == domain.AuditArtifactPut || op.Name == domain.AuditArtifactDelete {
			want = 403
		}
		if status != want {
			t.Fatalf("%s returned %d, want %d: %s", op.Name, status, want, raw)
		}
	}
	// Refusal occurs before parsing. A malformed write cannot bypass the permission gate.
	if status, _ := h.rawBody(t, http.MethodPost, "/v1/observations", `{"access":"read_write"`, token); status != 403 {
		t.Fatalf("malformed write returned %d", status)
	}
	var observations, facts int
	if err := h.pool.QueryRow(ctx, h.schema.SQL(`SELECT (SELECT count(*) FROM {schema}.observation),(SELECT count(*) FROM {schema}.fact)`)).Scan(&observations, &facts); err != nil || observations != 2 || facts == 0 {
		t.Fatalf("denied mutation changed memory: %d observations %d facts %v", observations, facts, err)
	}
	entries, err := pg.NewAuditStore(h.pool, h.schema).Recent(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	refusals := 0
	for _, e := range entries {
		if e.Principal == reader.CredentialID && e.Outcome == domain.OutcomeRefused {
			refusals++
			if e.Magnitude != 0 || e.Project != "p1" {
				t.Fatal("wrong refusal attribution")
			}
		}
	}
	if refusals != 8 {
		t.Fatalf("missing denied-operation audit: %d", refusals)
	}
	encoded, _ := json.Marshal(entries)
	if strings.Contains(string(encoded), "private unauthorized mutation") || strings.Contains(string(encoded), token) {
		t.Fatal("denied payload entered audit")
	}
	// Revocation removes read access as well as mutation authority.
	if err := credentials.Revoke(ctx, reader.CredentialID); err != nil {
		t.Fatal(err)
	}
	if status, _ := h.raw(t, http.MethodGet, "/v1/freshness", nil, token); status != 401 {
		t.Fatalf("revoked read-only key resolved: %d", status)
	}
}
