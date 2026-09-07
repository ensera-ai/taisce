// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package inference_test

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"testing"

	"github.com/ensera-ai/taisce/internal/infra/inference"
)

func vectors(t *testing.T, data []map[string]any) http.HandlerFunc {
	t.Helper()
	body, err := json.Marshal(map[string]any{"data": data})
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	return func(w http.ResponseWriter, r *http.Request) { w.Write(body) }
}

// The order is a promise. The interface returns an index with each vector and nothing requires them
// to arrive in order — a caller matching by position against a reply that came back sorted differently
// would attach every entity's vector to a different entity, silently, and it would look like poor
// retrieval rather than like a defect.
func TestVectorsComeBackAgainstTheInputTheyAreFor(t *testing.T) {
	config := serving(t, vectors(t, []map[string]any{
		{"index": 2, "embedding": []float32{0, 0, 1}},
		{"index": 0, "embedding": []float32{1, 0, 0}},
		{"index": 1, "embedding": []float32{0, 1, 0}},
	}))
	t.Setenv(inference.EnvEmbeddingModel, "test-embedder")
	config.EmbeddingModel = "test-embedder"

	got, err := inference.NewEmbedder(config).Embed(context.Background(),
		[]string{"first", "second", "third"})
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	for i, want := range [][]float32{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}} {
		for d := range want {
			if got[i][d] != want[d] {
				t.Fatalf("input %d received the vector for another input: %v", i, got[i])
			}
		}
	}
}

// Every way the reply can leave a caller matching a vector onto the wrong thing.
func TestAnEmbeddingReplyThatCannotBeTrustedIsRefused(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"fewer vectors than inputs": vectors(t, []map[string]any{
			{"index": 0, "embedding": []float32{1, 0}},
		}),
		"an index outside the batch": vectors(t, []map[string]any{
			{"index": 0, "embedding": []float32{1, 0}},
			{"index": 7, "embedding": []float32{0, 1}},
		}),
		"the same index twice": vectors(t, []map[string]any{
			{"index": 0, "embedding": []float32{1, 0}},
			{"index": 0, "embedding": []float32{0, 1}},
		}),
		"an empty vector": vectors(t, []map[string]any{
			{"index": 0, "embedding": []float32{1, 0}},
			{"index": 1, "embedding": []float32{}},
		}),
		"two different dimensions": vectors(t, []map[string]any{
			{"index": 0, "embedding": []float32{1, 0}},
			{"index": 1, "embedding": []float32{0, 1, 0}},
		}),
		"a refusal": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
		},
		"a body that is not JSON": func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, "not json")
		},
	} {
		t.Run(name, func(t *testing.T) {
			config := serving(t, handler)
			config.EmbeddingModel = "test-embedder"
			if _, err := inference.NewEmbedder(config).Embed(context.Background(),
				[]string{"first", "second"}); err == nil {
				t.Fatalf("%s was accepted", name)
			}
		})
	}
}

// A deployment that extracts and never embeds is a working deployment. What must not happen is a
// caller asking for a vector and receiving zeros, which would sit somewhere in the space and match
// things for no reason.
func TestAnUnconfiguredEmbedderRefusesRatherThanReturningZeros(t *testing.T) {
	config := serving(t, vectors(t, []map[string]any{{"index": 0, "embedding": []float32{1, 0}}}))
	config.EmbeddingModel = ""
	if _, err := inference.NewEmbedder(config).Embed(context.Background(), []string{"anything"}); err == nil {
		t.Fatal("an unconfigured embedder returned a vector")
	}
}

// An empty surface embeds to whatever the model does with nothing, which is a vector that matches
// things for no reason. Refused before it is sent.
func TestAnEmptyInputIsNotSent(t *testing.T) {
	sent := false
	config := serving(t, func(w http.ResponseWriter, r *http.Request) { sent = true })
	config.EmbeddingModel = "test-embedder"
	if _, err := inference.NewEmbedder(config).Embed(context.Background(),
		[]string{"a real surface", ""}); err == nil {
		t.Fatal("an empty input was embedded")
	}
	if sent {
		t.Fatal("the request went out anyway")
	}
	// And nothing to embed is not an error: a scope with no entities is an ordinary state.
	if got, err := inference.NewEmbedder(config).Embed(context.Background(), nil); err != nil || got != nil {
		t.Fatalf("an empty batch returned %v, %v", got, err)
	}
}

// ── The arithmetic ────────────────────────────────────────────────────────────────────────────

// Normalised, because a dot product used without normalising rewards long vectors, and whether a
// model returns normalised vectors is a property of the model rather than of the interface.
func TestSimilarityIsAnAngleRatherThanALength(t *testing.T) {
	short := []float32{1, 0, 0}
	long := []float32{100, 0, 0}
	same, err := inference.Similarity(short, long)
	if err != nil {
		t.Fatalf("similarity: %v", err)
	}
	if math.Abs(same-1) > 1e-6 {
		t.Fatalf("one direction at two lengths scored %v", same)
	}

	orthogonal, err := inference.Similarity([]float32{1, 0}, []float32{0, 1})
	if err != nil {
		t.Fatalf("similarity: %v", err)
	}
	if math.Abs(orthogonal) > 1e-6 {
		t.Fatalf("two unrelated directions scored %v", orthogonal)
	}

	opposite, err := inference.Similarity([]float32{1, 0}, []float32{-1, 0})
	if err != nil {
		t.Fatalf("similarity: %v", err)
	}
	if math.Abs(opposite+1) > 1e-6 {
		t.Fatalf("two opposite directions scored %v", opposite)
	}
}

// A comparison that is meaningless says so rather than returning zero, which would read as
// "unrelated" — and a caller thresholding on it would quietly treat every mismatch as a rejection.
func TestAMeaninglessComparisonIsAnErrorRatherThanZero(t *testing.T) {
	for name, pair := range map[string][2][]float32{
		"different spaces": {{1, 0}, {1, 0, 0}},
		"an empty vector":  {{}, {}},
		"a zero vector":    {{0, 0}, {1, 0}},
	} {
		if _, err := inference.Similarity(pair[0], pair[1]); err == nil {
			t.Errorf("%s returned a score", name)
		}
	}
}
