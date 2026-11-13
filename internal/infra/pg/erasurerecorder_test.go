// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
)

// TestAnErasureThatCannotLeaveATraceDoesNotHappen holds the order the trace is written in.
//
// The trace is what lets an erasure be applied again after a restore, so an erasure that committed
// without one is an erasure a restore undoes for good. The recorder is therefore called inside the
// transaction, and its failure is the erasure's failure: nothing is deleted, no receipt is
// written, and the caller is told.
func TestAnErasureThatCannotLeaveATraceDoesNotHappen(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "erasure_recorder")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	observations := pg.NewObservationStore(pool)
	first, err := observations.Append(ctx, schema, turnOf(domain.Message{Role: domain.RoleUser, Content: "one"}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := observations.Append(ctx, schema, turnOf(domain.Message{Role: domain.RoleUser, Content: "two"})); err != nil {
		t.Fatal(err)
	}
	count := func(query string) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, schema.SQL(query)).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	unavailable := errors.New("the log is not there")
	refusing := pg.NewEraser(pool).Recording(func(string, string, string, []string) error { return unavailable })
	if _, err := refusing.Erase(ctx, schema, "p1", "subject-1", "asked"); !errors.Is(err, unavailable) {
		t.Fatalf("an erasure whose trace could not be written must fail with that reason, got %v", err)
	}
	if count(`SELECT count(*) FROM {schema}.observation WHERE scope='p1'`) != 2 || count(`SELECT count(*) FROM {schema}.erasure_request`) != 0 {
		t.Fatal("an erasure whose trace could not be written deleted something or left a receipt")
	}

	// What the recorder is told is what the erasure selected, and whether it then committed is
	// visible to it: the rows are already gone inside the transaction it is called in.
	type told struct {
		request, project, kind string
		identifiers            []string
	}
	var heard []told
	recording := pg.NewEraser(pool).Recording(func(request, project, kind string, identifiers []string) error {
		heard = append(heard, told{request, project, kind, slices.Clone(identifiers)})
		return nil
	})
	bySource, err := recording.EraseSources(ctx, schema, "p1", []string{first.ID}, "asked")
	if err != nil {
		t.Fatal(err)
	}
	bySubject, err := recording.Erase(ctx, schema, "p1", "subject-1", "asked")
	if err != nil {
		t.Fatal(err)
	}
	want := []told{
		{bySource.RequestID, "p1", pg.ErasureOfSources, []string{first.ID}},
		{bySubject.RequestID, "p1", pg.ErasureOfSubject, []string{"subject-1"}},
	}
	if len(heard) != 2 || heard[0].request != want[0].request || heard[0].kind != want[0].kind || !slices.Equal(heard[0].identifiers, want[0].identifiers) ||
		heard[1].request != want[1].request || heard[1].kind != want[1].kind || !slices.Equal(heard[1].identifiers, want[1].identifiers) || heard[1].project != "p1" {
		t.Fatalf("the recorder heard %+v, want %+v", heard, want)
	}
}

// TestAReplayedErasureComesBackUnderItsOwnIdentifierOnce holds what makes a replay safe to run
// twice: the receipt returns under the identifier the erasure had, and a second attempt is told
// the database already holds it.
func TestAReplayedErasureComesBackUnderItsOwnIdentifierOnce(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "erasure_reapply")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	if _, err := pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.project(scope,label) VALUES('p2','p2')`)); err != nil {
		t.Fatal(err)
	}
	observations, eraser := pg.NewObservationStore(pool), pg.NewEraser(pool)
	mine, err := observations.Append(ctx, schema, turnOf(domain.Message{Role: domain.RoleUser, Content: "mine"}))
	if err != nil {
		t.Fatal(err)
	}
	elsewhere := turnOf(domain.Message{Role: domain.RoleUser, Content: "elsewhere"})
	elsewhere.Scope, elsewhere.DataSubjectID = "p2", "subject-2"
	if _, err := observations.Append(ctx, schema, elsewhere); err != nil {
		t.Fatal(err)
	}

	// What a project holds that an erasure could name is that project's and nobody else's.
	people, err := eraser.Erasable(ctx, schema, "p1", pg.ErasureOfSubject)
	if err != nil || !slices.Equal(people, []string{"subject-1"}) {
		t.Fatalf("the people of the first project: %v %v", people, err)
	}
	turns, err := eraser.Erasable(ctx, schema, "p1", pg.ErasureOfSources)
	if err != nil || !slices.Equal(turns, []string{mine.ID}) {
		t.Fatalf("the turns of the first project: %v %v", turns, err)
	}

	request := uuid.NewString()
	if held, err := eraser.Held(ctx, schema, request); err != nil || held {
		t.Fatalf("a receipt nobody wrote is held: %v %v", held, err)
	}
	receipt, err := eraser.ReapplySubject(ctx, schema, "p1", request, "subject-1")
	if err != nil || receipt.RequestID != request || !receipt.Clean() || receipt.Reason != pg.ReappliedErasureReason || receipt.Deleted["observation"] != 1 {
		t.Fatalf("a replayed erasure: %+v %v", receipt, err)
	}
	if held, err := eraser.Held(ctx, schema, request); err != nil || !held {
		t.Fatalf("the receipt of a replayed erasure is not held: %v %v", held, err)
	}
	if _, err := eraser.ReapplySubject(ctx, schema, "p1", request, "subject-1"); !errors.Is(err, pg.ErrErasureAlreadyHeld) {
		t.Fatalf("a second replay of one erasure: %v", err)
	}
	if _, err := eraser.ReapplySources(ctx, schema, "p1", request, []string{mine.ID}); !errors.Is(err, pg.ErrErasureAlreadyHeld) {
		t.Fatalf("a second replay of one erasure, by source: %v", err)
	}

	// What is not an erasure to replay.
	if _, err := eraser.ReapplySubject(ctx, schema, "p1", "not-an-identifier", "subject-1"); err == nil {
		t.Error("a replay under something that is not an identifier was accepted")
	}
	if _, err := eraser.ReapplySubject(ctx, schema, "p1", uuid.NewString(), ""); err == nil {
		t.Error("a replay naming nobody was accepted")
	}
	if _, err := eraser.ReapplySources(ctx, schema, "p1", uuid.NewString(), nil); err == nil {
		t.Error("a replay naming no turns was accepted")
	}
	if held, err := eraser.Held(ctx, schema, "not-an-identifier"); err != nil || held {
		t.Errorf("something that is not an identifier is held: %v %v", held, err)
	}
	// The other project was never named, and still holds its turn.
	var kept int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.observation WHERE scope='p2'`)).Scan(&kept); err != nil || kept != 1 {
		t.Fatalf("the other project holds %d turns (%v)", kept, err)
	}
}
