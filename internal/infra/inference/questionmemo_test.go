// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package inference_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/ensera-ai/taisce/internal/infra/inference"
)

// embeddingProvider is a provider that counts what it was asked and answers with a vector derived
// from the model it was asked for, so a vector from one space is recognisable in another.
func embeddingProvider(t *testing.T, calls *atomic.Int64) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var request struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		data := make([]map[string]any, len(request.Input))
		for i := range request.Input {
			// The first component names the space, so a shared vector is detectable.
			data[i] = map[string]any{"index": i, "embedding": []float32{float32(len(request.Model)), 1, 0}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	t.Cleanup(server.Close)
	return server
}

// Three retrievers ask for the same question in one request, and the provider is called once.
//
// This is the property the read path is paid for: entity candidates, report candidates and passages
// each embed the question they were given, and a deployment configures one embedding model, so
// without a request scope that is three identical calls to one endpoint with a caller waiting.
func TestOneQuestionAskedByEverySurfaceIsEmbeddedOnce(t *testing.T) {
	var calls atomic.Int64
	provider := embeddingProvider(t, &calls)
	embedder := inference.NewEmbedder(inference.Config{Endpoint: provider.URL, EmbeddingModel: "one-model"})

	ctx := inference.WithQuestionMemo(context.Background())
	var first []float32
	for surface := 0; surface < 3; surface++ {
		vectors, err := embedder.Embed(ctx, []string{"where does Hamza work"})
		if err != nil {
			t.Fatalf("surface %d: %v", surface, err)
		}
		if len(vectors) != 1 {
			t.Fatalf("surface %d received %d vectors", surface, len(vectors))
		}
		if surface == 0 {
			first = vectors[0]
			continue
		}
		if len(vectors[0]) != len(first) {
			t.Fatalf("surface %d received %d dimensions and the first %d", surface, len(vectors[0]), len(first))
		}
		for i := range first {
			if vectors[0][i] != first[i] {
				t.Fatalf("surface %d received a different vector at %d", surface, i)
			}
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("the provider was called %d times for one question", got)
	}
}

// What the memo is worth, stated as the behaviour without it: every surface embeds for itself.
//
// Named rather than left implicit because a request that is never given a scope must still answer —
// a missing memo costs a round trip and cannot produce a wrong vector.
func TestWithoutARequestScopeEverySurfaceEmbedsForItself(t *testing.T) {
	var calls atomic.Int64
	provider := embeddingProvider(t, &calls)
	embedder := inference.NewEmbedder(inference.Config{Endpoint: provider.URL, EmbeddingModel: "one-model"})

	for surface := 0; surface < 3; surface++ {
		if _, err := embedder.Embed(context.Background(), []string{"where does Hamza work"}); err != nil {
			t.Fatalf("surface %d: %v", surface, err)
		}
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("the provider was called %d times without a request scope, want 3", got)
	}
}

// A vector means something only in the space that produced it, so two differently configured
// embedders in one request never read each other's answer however identical the text.
func TestAVectorIsNeverSharedAcrossTwoEmbeddingSpaces(t *testing.T) {
	var calls atomic.Int64
	provider := embeddingProvider(t, &calls)
	ctx := inference.WithQuestionMemo(context.Background())

	short := inference.NewEmbedder(inference.Config{Endpoint: provider.URL, EmbeddingModel: "small"})
	long := inference.NewEmbedder(inference.Config{Endpoint: provider.URL, EmbeddingModel: "much-larger-model"})

	shortVector, err := short.Embed(ctx, []string{"the same question"})
	if err != nil {
		t.Fatal(err)
	}
	longVector, err := long.Embed(ctx, []string{"the same question"})
	if err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("the provider was called %d times for two spaces, want 2", got)
	}
	if shortVector[0][0] == longVector[0][0] {
		t.Fatalf("both spaces answered with %v, so one read the other's vector", shortVector[0][0])
	}
}

// The endpoint is part of the space as much as the model is: the same model name served by two hosts
// is two sets of weights as far as this process can know.
func TestTheEndpointIsPartOfTheSpace(t *testing.T) {
	var first, second atomic.Int64
	one, two := embeddingProvider(t, &first), embeddingProvider(t, &second)
	ctx := inference.WithQuestionMemo(context.Background())

	for _, endpoint := range []string{one.URL, two.URL} {
		embedder := inference.NewEmbedder(inference.Config{Endpoint: endpoint, EmbeddingModel: "same-name"})
		if _, err := embedder.Embed(ctx, []string{"the same question"}); err != nil {
			t.Fatal(err)
		}
	}
	if first.Load() != 1 || second.Load() != 1 {
		t.Fatalf("each endpoint must answer for itself; got %d and %d", first.Load(), second.Load())
	}
}

// A batch is the write path's call — every entity in a scope, every report in a rebuild — and two
// batches share nothing worth the bookkeeping. Stated as a test so the memo cannot grow into a
// write-path cache without somebody deciding to.
func TestABatchIsNotMemoised(t *testing.T) {
	var calls atomic.Int64
	provider := embeddingProvider(t, &calls)
	embedder := inference.NewEmbedder(inference.Config{Endpoint: provider.URL, EmbeddingModel: "one-model"})
	ctx := inference.WithQuestionMemo(context.Background())

	for attempt := 0; attempt < 2; attempt++ {
		if _, err := embedder.Embed(ctx, []string{"one entity", "another entity"}); err != nil {
			t.Fatal(err)
		}
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("the provider was called %d times for two identical batches, want 2", got)
	}
}

// A memo hit refuses exactly what a miss refuses. The alternative is an input that becomes
// acceptable on its second use within one request, which is a validation hole shaped like a cache.
func TestAnInputTheEmbedderRefusesIsRefusedEveryTime(t *testing.T) {
	var calls atomic.Int64
	provider := embeddingProvider(t, &calls)
	embedder := inference.NewEmbedder(inference.Config{Endpoint: provider.URL, EmbeddingModel: "one-model"})
	ctx := inference.WithQuestionMemo(context.Background())

	for attempt := 0; attempt < 2; attempt++ {
		if _, err := embedder.Embed(ctx, []string{"   "}); err == nil {
			t.Fatalf("attempt %d: an empty surface is not a thing to find", attempt)
		}
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("a refused input reached the provider %d times", got)
	}
}

// A caller owns what it is handed. Three retrievers holding one slice would make a mutation by any
// of them a defect in the other two.
func TestEachSurfaceOwnsTheVectorItIsGiven(t *testing.T) {
	var calls atomic.Int64
	provider := embeddingProvider(t, &calls)
	embedder := inference.NewEmbedder(inference.Config{Endpoint: provider.URL, EmbeddingModel: "one-model"})
	ctx := inference.WithQuestionMemo(context.Background())

	mine, err := embedder.Embed(ctx, []string{"a question"})
	if err != nil {
		t.Fatal(err)
	}
	mine[0][0] = 99

	theirs, err := embedder.Embed(ctx, []string{"a question"})
	if err != nil {
		t.Fatal(err)
	}
	if theirs[0][0] == 99 {
		t.Fatal("one surface's mutation reached another's vector")
	}
}

// Scoping twice is a mistake that would quietly start a second memo and lose the sharing the first
// one exists for, so the second call keeps the first scope.
func TestARequestIsGivenOneScopeHoweverOftenItIsAsked(t *testing.T) {
	var calls atomic.Int64
	provider := embeddingProvider(t, &calls)
	embedder := inference.NewEmbedder(inference.Config{Endpoint: provider.URL, EmbeddingModel: "one-model"})

	outer := inference.WithQuestionMemo(context.Background())
	if _, err := embedder.Embed(outer, []string{"a question"}); err != nil {
		t.Fatal(err)
	}
	inner := inference.WithQuestionMemo(outer)
	if _, err := embedder.Embed(inner, []string{"a question"}); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("the provider was called %d times, so a second scope was started", got)
	}
}

// Two requests share nothing. The memo is scoped to a request because a question held longer than
// that is a second place a caller's words live, with its own staleness and its own erasure
// obligation, to save a round trip somebody else's question never makes.
func TestTwoRequestsShareNoQuestion(t *testing.T) {
	var calls atomic.Int64
	provider := embeddingProvider(t, &calls)
	embedder := inference.NewEmbedder(inference.Config{Endpoint: provider.URL, EmbeddingModel: "one-model"})

	for request := 0; request < 2; request++ {
		ctx := inference.WithQuestionMemo(context.Background())
		if _, err := embedder.Embed(ctx, []string{"the same question twice over"}); err != nil {
			t.Fatal(err)
		}
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("the provider was called %d times for two requests, want 2", got)
	}
}
