// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func subjectFixture(t *testing.T, name string) (*pgxpool.Pool, pg.Schema, *pg.SubjectStore) {
	t.Helper()
	pool := testPool(t)
	schema := tenant(t, pool, name)
	if _, err := pool.Exec(context.Background(), schema.SQL(`INSERT INTO {schema}.project(scope,label) VALUES('p1','p1'),('p2','p2') ON CONFLICT DO NOTHING`)); err != nil {
		t.Fatal(err)
	}
	return pool, schema, pg.NewSubjectStore(pool, schema)
}
func subjectRequest() pg.SubjectRegistration {
	return pg.SubjectRegistration{IdempotencyKey: uuid.NewString(), SubjectFields: pg.SubjectFields{ExternalReference: "contact@example.test", Label: "A person"}}
}
func TestSubjectReferencesRotateWithoutChangingIdentityAndEraseWithTheirRetries(t *testing.T) {
	ctx := context.Background()
	pool, schema, store := subjectFixture(t, "subject_lifecycle")
	r := subjectRequest()
	actor := uuid.NewString()
	first, err := store.Register(ctx, "p1", actor, r)
	if err != nil {
		t.Fatal(err)
	}
	if first.Replayed || first.ID == r.IdempotencyKey || first.Version == "" || first.UpdatedBy != actor || first.InactiveAfter != nil {
		t.Fatal("invalid registration receipt")
	}
	if _, err := uuid.Parse(first.ID); err != nil {
		t.Fatal("subject identity is not random UUID")
	}
	resolved, err := store.Get(ctx, "p1", "", r.ExternalReference)
	if err != nil || resolved.ID != first.ID {
		t.Fatal("reference not resolved")
	}
	for _, ref := range []string{"CONTACT@example.test", " contact@example.test", "other"} {
		if _, err := store.Get(ctx, "p1", "", ref); !errors.Is(err, pg.ErrSubjectNotFound) {
			t.Fatal("reference was normalized or broadened")
		}
	}
	if _, err := store.Get(ctx, "p2", first.ID, ""); !errors.Is(err, pg.ErrSubjectNotFound) {
		t.Fatal("subject crossed project")
	}
	second, err := store.Register(ctx, "p2", actor, r)
	if err != nil || second.ID == first.ID {
		t.Fatal("reference mapping crossed project")
	}
	update := pg.SubjectUpdate{ID: first.ID, ExpectedVersion: first.Version, SubjectFields: pg.SubjectFields{ExternalReference: "rotated@example.test", Label: "Current label"}}
	updated, err := store.Update(ctx, "p1", actor, update)
	if err != nil || updated.ID != first.ID || updated.Version == first.Version || !updated.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("rotation: %v", err)
	}
	if _, err := store.Get(ctx, "p1", "", r.ExternalReference); !errors.Is(err, pg.ErrSubjectNotFound) {
		t.Fatal("old mapping survived rotation")
	}
	replay, err := store.Update(ctx, "p1", uuid.NewString(), update)
	if err != nil || !replay.Replayed || replay.Version != updated.Version || replay.UpdatedBy != actor {
		t.Fatal("overwrite retry mutated state")
	}
	original, err := store.Register(ctx, "p1", uuid.NewString(), r)
	if err != nil || !original.Replayed || original.Version != updated.Version || original.ExternalReference != update.ExternalReference {
		t.Fatal("registration retry restored stale mapping")
	}
	changed := r
	changed.Label = "different"
	if _, err := store.Register(ctx, "p1", actor, changed); !errors.Is(err, pg.ErrSubjectConflict) {
		t.Fatal("retry accepted changed intent")
	}
	update.Label = "competing"
	if _, err := store.Update(ctx, "p1", actor, update); !errors.Is(err, pg.ErrSubjectConflict) {
		t.Fatal("stale overwrite accepted")
	}
	exported, err := pg.NewExporter(pool, schema).Export(ctx, schema, "p1", first.ID)
	if err != nil || len(exported.Sections["data_subject"]) != 1 || len(exported.Sections["subject_retry"]) != 1 {
		t.Fatal("registry missing from export")
	}
	raw, _ := json.Marshal(exported)
	if !bytes.Contains(raw, []byte("rotated@example.test")) || bytes.Contains(raw, []byte("contact@example.test")) {
		t.Fatal("export retained old reference or omitted current reference")
	}
	erased, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", first.ID, "requested")
	if err != nil || erased.Deleted["data_subject"] != 1 || erased.Residual["data_subject"] != 0 || erased.Deleted["observation"] != 0 {
		t.Fatalf("subject-only erasure: %v", err)
	}
	if _, err := store.Register(ctx, "p1", actor, r); !errors.Is(err, pg.ErrSubjectConflict) {
		t.Fatal("late registration resurrected erased mapping")
	}
	if _, err := store.Get(ctx, "p1", first.ID, ""); !errors.Is(err, pg.ErrSubjectNotFound) {
		t.Fatal("erased registry entry survived")
	}
	var retained int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.subject_retry WHERE scope='p1' AND (subject_id IS NOT NULL OR request_digest IS NOT NULL)`)).Scan(&retained); err != nil || retained != 0 {
		t.Fatal("retry retained subject metadata")
	}
	fresh := r
	fresh.IdempotencyKey = uuid.NewString()
	again, err := store.Register(ctx, "p1", actor, fresh)
	if err != nil || again.ID == first.ID {
		t.Fatal("new collection did not receive new identity")
	}
	if _, err := store.Get(ctx, "p2", second.ID, ""); err != nil {
		t.Fatal("erasure crossed project")
	}
}

func TestConcurrentSubjectRegistrationAndVersionChecksAdmitOneWinner(t *testing.T) {
	ctx := context.Background()
	pool, schema, store := subjectFixture(t, "subject_concurrent")
	r := subjectRequest()
	actor := uuid.NewString()
	var wg sync.WaitGroup
	results := make(chan pg.SubjectWrite, 16)
	failures := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := store.Register(ctx, "p1", actor, r)
			results <- out
			failures <- err
		}()
	}
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	var first pg.SubjectWrite
	created := 0
	for result := range results {
		if first.ID == "" {
			first = result
		}
		if first.ID != result.ID || first.Version != result.Version {
			t.Fatal("retry split identity")
		}
		if !result.Replayed {
			created++
		}
	}
	if created != 1 {
		t.Fatal("concurrent registration accepted more than one creation")
	}
	failures = make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := store.Update(ctx, "p1", actor, pg.SubjectUpdate{ID: first.ID, ExpectedVersion: first.Version, SubjectFields: pg.SubjectFields{Label: fmt.Sprintf("label-%d", i)}})
			failures <- err
		}(i)
	}
	wg.Wait()
	close(failures)
	won := 0
	for err := range failures {
		if err == nil {
			won++
		} else if !errors.Is(err, pg.ErrSubjectConflict) {
			t.Fatal(err)
		}
	}
	if won != 1 {
		t.Fatal("lost-update protection admitted multiple writes")
	}
	var count int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.data_subject`)).Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate subject persisted")
	}
}

func TestSubjectInventoryPagesAcrossDeletedCursorsAndRefusesReferenceCollisions(t *testing.T) {
	ctx := context.Background()
	pool, schema, store := subjectFixture(t, "subject_pages")
	actor := uuid.NewString()
	ids := []string{}
	for i := 0; i < 5; i++ {
		r := subjectRequest()
		r.ExternalReference = fmt.Sprintf("ref-%d", i)
		out, err := store.Register(ctx, "p1", actor, r)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, out.ID)
	}
	sort.Strings(ids)
	first, err := store.List(ctx, "p1", "", 2)
	if err != nil || len(first.Subjects) != 2 || first.Next == nil || first.Next.ID != ids[1] {
		t.Fatal("invalid first inventory page")
	}
	if _, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", ids[1], "requested"); err != nil {
		t.Fatal(err)
	}
	next, err := store.List(ctx, "p1", first.Next.ID, 100)
	if err != nil || len(next.Subjects) != 3 || next.Next != nil || next.Subjects[0].ID != ids[2] {
		t.Fatal("deleted cursor broke pagination")
	}
	empty, err := store.List(ctx, "p2", "", 20)
	if err != nil || empty.Subjects == nil || len(empty.Subjects) != 0 {
		t.Fatal("empty project inventory is not empty")
	}
	existing := next.Subjects[0]
	r := subjectRequest()
	r.ExternalReference = existing.ExternalReference
	if _, err := store.Register(ctx, "p1", actor, r); !errors.Is(err, pg.ErrSubjectConflict) {
		t.Fatal("duplicate reference accepted")
	}
	target := next.Subjects[1]
	if _, err := store.Update(ctx, "p1", actor, pg.SubjectUpdate{ID: target.ID, ExpectedVersion: target.Version, SubjectFields: existing.SubjectFields}); !errors.Is(err, pg.ErrSubjectConflict) {
		t.Fatal("update stole another reference")
	}
	current, err := store.Get(ctx, "p1", target.ID, "")
	if err != nil || current.Version != target.Version {
		t.Fatal("reference conflict partially updated metadata")
	}
	cleared, err := store.Update(ctx, "p1", actor, pg.SubjectUpdate{ID: target.ID, ExpectedVersion: target.Version, SubjectFields: pg.SubjectFields{}})
	if err != nil || cleared.ExternalReference != "" || cleared.Label != "" {
		t.Fatal("optional metadata could not be cleared")
	}
}

func TestSubjectRequestBoundsRefuseBeforeStorage(t *testing.T) {
	ctx := context.Background()
	_, _, store := subjectFixture(t, "subject_refusals")
	actor := uuid.NewString()
	for _, r := range []pg.SubjectRegistration{
		{}, {IdempotencyKey: "bad"}, {IdempotencyKey: uuid.NewString(), SubjectFields: pg.SubjectFields{Label: strings.Repeat("x", 257)}},
		{IdempotencyKey: uuid.NewString(), SubjectFields: pg.SubjectFields{ExternalReference: strings.Repeat("x", 1025)}},
		{IdempotencyKey: uuid.NewString(), SubjectFields: pg.SubjectFields{Label: "\x00"}},
		{IdempotencyKey: uuid.NewString(), SubjectFields: pg.SubjectFields{Label: string([]byte{255})}},
		{IdempotencyKey: uuid.NewString(), SubjectFields: pg.SubjectFields{ExternalReference: "  "}},
	} {
		if _, err := store.Register(ctx, "p1", actor, r); !errors.Is(err, pg.ErrInvalidSubject) {
			t.Fatal("invalid subject accepted")
		}
	}
	for _, pair := range [][2]string{{"", ""}, {uuid.NewString(), "ref"}, {"bad", ""}, {"", strings.Repeat("r", 1025)}} {
		if _, err := store.Get(ctx, "p1", pair[0], pair[1]); !errors.Is(err, pg.ErrInvalidSubject) {
			t.Fatal("invalid lookup accepted")
		}
	}
	for _, r := range []pg.SubjectUpdate{{}, {ID: uuid.NewString(), ExpectedVersion: "bad"}, {ID: uuid.NewString(), ExpectedVersion: uuid.NewString(), SubjectFields: pg.SubjectFields{Label: "\x00"}}} {
		if _, err := store.Update(ctx, "p1", actor, r); !errors.Is(err, pg.ErrInvalidSubject) {
			t.Fatal("invalid update accepted")
		}
	}
	for _, limit := range []int{-1, 0, 101} {
		if _, err := store.List(ctx, "p1", "", limit); !errors.Is(err, pg.ErrInvalidSubject) {
			t.Fatal("invalid page size accepted")
		}
	}
	if _, err := store.List(ctx, "p1", "bad", 20); !errors.Is(err, pg.ErrInvalidSubject) {
		t.Fatal("invalid cursor accepted")
	}
	if _, err := store.Update(ctx, "p1", actor, pg.SubjectUpdate{ID: uuid.NewString(), ExpectedVersion: uuid.NewString()}); !errors.Is(err, pg.ErrSubjectNotFound) {
		t.Fatal("unknown update did not refuse")
	}
	if _, err := store.Register(ctx, "missing", actor, subjectRequest()); !errors.Is(err, pg.ErrNoSuchProject) {
		t.Fatal("missing project accepted registration")
	}
}
