// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package recall_test

import (
	"context"
	"testing"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/recall"
)

// cappedStore is a walk that left something behind: it returns facts that fit comfortably inside
// every bound the caller set, and reports that its own fanout cap took more off a busy entity.
type cappedStore struct {
	facts  []domain.CitedFact
	capped bool
}

func (cappedStore) AnchorsForSubject(_ context.Context, _, terms []string, _ string) ([]domain.Anchor, error) {
	return []domain.Anchor{{Entity: domain.Entity{ID: "e1", Scope: "p1", CanonicalName: "Atlas"}, Matched: terms[0]}}, nil
}

func (s cappedStore) FactsAboutForSubject(context.Context, []string, []string, int, []string,
	domain.AsOf, int, string) ([]domain.CitedFact, bool, error) {
	return s.facts, s.capped, nil
}

func oneFact() []domain.CitedFact {
	return []domain.CitedFact{{ID: "f1", Subject: "Atlas", Predicate: "works_at", Object: "Ensera",
		Statement: "Atlas works at Ensera", SourceRole: "user", AnchoredOn: "e1",
		Evidence: domain.Evidence{Quote: "works at Ensera"}}}
}

// A bundle whose neighbourhood was capped says it was cut, and says which cut did it.
//
// Truncated alone would be true and useless here: a caller acting on it asks again with a larger
// budget, receives exactly the same facts, and has no way to learn why. The two cuts need telling
// apart because only one of them moves when the caller asks differently.
func TestACappedNeighbourhoodIsReportedAsACutTheCallerCannotWiden(t *testing.T) {
	budget := recall.Budget{Characters: 100000, MaxRows: 200}
	store := cappedStore{facts: oneFact(), capped: true}

	bundle, err := recall.NewWithBudget(store, budget).Recall(context.Background(), []string{"p1"}, "Atlas")
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Facts) != 1 {
		t.Fatalf("the bundle carried %d facts, want the one that fitted", len(bundle.Facts))
	}
	if !bundle.Truncated {
		t.Error("a capped walk produced a bundle that claims to be complete")
	}
	if !bundle.Reach.NeighbourhoodCapped {
		t.Error("the bundle does not say the traversal's own cap made the cut")
	}
}

// And a walk that was not capped says nothing, so the signal means something when it appears.
func TestAnUncappedNeighbourhoodSaysNothingAboutACut(t *testing.T) {
	budget := recall.Budget{Characters: 100000, MaxRows: 200}
	store := cappedStore{facts: oneFact(), capped: false}

	bundle, err := recall.NewWithBudget(store, budget).Recall(context.Background(), []string{"p1"}, "Atlas")
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Truncated {
		t.Error("a complete bundle claims to have been cut")
	}
	if bundle.Reach.NeighbourhoodCapped {
		t.Error("a complete bundle claims its neighbourhood was capped")
	}
}

// The budget cut and the cap are independent: a bundle can be cut by both, and a bundle cut only by
// the budget must not claim the neighbourhood was bounded.
func TestTheBudgetCutDoesNotClaimTheNeighbourhoodWasCapped(t *testing.T) {
	// A character budget too small for the one fact on offer.
	budget := recall.Budget{Characters: 4, MaxRows: 200}
	store := cappedStore{facts: oneFact(), capped: false}

	bundle, err := recall.NewWithBudget(store, budget).Recall(context.Background(), []string{"p1"}, "Atlas")
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Facts) != 0 {
		t.Fatalf("a fact larger than the whole budget was returned anyway: %d", len(bundle.Facts))
	}
	if !bundle.Truncated {
		t.Error("a budget cut was not reported")
	}
	if bundle.Reach.NeighbourhoodCapped {
		t.Error("a budget cut was reported as a capped neighbourhood")
	}
}
