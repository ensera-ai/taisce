// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package recall

import "github.com/ensera-ai/taisce/internal/domain"

// explanation accumulates the per-stage view of one recall, or nothing at all.
//
// # Why a nil value rather than a flag at every call site
//
// Every method below is safe on a nil receiver, so the recall path calls them unconditionally and a
// recall that was not asked to explain itself allocates nothing and branches nowhere. The
// alternative — an `if r.explain` around each record — is the same condition written eight times,
// and the eighth is the one somebody forgets, which produces an explanation that is silently missing
// a stage.
//
// # Why it never touches the bundle
//
// It reads what each stage produced and records it. It does not decide what is returned, and the
// bundle a caller receives with an explanation is the bundle they would have received without one. An
// explanation that changed the answer would be the wrong instrument for finding out why the answer
// was what it was, and a test holds that.
type explanation struct {
	anchors []domain.ExplainedAnchor
	// stages in the order they ran, with an index so a stage can be completed after it is opened.
	stages []*domain.ExplainedStage
	byName map[string]*domain.ExplainedStage
}

// newExplanation returns a collector when the caller asked for one, and nil when they did not.
func newExplanation(on bool) *explanation {
	if !on {
		return nil
	}
	return &explanation{byName: map[string]*domain.ExplainedStage{}}
}

// proposed records an anchor and the arm that proposed it.
func (e *explanation) proposed(anchors []domain.Anchor, arm string) {
	if e == nil {
		return
	}
	for _, a := range anchors {
		entry := domain.ExplainedAnchor{
			EntityID: a.Entity.ID, Name: a.Entity.CanonicalName, Arm: arm, Score: a.Similarity,
		}
		if arm == domain.ExplainArmName {
			// The term that reached it. The meaning arm matched the question rather than a term in
			// it, and reporting the question back as the match would read as a term nobody typed.
			entry.Matched = a.Matched
		}
		e.anchors = append(e.anchors, entry)
	}
}

// kept marks which anchors survived into the bundle, and how many facts each brought.
//
// Called once, after every rule that can drop an anchor has run. An anchor proposed and then dropped
// is the most useful row in an explanation and the one the bundle cannot show, because the bundle
// lists only what survived.
func (e *explanation) kept(anchors []domain.Anchor, facts map[string]int) {
	if e == nil {
		return
	}
	survived := make(map[string]bool, len(anchors))
	for _, a := range anchors {
		survived[a.Entity.ID] = true
	}
	for i := range e.anchors {
		e.anchors[i].Kept = survived[e.anchors[i].EntityID]
		e.anchors[i].Facts = facts[e.anchors[i].EntityID]
		if !e.anchors[i].Kept {
			// An anchor that was dropped keeps its id and loses its name. The rule that drops one is
			// the subject rule — a name the meaning arm proposed that this person has no fact about
			// — and that name is somebody else's memory inside a project one credential opens.
			// Reporting that an anchor was proposed and dropped is what makes the cut visible;
			// reporting what it was called would be a disclosure the bundle itself refuses. The id
			// is opaque and resolves through entity inspection, which is authorised in its own
			// right.
			e.anchors[i].Name = ""
		}
	}
}

// stage opens a stage, or returns the one already open under that name.
//
// Opened even for a stage that did not run, because `ran: false` and an absent stage are different
// answers: one says the surface was selected and skipped, the other says nothing at all.
func (e *explanation) stage(surface string, ran bool) *domain.ExplainedStage {
	if e == nil {
		return nil
	}
	if s, ok := e.byName[surface]; ok {
		s.Ran = s.Ran || ran
		return s
	}
	s := &domain.ExplainedStage{Surface: surface, Ran: ran}
	e.byName[surface] = s
	e.stages = append(e.stages, s)
	return s
}

// offered adds to what a stage produced before anything in this process cut it.
func (e *explanation) offered(surface string, n int) {
	if e == nil {
		return
	}
	e.stage(surface, true).Offered += n
}

// kept records one candidate that reached the caller.
func (e *explanation) keptOne(surface string) {
	if e == nil {
		return
	}
	e.stage(surface, true).Kept++
}

// dropped records one candidate that did not, and which cut took it.
func (e *explanation) dropped(surface, ref, cut string, characters int, score float64) {
	if e == nil {
		return
	}
	s := e.stage(surface, true)
	s.Dropped = append(s.Dropped, domain.ExplainedDrop{
		Ref: ref, Cut: cut, Characters: characters, Score: score,
	})
}

// explained is the view to hand back, or nil when nothing was asked for.
func (e *explanation) explained() *domain.Explain {
	if e == nil {
		return nil
	}
	out := &domain.Explain{Anchors: e.anchors}
	for _, s := range e.stages {
		out.Stages = append(out.Stages, *s)
	}
	return out
}
