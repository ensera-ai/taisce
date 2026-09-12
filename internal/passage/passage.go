// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

// Package passage retrieves source messages without turning their similarity into asserted facts.
// It bounds requests and binds a query provider to the operator's active model generation. Storage
// owns source consistency; the transport owns project authorization and credential auditing.
package passage

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

var ErrInvalidQuery = errors.New("invalid passage query")
var ErrProvider = errors.New("passage embedding provider failed")
var ErrConfiguration = errors.New("passage retrieval is not configured")

type Embedder interface {
	Embed(context.Context, []string) ([][]float32, error)
}
type Store interface {
	Active(context.Context, string) (pg.EmbeddingGeneration, error)
	SearchEvidence(context.Context, string, pg.EmbeddingModel, []float32, pg.MessageSearchOptions) (pg.MessageSearchResult, error)
}

// Binding carries only configuration identity. Dimensions come from the selected generation and
// are checked against the actual provider result by the store; equal names alone never join spaces.
type Binding struct{ Name, Revision, EndpointHash string }
type Query struct {
	Question          string
	Limit, Candidates int
	Subject, Role     string
}
type Page struct {
	GenerationID         string            `json:"generation_id"`
	ThroughOffset        int64             `json:"through_offset"`
	CoveredThroughOffset int64             `json:"covered_through_offset"`
	BuildState           string            `json:"build_state"`
	Approximate          bool              `json:"approximate"`
	Passages             []pg.MessageMatch `json:"passages"`
}
type Retriever struct {
	store    Store
	embedder Embedder
	binding  Binding
}

func New(store Store, embedder Embedder, binding Binding) (*Retriever, error) {
	// Validate the configuration fields before any provider call. The placeholder dimension is not
	// persisted or sent to inference; a request replaces it with the active generation's dimension.
	model := pg.EmbeddingModel{Name: binding.Name, Revision: binding.Revision, EndpointHash: binding.EndpointHash, Dimensions: 1}
	if store == nil || embedder == nil || model.Validate() != nil {
		return nil, ErrConfiguration
	}
	return &Retriever{store: store, embedder: embedder, binding: binding}, nil
}

func (q Query) Validate() error {
	if len(q.Question) > domain.MaxRecallQuestionBytes || strings.TrimSpace(q.Question) == "" || !utf8.ValidString(q.Question) || strings.ContainsRune(q.Question, 0) {
		return ErrInvalidQuery
	}
	if q.Limit < 1 || q.Limit > MaxLimit || q.Candidates < 0 || q.Candidates > MaxCandidates || (q.Candidates > 0 && q.Candidates < q.Limit) {
		return ErrInvalidQuery
	}
	if len(q.Subject) > 1024 || !utf8.ValidString(q.Subject) || strings.ContainsRune(q.Subject, 0) {
		return ErrInvalidQuery
	}
	switch q.Role {
	case "", "user", "assistant", "system", "tool":
	default:
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
	model := pg.EmbeddingModel{Name: r.binding.Name, Revision: r.binding.Revision, EndpointHash: r.binding.EndpointHash, Dimensions: current.Model.Dimensions}
	if model.Identity() != current.Model.Identity() {
		return Page{}, pg.ErrEmbeddingConflict
	}
	vectors, err := r.embedder.Embed(ctx, []string{query.Question})
	if err != nil {
		return Page{}, fmt.Errorf("%w: %w", ErrProvider, err)
	}
	if len(vectors) != 1 {
		return Page{}, ErrProvider
	}
	result, err := r.store.SearchEvidence(ctx, scope, model, vectors[0], pg.MessageSearchOptions{Limit: query.Limit, Candidates: query.Candidates, Subject: query.Subject, Role: query.Role})
	if err != nil {
		return Page{}, err
	}
	return Page{GenerationID: result.Generation.ID, ThroughOffset: result.Generation.ThroughOffset, CoveredThroughOffset: result.Generation.CoveredThroughOffset, BuildState: result.Generation.State, Approximate: result.Approximate, Passages: result.Matches}, nil
}
