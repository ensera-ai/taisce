// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package recall_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/recall"
)

// ── A recall can explain itself ────────────────────────────────────────────────────────────────
//
// `controls`, `reach` and `degraded` say what applied, what was reached and what did not run. They
// stop short of the question somebody debugging a bad answer has: why these and not others. These
// tests hold the three things an explanation has to get right — which arm proposed each anchor, what
// each cut took, and that asking changes nothing — and the one it must not do: hand back the text the
// bundle withheld.
//
// The store is a stub here because what is under test is this package's account of its own stages.
// The store is a port, and a recall whose explanation is checked against a real deployment is
// `TestAnExplanationOfARealRecallMatchesTheBundleItCameWith` below.

// explainStore offers four facts about one anchor, each costing a known amount, so a budget can be
// set to cut at a chosen place.
type explainStore struct {
	facts   []domain.CitedFact
	anchors []domain.Anchor
}

func (s explainStore) AnchorsForSubject(context.Context, []string, []string, string) ([]domain.Anchor, error) {
	return s.anchors, nil
}

func (s explainStore) FactsAboutForSubject(_ context.Context, _, _ []string, limit int, _ []string,
	_ domain.AsOf, _ int, _ string) ([]domain.CitedFact, bool, error) {
	if limit < len(s.facts) {
		return s.facts[:limit], true, nil
	}
	return s.facts, false, nil
}

func fact(id, statement string) domain.CitedFact {
	return domain.CitedFact{
		ID: id, Subject: "Marta", Predicate: "works_at", Object: "Ensera", Statement: statement,
		Confidence: 0.9, SourceRole: "user", AnchoredOn: "e1",
		Evidence: domain.Evidence{Quote: statement, Context: domain.Context{Text: statement}},
	}
}

func fourFacts() explainStore {
	return explainStore{
		anchors: []domain.Anchor{{Entity: domain.Entity{ID: "e1", CanonicalName: "Marta"}, Matched: "marta"}},
		facts: []domain.CitedFact{
			fact("f1", "Marta works at Ensera"),
			fact("f2", "Marta works at Ensera too"),
			fact("f3", "Marta works at Ensera three"),
			fact("f4", "Marta works at Ensera four"),
		},
	}
}

func stage(t *testing.T, e *domain.Explain, surface string) domain.ExplainedStage {
	t.Helper()
	if e == nil {
		t.Fatal("no explanation came back")
	}
	for _, s := range e.Stages {
		if s.Surface == surface {
			return s
		}
	}
	t.Fatalf("no %s stage in the explanation: %+v", surface, e.Stages)
	return domain.ExplainedStage{}
}

// Asking for an explanation changes nothing about the bundle.
//
// This is the property that makes the rest of it usable: an explanation that changed the answer would
// be the wrong instrument for finding out why the answer was what it was. It is also the one a later
// change is most likely to break, by recording something in a way that consumes budget or reorders a
// cut.
func TestAskingForAnExplanationChangesNothingAboutTheBundle(t *testing.T) {
	ctx := context.Background()
	// Both a budget that fits everything and one that cuts, because a comparison made only under a
	// cut can agree by accident: two different numbers of facts can survive the same cut.
	for _, budget := range []recall.Budget{
		{Characters: 100000, MaxRows: 50},
		{Characters: 220, MaxRows: 3},
	} {
		r := recall.NewWithBudget(fourFacts(), budget)

		plain, controls, err := r.RecallWithControls(ctx, []string{"p1"}, "Marta", domain.AsOf{}, "", recall.Controls{})
		if err != nil {
			t.Fatal(err)
		}
		if controls.Explain {
			t.Fatal("an explanation was reported for a recall that did not ask for one")
		}
		if plain.Explain != nil {
			t.Fatalf("an explanation came back unasked: %+v", plain.Explain)
		}

		explained, controls, err := r.RecallWithControls(ctx, []string{"p1"}, "Marta", domain.AsOf{}, "",
			recall.Controls{Explain: true})
		if err != nil {
			t.Fatal(err)
		}
		if !controls.Explain {
			t.Fatal("the controls did not report the explanation back")
		}
		if explained.Explain == nil {
			t.Fatal("no explanation came back for a recall that asked")
		}

		// Compared field by field over everything the caller is given, rather than with a
		// whole-value comparison that the new field would always fail.
		if explained.Characters != plain.Characters || explained.Truncated != plain.Truncated ||
			len(explained.Facts) != len(plain.Facts) || len(explained.Anchors) != len(plain.Anchors) ||
			len(explained.Degraded) != len(plain.Degraded) || explained.Reach.Anchored != plain.Reach.Anchored {
			t.Fatalf("under %+v the explanation changed the bundle:\n with: %+v\n without: %+v",
				budget, explained, plain)
		}
		for i := range plain.Facts {
			if explained.Facts[i].ID != plain.Facts[i].ID {
				t.Fatalf("under %+v fact %d is %q with an explanation and %q without",
					budget, i, explained.Facts[i].ID, plain.Facts[i].ID)
			}
		}
	}
}

// An explanation says what each cut took, and which cut took it.
//
// The budget cut was the one nothing could answer from outside: the bundle said it happened and not
// what it dropped, so a caller seeing four facts become two had no way to tell a thin memory from a
// tight allowance. The row bound is the other cut, and they call for different responses — more
// characters returns more facts, a bounded neighbourhood returns the same ones.
func TestAnExplanationNamesWhatEachCutDropped(t *testing.T) {
	ctx := context.Background()
	// Three rows is below the four on offer, so the read's own bound cuts one; the budget then fits
	// some of the three that survived and cuts the rest.
	r := recall.NewWithBudget(fourFacts(), recall.Budget{Characters: 220, MaxRows: 3})
	bundle, _, err := r.RecallWithControls(ctx, []string{"p1"}, "Marta", domain.AsOf{}, "",
		recall.Controls{Explain: true})
	if err != nil {
		t.Fatal(err)
	}
	facts := stage(t, bundle.Explain, recall.SurfaceFacts)
	if !facts.Ran {
		t.Fatal("the facts stage is reported as not run")
	}
	if facts.Kept != len(bundle.Facts) {
		t.Fatalf("the explanation kept %d and the bundle holds %d", facts.Kept, len(bundle.Facts))
	}
	if facts.Offered != facts.Kept+len(facts.Dropped) {
		t.Fatalf("%d offered, %d kept, %d dropped: the three do not add up", facts.Offered, facts.Kept, len(facts.Dropped))
	}
	cuts := map[string]int{}
	for _, d := range facts.Dropped {
		cuts[d.Cut]++
		if d.Ref == "" || d.Characters <= 0 {
			t.Fatalf("a dropped candidate says nothing about itself: %+v", d)
		}
	}
	if cuts[domain.ExplainCutRows] == 0 || cuts[domain.ExplainCutBudget] == 0 {
		t.Fatalf("both cuts fired and the explanation names %v", cuts)
	}
	// Every dropped fact is one the caller did not get.
	returned := map[string]bool{}
	for _, f := range bundle.Facts {
		returned[f.ID] = true
	}
	for _, d := range facts.Dropped {
		if returned[d.Ref] {
			t.Fatalf("%s is reported as dropped and is in the bundle", d.Ref)
		}
	}
}

// An explanation carries no text the bundle withheld.
//
// A fact the caller's own budget cut is a fact they were not given. Naming its words in the
// explanation would hand it back through the other door, which would make the budget a suggestion and
// an `explain` recall a way around it. What a dropped candidate carries is a reference, a size and a
// score — enough to look it up through record inspection, which is authorised in its own right.
func TestAnExplanationCarriesNoTextTheBundleWithheld(t *testing.T) {
	ctx := context.Background()
	r := recall.NewWithBudget(fourFacts(), recall.Budget{Characters: 220, MaxRows: 10})
	bundle, _, err := r.RecallWithControls(ctx, []string{"p1"}, "Marta", domain.AsOf{}, "",
		recall.Controls{Explain: true})
	if err != nil {
		t.Fatal(err)
	}
	facts := stage(t, bundle.Explain, recall.SurfaceFacts)
	if len(facts.Dropped) == 0 {
		t.Fatal("nothing was dropped, so this test would prove nothing")
	}
	returned := map[string]bool{}
	for _, f := range bundle.Facts {
		returned[f.ID] = true
	}
	for _, d := range facts.Dropped {
		for _, withheld := range fourFacts().facts {
			if returned[withheld.ID] {
				continue
			}
			// The reference is an identifier, and the statement is text. A reference that contained
			// the statement would be the leak this is about.
			if strings.Contains(d.Ref, withheld.Statement) || strings.Contains(d.Ref, withheld.Evidence.Quote) {
				t.Fatalf("a dropped candidate carried the words the budget withheld: %q", d.Ref)
			}
		}
	}
}

// explainSemantic proposes one entity by meaning, with the score it was proposed at.
type explainSemantic struct{ recall.Semantic }

func (explainSemantic) EntityAnchors(context.Context, string, string, int) ([]domain.Anchor, error) {
	return []domain.Anchor{{
		Entity: domain.Entity{ID: "e9", CanonicalName: "Ensera"}, Matched: "semantic:0.77", Similarity: 0.77,
	}}, nil
}

func (explainSemantic) Reports(context.Context, string, string, int, int) ([]domain.ReportHit, error) {
	return nil, nil
}

func (explainSemantic) Passages(context.Context, string, string, int, []string, string) ([]domain.Passage, error) {
	return nil, nil
}

// nothingNamed answers no anchor by name, so the meaning arm is the one that proposes.
type nothingNamed struct{ explainStore }

func (nothingNamed) AnchorsForSubject(context.Context, []string, []string, string) ([]domain.Anchor, error) {
	return nil, nil
}

// An explanation says which arm proposed each anchor, and what it scored.
//
// With two arms proposing anchors, an unexpected anchor is the first thing to look at and the bundle
// cannot say where it came from: the published `matched` carries the meaning arm's score as a string
// because version 1 froze it that way, and an explanation needs the arm and the number apart.
func TestAnExplanationSaysWhichArmProposedEachAnchor(t *testing.T) {
	ctx := context.Background()
	byName := recall.NewWithBudget(fourFacts(), recall.DefaultBudget())
	bundle, _, err := byName.RecallWithControls(ctx, []string{"p1"}, "Marta", domain.AsOf{}, "",
		recall.Controls{Explain: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Explain.Anchors) != 1 {
		t.Fatalf("expected the one anchor the question named: %+v", bundle.Explain.Anchors)
	}
	named := bundle.Explain.Anchors[0]
	if named.Arm != domain.ExplainArmName || named.Matched != "marta" || named.Score != 0 || !named.Kept {
		t.Fatalf("the name arm's anchor is reported as %+v", named)
	}
	if named.Facts != len(bundle.Facts) {
		t.Fatalf("the anchor brought %d facts and the explanation says %d", len(bundle.Facts), named.Facts)
	}

	byMeaning := recall.NewWithBudget(nothingNamed{fourFacts()}, recall.DefaultBudget()).
		WithSemantic(explainSemantic{})
	bundle, _, err = byMeaning.RecallWithControls(ctx, []string{"p1"}, "where do people work", domain.AsOf{}, "",
		recall.Controls{Explain: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Explain.Anchors) != 1 {
		t.Fatalf("expected the one anchor the meaning arm proposed: %+v", bundle.Explain.Anchors)
	}
	proposed := bundle.Explain.Anchors[0]
	if proposed.Arm != domain.ExplainArmMeaning || proposed.Score != 0.77 || proposed.Matched != "" {
		t.Fatalf("the meaning arm's anchor is reported as %+v", proposed)
	}
}

// A stage that was selected and did not run is present and says so.
//
// Absent and "ran: false" are different answers — one says the surface was selected and skipped, the
// other says nothing at all — and a caller that cannot tell them apart cannot tell a surface that
// found nothing from one the deployment cannot do. Same discipline as `degraded`.
func TestAnExplanationNamesAStageThatDidNotRunRatherThanOmittingIt(t *testing.T) {
	ctx := context.Background()
	// No semantic surface configured, so reports and passages cannot run; facts can.
	r := recall.NewWithBudget(fourFacts(), recall.DefaultBudget())
	bundle, _, err := r.RecallWithControls(ctx, []string{"p1"}, "Marta", domain.AsOf{}, "",
		recall.Controls{Explain: true, Surfaces: []string{recall.SurfaceFacts, recall.SurfaceReports, recall.SurfacePassages}})
	if err != nil {
		t.Fatal(err)
	}
	for _, surface := range []string{recall.SurfaceFacts, recall.SurfaceReports, recall.SurfacePassages} {
		s := stage(t, bundle.Explain, surface)
		switch surface {
		case recall.SurfaceFacts:
			if !s.Ran || s.Kept == 0 {
				t.Fatalf("the facts stage ran and is reported as %+v", s)
			}
		default:
			if s.Ran || s.Offered != 0 || len(s.Dropped) != 0 {
				t.Fatalf("the %s stage could not run and is reported as %+v", surface, s)
			}
		}
	}
	// And the stage the caller deselected is not in the explanation at all: it was never a stage of
	// this recall.
	bundle, _, err = r.RecallWithControls(ctx, []string{"p1"}, "Marta", domain.AsOf{}, "",
		recall.Controls{Explain: true, Surfaces: []string{recall.SurfaceFacts}})
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Explain.Stages) != 1 || bundle.Explain.Stages[0].Surface != recall.SurfaceFacts {
		t.Fatalf("a deselected surface is in the explanation: %+v", bundle.Explain.Stages)
	}
}

// An explanation of a real recall agrees with the bundle it came with.
//
// The tests above drive the accounting with a stub, which is right for accounting and proves nothing
// about a real read. This one runs the journey against a real deployment — a turn appended, formed,
// and recalled — and holds the explanation against what the caller actually received: the anchor the
// question named, the facts it brought, and nothing claimed to be dropped that is in the bundle.
func TestAnExplanationOfARealRecallMatchesTheBundleItCameWith(t *testing.T) {
	ctx := context.Background()
	_, _, recaller := world(t, "rc_explain",
		turn("p1", "Marta works at Ensera, by the way."))

	bundle, controls, err := recaller.RecallWithControls(ctx, []string{"p1"}, "Where does Marta work?",
		domain.AsOf{}, "", recall.Controls{Explain: true})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if !controls.Explain || bundle.Explain == nil {
		t.Fatalf("no explanation came back: controls=%+v", controls)
	}
	if len(bundle.Facts) == 0 {
		t.Fatal("the fixture produced no facts, so this test would prove nothing")
	}

	facts := stage(t, bundle.Explain, recall.SurfaceFacts)
	if !facts.Ran || facts.Kept != len(bundle.Facts) || facts.Offered < facts.Kept {
		t.Fatalf("the facts stage says %+v and the bundle holds %d facts", facts, len(bundle.Facts))
	}
	if len(facts.Dropped) != facts.Offered-facts.Kept {
		t.Fatalf("%d offered, %d kept, %d dropped", facts.Offered, facts.Kept, len(facts.Dropped))
	}

	// Every anchor the bundle shows is in the explanation, marked kept, with the facts it brought;
	// and the arm is the name arm, because the question named a term the deployment holds.
	byID := map[string]domain.ExplainedAnchor{}
	for _, a := range bundle.Explain.Anchors {
		byID[a.EntityID] = a
	}
	for _, a := range bundle.Anchors {
		got, ok := byID[a.Entity.ID]
		if !ok {
			t.Fatalf("the bundle's anchor %q is not in the explanation", a.Entity.CanonicalName)
		}
		if !got.Kept || got.Arm != domain.ExplainArmName || got.Matched == "" {
			t.Fatalf("an anchor the question named is reported as %+v", got)
		}
		if got.Facts != bundle.Reach.FactsPerAnchor[a.Entity.ID] {
			t.Fatalf("the explanation says %d facts for %q and reach says %d",
				got.Facts, a.Entity.CanonicalName, bundle.Reach.FactsPerAnchor[a.Entity.ID])
		}
	}
}
