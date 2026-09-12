// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package recall_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/recall"
)

// Fresh values per call reflect a store read, allowing the race test to isolate shared budget state.
type controlStore struct{}

func (controlStore) AnchorsForSubject(context.Context, []string, []string, string) ([]domain.Anchor, error) {
	return []domain.Anchor{{Entity: domain.Entity{ID: "e1", CanonicalName: "Marta"}}}, nil
}
func (controlStore) FactsAboutForSubject(context.Context, []string, []string, int, []string, domain.AsOf, int, string) ([]domain.CitedFact, error) {
	return []domain.CitedFact{{ID: "f1", Subject: "م", Predicate: "p", Object: "🌍", Statement: "ع", SourceRole: "tool", Path: []string{"أ", "ب"}, Via: []string{"c"}, Evidence: domain.Evidence{Quote: "ح", Context: domain.Context{Text: "د"}}}}, nil
}

func TestRecallControlsAreIndependentAcrossConcurrentClients(t *testing.T) {
	r := recall.NewWithBudget(controlStore{}, recall.Budget{Characters: 100, MaxRows: 2})
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			limit := 1
			want := 0
			if i%2 == 0 {
				limit = 100
				want = 1
			}
			b, c, err := r.RecallWithControls(context.Background(), []string{"p1"}, "Marta", domain.AsOf{}, "", recall.Controls{MaxCharacters: &limit, SourceRoles: []string{"tool"}, Hops: 999})
			if err != nil || len(b.Facts) != want || b.Characters > limit || c.MaxCharacters != limit || c.MaxRows != 2 || c.Hops != 2 || b.Truncated != (want == 0) {
				errs <- fmt.Errorf("mixed request controls: %+v %+v %v", b, c, err)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	_, c, err := r.RecallWithControls(context.Background(), nil, "Marta", domain.AsOf{}, "", recall.Controls{})
	if err != nil || c.MaxCharacters != 100 || c.SourceRoles[0] != "user" || c.Hops != 1 {
		t.Fatal("request mutated server defaults")
	}
}

func TestRecallCharacterAccountingCountsUnicodeRolesAndPaths(t *testing.T) {
	r := recall.NewWithBudget(controlStore{}, recall.Budget{Characters: 100, MaxRows: 2})
	// Six single-code-point text values, the four-letter source label and a three-character path.
	want := 13
	b, _, err := r.RecallWithControls(context.Background(), []string{"p1"}, "Marta", domain.AsOf{}, "", recall.Controls{MaxCharacters: &want})
	if err != nil || len(b.Facts) != 1 || b.Characters != want || b.Truncated {
		t.Fatalf("wrong Unicode accounting: %+v %v", b, err)
	}
	small := want - 1
	b, _, err = r.RecallWithControls(context.Background(), []string{"p1"}, "Marta", domain.AsOf{}, "", recall.Controls{MaxCharacters: &small})
	if err != nil || len(b.Facts) != 0 || b.Characters != 0 || !b.Truncated {
		t.Fatalf("single oversized fact exceeded allowance: %+v %v", b, err)
	}
}
