// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package domain

// Explain is one recall, stage by stage.
//
// # What it is for
//
// `reach`, `degraded` and `controls` say what applied, what was reached, what did not run and
// whether the bundle was cut. They stop short of the question somebody debugging a bad answer
// actually has: why these facts and not others. Which arm proposed each anchor, how many candidates
// each surface offered before its own cut, and — the one nothing could answer from outside — what
// the cut took. Without it a bad answer is a mystery; with it, it is a lookup.
//
// # It reports, and never changes what is returned
//
// Asking for it does not widen a budget, reorder a bundle, or make a surface run that would not have
// run. The bundle a caller receives with it on is the bundle they would have received with it off,
// and a test holds that: an explanation that changed the answer would be the wrong tool for finding
// out why the answer was what it was.
//
// # Why it is off by default
//
// It is a different cost and a larger response, and most recalls do not need it. The discipline is
// `degraded`'s: once asked for, every stage the controls selected is present — a stage that ran and
// found nothing reads as zero, not as absent, so "it found nothing" is distinguishable from "it did
// not run".
//
// # What it must not do, which is the part with a correctness argument
//
// It carries no text the bundle itself withheld. A fact the caller's own budget cut is a fact they
// were not given; naming its words here would hand it back through the other door and make the
// budget a suggestion. So a dropped candidate is a reference, a size and whatever score the stage
// had for it — enough to look it up with a record inspection, which is authorised separately, and
// never the statement, quote or passage. A test is named for that.
type Explain struct {
	// Anchors is every anchor any arm proposed, in the order they were proposed, including ones a
	// later rule dropped. An anchor that was proposed and discarded is the most useful row here and
	// the one the bundle cannot show, because the bundle only lists what survived.
	Anchors []ExplainedAnchor
	// Stages is one entry per surface the controls selected, always present once asked for.
	Stages []ExplainedStage
}

// ExplainedAnchor is one entity a stage proposed, and how it got there.
type ExplainedAnchor struct {
	EntityID string
	// Name is empty for an anchor that was dropped. The rule that drops one is the subject rule — a
	// name the meaning arm proposed that this person has no fact about — and that name is somebody
	// else's memory inside a project one credential opens. The id stays, and resolves through entity
	// inspection, which is authorised in its own right.
	Name string
	// Arm is which stage proposed it: ExplainArmName for a term in the question that matched a
	// stored name or alias, ExplainArmMeaning for the semantic surface's proposal.
	Arm string
	// Matched is the term that reached it, for the name arm. Empty for the meaning arm, which
	// matched the question rather than a term in it.
	Matched string
	// Score is the meaning arm's similarity. Zero for the name arm, which is an exact match and has
	// nothing to score — reported as zero rather than omitted, because "this arm does not score" is
	// a fact about the arm and is worth being able to see.
	Score float64
	// Kept says the anchor survived into the bundle. An anchor the subject rule dropped — a name the
	// meaning arm proposed that this person has no fact about — is reported with Kept false, which
	// is the one place that rule is visible at all.
	Kept bool
	// Facts is how many facts in the returned bundle came from this anchor.
	Facts int
}

// ExplainedStage is one surface's share of the work.
type ExplainedStage struct {
	// Surface is facts, reports or passages.
	Surface string
	// Ran says the stage executed. False when it was selected and then skipped — no anchor to walk
	// from, no semantic surface configured, a subject filter that withholds reports — and the reason
	// is in the bundle's Degraded.
	Ran bool
	// Offered is how many candidates the stage produced before anything in this process cut them.
	// It is what the store returned, so a stage whose own query hit a limit reports the limit, and
	// the limit is in Controls.
	Offered int
	// Kept is how many of them are in the bundle.
	Kept int
	// Dropped is what the difference was, in the order the candidates were offered.
	Dropped []ExplainedDrop
}

// ExplainedDrop is one candidate that did not reach the caller.
type ExplainedDrop struct {
	// Ref identifies the candidate: a fact id, a report id, a passage's chunk id. Enough to look it
	// up through record inspection, which is authorised in its own right, and not the text.
	Ref string
	// Cut is which cut took it: ExplainCutRows for the query's own row bound, ExplainCutBudget for
	// the caller's character budget.
	Cut string
	// Characters is what it would have cost the budget. Reported because it is the number that
	// explains the cut: a caller who sees the first dropped candidate cost more than their whole
	// remaining budget knows to ask for more characters rather than to doubt the retrieval.
	Characters int
	// Score is what the stage had for it, where the stage scores: a report's or passage's
	// similarity, a fact's confidence. Zero where nothing scored it.
	Score float64
}

// The arms that propose an anchor, and the cuts that drop a candidate.
//
// Named constants rather than strings at the point they are set, so a caller matching on one is
// matching the value the code produces.
const (
	ExplainArmName    = "name"
	ExplainArmMeaning = "meaning"

	ExplainCutRows   = "rows"
	ExplainCutBudget = "budget"
)
