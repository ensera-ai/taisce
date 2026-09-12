// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package recall_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/recall"
)

// A scripted semantic surface: what each method returns is set by the test, and every call is
// recorded, so a test can assert that a surface was consulted or was not.
type scriptedSemantic struct {
	anchors  []domain.Anchor
	reports  []domain.ReportHit
	passages []domain.Passage
	fail     map[string]bool
	calls    []string
	subjects []string
}

func (s *scriptedSemantic) EntityAnchors(_ context.Context, _, _ string, limit int) ([]domain.Anchor, error) {
	s.calls = append(s.calls, "anchors")
	if s.fail["anchors"] {
		return nil, errors.New("refused")
	}
	if len(s.anchors) > limit {
		return s.anchors[:limit], nil
	}
	return s.anchors, nil
}

func (s *scriptedSemantic) Reports(_ context.Context, _, _ string, _, _ int) ([]domain.ReportHit, error) {
	s.calls = append(s.calls, "reports")
	if s.fail["reports"] {
		return nil, errors.New("refused")
	}
	return s.reports, nil
}

func (s *scriptedSemantic) Passages(_ context.Context, _, _ string, _ int, _ []string, subject string) ([]domain.Passage, error) {
	s.calls = append(s.calls, "passages")
	s.subjects = append(s.subjects, subject)
	if s.fail["passages"] {
		return nil, errors.New("refused")
	}
	return s.passages, nil
}

func composed(t *testing.T, store recall.Store, sem recall.Semantic, controls recall.Controls) (domain.Bundle, recall.EffectiveControls) {
	t.Helper()
	r := recall.NewWithBudget(store, recall.Budget{Characters: 400, MaxRows: 50}).WithSemantic(sem)
	bundle, effective, err := r.RecallWithControls(context.Background(), []string{"p1"}, "what is going on with the dublin office", domain.AsOf{}, "", controls)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	return bundle, effective
}

// With no exact anchor and no semantic surfaces, a caller who selected surfaces by name is told
// which did not run, rather than getting an empty answer that looks like ignorance. A caller who
// named none gets the exact path's answer and nothing about surfaces, as before.
func TestAnUnanchoredQuestionNamesTheSurfacesThatDidNotRun(t *testing.T) {
	plain, _ := composed(t, emptyAnchorStore{}, nil, recall.Controls{})
	if len(plain.Degraded) != 0 {
		t.Fatalf("a default recall on a deployment without surfaces must report nothing degraded, got %v", plain.Degraded)
	}
	bundle, effective := composed(t, emptyAnchorStore{}, nil, recall.Controls{Surfaces: recall.AllSurfaces})
	if len(bundle.Facts) != 0 || len(bundle.Reports) != 0 || len(bundle.Passages) != 0 {
		t.Fatalf("nothing should answer without surfaces, got %+v", bundle)
	}
	for _, want := range []string{domain.DegradedSemanticAnchors, domain.DegradedReports, domain.DegradedPassages} {
		if !slices.Contains(bundle.Degraded, want) {
			t.Fatalf("missing degraded %q in %v", want, bundle.Degraded)
		}
	}
	if !slices.Equal(effective.Surfaces, recall.AllSurfaces) {
		t.Fatalf("all surfaces should be the default, got %v", effective.Surfaces)
	}
}

// A question that names nothing stored by name is anchored by meaning, and the anchor says so.
func TestASemanticAnchorStartsAWalkWhenNoNameMatched(t *testing.T) {
	sem := &scriptedSemantic{anchors: []domain.Anchor{{Entity: domain.Entity{ID: "e1", CanonicalName: "the Dublin office"}, Matched: "semantic:0.91"}}}
	bundle, _ := composed(t, emptyAnchorStore{}, sem, recall.Controls{Surfaces: []string{recall.SurfaceFacts}})
	if len(bundle.Anchors) != 1 || bundle.Anchors[0].Matched != "semantic:0.91" {
		t.Fatalf("expected the semantic anchor, got %+v", bundle.Anchors)
	}
	if !slices.Contains(sem.calls, "anchors") || slices.Contains(sem.calls, "reports") || slices.Contains(sem.calls, "passages") {
		t.Fatalf("only the anchor surface was selected, calls were %v", sem.calls)
	}
}

// A thematic question with no anchor is answered from reports, each carrying its sources, and the
// reach still says nothing was anchored because that is still true.
func TestAThematicQuestionWithNoAnchorIsAnsweredFromReportsWithSources(t *testing.T) {
	sem := &scriptedSemantic{reports: []domain.ReportHit{
		{CommunityID: "c1", Title: "The Dublin office move", Summary: "The office is moving in March.", Sources: []string{"o1", "o2"}},
		{CommunityID: "c2", Parent: "c1", Level: 1, Title: "The lease", Summary: "A lease was signed.", Sources: []string{"o3"}},
	}}
	bundle, _ := composed(t, emptyAnchorStore{}, sem, recall.Controls{})
	if len(bundle.Reports) != 2 || len(bundle.Reports[0].Sources) != 2 || bundle.Reports[1].Parent != "c1" {
		t.Fatalf("expected both reports with their sources and lineage, got %+v", bundle.Reports)
	}
	if bundle.Reach.Anchored != 0 || bundle.Characters == 0 {
		t.Fatalf("reach and cost must still be reported: %+v", bundle.Reach)
	}
}

// Passages fill what the budget has left and are never facts: a tool message's words come back as a
// passage with its role, and the facts list is untouched by them.
func TestPassagesFillTheRemainingBudgetAndAreNeverFacts(t *testing.T) {
	sem := &scriptedSemantic{passages: []domain.Passage{
		{ChunkID: "k1", SourceID: "o9", Role: "tool", Quote: "Acme Corp is an approved supplier."},
	}}
	bundle, _ := composed(t, emptyAnchorStore{}, sem, recall.Controls{Surfaces: []string{recall.SurfacePassages}})
	if len(bundle.Passages) != 1 || bundle.Passages[0].Role != "tool" || len(bundle.Facts) != 0 {
		t.Fatalf("expected one labelled passage and no fact, got %+v", bundle)
	}
	if slices.Contains(sem.calls, "reports") || slices.Contains(sem.calls, "anchors") {
		t.Fatalf("surfaces the caller left out must not run, calls were %v", sem.calls)
	}
}

// A surface that refuses is named as degraded and the rest of the bundle still answers.
func TestARefusingSurfaceIsDegradedNotFatal(t *testing.T) {
	sem := &scriptedSemantic{fail: map[string]bool{"reports": true}, passages: []domain.Passage{{ChunkID: "k1", Role: "user", Quote: "hello"}}}
	bundle, _ := composed(t, emptyAnchorStore{}, sem, recall.Controls{})
	if !slices.Contains(bundle.Degraded, domain.DegradedReports) || len(bundle.Passages) != 1 {
		t.Fatalf("expected reports degraded and passages answering, got degraded=%v passages=%d", bundle.Degraded, len(bundle.Passages))
	}
}

// The budget is one budget: reports and passages are cut by the characters the facts left.
func TestTheComposedSurfacesShareOneBudget(t *testing.T) {
	long := make([]byte, 390)
	for i := range long {
		long[i] = 'x'
	}
	sem := &scriptedSemantic{
		reports:  []domain.ReportHit{{CommunityID: "c1", Title: "t", Summary: string(long)}},
		passages: []domain.Passage{{ChunkID: "k1", Role: "user", Quote: "a passage that no longer fits"}},
	}
	bundle, _ := composed(t, emptyAnchorStore{}, sem, recall.Controls{})
	if len(bundle.Reports) != 1 || len(bundle.Passages) != 0 || !bundle.Truncated {
		t.Fatalf("expected the report to fit, the passage to be cut and the cut reported: %+v", bundle)
	}
}

// Surface names are a closed set: an unknown or repeated name is refused, and the exact path is not
// touched when only facts are selected and a name matched.
func TestSurfaceControlsAreRefusedWhenNotInTheSet(t *testing.T) {
	r := recall.NewWithBudget(emptyAnchorStore{}, recall.Budget{Characters: 400, MaxRows: 50})
	for _, bad := range [][]string{{"themes"}, {"facts", "facts"}, {}} {
		if _, _, err := r.RecallWithControls(context.Background(), []string{"p1"}, "anything", domain.AsOf{}, "", recall.Controls{Surfaces: bad}); !errors.Is(err, recall.ErrInvalidControls) {
			t.Fatalf("surfaces %v should be refused, got %v", bad, err)
		}
	}
}

// A meaning match proposes at most a fixed number of places to start, however many scopes are
// asked: the first scope that fills the cap ends the search, so a caller with many projects does
// not pay one embedding query per project for anchors it will not walk.
func TestSemanticAnchorsAreCappedAndStopAtTheCap(t *testing.T) {
	var proposed []domain.Anchor
	for i := 0; i < recall.MaxSemanticAnchors; i++ {
		proposed = append(proposed, domain.Anchor{Entity: domain.Entity{ID: "e" + string(rune('1'+i)), CanonicalName: "an office"}, Matched: "semantic:0.80"})
	}
	sem := &scriptedSemantic{anchors: proposed}
	r := recall.NewWithBudget(emptyAnchorStore{}, recall.Budget{Characters: 400, MaxRows: 50}).WithSemantic(sem)
	bundle, _, err := r.RecallWithControls(context.Background(), []string{"p1", "p2"}, "where is the office", domain.AsOf{}, "", recall.Controls{Surfaces: []string{recall.SurfaceFacts}})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(bundle.Anchors) != recall.MaxSemanticAnchors {
		t.Fatalf("expected the cap, got %d anchors", len(bundle.Anchors))
	}
	if n := slices.Index(sem.calls, "anchors"); n < 0 || slices.Contains(sem.calls[n+1:], "anchors") {
		t.Fatalf("the second scope must not be asked once the cap is full, calls were %v", sem.calls)
	}
}

// A report that does not fit what the budget has left is cut whole and the cut is reported: half a
// report is not a smaller report, it is a different one.
func TestAReportLargerThanTheRemainingBudgetIsCutWholeAndReported(t *testing.T) {
	long := make([]byte, 500)
	for i := range long {
		long[i] = 'x'
	}
	sem := &scriptedSemantic{reports: []domain.ReportHit{{CommunityID: "c1", Title: "t", Summary: string(long)}}}
	bundle, _ := composed(t, emptyAnchorStore{}, sem, recall.Controls{Surfaces: []string{recall.SurfaceReports}})
	if len(bundle.Reports) != 0 || !bundle.Truncated {
		t.Fatalf("expected no report and the cut reported, got %+v", bundle)
	}
}

// The passage surface refusing is named as degraded like any other, and the reports still answer.
func TestARefusingPassageSurfaceIsDegradedAndReportsStillAnswer(t *testing.T) {
	sem := &scriptedSemantic{fail: map[string]bool{"passages": true},
		reports: []domain.ReportHit{{CommunityID: "c1", Title: "t", Summary: "a theme"}}}
	bundle, _ := composed(t, emptyAnchorStore{}, sem, recall.Controls{})
	if !slices.Contains(bundle.Degraded, domain.DegradedPassages) || len(bundle.Reports) != 1 {
		t.Fatalf("expected passages degraded and the report answering, got degraded=%v reports=%d", bundle.Degraded, len(bundle.Reports))
	}
}

// Nothing to search is an empty answer, not an error, and a repeated word is one term: a term is
// a thing to look up, and looking the same thing up twice is the same lookup.
func TestNoScopesAnswerNothingAndARepeatedWordIsOneTerm(t *testing.T) {
	r := recall.NewWithBudget(emptyAnchorStore{}, recall.Budget{Characters: 400, MaxRows: 50})
	bundle, err := r.RecallForSubjectAsOf(context.Background(), nil, "where is the office", nil, domain.AsOf{}, 1, "")
	if err != nil || len(bundle.Anchors) != 0 || len(bundle.Facts) != 0 {
		t.Fatalf("no scopes must answer nothing without error, got %+v %v", bundle, err)
	}
	terms := recall.Terms("Dublin Dublin")
	if len(terms) != 2 || terms[0] != "dublin" || terms[1] != "dublin dublin" {
		t.Fatalf("expected the word once and the pair once, got %v", terms)
	}
}

// ── #235: a recall narrowed to one person is narrowed on every surface ────────────────────────

type namedStore struct{ facts []domain.CitedFact }

func (namedStore) AnchorsForSubject(context.Context, []string, []string, string) ([]domain.Anchor, error) {
	return []domain.Anchor{{Entity: domain.Entity{ID: "e-marta", Scope: "p1", CanonicalName: "Marta"}, Matched: "marta"}}, nil
}

func (s namedStore) FactsAboutForSubject(context.Context, []string, []string, int, []string, domain.AsOf, int, string) ([]domain.CitedFact, error) {
	return s.facts, nil
}

type semanticFactsStore struct{ facts []domain.CitedFact }

func (semanticFactsStore) AnchorsForSubject(context.Context, []string, []string, string) ([]domain.Anchor, error) {
	return nil, nil
}

func (s semanticFactsStore) FactsAboutForSubject(context.Context, []string, []string, int, []string, domain.AsOf, int, string) ([]domain.CitedFact, error) {
	return s.facts, nil
}

func holds(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// Passages are searched for that person only; reports, which span people and name nobody as their
// source, are withheld and the bundle says so; and a name the semantic surface proposed that this
// person has no fact about is not returned, because it is somebody else's memory. Without a subject
// the same recall consults reports and keeps the anchor.
func TestASubjectFilterReachesEverySurface(t *testing.T) {
	scripted := func() *scriptedSemantic {
		return &scriptedSemantic{
			anchors:  []domain.Anchor{{Entity: domain.Entity{ID: "e-dublin", Scope: "p1", CanonicalName: "Dublin"}}},
			reports:  []domain.ReportHit{{Title: "the office", Summary: "what everybody said"}},
			passages: []domain.Passage{{Quote: "a quote", Role: "user"}},
		}
	}
	sem := scripted()
	bundle, _, err := recall.NewWithBudget(emptyAnchorStore{}, recall.Budget{Characters: 400, MaxRows: 50}).WithSemantic(sem).
		RecallWithControls(context.Background(), []string{"p1"}, "what is going on with the dublin office",
			domain.AsOf{}, "someone", recall.Controls{Surfaces: recall.AllSurfaces, Themes: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(sem.subjects) == 0 || sem.subjects[0] != "someone" {
		t.Fatalf("passages were not searched for the person asked about: %v", sem.subjects)
	}
	if len(bundle.Reports) != 0 || holds(sem.calls, "reports") || !holds(bundle.Degraded, domain.DegradedReportsWithheld) {
		t.Fatalf("reports answered a recall narrowed to one person, or their absence went unsaid: %v %v", bundle.Degraded, sem.calls)
	}
	if len(bundle.Anchors) != 0 || bundle.Reach.Anchored != 0 {
		t.Fatalf("a proposed name this person has no fact about was returned: %+v", bundle.Anchors)
	}

	open := scripted()
	bundle, _, err = recall.NewWithBudget(emptyAnchorStore{}, recall.Budget{Characters: 400, MaxRows: 50}).WithSemantic(open).
		RecallWithControls(context.Background(), []string{"p1"}, "what is going on with the dublin office",
			domain.AsOf{}, "", recall.Controls{Surfaces: recall.AllSurfaces, Themes: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Reports) != 1 || len(bundle.Anchors) != 1 || holds(bundle.Degraded, domain.DegradedReportsWithheld) || len(open.subjects) == 0 || open.subjects[0] != "" {
		t.Fatalf("an unnarrowed recall lost a surface: reports %d anchors %d degraded %v", len(bundle.Reports), len(bundle.Anchors), bundle.Degraded)
	}
}

// Deselecting facts deselects them even when the question named something: no walk, no anchors, no
// facts — and the name it matched is still counted, so the reach does not claim it named nothing.
func TestDeselectingFactsReturnsNoFactsEvenWhenANameMatched(t *testing.T) {
	store := namedStore{facts: []domain.CitedFact{{ID: "f1", Scope: "p1", Subject: "Marta", Predicate: "works_at", Object: "Ensera",
		Statement: "Marta works at Ensera.", AnchoredOn: "e-marta"}}}
	sem := &scriptedSemantic{passages: []domain.Passage{{Quote: "a quote", Role: "user"}}}
	r := recall.NewWithBudget(store, recall.Budget{Characters: 400, MaxRows: 50}).WithSemantic(sem)
	bundle, _, err := r.RecallWithControls(context.Background(), []string{"p1"}, "Marta", domain.AsOf{}, "", recall.Controls{Surfaces: []string{recall.SurfacePassages}})
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Facts) != 0 || len(bundle.Anchors) != 0 || len(bundle.Passages) != 1 {
		t.Fatalf("deselected facts came back: facts %d anchors %d passages %d", len(bundle.Facts), len(bundle.Anchors), len(bundle.Passages))
	}
	if bundle.Reach.Anchored != 1 || bundle.Reach.NamedNothingKnown() {
		t.Fatalf("the matched name was not counted: %+v", bundle.Reach)
	}
	bundle, _, err = r.RecallWithControls(context.Background(), []string{"p1"}, "Marta", domain.AsOf{}, "", recall.Controls{Surfaces: []string{recall.SurfaceFacts}})
	if err != nil || len(bundle.Facts) != 1 {
		t.Fatalf("selecting facts did not return them: %d %v", len(bundle.Facts), err)
	}
}

// A bundle answered through a semantic anchor counts that anchor, so it does not also report that
// the question named nothing this memory holds; under a subject filter the anchor stays, because
// that person has a fact about it.
func TestASemanticAnchorThatAnswersIsCountedAsAnchored(t *testing.T) {
	store := semanticFactsStore{facts: []domain.CitedFact{{ID: "f1", Scope: "p1", Subject: "Marta", Predicate: "lives_in", Object: "Dublin",
		Statement: "Marta lives in Dublin.", AnchoredOn: "e-dublin"}}}
	for _, subject := range []string{"", "someone"} {
		sem := &scriptedSemantic{anchors: []domain.Anchor{{Entity: domain.Entity{ID: "e-dublin", Scope: "p1", CanonicalName: "Dublin"}}}}
		bundle, _, err := recall.NewWithBudget(store, recall.Budget{Characters: 400, MaxRows: 50}).WithSemantic(sem).
			RecallWithControls(context.Background(), []string{"p1"}, "where does she live", domain.AsOf{}, subject, recall.Controls{Surfaces: []string{recall.SurfaceFacts}})
		if err != nil {
			t.Fatal(err)
		}
		if len(bundle.Facts) != 1 || len(bundle.Anchors) != 1 || bundle.Reach.Anchored != 1 || bundle.Reach.NamedNothingKnown() {
			t.Fatalf("subject %q: facts %d anchors %d reach %+v", subject, len(bundle.Facts), len(bundle.Anchors), bundle.Reach)
		}
	}
}

// The same fact twice is one fact, never co-derived with itself.
func TestAFactIsNeverCoDerivedWithItself(t *testing.T) {
	f := domain.CitedFact{ID: "f1", Scope: "p1", Subject: "Marta", Predicate: "works_at", Object: "Ensera",
		Statement: "Marta works at Ensera.", AnchoredOn: "e-marta"}
	f.Evidence.SourceObservationID, f.Evidence.SourceOrdinal = "o1", 0
	bundle, _, err := recall.NewWithBudget(namedStore{facts: []domain.CitedFact{f, f}}, recall.Budget{Characters: 400, MaxRows: 50}).
		RecallWithControls(context.Background(), []string{"p1"}, "Marta", domain.AsOf{}, "", recall.Controls{Surfaces: []string{recall.SurfaceFacts}})
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range bundle.Facts {
		if len(got.CoDerivedWith) != 0 {
			t.Fatalf("a fact was marked co-derived with a copy of itself: %v", got.CoDerivedWith)
		}
	}
}
