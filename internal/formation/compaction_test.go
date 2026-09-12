// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package formation_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/compaction"
	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/formation"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/report"
	"github.com/jackc/pgx/v5/pgxpool"
)

// scriptedSummariser writes a deterministic summary from the material and counts its calls: the
// count is the model calls a history cost.
type scriptedSummariser struct {
	calls  int
	fail   bool
	before func(compaction.Material)
}

func (s *scriptedSummariser) Write(_ context.Context, m compaction.Material) (compaction.Summary, error) {
	s.calls++
	if s.before != nil {
		s.before(m)
	}
	if s.fail {
		return compaction.Summary{}, errors.New("the model is down")
	}
	return compaction.Summary{Text: fmt.Sprintf("level %d over %d-%d from %d turns and %d units", m.Level, m.From, m.To, len(m.Turns), len(m.Units))}, nil
}

func storeFormedTurns(t *testing.T, pool *pgxpool.Pool, schema pg.Schema, subject string, n int) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < n; i++ {
		if _, err := pg.NewObservationStore(pool).Append(ctx, schema, domain.Turn{Scope: "p1", DataSubjectID: subject, OccurredAt: time.Now(),
			Messages: []domain.Message{{Role: domain.RoleUser, Content: fmt.Sprintf("turn %d", i)}}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.observation SET formed_at=now() WHERE scope='p1' AND data_subject_id=$1`), subject); err != nil {
		t.Fatal(err)
	}
}

// The pass writes at most its limit of segments per call, climbs one level when a run of segments
// is full, and stops when the history is rolled up; a summariser that fails is counted, not fatal;
// no summariser writes nothing.
func TestTheCompactionPassRollsUpAHistoryOneBoundedCallAtATime(t *testing.T) {
	pool := testPool(t)
	schema := tenant(t, pool, "form_compaction")
	ctx := context.Background()
	storeFormedTurns(t, pool, schema, "alice", compaction.Verbatim+compaction.Branch*compaction.Branch)
	store := pg.NewSegmentStore(pool, schema)
	if pass, err := formation.NewCompaction(store, nil).Run(ctx, "p1", 10); err != nil || pass.Written != 0 {
		t.Fatalf("no summariser must write nothing, got %+v %v", pass, err)
	}
	model := &scriptedSummariser{}
	c := formation.NewCompaction(store, model)
	first, err := c.Run(ctx, "p1", 3)
	if err != nil || first.Written != 3 || first.Subjects != 1 {
		t.Fatalf("expected three segments from a bounded pass, got %+v %v", first, err)
	}
	total := first.Written
	for i := 0; i < 20 && total < compaction.Branch+1; i++ {
		pass, err := c.Run(ctx, "p1", 3)
		if err != nil {
			t.Fatal(err)
		}
		total += pass.Written
	}
	segments, err := store.Segments(ctx, "p1", "alice")
	if err != nil {
		t.Fatal(err)
	}
	levels := map[int]int{}
	for _, s := range segments {
		levels[s.Level]++
	}
	if levels[1] != compaction.Branch || levels[2] != 1 || model.calls != compaction.Branch+1 {
		t.Fatalf("expected %d level-1 and one level-2 segment from %d model calls, got %v from %d", compaction.Branch, compaction.Branch+1, levels, model.calls)
	}
	if pass, _ := c.Run(ctx, "p1", 3); pass.Written != 0 {
		t.Fatal("a rolled-up history has nothing more to write")
	}
	// A failing model counts and does not stop the pass.
	storeFormedTurns(t, pool, schema, "bob", compaction.Verbatim+compaction.Branch)
	broken := formation.NewCompaction(store, &scriptedSummariser{fail: true})
	pass, err := broken.Run(ctx, "p1", 3)
	if err != nil || pass.Errored != 1 || pass.Written != 0 {
		t.Fatalf("a failing summariser must be counted and not fatal, got %+v %v", pass, err)
	}
	// A segment another replica wrote while the model was answering is not this pass's failure:
	// nothing is written twice, nothing is counted as an error, and the next pass plans again.
	raced := &scriptedSummariser{}
	raced.before = func(m compaction.Material) {
		if _, err := store.Write(ctx, "p1", "bob", compaction.Plan{Level: m.Level, From: m.From, To: m.To, Turns: len(m.Turns)}, compaction.Summary{Text: "the other replica's"}); err != nil {
			t.Fatal(err)
		}
	}
	pass, err = formation.NewCompaction(store, raced).Run(ctx, "p1", 3)
	if err != nil || pass.Errored != 0 || pass.Written != 0 || raced.calls != 1 {
		t.Fatalf("a conflict is neither a write nor an error, got %+v %v after %d calls", pass, err, raced.calls)
	}
	// A cancelled context ends the pass with the cancellation, whether it lands before the first
	// read or while the model is answering.
	gone, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := c.Run(gone, "p1", 3); err == nil {
		t.Fatal("a cancelled context must end the pass with an error")
	}
	storeFormedTurns(t, pool, schema, "carol", compaction.Verbatim+compaction.Branch)
	during, cancelDuring := context.WithCancel(ctx)
	defer cancelDuring()
	interrupted := &scriptedSummariser{fail: true, before: func(compaction.Material) { cancelDuring() }}
	if _, err := formation.NewCompaction(store, interrupted).Run(during, "p1", 3); !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancellation during the model call must surface as the cancellation, got %v", err)
	}
}

// The driver runs the projection passes after draining a scope, so a deployment writes segments
// without an operator running anything; the pass counts appear in what the driver did.
func TestTheDriverRunsTheCompactionPassAfterDraining(t *testing.T) {
	pool := testPool(t)
	schema := tenant(t, pool, "form_compaction_driver")
	ctx := context.Background()
	storeFormedTurns(t, pool, schema, "alice", compaction.Verbatim+compaction.Branch)
	subjectsFixture(t, pool, schema, "subject-1")
	// One unformed turn so the scope has a backlog for the driver to drain.
	if _, err := pg.NewObservationStore(pool).Append(ctx, schema, domain.Turn{Scope: "p1", DataSubjectID: "alice", OccurredAt: time.Now(),
		Messages: []domain.Message{{Role: domain.RoleUser, Content: "one more"}}}); err != nil {
		t.Fatal(err)
	}
	model := &scriptedSummariser{}
	policy := formation.DefaultPolicy()
	policy.SegmentsPerPass = 2
	worker := workerWith(t, pool, schema, scriptedModel{}, policy)
	driver := formation.NewDriver(worker, pg.NewObservationStore(pool), pg.NewRetentionStore(pool, schema),
		pg.NewAuditStore(pool, schema), schema, policy, nil).
		WithPasses(formation.NewSubjects(pg.NewCommunityStore(pool, schema), report.New(&scriptedWriter{})),
			formation.NewCompaction(pg.NewSegmentStore(pool, schema), model))
	pass, err := driver.Once(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if pass.Segments != 1 || model.calls != 1 || pass.Reports == 0 {
		t.Fatalf("expected the passes to write one segment and the subjects' reports after draining, got %+v with %d calls", pass, model.calls)
	}
	segments, _ := pg.NewSegmentStore(pool, schema).Segments(ctx, "p1", "alice")
	if len(segments) != 1 || !strings.HasPrefix(segments[0].ID, "") {
		t.Fatalf("expected one segment, got %+v", segments)
	}
}
