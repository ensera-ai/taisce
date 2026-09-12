// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package pg_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/compaction"
	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/jackc/pgx/v5/pgxpool"
)

func storeFormed(t *testing.T, pool *pgxpool.Pool, schema pg.Schema, subject string, n int) []string {
	t.Helper()
	ctx := context.Background()
	var ids []string
	for i := 0; i < n; i++ {
		obs, err := pg.NewObservationStore(pool).Append(ctx, schema, domain.Turn{Scope: "p1", DataSubjectID: subject, OccurredAt: time.Now(),
			Messages: []domain.Message{{Role: domain.RoleUser, Content: fmt.Sprintf("turn %d of %s", i, subject)}, {Role: domain.RoleAssistant, Ordinal: 1, Content: "noted"}}})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, obs.ID)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.observation SET formed_at=now() WHERE scope='p1' AND data_subject_id=$1`), subject); err != nil {
		t.Fatal(err)
	}
	return ids
}

// A segment is written with a registration per covered turn, in one transaction; a second segment
// over the same range is refused; a range whose turns moved is refused; and an erasure of the subject
// removes their segments through the registrations and leaves another subject's.
func TestASegmentIsRegisteredToEveryTurnItCoversAndErasureReachesIt(t *testing.T) {
	pool := testPool(t)
	schema := tenant(t, pool, "wp_segments")
	ctx := context.Background()
	store := pg.NewSegmentStore(pool, schema)
	storeFormed(t, pool, schema, "alice", compaction.Verbatim+compaction.Branch+2)
	storeFormed(t, pool, schema, "bob", compaction.Verbatim+compaction.Branch)
	subjects, err := store.Subjects(ctx, "p1", 10)
	if err != nil || len(subjects) != 2 {
		t.Fatalf("both subjects have more than the verbatim tail, got %v %v", subjects, err)
	}
	turns, err := store.Formed(ctx, "p1", "alice")
	if err != nil || len(turns) != compaction.Verbatim+compaction.Branch+2 || turns[0].Size == 0 {
		t.Fatalf("formed turns with sizes expected, got %d %v", len(turns), err)
	}
	plan, ok, err := compaction.Next(turns, nil)
	if err != nil || !ok || plan.Level != 1 {
		t.Fatalf("expected a level-1 plan, got %+v %v %v", plan, ok, err)
	}
	material, err := store.Material(ctx, "p1", "alice", plan)
	if err != nil || len(material.Turns) != compaction.Branch || len(material.Turns[0].Messages) != 2 || material.Turns[0].Messages[1].Content != "noted" {
		t.Fatalf("the material is the turns with their messages, got %+v %v", material, err)
	}
	seg, err := store.Write(ctx, "p1", "alice", plan, compaction.Summary{Text: "Alice began."})
	if err != nil {
		t.Fatal(err)
	}
	var registrations int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.projection_dependency WHERE scope='p1' AND projection_kind='segment' AND projection_id=$1`), seg.ID).Scan(&registrations); err != nil {
		t.Fatal(err)
	}
	if registrations != compaction.Branch {
		t.Fatalf("expected one registration per covered turn, got %d", registrations)
	}
	if _, err := store.Write(ctx, "p1", "alice", plan, compaction.Summary{Text: "again"}); !errors.Is(err, pg.ErrSegmentConflict) {
		t.Fatalf("a second segment over the range must be refused, got %v", err)
	}
	stale := plan
	stale.Turns = compaction.Branch + 1
	stale.From = plan.To + 1
	stale.To = plan.To + compaction.Branch
	if _, err := store.Write(ctx, "p1", "alice", stale, compaction.Summary{Text: "stale"}); !errors.Is(err, pg.ErrSegmentConflict) {
		t.Fatalf("a plan the history moved under must be refused, got %v", err)
	}
	var stray int
	_ = pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.segment WHERE scope='p1' AND data_subject_id='alice'`)).Scan(&stray)
	if stray != 1 {
		t.Fatalf("a refused write must leave nothing behind, got %d segments", stray)
	}
	// Level 2 material is the units' summaries.
	segments, err := store.Segments(ctx, "p1", "alice")
	if err != nil || len(segments) != 1 || segments[0].Covered != compaction.Branch {
		t.Fatalf("expected the written segment, got %+v %v", segments, err)
	}
	m2, err := store.Material(ctx, "p1", "alice", compaction.Plan{Level: 2, From: 0, To: plan.To, Units: segments, Turns: compaction.Branch})
	if err != nil || len(m2.Units) != 1 || m2.Units[0].Summary != "Alice began." {
		t.Fatalf("level-2 material is the unit summaries, got %+v %v", m2, err)
	}
	// Bob's segment survives Alice's erasure; Alice's does not.
	bobTurns, _ := store.Formed(ctx, "p1", "bob")
	bobPlan, _, _ := compaction.Next(bobTurns, nil)
	if _, err := store.Write(ctx, "p1", "bob", bobPlan, compaction.Summary{Text: "Bob began."}); err != nil {
		t.Fatal(err)
	}
	receipt, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", "alice", "test")
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Deleted["segment"] != 1 || receipt.Residual["segment"] != 0 {
		t.Fatalf("erasure must delete the subject's segment and count no residual, got deleted=%v residual=%v", receipt.Deleted, receipt.Residual)
	}
	remaining, _ := store.Segments(ctx, "p1", "bob")
	if len(remaining) != 1 {
		t.Fatal("another subject's segment must survive")
	}
	// The assembled context: Bob's segment stands for eight turns; the rest are verbatim.
	assembled, err := store.Context(ctx, "p1", "bob", 1<<20)
	if err != nil || len(assembled.Segments) != 1 || assembled.Segments[0].Summary != "Bob began." || len(assembled.Turns) != compaction.Verbatim {
		t.Fatalf("expected one segment and the verbatim tail, got %+v %v", assembled, err)
	}
	if assembled.Turns[0].Messages[0].Content != fmt.Sprintf("turn %d of bob", compaction.Branch) {
		t.Fatalf("the verbatim tail starts after the segment, got %q", assembled.Turns[0].Messages[0].Content)
	}
	// A database that cannot answer is an error from every read and the write, never an empty
	// history: an empty history would be planned over and written from.
	gone, cancel := context.WithCancel(ctx)
	cancel()
	bobSegments, _ := store.Segments(ctx, "p1", "bob")
	level2 := compaction.Plan{Level: 2, From: bobPlan.From, To: bobPlan.To, Units: bobSegments, Turns: compaction.Branch}
	for name, call := range map[string]func() error{
		"subjects": func() error { _, err := store.Subjects(gone, "p1", 10); return err },
		"formed":   func() error { _, err := store.Formed(gone, "p1", "bob"); return err },
		"segments": func() error { _, err := store.Segments(gone, "p1", "bob"); return err },
		"material": func() error { _, err := store.Material(gone, "p1", "bob", level2); return err },
		"write": func() error {
			_, err := store.Write(gone, "p1", "bob", level2, compaction.Summary{Text: "x"})
			return err
		},
		"context": func() error { _, err := store.Context(gone, "p1", "bob", 1<<20); return err },
	} {
		if err := call(); err == nil {
			t.Fatalf("%s must fail when the database cannot answer", name)
		}
	}
}
