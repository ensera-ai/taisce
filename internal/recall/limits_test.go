// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package recall_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/recall"
)

func TestOversizedQuestionsAreRefusedBeforeAccessingAStore(t *testing.T) {
	var terms []string
	for i := 0; i < 150; i++ {
		terms = append(terms, fmt.Sprintf("word%d", i))
	}
	for _, tc := range []struct {
		question string
		want     error
	}{
		{strings.Repeat("x", domain.MaxRecallQuestionBytes+1), domain.ErrRecallQuestionSize},
		{strings.Repeat("é", domain.MaxRecallQuestionBytes/2+1), domain.ErrRecallQuestionSize},
		{strings.Join(terms, " "), domain.ErrRecallTerms},
	} {
		// A nil store proves refusal precedes any backend operation.
		_, err := recall.NewWithBudget(nil, recall.DefaultBudget()).Recall(context.Background(), []string{"p1"}, tc.question)
		if !errors.Is(err, tc.want) {
			t.Fatalf("want %v, got %v", tc.want, err)
		}
	}
}

type overflowingAnchorStore struct{ stubStore }

func (overflowingAnchorStore) AnchorsForSubject(context.Context, []string, []string, string) ([]domain.Anchor, error) {
	return make([]domain.Anchor, domain.MaxRecallMatches+1), nil
}
func (overflowingAnchorStore) FactsAboutForSubject(context.Context, []string, []string, int, []string, domain.AsOf, int, string) ([]domain.CitedFact, error) {
	panic("over-limit anchors reached traversal")
}

func TestAnOverproducingStoreCannotStartUnboundedTraversal(t *testing.T) {
	b, err := recall.NewWithBudget(overflowingAnchorStore{}, recall.DefaultBudget()).Recall(context.Background(), []string{"p1"}, "known")
	if !errors.Is(err, domain.ErrRecallMatches) || len(b.Anchors) != 0 || len(b.Facts) != 0 {
		t.Fatalf("partial recall: %+v %v", b, err)
	}
}
