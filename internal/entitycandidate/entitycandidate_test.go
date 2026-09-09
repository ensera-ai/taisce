// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package entitycandidate

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/infra/pg"
)

type candidateStoreStub struct {
	active pg.EntityEmbeddingGeneration
	err    error
	result pg.EntityCandidateResult
	finds  int
}

func (s *candidateStoreStub) Active(context.Context, string) (pg.EntityEmbeddingGeneration, error) {
	return s.active, s.err
}

func (s *candidateStoreStub) FindCandidates(context.Context, string, pg.EmbeddingModel, []float32,
	pg.EntityCandidateOptions) (pg.EntityCandidateResult, error) {
	s.finds++
	return s.result, s.err
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
	binding := Binding{Name: "entity-model", Revision: "v1", EndpointHash: strings.Repeat("b", 64)}
	store := &candidateStoreStub{active: pg.EntityEmbeddingGeneration{State: "ready", Model: pg.EmbeddingModel{Dimensions: 1}}}
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
	store.err = errors.New("active failed")
	if _, err := retriever.Search(context.Background(), "p1", Query{Question: "Ensera", Limit: 1}); err == nil {
		t.Fatal("active generation failure was hidden")
	}
	store.err = nil
	embedder.err = errors.New("provider failed")
	if _, err := retriever.Search(context.Background(), "p1", Query{Question: "Ensera", Limit: 1}); !errors.Is(err, ErrProvider) {
		t.Fatalf("provider error: %v", err)
	}
	embedder.err = nil
	embedder.vectors = [][]float32{}
	if _, err := retriever.Search(context.Background(), "p1", Query{Question: "Ensera", Limit: 1}); !errors.Is(err, ErrProvider) {
		t.Fatalf("missing provider vector: %v", err)
	}
	embedder.vectors = [][]float32{{1}}
	store.result = pg.EntityCandidateResult{Generation: pg.EntityEmbeddingGeneration{ID: "generation", TargetCount: 3, State: "ready"}, Candidates: []pg.EntityCandidate{}}
	page, err := retriever.Search(context.Background(), "p1", Query{Question: "Ensera", Limit: 1})
	if err != nil || page.GenerationID != "generation" || page.TargetCount != 3 || store.finds != 1 {
		t.Fatalf("successful page %+v err=%v finds=%d", page, err, store.finds)
	}
}

func TestSearchRefusesStaleGenerationBeforeProviderCall(t *testing.T) {
	store := &candidateStoreStub{active: pg.EntityEmbeddingGeneration{
		State: "stale", Model: pg.EmbeddingModel{Dimensions: 1},
	}}
	embedder := &candidateEmbedderStub{}
	retriever, err := New(store, embedder, Binding{
		Name: "entity-model", Revision: "v1", EndpointHash: strings.Repeat("a", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = retriever.Search(context.Background(), "p1", Query{Question: "Ensera", Limit: 1})
	if !errors.Is(err, pg.ErrEmbeddingConflict) || embedder.calls != 0 || store.finds != 0 {
		t.Fatalf("stale search err=%v provider_calls=%d store_calls=%d", err, embedder.calls, store.finds)
	}
}
