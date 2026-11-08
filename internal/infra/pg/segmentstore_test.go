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

// A context is every turn the subject said: the summaries written over the history, the turns no
// summary was written from, and the ones the formation pass has not reached yet. The compaction
// pass still sees only what it may summarise.
//
// The two losses this holds against are not the same shape. A turn inside the formation window is
// missing for as long as the workers are behind, which for an active conversation is exactly the
// newest turns a caller asked for. A parked turn is missing forever: it exhausted its attempts, the
// watermark moved past it, and it will never be formed and so never summarised — while a segment's
// range closes across it, because the pass planned over the turns beside it.
func TestAContextCarriesTheTurnsFormationHasNotReachedAndTheOnesItParked(t *testing.T) {
	pool := testPool(t)
	schema := tenant(t, pool, "wp_context_unformed")
	ctx := context.Background()
	store := pg.NewSegmentStore(pool, schema)
	observations := pg.NewObservationStore(pool)
	ids := storeFormed(t, pool, schema, "carol", compaction.Verbatim+compaction.Branch+1)

	// One turn behind the verbatim tail parks. The attempts are set by hand because the worker
	// counts them as it claims the turn, and this test runs no worker; the park itself goes through
	// the store, so the row reaches the state the running system puts it in — unformed, parked, and
	// behind the watermark.
	parked := int64(3)
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.observation SET formed_at=NULL, formation_attempts=6 WHERE observation_id=$1::uuid`), ids[parked]); err != nil {
		t.Fatal(err)
	}
	if err := observations.Park(ctx, schema, ids[parked]); err != nil {
		t.Fatal(err)
	}
	var isParked bool
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT parked_at IS NOT NULL AND formed_at IS NULL FROM {schema}.observation WHERE observation_id=$1::uuid`), ids[parked]).Scan(&isParked); err != nil || !isParked {
		t.Fatalf("the turn must be parked and unformed, got %v %v", isParked, err)
	}
	// And one turn is stored but not yet formed: the window the workers are behind by.
	if _, err := observations.Append(ctx, schema, domain.Turn{Scope: "p1", DataSubjectID: "carol", OccurredAt: time.Now(),
		Messages: []domain.Message{{Role: domain.RoleUser, Content: "the newest thing carol said"}}}); err != nil {
		t.Fatal(err)
	}

	// The pass sees neither, and plans a run that closes across the parked offset.
	formed, err := store.Formed(ctx, "p1", "carol")
	if err != nil {
		t.Fatal(err)
	}
	for _, turn := range formed {
		if turn.Offset == parked {
			t.Fatal("the compaction pass must not be offered a parked turn: a summary would stand in for words it never read")
		}
	}
	if len(formed) != compaction.Verbatim+compaction.Branch {
		t.Fatalf("the pass sees the formed turns and nothing else, got %d", len(formed))
	}
	plan, ok, err := compaction.Next(formed, nil)
	if err != nil || !ok || !(plan.From <= parked && parked <= plan.To) {
		t.Fatalf("the test needs a plan whose range closes across the parked turn, got %+v %v %v", plan, ok, err)
	}
	if _, err := store.Write(ctx, "p1", "carol", plan, compaction.Summary{Text: "Carol began."}); err != nil {
		t.Fatal(err)
	}

	// The history is every turn, and it says which of them a summary was written from.
	history, err := store.History(ctx, "p1", "carol")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != compaction.Verbatim+compaction.Branch+2 {
		t.Fatalf("every stored turn is in the history, got %d", len(history))
	}
	summarised := map[int64]bool{}
	for _, turn := range history {
		if turn.Size == 0 {
			t.Fatalf("every turn carries its size, offset %d has none", turn.Offset)
		}
		summarised[turn.Offset] = turn.Summarised
	}
	if summarised[parked] {
		t.Fatal("no summary was written from the parked turn, so it must not be marked as summarised")
	}
	if !summarised[plan.From] || !summarised[plan.To] {
		t.Fatalf("the turns the segment was written from must be marked, got %+v", summarised)
	}

	assembled, err := store.Context(ctx, "p1", "carol", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if len(assembled.Segments) != 1 || assembled.Segments[0].Summary != "Carol began." {
		t.Fatalf("the one written summary stands in for what it covered, got %+v", assembled.Segments)
	}
	said := map[int64]string{}
	for _, turn := range assembled.Turns {
		said[turn.Offset] = turn.Messages[0].Content
	}
	if said[parked] != fmt.Sprintf("turn %d of carol", parked) {
		t.Fatalf("a parked turn's words are the subject's and must come back verbatim, got %q", said[parked])
	}
	if said[int64(compaction.Verbatim+compaction.Branch+1)] != "the newest thing carol said" {
		t.Fatalf("a turn formation has not reached must come back verbatim, got %+v", said)
	}
	if assembled.Truncated || assembled.Characters == 0 {
		t.Fatalf("an ample budget cuts nothing and the cost is counted, got %+v", assembled)
	}
	for i := 1; i < len(assembled.Turns); i++ {
		if assembled.Turns[i].Offset <= assembled.Turns[i-1].Offset {
			t.Fatal("the turns are oldest first, each in its own place in the log")
		}
	}

	// A budget too small to hold them cuts from the oldest end and says so, rather than answering
	// short in silence: that is the whole difference between a context and an empty one.
	tight, err := store.Context(ctx, "p1", "carol", 40)
	if err != nil {
		t.Fatal(err)
	}
	if !tight.Truncated || len(tight.Turns) == 0 {
		t.Fatalf("a tight budget keeps the newest and reports the cut, got %+v", tight)
	}

	// History is an error when the database cannot answer, never an empty history.
	gone, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := store.History(gone, "p1", "carol"); err == nil {
		t.Fatal("history must fail when the database cannot answer")
	}
}

// A turn's size comes off the turn, and is what summing its messages would have given.
//
// Both readers used to compute it by joining turn_message. That table carries neither scope nor
// subject — it is reached through the observation — so nothing narrowed the join, and the planner
// read every message row in the project to size the turns of one person in it. On a project of
// 45,960 turns across 81 subjects, answering for the 5,000-turn subject found its own rows through
// an index in 92 buffers and spent 1,699 sizing them: 85% of the query, and growing with the project
// while the answer did not.
//
// Rewriting the query was measured and rejected. A correlated scalar subquery bounds the work by the
// subject and costs four buffers per turn, which took the same answer from 1,985 buffers to 20,283;
// each shape wins on a different shape of project, which is a plan that flips rather than a fix.
//
// So this holds the two properties the stored value needs: that it equals what the join computed,
// for every turn in the schema, and that a turn written now carries it. It is safe to store because
// a message's role and content are written once and never updated.
func TestATurnCarriesTheSizeSummingItsMessagesWouldHaveGiven(t *testing.T) {
	pool := testPool(t)
	schema := tenant(t, pool, "wp_turn_size")
	ctx := context.Background()
	store := pg.NewSegmentStore(pool, schema)
	observations := pg.NewObservationStore(pool)
	storeFormed(t, pool, schema, "dana", 5)

	// Characters, not bytes: counted by the database, where length() on a UTF-8 database counts
	// code points. A turn whose text is outside ASCII is where a byte count would diverge, and the
	// budget a context spends is documented in code points.
	wide, err := observations.Append(ctx, schema, domain.Turn{Scope: "p1", DataSubjectID: "dana", OccurredAt: time.Now(),
		Messages: []domain.Message{{Role: domain.RoleUser, Content: "sé mo chroí é — ⌘ ∑ 文字"}}})
	if err != nil {
		t.Fatal(err)
	}
	var stored, characters, bytes int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT o.content_characters,
		 (SELECT coalesce(sum(length(m.role)+length(m.content)),0) FROM {schema}.turn_message m WHERE m.observation_id=o.observation_id),
		 (SELECT coalesce(sum(octet_length(m.role)+octet_length(m.content)),0) FROM {schema}.turn_message m WHERE m.observation_id=o.observation_id)
		 FROM {schema}.observation o WHERE o.observation_id=$1::uuid`), wide.ID).Scan(&stored, &characters, &bytes); err != nil {
		t.Fatal(err)
	}
	if stored != characters {
		t.Fatalf("a turn's stored size must be what summing its messages gives: %d against %d", stored, characters)
	}
	if bytes == characters {
		t.Fatal("this turn is meant to have multi-byte text, so that a byte count would be visibly wrong")
	}

	// Every turn in the schema, not only the one just written: a backfill that missed rows would
	// make a context silently value them at nothing.
	var mismatched int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.observation o
		 WHERE o.content_characters <>
		       coalesce((SELECT sum(length(m.role)+length(m.content)) FROM {schema}.turn_message m
		                  WHERE m.observation_id=o.observation_id), 0)`)).Scan(&mismatched); err != nil {
		t.Fatal(err)
	}
	if mismatched != 0 {
		t.Fatalf("%d turns carry a size that is not their messages' size", mismatched)
	}

	// And the readers hand that number on, so a budget spends what the turn actually holds.
	turns, err := store.Formed(ctx, "p1", "dana")
	if err != nil || len(turns) != 5 {
		t.Fatalf("the pass reads the formed turns, got %d %v", len(turns), err)
	}
	for _, turn := range turns {
		if turn.Size == 0 {
			t.Fatalf("every turn carries a size, offset %d has none", turn.Offset)
		}
	}
	history, err := store.History(ctx, "p1", "dana")
	if err != nil || len(history) != 6 {
		t.Fatalf("the context reads every stored turn, got %d %v", len(history), err)
	}
	if history[5].Size != characters {
		t.Fatalf("the newest turn's size must be its own: %d against %d", history[5].Size, characters)
	}
	// A context assembled over them spends exactly the sum of what it returned.
	assembled, err := store.Context(ctx, "p1", "dana", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, turn := range history {
		total += turn.Size
	}
	if assembled.Characters != total || assembled.Truncated {
		t.Fatalf("an ample budget returns every turn and counts every character: %d against %d, truncated=%v",
			assembled.Characters, total, assembled.Truncated)
	}
}
