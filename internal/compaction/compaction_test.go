// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package compaction_test

import (
	"fmt"
	"math"
	"testing"

	"github.com/ensera-ai/taisce/internal/compaction"
)

func turns(n int) []compaction.Turn {
	out := make([]compaction.Turn, n)
	for i := range out {
		out[i] = compaction.Turn{Offset: int64(i), Size: 100}
	}
	return out
}

// rollUp writes every segment the planner asks for, the way the pass does, and counts the writes:
// that count is the number of model calls a history costs.
func rollUp(t *testing.T, history []compaction.Turn) ([]compaction.Segment, int) {
	t.Helper()
	var segments []compaction.Segment
	writes := 0
	for {
		plan, ok, err := compaction.Next(history, segments)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			return segments, writes
		}
		writes++
		segments = append(segments, compaction.Segment{ID: fmt.Sprintf("s%d", writes), Level: plan.Level, From: plan.From, To: plan.To, Covered: plan.Turns, Size: 200})
		if writes > 10*len(history) {
			t.Fatal("the roll-up does not terminate")
		}
	}
}

// Nothing is written while the history fits in the verbatim tail; the first run behind the tail
// becomes a level-1 segment; a full run of level-1 segments becomes a level-2 one over their union.
func TestTheRollUpClimbsOneLevelPerFullRun(t *testing.T) {
	if _, ok, _ := compaction.Next(turns(compaction.Verbatim), nil); ok {
		t.Fatal("a history inside the verbatim tail has nothing to roll up")
	}
	history := turns(compaction.Verbatim + compaction.Branch)
	plan, ok, err := compaction.Next(history, nil)
	if err != nil || !ok || plan.Level != 1 || plan.From != 0 || plan.To != compaction.Branch-1 || plan.Turns != compaction.Branch {
		t.Fatalf("expected the first level-1 run, got %+v %v %v", plan, ok, err)
	}
	segments, writes := rollUp(t, turns(compaction.Verbatim+compaction.Branch*compaction.Branch))
	levels := map[int]int{}
	for _, s := range segments {
		levels[s.Level]++
	}
	if levels[1] != compaction.Branch || levels[2] != 1 || writes != compaction.Branch+1 {
		t.Fatalf("expected %d level-1 and one level-2 segment from %d writes, got %v from %d", compaction.Branch, compaction.Branch+1, levels, writes)
	}
	top := segments[len(segments)-1]
	if top.Level != 2 || top.From != 0 || top.To != int64(compaction.Branch*compaction.Branch-1) || top.Covered != compaction.Branch*compaction.Branch {
		t.Fatalf("the level-2 segment must cover the union of its units, got %+v", top)
	}
}

// Ten thousand turns cost a number of summaries linear in the turns, not quadratic: each turn is
// summarised once per level it climbs, and the levels are logarithmic. The bound below is the
// geometric series n/(Branch-1); the measured count is printed as the evidence G9 asks for.
func TestTenThousandTurnsCostLinearlyManySummaries(t *testing.T) {
	n := 10000
	segments, writes := rollUp(t, turns(n))
	bound := n / (compaction.Branch - 1)
	if writes > bound {
		t.Fatalf("%d turns cost %d summaries; the linear bound is %d", n, writes, bound)
	}
	t.Logf("%d turns: %d summaries (%d level-1), bound %d", n, writes, countLevel(segments, 1), bound)
	// And a context over that history reads a logarithmic number of segments.
	ctx, err := compaction.Assemble(turns(n), segments, math.MaxInt32)
	if err != nil {
		t.Fatal(err)
	}
	logBound := (compaction.Branch - 1) * (int(math.Log(float64(n))/math.Log(compaction.Branch)) + 2)
	if len(ctx.Segments) > logBound || len(ctx.Turns) != compaction.Verbatim {
		t.Fatalf("a context read %d segments and %d turns; the bounds are %d and %d", len(ctx.Segments), len(ctx.Turns), logBound, compaction.Verbatim)
	}
	t.Logf("context over %d turns: %d segments, %d verbatim turns", n, len(ctx.Segments), len(ctx.Turns))
}

// A gap in the history, which is what an erasure leaves, ends a run: no segment spans it, and the
// segments that covered erased turns are the only ones the pass has to write again.
func TestAGapEndsARunAndOnlyTheCoveringSegmentsAreWrittenAgain(t *testing.T) {
	history := turns(compaction.Verbatim + 3*compaction.Branch)
	segments, _ := rollUp(t, history)
	// Erase turn 10: it lay in the second level-1 segment. The pass deletes that segment through its
	// registrations; here the deletion is done by hand, then the planner is asked what is next.
	var survivors []compaction.Segment
	for _, s := range segments {
		if !(s.From <= 10 && 10 <= s.To) {
			survivors = append(survivors, s)
		}
	}
	var remaining []compaction.Turn
	for _, tu := range history {
		if tu.Offset != 10 {
			remaining = append(remaining, tu)
		}
	}
	plan, ok, err := compaction.Next(remaining, survivors)
	if err != nil || !ok {
		t.Fatalf("a gap must leave something to write, got %v %v", ok, err)
	}
	if plan.Level != 1 || plan.From != compaction.Branch || plan.To != 2*compaction.Branch-1 || plan.Turns != compaction.Branch-1 {
		t.Fatalf("the rewritten segment must cover the gap's surviving turns and nothing else, got %+v", plan)
	}
	for _, s := range survivors {
		if s.From <= 10 && 10 <= s.To {
			t.Fatal("no surviving segment may span the erased turn")
		}
	}
}

// The assembly is the newest turns verbatim and the highest segment over everything older,
// oldest first; it is cut from the oldest end to fit the budget and says so.
func TestTheAssemblyIsNewestVerbatimThenHighestSegmentsAndCutsFromTheOldestEnd(t *testing.T) {
	history := turns(compaction.Verbatim + compaction.Branch*compaction.Branch + 3)
	segments, _ := rollUp(t, history)
	ctx, err := compaction.Assemble(history, segments, math.MaxInt32)
	if err != nil {
		t.Fatal(err)
	}
	if len(ctx.Turns) != compaction.Verbatim+3 {
		t.Fatalf("the three turns behind the tail have no full run yet and stay verbatim: got %d turns", len(ctx.Turns))
	}
	if len(ctx.Segments) != 1 || ctx.Segments[0].Level != 2 {
		t.Fatalf("one level-2 segment must stand for its %d turns, got %+v", compaction.Branch*compaction.Branch, ctx.Segments)
	}
	for i := 1; i < len(ctx.Turns); i++ {
		if ctx.Turns[i].Offset <= ctx.Turns[i-1].Offset {
			t.Fatal("turns must be oldest first")
		}
	}
	if ctx.Truncated {
		t.Fatal("an unbounded budget cuts nothing")
	}
	tight, err := compaction.Assemble(history, segments, 100*3)
	if err != nil {
		t.Fatal(err)
	}
	if !tight.Truncated || len(tight.Segments) != 0 || len(tight.Turns) != 3 || tight.Turns[2].Offset != history[len(history)-1].Offset {
		t.Fatalf("a budget of three turns keeps the newest three and reports the cut, got %+v", tight)
	}
	if _, _, err := compaction.Next([]compaction.Turn{{Offset: 2}, {Offset: 1}}, nil); err == nil {
		t.Fatal("turns out of order must be refused")
	}
	if _, err := compaction.Assemble([]compaction.Turn{{Offset: 2}, {Offset: 1}}, nil, 10); err == nil {
		t.Fatal("turns out of order must be refused")
	}
}

func countLevel(segments []compaction.Segment, level int) int {
	n := 0
	for _, s := range segments {
		if s.Level == level {
			n++
		}
	}
	return n
}

// A run of level-1 segments shorter than a branch closes when it ends at a level-2 segment that
// already exists, the way a level-1 run does at level 1: that is the shape an erasure leaves. A
// single unit before such a segment is not a run. Two segments that overlap are never joined.
func TestAHigherLevelGapClosesEarlyAndOverlappingSegmentsAreNeverJoined(t *testing.T) {
	history := turns(compaction.Verbatim + 16*compaction.Branch)
	var level1 []compaction.Segment
	for i := 0; i < 16; i++ {
		level1 = append(level1, compaction.Segment{ID: fmt.Sprintf("l1-%d", i), Level: 1, From: int64(i * compaction.Branch), To: int64(i*compaction.Branch + compaction.Branch - 1), Covered: compaction.Branch, Size: 200})
	}
	// Four uncovered units, then a level-2 segment over the next eight: the four are written as one.
	bounded := append(append([]compaction.Segment{}, level1...), compaction.Segment{ID: "l2", Level: 2, From: level1[4].From, To: level1[11].To, Covered: 8 * compaction.Branch, Size: 200})
	plan, ok, err := compaction.Next(history, bounded)
	if err != nil || !ok || plan.Level != 2 || plan.From != 0 || plan.To != level1[3].To || len(plan.Units) != 4 {
		t.Fatalf("a short run bounded by a higher segment must close, got %+v %v %v", plan, ok, err)
	}
	// One unit before the segment is a remnant, not a run; seven after it are not a branch.
	remnant := append(append([]compaction.Segment{}, level1...), compaction.Segment{ID: "l2", Level: 2, From: level1[1].From, To: level1[8].To, Covered: 8 * compaction.Branch, Size: 200})
	if _, ok, err := compaction.Next(history, remnant); ok || err != nil {
		t.Fatalf("a single unit must not be written as a segment, got %v %v", ok, err)
	}
	overlapping := []compaction.Segment{
		{ID: "a", Level: 1, From: 0, To: 7, Covered: 8, Size: 200},
		{ID: "b", Level: 1, From: 5, To: 12, Covered: 8, Size: 200},
	}
	if _, ok, err := compaction.Next(turns(compaction.Verbatim+2*compaction.Branch), overlapping); ok || err != nil {
		t.Fatalf("overlapping segments are not consecutive and nothing is written over them, got %v %v", ok, err)
	}
}
