// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api_test

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/google/uuid"
)

func entityIDForInspection(t *testing.T, h *harness) string {
	t.Helper()
	var id string
	if err := h.pool.QueryRow(context.Background(), h.schema.SQL(`SELECT entity_id::text FROM {schema}.entity ORDER BY entity_id LIMIT 1`)).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestEntityInspectionResolvesRecordEndpointsAndRefusesForeignIDs(t *testing.T) {
	h := newHarness(t, "api_entity_inspection")
	ctx := context.Background()
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": "private-owner", "messages": []map[string]any{{"role": "user", "content": "I work at Ensera and I live in Dublin."}}}, 201, nil)
	h.form(t)
	var page pg.EntityPage
	h.do(t, http.MethodPost, "/v1/entities/list", map[string]any{"limit": 1}, 200, &page)
	if len(page.Entities) != 1 || page.Next == nil {
		t.Fatalf("missing page %+v", page)
	}
	var next pg.EntityPage
	h.do(t, http.MethodPost, "/v1/entities/list", map[string]any{"limit": 1, "after": page.Next}, 200, &next)
	if len(next.Entities) != 1 || next.Entities[0].ID <= page.Entities[0].ID {
		t.Fatal("cursor did not advance")
	}
	var records pg.RecordPage
	h.do(t, http.MethodPost, "/v1/records/list", map[string]any{}, 200, &records)
	sawSpeaker := false
	for _, record := range records.Records {
		for _, id := range []*string{record.SubjectID, record.ObjectID} {
			if id == nil {
				continue
			}
			var entity pg.EntityIdentity
			h.do(t, http.MethodPost, "/v1/entities/get", map[string]any{"id": *id}, 200, &entity)
			if entity.ID != *id || entity.Name == "" || entity.Aliases == nil {
				t.Fatalf("invalid identity %+v", entity)
			}
			if entity.Kind == "speaker" {
				sawSpeaker = true
				if entity.SpeakerSubjectID == nil || *entity.SpeakerSubjectID != "private-owner" {
					t.Fatal("speaker attribution lost")
				}
			}
		}
	}
	if !sawSpeaker {
		t.Fatal("speaker identity not distinguished")
	}
	for _, body := range []map[string]any{{"limit": 0}, {"limit": 101}, {"after": map[string]any{"id": "bad"}}, {"scope": "p2"}} {
		h.do(t, http.MethodPost, "/v1/entities/list", body, 400, nil)
	}
	h.do(t, http.MethodPost, "/v1/entities/get", map[string]any{"id": "bad"}, 400, nil)
	if err := migrate.ProvisionScope(ctx, h.pool, h.schema.String(), "p2"); err != nil {
		t.Fatal(err)
	}
	token, _, err := credential.NewStore(h.pool, string(migrate.ControlSchema)).IssueWithAccess(ctx, "entity-reader", "p2", credential.ReadOnly)
	if err != nil {
		t.Fatal(err)
	}
	status, foreign := h.raw(t, http.MethodPost, "/v1/entities/get", map[string]any{"id": page.Entities[0].ID}, token)
	status2, absent := h.raw(t, http.MethodPost, "/v1/entities/get", map[string]any{"id": uuid.NewString()}, token)
	if status != 404 || status2 != 404 || !bytes.Equal(foreign, absent) {
		t.Fatalf("existence leaked %d %d %s %s", status, status2, foreign, absent)
	}
	var audit string
	if err := h.pool.QueryRow(ctx, h.schema.SQL(`SELECT coalesce(json_agg(a)::text,'[]') FROM {schema}.audit_entry a WHERE operation IN ('entity.list','entity.get')`)).Scan(&audit); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains([]byte(audit), []byte("private-owner")) || bytes.Contains([]byte(audit), []byte("Ensera")) {
		t.Fatal("identity content entered audit")
	}
	h.do(t, http.MethodPost, "/v1/erasures", map[string]any{"data_subject_id": "private-owner", "reason": "test"}, 200, nil)
	h.do(t, http.MethodPost, "/v1/entities/get", map[string]any{"id": page.Entities[0].ID}, 404, nil)
	var empty pg.EntityPage
	h.do(t, http.MethodPost, "/v1/entities/list", map[string]any{}, 200, &empty)
	if empty.Entities == nil || len(empty.Entities) != 0 {
		t.Fatalf("erased identities retained %+v", empty)
	}
}
