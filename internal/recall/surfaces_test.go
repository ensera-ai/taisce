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

func (s *scriptedSemantic) Passages(_ context.Context, _, _ string, _ int, _ []string) ([]domain.Passage, error) {
	s.calls = append(s.calls, "passages")
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
