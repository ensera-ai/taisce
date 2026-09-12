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

// ── The entity purge ──────────────────────────────────────────────────────────────────────────
//
// An entity is an inference, and a wrong one poisons every claim anchored to it. Erasure cannot
// reach that: erasure is about a person's data, and this is about the system's own mistake. So the
// purge withdraws the node, reports what it costs BEFORE the caller confirms, and leaves the words
// alone — the turns are what a better extractor would be given a second time.
func TestAnEntityIsPurgedOnlyAfterAPreviewThatSaysWhoseClaimsGoAndTheWordsRemain(t *testing.T) {
	h := newHarness(t, "api_entity_purge")
	ctx := context.Background()
	// Two people name the same organisation, so the entity is shared and a purge reaches both.
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": "marta",
		"messages": []map[string]any{{"role": "user", "content": "I work at Ensera and I live in Dublin."}}}, 201, nil)
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": "tomas",
		"messages": []map[string]any{{"role": "user", "content": "I work at Ensera too."}}}, 201, nil)
	h.form(t)

	var target, targetName string
	if err := h.pool.QueryRow(ctx, h.schema.SQL(
		`SELECT entity_id::text, canonical_name FROM {schema}.entity WHERE normalized_name='ensera'`)).
		Scan(&target, &targetName); err != nil {
		t.Fatalf("the shared entity is the case that matters: %v", err)
	}
	before := factsAbout(t, h, target)
	if before == 0 {
		t.Fatal("nothing is anchored to the entity, so the purge would assert nothing")
	}

	// The preview: everything the confirmed call would do, and nothing committed.
	var preview pg.EntityPurge
	h.do(t, http.MethodPost, "/v1/entities/purge", map[string]any{"id": target, "reason": "wrongly resolved"}, 200, &preview)
	if !preview.Previewed {
		t.Fatal("a purge without confirm must not commit")
	}
	if preview.Removed["fact"] != before || preview.Name != targetName {
		t.Fatalf("the preview must describe the purge it would perform: %+v, %d facts stand on the entity", preview, before)
	}
	if preview.Subjects != 2 {
		t.Fatalf("both people's claims go and the preview must say so: %d subject(s)", preview.Subjects)
	}
	if preview.Observations == 0 {
		t.Fatal("the preview must say how many turns supported the claims it would remove")
	}
	if !preview.Clean() {
		t.Fatalf("the preview left a residual: %+v", preview.Residual)
	}
	if got := factsAbout(t, h, target); got != before {
		t.Fatalf("a preview changed the database: %d facts before, %d after", before, got)
	}
	var still pg.EntityIdentity
	h.do(t, http.MethodPost, "/v1/entities/get", map[string]any{"id": target}, 200, &still)

	// Confirmed: the node and its claims go, and the residual is counted in the same transaction.
	var purge pg.EntityPurge
	h.do(t, http.MethodPost, "/v1/entities/purge",
		map[string]any{"id": target, "reason": "wrongly resolved", "confirm": true}, 200, &purge)
	if purge.Previewed || !purge.Clean() || purge.Removed["entity"] != 1 || purge.Removed["fact"] != before {
		t.Fatalf("the purge did not do what its preview promised: %+v", purge)
	}
	h.do(t, http.MethodPost, "/v1/entities/get", map[string]any{"id": target}, 404, nil)
	if got := factsAbout(t, h, target); got != 0 {
		t.Fatalf("%d claims survived the purge of their entity", got)
	}

	// The words remain. A purge is not an erasure and must never be offered as one: both turns are
	// still stored, and so is the sentence that produced the entity.
	var messages int
	if err := h.pool.QueryRow(ctx, h.schema.SQL(
		`SELECT count(*) FROM {schema}.turn_message WHERE content LIKE '%Ensera%'`)).Scan(&messages); err != nil {
		t.Fatal(err)
	}
	if messages != 2 {
		t.Fatalf("the purge took the words with it: %d message(s) still mention the entity", messages)
	}
	// And the other person's unrelated knowledge is untouched: Dublin was never the purged node.
	var dublin int
	if err := h.pool.QueryRow(ctx, h.schema.SQL(
		`SELECT count(*) FROM {schema}.entity WHERE normalized_name='dublin'`)).Scan(&dublin); err != nil {
		t.Fatal(err)
	}
	if dublin != 1 {
		t.Fatal("purging one entity removed another")
	}

	// A second purge of the same id is answered as a purge of something that is not there.
	h.do(t, http.MethodPost, "/v1/entities/purge", map[string]any{"id": target, "confirm": true}, 404, nil)

	// The ledger holds the preview and the purge, and neither carries a name.
	var audit string
	if err := h.pool.QueryRow(ctx, h.schema.SQL(
		`SELECT coalesce(json_agg(a)::text,'[]') FROM {schema}.audit_entry a WHERE operation='entity.purge'`)).Scan(&audit); err != nil {
		t.Fatal(err)
	}
	if bytes.Count([]byte(audit), []byte("entity.purge")) < 2 {
		t.Fatalf("both the preview and the purge belong on the ledger: %s", audit)
	}
	if bytes.Contains([]byte(audit), []byte("Ensera")) || bytes.Contains([]byte(audit), []byte("marta")) {
		t.Fatalf("identity content entered the audit: %s", audit)
	}
}

// A confirmed purge and its ledger row commit together or not at all. With the ledger unable
// to take the row, the purge is refused and the entity and every claim on it are still there; with
// the ledger back, the purge commits exactly one row, and that row counts what the purge removed.
func TestAPurgeThatCannotBeRecordedIsNotCommitted(t *testing.T) {
	h := newHarness(t, "api_entity_purge_atomic")
	ctx := context.Background()
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": "marta",
		"messages": []map[string]any{{"role": "user", "content": "I work at Ensera."}}}, 201, nil)
	h.form(t)
	var target string
	if err := h.pool.QueryRow(ctx, h.schema.SQL(
		`SELECT entity_id::text FROM {schema}.entity WHERE normalized_name='ensera'`)).Scan(&target); err != nil {
		t.Fatal(err)
	}
	before := factsAbout(t, h, target)
	if before == 0 {
		t.Fatal("nothing is anchored to the entity, so the purge would assert nothing")
	}

	if _, err := h.pool.Exec(ctx, h.schema.SQL(`ALTER TABLE {schema}.audit_entry RENAME TO audit_entry_moved`)); err != nil {
		t.Fatal(err)
	}
	status, _ := h.raw(t, http.MethodPost, "/v1/entities/purge", map[string]any{"id": target, "confirm": true}, h.token)
	if _, err := h.pool.Exec(ctx, h.schema.SQL(`ALTER TABLE {schema}.audit_entry_moved RENAME TO audit_entry`)); err != nil {
		t.Fatal(err)
	}
	if status < 500 {
		t.Fatalf("a purge the ledger could not record answered %d", status)
	}
	h.do(t, http.MethodPost, "/v1/entities/get", map[string]any{"id": target}, 200, nil)
	if got := factsAbout(t, h, target); got != before {
		t.Fatalf("an unrecorded purge removed %d of %d claims", before-got, before)
	}

	var purge pg.EntityPurge
	h.do(t, http.MethodPost, "/v1/entities/purge", map[string]any{"id": target, "confirm": true}, 200, &purge)
	removed := 0
	for _, n := range purge.Removed {
		removed += n
	}
	rows, err := h.pool.Query(ctx, h.schema.SQL(
		`SELECT magnitude, principal_kind FROM {schema}.audit_entry WHERE operation='entity.purge'`))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var count, magnitude int
	var kind string
	for rows.Next() {
		if err := rows.Scan(&magnitude, &kind); err != nil {
			t.Fatal(err)
		}
		count++
	}
	if count != 1 || magnitude != removed || removed == 0 || kind != "credential" {
		t.Fatalf("the committed purge left %d ledger row(s), the last counting %d of %d removed rows as %q", count, magnitude, removed, kind)
	}
}

// A purge is a write, and it reaches across a project only if somebody forgot the scope.
func TestAPurgeIsRefusedToAReaderAndCannotReachAnotherProject(t *testing.T) {
	h := newHarness(t, "api_entity_purge_door")
	ctx := context.Background()
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": "marta",
		"messages": []map[string]any{{"role": "user", "content": "I work at Ensera."}}}, 201, nil)
	h.form(t)
	target := entityIDForInspection(t, h)

	reader, _, err := credential.NewStore(h.pool, string(migrate.ControlSchema)).
		IssueWithAccess(ctx, "purge-reader", "p1", credential.ReadOnly)
	if err != nil {
		t.Fatal(err)
	}
	if status, _ := h.raw(t, http.MethodPost, "/v1/entities/purge",
		map[string]any{"id": target, "confirm": true}, reader); status != http.StatusForbidden {
		t.Fatalf("a read-only credential purged an entity: %d", status)
	}

	if err := migrate.ProvisionScope(ctx, h.pool, h.schema.String(), "p2"); err != nil {
		t.Fatal(err)
	}
	stranger, _, err := credential.NewStore(h.pool, string(migrate.ControlSchema)).
		IssueWithAccess(ctx, "purge-stranger", "p2", credential.ReadWrite)
	if err != nil {
		t.Fatal(err)
	}
	foreign, foreignBody := h.raw(t, http.MethodPost, "/v1/entities/purge",
		map[string]any{"id": target, "confirm": true}, stranger)
	absent, absentBody := h.raw(t, http.MethodPost, "/v1/entities/purge",
		map[string]any{"id": uuid.NewString(), "confirm": true}, stranger)
	if foreign != 404 || absent != 404 || !bytes.Equal(foreignBody, absentBody) {
		t.Fatalf("a purge told a stranger what exists: %d %d %s %s", foreign, absent, foreignBody, absentBody)
	}
	// And it did not reach it either.
	if got := factsAbout(t, h, target); got == 0 {
		t.Fatal("a purge from another project removed claims")
	}
	// A malformed id is the same answer as an unknown one, for the same reason.
	if status, _ := h.raw(t, http.MethodPost, "/v1/entities/purge",
		map[string]any{"id": "not-an-id", "confirm": true}, h.token); status != 404 {
		t.Fatalf("a malformed id was answered differently: %d", status)
	}
}

func factsAbout(t *testing.T, h *harness, entity string) int {
	t.Helper()
	var n int
	if err := h.pool.QueryRow(context.Background(), h.schema.SQL(
		`SELECT count(*) FROM {schema}.fact WHERE subject_entity_id=$1::uuid OR object_entity_id=$1::uuid`),
		entity).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
