// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

// Package compaction decides how a subject's history rolls up on the time axis, and how a context
// is assembled from the roll-up under a budget. It has no I/O, no clock and no model: it takes what
// a store reports and says what to write next and what to hand back, so both rules can be read and
// tested without a database or a provider.
//
// # The roll-up
//
// A history is a sequence of formed turns in log order. The newest turns stay verbatim. Behind
// them, every full run of Branch consecutive turns becomes one level-1 segment: a summary carrying
// the offsets it covers. Every full run of Branch consecutive level-1 segments becomes one level-2
// segment over their union, and so on. A segment is written once and never rewritten: what makes
// it stale is erasure, which deletes it through its registrations, and the pass then finds the gap
// and writes a new one from what survived. Summarisation work is therefore amortised O(1) per turn:
// each turn is summarised once per level it climbs, and the number of levels is logarithmic.
//
// # The assembly
//
// A context is the newest Verbatim turns as they were said, then the highest-level segments that
// cover everything older, newest first, each replacing the turns and lower segments under it.
// Reading a context touches O(log n) segments. It is assembled under a character budget and cut
// from the oldest end, because the newest is what the next turn is about; the cut is reported,
// never silent.
//
// # Why segments cover whole turns
//
// A turn is the unit of the log and the unit of the write contract, and a tool call with its
// results is one group inside one turn. A segment that covers whole turns therefore cannot
// split a group, which is the atomicity G9 requires, and it needs no rule of its own to keep it.
package compaction

import (
	"errors"
	"sort"
)

const (
	// Branch is how many consecutive units, turns or segments of one level, one segment covers.
	Branch = 8
	// Verbatim is how many of the newest turns a context keeps as they were said.
	Verbatim = 8
	// MaxLevel bounds the hierarchy; 8^6 turns is more history than any subject holds.
	MaxLevel = 6
)

// Turn is one formed turn as the store reports it: its offset in the log and its size in
// characters, which is what the assembly budgets.
type Turn struct {
	Offset int64
	Size   int
}

// Segment is one written summary: the level it sits at and the contiguous offset range it covers,
// inclusive at both ends.
type Segment struct {
	ID    string
	Level int
	From  int64
	To    int64
	// Covered is how many turns the range held when the segment was written.
	Covered int
	Size    int
}

// Plan is the next segment to write, or nothing.
type Plan struct {
	Level int
	From  int64
	To    int64
	// Units are the segments the new one summarises (level 2 and up); empty at level 1, where the
	// material is the turns themselves.
	Units []Segment
	// Turns is how many turns the range covers, taken from the units or from the turns.
	Turns int
}

var ErrDisorder = errors.New("turns and segments must be in offset order")

// Next says what to write next for one subject, given every formed turn and every segment that
// exists. It returns false when the history is fully rolled up.
//
// The newest Verbatim turns are never covered: a segment written over turns that are still shown
// verbatim would be paid for and never read. Level 1 is offered first, oldest run first, because a
// level cannot be built over a lower one that has gaps.
func Next(turns []Turn, segments []Segment) (Plan, bool, error) {
	if !sorted(turns) {
		return Plan{}, false, ErrDisorder
	}
	if len(turns) <= Verbatim {
		return Plan{}, false, nil
	}
	compactable := turns[:len(turns)-Verbatim]
	byLevel := map[int][]Segment{}
	for _, s := range segments {
		byLevel[s.Level] = append(byLevel[s.Level], s)
	}
	for level := range byLevel {
		sort.Slice(byLevel[level], func(i, j int) bool { return byLevel[level][i].From < byLevel[level][j].From })
	}
	// Level 1: the oldest run of Branch compactable turns not under any level-1 segment. A shorter
	// run closes too when it ends at a segment that already exists: that is a gap an erasure left
	// between two segments, and it is written again from what survived rather than left loose.
	run := make([]Turn, 0, Branch)
	for _, t := range compactable {
		if coveredAt(byLevel[1], t.Offset) {
			if len(run) >= MinGapRun {
				return Plan{Level: 1, From: run[0].Offset, To: run[len(run)-1].Offset, Turns: len(run)}, true, nil
			}
			run = run[:0]
			continue
		}
		run = append(run, t)
		if len(run) == Branch {
			return Plan{Level: 1, From: run[0].Offset, To: run[Branch-1].Offset, Turns: Branch}, true, nil
		}
	}
	// Higher levels: the oldest run of Branch consecutive segments of the level below, contiguous
	// in the log, not under a segment of this level; a shorter run bounded by an existing segment
	// of this level closes the same way.
	for level := 2; level <= MaxLevel; level++ {
		below := byLevel[level-1]
		var units []Segment
		for _, s := range below {
			if coveredAt(byLevel[level], s.From) {
				if len(units) >= MinGapRun {
					return plan(level, units), true, nil
				}
				units = units[:0]
				continue
			}
			if len(units) > 0 && !adjacent(units[len(units)-1], s, compactable) {
				units = units[:0]
			}
			units = append(units, s)
			if len(units) == Branch {
				return plan(level, units), true, nil
			}
		}
	}
	return Plan{}, false, nil
}

// MinGapRun is the smallest run written when it is bounded by an existing segment rather than by
// reaching Branch: a single turn summarised is a summary of nothing.
const MinGapRun = 2

func plan(level int, units []Segment) Plan {
	covered := 0
	for _, u := range units {
		covered += u.Covered
	}
	return Plan{Level: level, From: units[0].From, To: units[len(units)-1].To, Units: append([]Segment(nil), units...), Turns: covered}
}

// Context is what a caller is handed: the verbatim turns, oldest first, and the segments covering
// what is older, oldest first, plus how much was cut.
type Context struct {
	Segments   []Segment
	Turns      []Turn
	Characters int
	Truncated  bool
}

// Assemble picks the newest Verbatim turns and, for everything older, the highest-level segment
// covering each offset, then cuts from the oldest end to fit the budget.
func Assemble(turns []Turn, segments []Segment, budget int) (Context, error) {
	if !sorted(turns) {
		return Context{}, ErrDisorder
	}
	verbatimFrom := 0
	if len(turns) > Verbatim {
		verbatimFrom = len(turns) - Verbatim
	}
	older := turns[:verbatimFrom]
	// The highest segment covering each older turn, once each, in offset order.
	sort.Slice(segments, func(i, j int) bool {
		if segments[i].Level != segments[j].Level {
			return segments[i].Level > segments[j].Level
		}
		return segments[i].From < segments[j].From
	})
	var chosen []Segment
	var loose []Turn
	for _, t := range older {
		best, ok := covering(segments, t.Offset)
		if !ok {
			loose = append(loose, t)
			continue
		}
		if len(chosen) == 0 || chosen[len(chosen)-1].ID != best.ID {
			chosen = append(chosen, best)
		}
	}
	// Oldest first: segments in offset order, then any uncovered older turn, then the verbatim tail.
	sort.Slice(chosen, func(i, j int) bool { return chosen[i].From < chosen[j].From })
	items := make([]item, 0, len(chosen)+len(loose)+Verbatim)
	for _, s := range chosen {
		items = append(items, item{segment: &s, size: s.Size, from: s.From})
	}
	for _, t := range loose {
		items = append(items, item{turn: &t, size: t.Size, from: t.Offset})
	}
	for _, t := range turns[verbatimFrom:] {
		items = append(items, item{turn: &t, size: t.Size, from: t.Offset})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].from < items[j].from })
	// Budget from the newest end: keep the newest that fit, cut the oldest.
	total := 0
	keepFrom := len(items)
	for i := len(items) - 1; i >= 0; i-- {
		if total+items[i].size > budget {
			break
		}
		total += items[i].size
		keepFrom = i
	}
	out := Context{Characters: total, Truncated: keepFrom > 0}
	for _, it := range items[keepFrom:] {
		if it.segment != nil {
			out.Segments = append(out.Segments, *it.segment)
		} else {
			out.Turns = append(out.Turns, *it.turn)
		}
	}
	return out, nil
}

type item struct {
	segment *Segment
	turn    *Turn
	size    int
	from    int64
}

func covering(sorted []Segment, offset int64) (Segment, bool) {
	for _, s := range sorted {
		if s.From <= offset && offset <= s.To {
			return s, true
		}
	}
	return Segment{}, false
}

func coveredAt(segments []Segment, offset int64) bool {
	for _, s := range segments {
		if s.From <= offset && offset <= s.To {
			return true
		}
	}
	return false
}

// adjacent says whether two segments of one level are consecutive in the log: no compactable
// turn lies between them.
func adjacent(a, b Segment, turns []Turn) bool {
	for _, t := range turns {
		if t.Offset > a.To && t.Offset < b.From {
			return false
		}
	}
	return a.To < b.From
}

func sorted(turns []Turn) bool {
	for i := 1; i < len(turns); i++ {
		if turns[i].Offset <= turns[i-1].Offset {
			return false
		}
	}
	return true
}
