// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package reportcandidate

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/infra/pg"
)

type candidateStoreStub struct {
	active    pg.ReportEmbeddingGeneration
	activeErr error
	findErr   error
	result    pg.ReportCandidateResult
	finds     int
}

func (s *candidateStoreStub) Active(context.Context, string) (pg.ReportEmbeddingGeneration, error) {
	return s.active, s.activeErr
}

func (s *candidateStoreStub) FindCandidates(context.Context, string, pg.EmbeddingModel, []float32,
	pg.ReportCandidateOptions) (pg.ReportCandidateResult, error) {
	s.finds++
	return s.result, s.findErr
}

type candidateEmbedderStub struct {
	calls   int
	vectors [][]float32
	err     error
}

func (e *candidateEmbedderStub) Embed(context.Context, []string) ([][]float32, error) {
	e.calls++
	if e.vectors == nil {
		e.vectors = [][]float32{{1}}
	}
	return e.vectors, e.err
}

func TestRetrieverConfigurationAndProviderFailuresAreExplicit(t *testing.T) {
	binding := Binding{Name: "report-model", Revision: "v1", EndpointHash: strings.Repeat("b", 64)}
	store := &candidateStoreStub{active: pg.ReportEmbeddingGeneration{State: "ready", Model: pg.EmbeddingModel{Dimensions: 1}}}
	embedder := &candidateEmbedderStub{}
	if _, err := New(nil, embedder, binding); !errors.Is(err, ErrConfiguration) {
		t.Fatalf("nil store: %v", err)
	}
	if _, err := New(store, nil, binding); !errors.Is(err, ErrConfiguration) {
		t.Fatalf("nil embedder: %v", err)
	}
	if _, err := New(store, embedder, Binding{}); !errors.Is(err, ErrConfiguration) {
		t.Fatalf("empty binding: %v", err)
	}
	retriever, err := New(store, embedder, binding)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := retriever.Search(context.Background(), "bad scope", Query{Question: "growth theme", Limit: 1}); !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("invalid scope: %v", err)
	}
	if _, err := retriever.Search(context.Background(), "p1", Query{}); !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("invalid query: %v", err)
	}
	store.activeErr = errors.New("active failed")
	if _, err := retriever.Search(context.Background(), "p1", Query{Question: "growth theme", Limit: 1}); err == nil {
		t.Fatal("active generation failure was hidden")
	}
	store.activeErr = nil
	embedder.err = errors.New("provider failed")
	if _, err := retriever.Search(context.Background(), "p1", Query{Question: "growth theme", Limit: 1}); !errors.Is(err, ErrProvider) {
		t.Fatalf("provider error: %v", err)
	}
	embedder.err = nil
	embedder.vectors = [][]float32{}
	if _, err := retriever.Search(context.Background(), "p1", Query{Question: "growth theme", Limit: 1}); !errors.Is(err, ErrProvider) {
		t.Fatalf("missing provider vector: %v", err)
	}
	embedder.vectors = [][]float32{{1}}
	store.findErr = errors.New("candidate lookup failed")
	if _, err := retriever.Search(context.Background(), "p1", Query{Question: "growth theme", Limit: 1}); err == nil {
		t.Fatal("candidate lookup failure was hidden")
	}
	store.findErr = nil
	store.result = pg.ReportCandidateResult{Generation: pg.ReportEmbeddingGeneration{ID: "generation", TargetCount: 3, State: "ready"}, Candidates: []pg.ReportCandidate{}}
	page, err := retriever.Search(context.Background(), "p1", Query{Question: "growth theme", Limit: 1})
	if err != nil || page.GenerationID != "generation" || page.TargetCount != 3 || store.finds != 2 {
		t.Fatalf("successful page %+v err=%v finds=%d", page, err, store.finds)
	}
}

func TestSearchRefusesStaleGenerationBeforeProviderCall(t *testing.T) {
	store := &candidateStoreStub{active: pg.ReportEmbeddingGeneration{
		State: "stale", Model: pg.EmbeddingModel{Dimensions: 1},
	}}
	embedder := &candidateEmbedderStub{}
	retriever, err := New(store, embedder, Binding{
		Name: "report-model", Revision: "v1", EndpointHash: strings.Repeat("a", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = retriever.Search(context.Background(), "p1", Query{Question: "growth theme", Limit: 1})
	if !errors.Is(err, pg.ErrEmbeddingConflict) || embedder.calls != 0 || store.finds != 0 {
		t.Fatalf("stale search err=%v provider_calls=%d store_calls=%d", err, embedder.calls, store.finds)
	}
}
