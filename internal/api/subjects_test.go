// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/google/uuid"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestManagedSubjectHTTPJourneyPreservesSpeakerIdentityAndKeepsReferencesOutOfRecall(t *testing.T) {
	h := newHarness(t, "api_subject_journey")
	ctx := context.Background()
	request := map[string]any{"idempotency_key": uuid.NewString(), "external_reference": "sensitive-contact@example.test", "label": "Sensitive label"}
	var first, second pg.SubjectWrite
	h.do(t, http.MethodPost, "/v1/subjects/register", request, 200, &first)
	h.do(t, http.MethodPost, "/v1/subjects/register", map[string]any{"idempotency_key": uuid.NewString(), "external_reference": "other-contact@example.test"}, 200, &second)
	former := speakerFormer(t, h)
	for _, item := range []struct{ id, text string }{{first.ID, "I live in Dublin."}, {second.ID, "I live in Amman."}} {
		source := speakerObservation(t, h, item.id, item.text, time.Now().UTC())
		if _, err := former.Form(ctx, h.schema, source); err != nil {
			t.Fatal(err)
		}
	}
	var before string
	if err := h.pool.QueryRow(ctx, h.schema.SQL(`SELECT fact_id::text FROM {schema}.fact WHERE data_subject_id=$1`), first.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	var updated pg.SubjectWrite
	h.do(t, http.MethodPost, "/v1/subjects/update", map[string]any{"id": first.ID, "expected_version": first.Version, "external_reference": "rotated-contact@example.test", "label": "Rotated label"}, 200, &updated)
	var resolved pg.ManagedSubject
	h.do(t, http.MethodPost, "/v1/subjects/get", map[string]any{"external_reference": "rotated-contact@example.test"}, 200, &resolved)
	if resolved.ID != first.ID || updated.Version == first.Version {
		t.Fatal("rotation changed identity or ignored version")
	}
	h.do(t, http.MethodPost, "/v1/subjects/get", map[string]any{"external_reference": "sensitive-contact@example.test"}, 404, nil)
	var replay pg.SubjectWrite
	h.do(t, http.MethodPost, "/v1/subjects/register", request, 200, &replay)
	if !replay.Replayed || replay.Version != updated.Version {
		t.Fatal("registration retry restored obsolete reference")
	}
	var page pg.SubjectPage
	h.do(t, http.MethodPost, "/v1/subjects/list", map[string]any{"limit": 1}, 200, &page)
	if len(page.Subjects) != 1 || page.Next == nil {
		t.Fatal("HTTP inventory did not page")
	}
	var nextPage pg.SubjectPage
	h.do(t, http.MethodPost, "/v1/subjects/list", map[string]any{"after": page.Next.ID}, 200, &nextPage)
	if len(nextPage.Subjects) != 1 || nextPage.Next != nil {
		t.Fatal("HTTP cursor omitted remaining subject")
	}

	// Registry state is authoritative bookkeeping. Missing-fact recovery uses the source's stable
	// subject ID and must neither reconstruct nor revert the separately rotated external mapping.
	if _, err := h.pool.Exec(ctx, h.schema.SQL(`DELETE FROM {schema}.fact WHERE fact_id=$1::uuid`), before); err != nil {
		t.Fatal(err)
	}
	recovery, err := pg.NewFactStore(h.pool).RecoverFacts(ctx, h.schema, "p1", "", "", uuid.NewString(), 100)
	if err != nil || recovery.Restored != 1 {
		t.Fatal("stable subject fact recovery failed")
	}
	h.do(t, http.MethodPost, "/v1/subjects/get", map[string]any{"id": first.ID}, 200, &resolved)
	if resolved.Version != updated.Version || resolved.ExternalReference != updated.ExternalReference {
		t.Fatal("fact recovery changed authoritative subject metadata")
	}
	status, wire := h.raw(t, http.MethodPost, "/v1/recalls", map[string]any{"question": "Where do I live?", "data_subject_id": resolved.ID}, h.token)
	var bundle speakerBundle
	if err := json.Unmarshal(wire, &bundle); err != nil {
		t.Fatal(err)
	}
	if status != 200 || len(bundle.Facts) != 1 || bundle.Facts[0].ID != before || bundle.Facts[0].Object != "Dublin" {
		t.Fatal("reference rotation lost speaker memory")
	}
	for _, forbidden := range []string{first.ID, second.ID, "contact@example", "Sensitive label", "Rotated label", "Amman"} {
		if bytes.Contains(wire, []byte(forbidden)) {
			t.Fatal("recall exposed protected identity or another speaker")
		}
	}
	if err := migrate.ProvisionScope(ctx, h.pool, h.schema.String(), "p2"); err != nil {
		t.Fatal(err)
	}
	foreign, _, err := credential.NewStore(h.pool, string(migrate.ControlSchema)).Issue(ctx, "subject-foreign", "p2")
	if err != nil {
		t.Fatal(err)
	}
	unknownStatus, unknown := h.raw(t, http.MethodPost, "/v1/subjects/get", map[string]any{"id": uuid.NewString()}, foreign)
	foreignStatus, foreignBody := h.raw(t, http.MethodPost, "/v1/subjects/get", map[string]any{"id": first.ID}, foreign)
	if foreignStatus != 404 || unknownStatus != 404 || !bytes.Equal(unknown, foreignBody) {
		t.Fatal("foreign registry existence disclosed")
	}
	if status, _ := h.raw(t, http.MethodPost, "/v1/subjects/get", map[string]any{"external_reference": resolved.ExternalReference}, foreign); status != 404 {
		t.Fatal("reference resolved in another project")
	}
	if status, _ := h.raw(t, http.MethodPost, "/v1/subjects/update", map[string]any{"id": first.ID, "expected_version": updated.Version}, foreign); status != 404 {
		t.Fatal("foreign subject updated")
	}
	status, exported := h.raw(t, http.MethodPost, "/v1/exports", map[string]any{"data_subject_id": first.ID}, h.token)
	if status != 200 || !bytes.Contains(exported, []byte("rotated-contact@example.test")) || bytes.Contains(exported, []byte("sensitive-contact@example.test")) || bytes.Contains(exported, []byte(second.ID)) {
		t.Fatal("export omitted current mapping or retained old/foreign references")
	}
	h.do(t, http.MethodPost, "/v1/erasures", map[string]any{"data_subject_id": first.ID, "reason": "requested"}, 200, nil)
	h.do(t, http.MethodPost, "/v1/subjects/get", map[string]any{"id": first.ID}, 404, nil)
	h.do(t, http.MethodPost, "/v1/subjects/register", request, 409, nil)
	h.do(t, http.MethodPost, "/v1/subjects/get", map[string]any{"id": second.ID}, 200, nil)
	entries, err := pg.NewAuditStore(h.pool, h.schema).Recent(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	audit, _ := json.Marshal(entries)
	for _, secret := range []string{first.ID, second.ID, "contact@example", "Sensitive label", "Rotated label"} {
		if bytes.Contains(audit, []byte(secret)) {
			t.Fatal("protected subject metadata entered permanent audit")
		}
	}
}

func TestSubjectHTTPRefusalsDoNotWriteOrExposeStorageDiagnostics(t *testing.T) {
	h := newHarness(t, "api_subject_refusals")
	ctx := context.Background()
	for _, route := range []string{"register", "get", "list", "update"} {
		for _, body := range []string{`{"unknown":1}`, `{"id":`, `{"label":"first","LABEL":"second"}`} {
			if status, _ := h.rawBody(t, http.MethodPost, "/v1/subjects/"+route, body, h.token); status != 400 {
				t.Fatal("ambiguous subject body accepted")
			}
		}
	}
	for _, body := range []map[string]any{{}, {"idempotency_key": "bad"}, {"idempotency_key": uuid.NewString(), "external_reference": strings.Repeat("x", 1025)}} {
		h.do(t, http.MethodPost, "/v1/subjects/register", body, 400, nil)
	}
	for _, body := range []map[string]any{{}, {"id": "bad"}, {"id": uuid.NewString(), "external_reference": "ref"}} {
		h.do(t, http.MethodPost, "/v1/subjects/get", body, 400, nil)
	}
	h.do(t, http.MethodPost, "/v1/subjects/list", map[string]any{"limit": 101}, 400, nil)
	h.do(t, http.MethodPost, "/v1/subjects/list", map[string]any{"after": "bad"}, 400, nil)
	h.do(t, http.MethodPost, "/v1/subjects/update", map[string]any{}, 400, nil)
	var count int
	if err := h.pool.QueryRow(ctx, h.schema.SQL(`SELECT count(*) FROM {schema}.data_subject`)).Scan(&count); err != nil || count != 0 {
		t.Fatal("refused body wrote registry")
	}
	var first pg.SubjectWrite
	request := map[string]any{"idempotency_key": uuid.NewString(), "external_reference": "private-reference"}
	h.do(t, http.MethodPost, "/v1/subjects/register", request, 200, &first)
	if _, err := h.pool.Exec(ctx, h.schema.SQL(`ALTER TABLE {schema}.audit_entry ADD CONSTRAINT private_diagnostic CHECK(operation NOT IN ('subject.register','subject.update')) NOT VALID`)); err != nil {
		t.Fatal(err)
	}
	for _, pair := range []struct {
		route string
		body  map[string]any
	}{
		{"register", map[string]any{"idempotency_key": uuid.NewString(), "external_reference": "private-new"}},
		{"update", map[string]any{"id": first.ID, "expected_version": first.Version, "label": "private-label"}},
	} {
		status, body := h.raw(t, http.MethodPost, "/v1/subjects/"+pair.route, pair.body, h.token)
		if status != 500 || bytes.Contains(body, []byte("private")) {
			t.Fatal("storage failure exposed diagnostic or reported success")
		}
	}
	var got pg.ManagedSubject
	h.do(t, http.MethodPost, "/v1/subjects/get", map[string]any{"id": first.ID}, 200, &got)
	if got.Version != first.Version || got.Label != "" {
		t.Fatal("failed update partially changed subject")
	}
	if _, err := h.pool.Exec(ctx, h.schema.SQL(`ALTER TABLE {schema}.data_subject RENAME TO unavailable_subject_storage`)); err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{"get", "list"} {
		body := map[string]any{}
		if route == "get" {
			body["id"] = first.ID
		}
		status, wire := h.raw(t, http.MethodPost, "/v1/subjects/"+route, body, h.token)
		if status != 500 || bytes.Contains(wire, []byte("unavailable_subject")) {
			t.Fatal("read storage failure exposed diagnostics")
		}
	}
}
