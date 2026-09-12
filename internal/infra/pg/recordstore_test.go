// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
)

func TestRecordInventoryPreservesSharedAttributionAndBoundsPreviews(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "record_inventory")
	obs, facts := pg.NewObservationStore(pool), pg.NewFactStore(pool)
	long := strings.Repeat("معرفة🌍", 1000)
	ids := map[string]bool{}
	for _, text := range []string{"Atlas works at Ensera", long, "Atlas works at Acme"} {
		id := statedAt(t, ctx, obs, facts, schema, time.Now().UTC(), "Atlas works at Ensera", domain.Claim{Subject: "Atlas", Predicate: "works_at", Cardinality: domain.CardinalityMany, Object: "Ensera", Statement: text})
		ids[id] = true
	}
	// One record acquires two supports from another subject. Inventory must deduplicate them.
	var shared string
	for id := range ids {
		shared = id
		break
	}
	for i := 0; i < 2; i++ {
		turn := turnOf(domain.Message{Role: domain.RoleUser, Content: "Atlas works at Ensera"})
		turn.DataSubjectID = "subject-2"
		source, err := obs.Append(ctx, schema, turn)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.projection_dependency(source_observation_id,scope,projection_kind,projection_id,data_subject_id)
 VALUES($1,'p1','fact',$2,'subject-2')`), source.ID, shared); err != nil {
			t.Fatal(err)
		}
	}
	store := pg.NewRecordStore(pool, schema)
	for _, subject := range []string{"", "subject-1"} {
		seen := map[string]bool{}
		var cursor *pg.RecordCursor
		for pages := 0; pages < 4; pages++ {
			out, err := store.List(ctx, "p1", subject, cursor, 1)
			if err != nil {
				t.Fatal(err)
			}
			if len(out.Records) != 1 {
				t.Fatalf("wrong page: %+v", out)
			}
			r := out.Records[0]
			if !ids[r.ID] || seen[r.ID] || r.Predicate != "works_at" || r.Status != "current" || r.SubjectID == nil || r.ObjectID == nil || r.SourceRole != "user" || r.RecordedAt.IsZero() {
				t.Fatalf("bad inventory: %+v", r)
			}
			if !utf8.ValidString(r.StatementPreview) || utf8.RuneCountInString(r.StatementPreview) > pg.RecordPreviewCharacters || r.PreviewTruncated != (r.StatementBytes > len(r.StatementPreview)) {
				t.Fatalf("unbounded preview: %+v", r)
			}
			seen[r.ID] = true
			cursor = out.Next
			if cursor == nil {
				break
			}
		}
		if len(seen) != 3 || cursor != nil {
			t.Fatal("inventory lost records")
		}
	}
	out, err := store.List(ctx, "p1", "subject-2", nil, 1)
	if err != nil || len(out.Records) != 1 || out.Records[0].ID != shared || out.Next != nil {
		t.Fatalf("shared record duplication: %+v %v", out, err)
	}
	for _, scope := range []string{"p1", "p2"} {
		out, err := store.List(ctx, scope, "absent", nil, pg.MaxRecordPage)
		if err != nil || out.Records == nil || len(out.Records) != 0 || out.Next != nil {
			t.Fatalf("empty inventory: %+v %v", out, err)
		}
	}
	if _, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", "subject-2", "test"); err != nil {
		t.Fatal(err)
	}
	out, err = store.List(ctx, "p1", "subject-2", nil, 20)
	if err != nil || len(out.Records) != 0 {
		t.Fatal("erased attribution survived")
	}
	out, err = store.List(ctx, "p1", "subject-1", nil, 20)
	if err != nil || len(out.Records) != 3 {
		t.Fatal("unrelated shared knowledge lost")
	}
}

func TestRecordCursorSurvivesDeletionAndConcurrentInsertions(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "record_cursor")
	// Ordered identifiers make insertions on both sides of the cursor deterministic.
	for _, n := range []int{10, 20, 30, 40} {
		id := fmt.Sprintf("00000000-0000-4000-8000-%012d", n)
		if _, err := pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.fact(fact_id,scope,predicate,statement,cardinality,valid,source_role)
 VALUES($1,'p1','works_at','cursor fixture','many','[2020-01-01,)'::tstzrange,'user')`), id); err != nil {
			t.Fatal(err)
		}
	}
	store := pg.NewRecordStore(pool, schema)
	page, err := store.List(ctx, "p1", "", nil, 2)
	if err != nil || len(page.Records) != 2 || page.Next == nil {
		t.Fatalf("first page %+v %v", page, err)
	}
	cursor := page.Next
	if _, err := pool.Exec(ctx, schema.SQL(`DELETE FROM {schema}.fact WHERE fact_id=$1`), cursor.ID); err != nil {
		t.Fatal(err)
	}
	changed := make(chan error, 1)
	go func() {
		_, err := pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.fact(fact_id,scope,predicate,statement,cardinality,valid,source_role)
 VALUES('00000000-0000-4000-8000-000000000005','p1','works_at','insert behind cursor','many','[2020-01-01,)'::tstzrange,'user'),
 ('00000000-0000-4000-8000-000000000025','p1','works_at','insert ahead of cursor','many','[2020-01-01,)'::tstzrange,'user')`))
		changed <- err
	}()
	seen := map[string]bool{}
	last := cursor.ID
	for i := 0; i < 5; i++ {
		out, err := store.List(ctx, "p1", "", cursor, 1)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range out.Records {
			if r.ID <= last || seen[r.ID] {
				t.Fatal("cursor repeated or moved backwards")
			}
			seen[r.ID] = true
			last = r.ID
		}
		cursor = out.Next
		if cursor == nil {
			break
		}
	}
	if err := <-changed; err != nil {
		t.Fatal(err)
	}
	if !seen["00000000-0000-4000-8000-000000000030"] || !seen["00000000-0000-4000-8000-000000000040"] {
		t.Fatal("retained records lost during concurrent insertion")
	}
}

func TestRecordHistoryExplainsSupersessionAndDisappearsWithItsSource(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "record_history")
	obs, facts := pg.NewObservationStore(pool), pg.NewFactStore(pool)
	march := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	first := statedAt(t, ctx, obs, facts, schema, march, "I live in Dublin", domain.Claim{Subject: "user", Predicate: "lives_in", Cardinality: domain.CardinalityOne, Object: "Dublin", Statement: "Dublin resident", ValidFrom: march})
	store := pg.NewRecordStore(pool, schema)
	initial, err := store.History(ctx, "p1", first, nil, 1)
	if err != nil || initial.ID != first || initial.Previous == nil || len(initial.Previous) != 0 || initial.Current.Valid.Until != nil {
		t.Fatalf("initial history %+v %v", initial, err)
	}
	second := statedAt(t, ctx, obs, facts, schema, march.AddDate(0, 1, 0), "I live in Oslo", domain.Claim{Subject: "user", Predicate: "lives_in", Cardinality: domain.CardinalityOne, Object: "Oslo", Statement: "Oslo resident", ValidFrom: march.AddDate(0, 1, 0)})
	out, err := store.History(ctx, "p1", first, nil, 1)
	if err != nil || len(out.Previous) != 1 || out.Next != nil {
		t.Fatalf("history %+v %v", out, err)
	}
	prior := out.Previous[0]
	if out.Current.Valid.Until == nil || !out.Current.Valid.Until.Equal(march.AddDate(0, 1, 0)) || prior.Valid.Until != nil || !prior.Known.From.Equal(*initial.Current.Known.From) || !prior.Known.Until.Equal(*out.Current.Known.From) || !prior.Known.FromInclusive || prior.Known.UntilInclusive {
		t.Fatalf("incoherent intervals %+v", out)
	}
	citation, err := pg.NewCitationStore(pool, schema).Resolve(ctx, "p1", second, nil, 1)
	if err != nil || len(citation.Evidence) != 1 || prior.EndedByObservationID != citation.Evidence[0].ObservationID {
		t.Fatal("history lacks its actual cause")
	}
	list, err := store.List(ctx, "p1", "", nil, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range list.Records {
		if r.ID == first && r.Status != "validity_closed" {
			t.Fatal("superseded record looks open")
		}
	}
	for _, scope := range []string{"p2", "unknown_project"} {
		if _, err := store.History(ctx, scope, first, nil, 1); !errors.Is(err, pg.ErrRecordNotFound) {
			t.Fatalf("project boundary: %v", err)
		}
	}
	if _, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", "subject-1", "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.History(ctx, "p1", first, nil, 1); !errors.Is(err, pg.ErrRecordNotFound) {
		t.Fatalf("erased history resolved: %v", err)
	}
}

func TestRecordInspectionRejectsInvalidInputsAndPropagatesCancellation(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "record_refusals")
	store := pg.NewRecordStore(pool, schema)
	for _, tc := range []struct {
		scope, subject string
		after          *pg.RecordCursor
		limit          int
	}{
		{"bad scope", "", nil, 1}, {"p1", "", nil, 0}, {"p1", "", nil, 101}, {"p1", "", &pg.RecordCursor{ID: "invalid"}, 1},
		{"p1", " ", nil, 1}, {"p1", string([]byte{0xff}), nil, 1}, {"p1", strings.Repeat("a", pg.MaxRecordSubjectBytes+1), nil, 1},
	} {
		if _, err := store.List(ctx, tc.scope, tc.subject, tc.after, tc.limit); !errors.Is(err, pg.ErrInvalidRecordPage) {
			t.Fatalf("invalid inventory input: %v", err)
		}
	}
	id := uuid.NewString()
	for _, tc := range []struct {
		scope, id string
		before    *pg.RecordHistoryCursor
		limit     int
	}{
		{"bad scope", id, nil, 1}, {"p1", "bad", nil, 1}, {"p1", id, nil, 0}, {"p1", id, nil, 101},
		{"p1", id, &pg.RecordHistoryCursor{ID: "invalid", KnownUntil: time.Now()}, 1},
		{"p1", id, &pg.RecordHistoryCursor{ID: id}, 1},
		{"p1", id, &pg.RecordHistoryCursor{ID: id, KnownUntil: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)}, 1},
	} {
		if _, err := store.History(ctx, tc.scope, tc.id, tc.before, tc.limit); !errors.Is(err, pg.ErrInvalidRecordPage) {
			t.Fatalf("invalid history input: %v", err)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := store.List(cancelled, "p1", "", nil, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("list swallowed cancellation: %v", err)
	}
	if _, err := store.History(cancelled, "p1", id, nil, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("history swallowed cancellation: %v", err)
	}
}
