// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package inference

import (
	"context"
	"sync"
)

// One question reaches up to three retrievers, and each of them embeds it.
//
// Entity candidates, report candidates and passages each search their own generation, so each builds
// its own model identity and asks for its own vector. The generations are genuinely separate; the
// model is not. A deployment configures one embedding model — `TAISCE_INFERENCE_EMBEDDING_MODEL` at
// one endpoint — so a recall that consults all three surfaces sends the same string to the same
// endpoint three times, on the read path, with a caller waiting.
//
// This memo removes the repeats and changes nothing else. Every surface still reads its own active
// generation, still builds its own identity, and every store still re-checks that identity inside
// the transaction that reads the vectors. A surface that would have refused still refuses, for the
// same reason, at the same point.
//
// # Why it lives for exactly one request, and not a moment longer
//
// A memo that outlived the request would be a second place a caller's question is kept: with its own
// lifetime, its own staleness, and its own obligation under an erasure — to save a round trip that
// somebody else's question never makes. So it is created when a request is admitted and discarded
// when the request ends, and there is deliberately no eviction policy, no size bound beyond the
// request, and no sharing between requests.
//
// # Why the key is the space and not the text
//
// A vector means something only in the space that produced it. The key is the model name and the
// endpoint that answered, so two embedders configured differently cannot read each other's vectors
// however identical the text. The API key is not part of the key: it does not change the space, and
// a secret has no business in a map key.
//
// # What this does not cover
//
// Only a single input is memoised. A batch is the write path's call — every entity in a scope, every
// report in a rebuild — and two batches share nothing worth the bookkeeping. And a request that was
// never given a memo simply embeds per surface, as it did before: a missing memo costs a round trip
// and cannot produce a wrong vector, which is the only failure worth designing against here.
type questionMemo struct {
	mu      sync.Mutex
	vectors map[string][]float32
}

// questionMemoKey is unexported and of an unexported type, so nothing outside this package can put a
// value where the memo is read from.
type questionMemoKey struct{}

// WithQuestionMemo scopes one embedding memo to one unit of work.
//
// Called once where every protected request is admitted, so no route can be added that forgets it.
// Calling it again on a context that already carries one returns that context unchanged: a nested
// scope would quietly start a second memo and lose the sharing this exists for.
func WithQuestionMemo(ctx context.Context) context.Context {
	if ctx.Value(questionMemoKey{}) != nil {
		return ctx
	}
	return context.WithValue(ctx, questionMemoKey{}, &questionMemo{vectors: make(map[string][]float32, 1)})
}

func questionMemoFrom(ctx context.Context) *questionMemo {
	memo, _ := ctx.Value(questionMemoKey{}).(*questionMemo)
	return memo
}

// lookup returns a copy, because the caller of Embed owns what it is handed. Three retrievers
// holding one slice would make a mutation by any of them a defect in the other two, and the cost of
// ruling that out is one copy of a few kilobytes.
func (m *questionMemo) lookup(key string) ([]float32, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	vector, ok := m.vectors[key]
	if !ok {
		return nil, false
	}
	return append(make([]float32, 0, len(vector)), vector...), true
}

func (m *questionMemo) keep(key string, vector []float32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.vectors[key] = append(make([]float32, 0, len(vector)), vector...)
}

// questionMemoKeyFor names the space a vector was computed in, then the text.
func (e *Embedder) questionMemoKeyFor(input string) string {
	endpoint, _ := e.config.EmbeddingsAt()
	return e.config.EmbeddingModel + "\x00" + endpoint + "\x00" + input
}
