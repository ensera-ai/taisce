// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

// Package entitycandidate embeds a bounded question and returns possible retained identities.
// Similarity is a navigation hint only: this package cannot merge, rename or create an entity.
package entitycandidate

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
)

const DefaultLimit = 10
const MaxLimit = 32
const MaxCandidates = 1000

var ErrInvalidQuery = errors.New("invalid entity candidate query")
var ErrProvider = errors.New("entity candidate embedding provider failed")
var ErrConfiguration = errors.New("entity candidate retrieval is not configured")

type Embedder interface {
	Embed(context.Context, []string) ([][]float32, error)
}

type Store interface {
	Active(context.Context, string) (pg.EntityEmbeddingGeneration, error)
	FindCandidates(context.Context, string, pg.EmbeddingModel, []float32, pg.EntityCandidateOptions) (pg.EntityCandidateResult, error)
}

type Binding struct{ Name, Revision, EndpointHash string }

type Query struct {
	Question          string
	Limit, Candidates int
}

type Page struct {
	GenerationID string               `json:"generation_id"`
	TargetCount  int64                `json:"target_count"`
	BuildState   string               `json:"build_state"`
	Approximate  bool                 `json:"approximate"`
	Candidates   []pg.EntityCandidate `json:"candidates"`
}

type Retriever struct {
	store    Store
	embedder Embedder
	binding  Binding
}

func New(store Store, embedder Embedder, binding Binding) (*Retriever, error) {
	model := pg.EmbeddingModel{Name: binding.Name, Revision: binding.Revision, EndpointHash: binding.EndpointHash, Dimensions: 1}
	if store == nil || embedder == nil || model.Validate() != nil {
		return nil, ErrConfiguration
	}
	return &Retriever{store: store, embedder: embedder, binding: binding}, nil
}

func (q Query) Validate() error {
	if len(q.Question) > domain.MaxRecallQuestionBytes || strings.TrimSpace(q.Question) == "" ||
		!utf8.ValidString(q.Question) || strings.ContainsRune(q.Question, 0) {
		return ErrInvalidQuery
	}
	if q.Limit < 1 || q.Limit > MaxLimit || q.Candidates < 0 || q.Candidates > MaxCandidates ||
		(q.Candidates > 0 && q.Candidates < q.Limit) {
		return ErrInvalidQuery
	}
	return nil
}

func (r *Retriever) Search(ctx context.Context, scope string, query Query) (Page, error) {
	if err := query.Validate(); err != nil {
		return Page{}, err
	}
	if _, err := pg.NewSchema(scope); err != nil {
		return Page{}, ErrInvalidQuery
	}
	current, err := r.store.Active(ctx, scope)
	if err != nil {
		return Page{}, err
	}
	if current.State != "ready" {
		return Page{}, pg.ErrEmbeddingConflict
	}
	model := pg.EmbeddingModel{Name: r.binding.Name, Revision: r.binding.Revision,
		EndpointHash: r.binding.EndpointHash, Dimensions: current.Model.Dimensions}
	vectors, err := r.embedder.Embed(ctx, []string{query.Question})
	if err != nil {
		return Page{}, fmt.Errorf("%w: %w", ErrProvider, err)
	}
	if len(vectors) != 1 {
		return Page{}, ErrProvider
	}
	result, err := r.store.FindCandidates(ctx, scope, model, vectors[0], pg.EntityCandidateOptions{
		Limit: query.Limit, Candidates: query.Candidates,
	})
	if err != nil {
		return Page{}, err
	}
	return Page{GenerationID: result.Generation.ID, TargetCount: result.Generation.TargetCount,
		BuildState: result.Generation.State, Approximate: result.Approximate, Candidates: result.Candidates}, nil
}
