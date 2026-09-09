// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package inference_test

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ensera-ai/taisce/internal/infra/inference"
)

func TestEmbeddingRejectsAZeroDirectionBeforeReturningVectors(t *testing.T) {
	config := serving(t, vectors(t, []map[string]any{{"index": 0, "embedding": []float32{0, 0}}}))
	config.EmbeddingModel = "test"
	if got, err := inference.NewEmbedder(config).Embed(context.Background(), []string{"retained evidence"}); err == nil || got != nil {
		t.Fatalf("zero direction accepted: %v %v", got, err)
	}
}

func TestCosineRejectsNonFiniteComponentsInsteadOfReturningNaN(t *testing.T) {
	for _, v := range []float32{float32(math.NaN()), float32(math.Inf(1)), float32(math.Inf(-1))} {
		for _, pair := range [][2][]float32{{{v, 1}, {1, 1}}, {{1, 1}, {v, 1}}} {
			if score, err := inference.Similarity(pair[0], pair[1]); err == nil {
				t.Fatalf("invalid component returned score %v", score)
			}
		}
	}
}

func TestEmbeddingInputLimitsRefuseBeforeNetworkAndAdmitBoundaries(t *testing.T) {
	var calls atomic.Int32
	config := serving(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		data := []map[string]any{}
		for i := range body.Input {
			data = append(data, map[string]any{"index": i, "embedding": []float32{1, 0}})
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	})
	config.EmbeddingModel = "test"
	embedder := inference.NewEmbedder(config)
	tooMany := make([]string, inference.MaxEmbeddingInputs+1)
	for i := range tooMany {
		tooMany[i] = "text"
	}
	tooLarge := make([]string, inference.MaxEmbeddingBatchBytes/inference.MaxEmbeddingInputBytes+1)
	for i := range tooLarge {
		tooLarge[i] = strings.Repeat("x", inference.MaxEmbeddingInputBytes)
	}
	for _, inputs := range [][]string{tooMany, tooLarge, {strings.Repeat("x", inference.MaxEmbeddingInputBytes+1)}, {" \t\n"}, {string([]byte{0xff})}} {
		if got, err := embedder.Embed(context.Background(), inputs); err == nil || got != nil {
			t.Fatal("unbounded/invalid input accepted")
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid inputs reached provider %d times", calls.Load())
	}
	for _, inputs := range [][]string{tooMany[:inference.MaxEmbeddingInputs], tooLarge[:len(tooLarge)-1]} {
		got, err := embedder.Embed(context.Background(), inputs)
		if err != nil || len(got) != len(inputs) {
			t.Fatalf("valid boundary refused: %d %v", len(got), err)
		}
	}
	if calls.Load() != 2 {
		t.Fatal("valid batches were not sent exactly once")
	}
}

func TestEmbeddingDimensionBoundsAndFiniteExtremeCosines(t *testing.T) {
	for _, n := range []int{inference.MaxEmbeddingDimensions, inference.MaxEmbeddingDimensions + 1} {
		vector := make([]float32, n)
		vector[0] = 1
		config := serving(t, vectors(t, []map[string]any{{"index": 0, "embedding": vector}}))
		config.EmbeddingModel = "test"
		got, err := inference.NewEmbedder(config).Embed(context.Background(), []string{"input"})
		if n == inference.MaxEmbeddingDimensions {
			if err != nil || len(got) != 1 || len(got[0]) != n {
				t.Fatal("supported dimension boundary refused")
			}
		} else {
			if err == nil || got != nil {
				t.Fatal("oversized vector accepted")
			}
			if _, err := inference.Similarity(vector, vector); err == nil {
				t.Fatal("oversized comparison accepted")
			}
		}
	}
	for _, v := range [][]float32{{math.MaxFloat32, math.MaxFloat32}, {math.SmallestNonzeroFloat32, 0}, {1, 2, 3, 4, 5, 6, 7}} {
		score, err := inference.Similarity(v, v)
		if err != nil || math.IsNaN(score) || math.IsInf(score, 0) || score > 1 || score < 0.999999 {
			t.Fatalf("invalid extreme cosine %v %v", score, err)
		}
	}
}

// A complete JSON value followed by whitespace is still a valid prefix. The adapter must detect
// overflow explicitly instead of accepting that prefix when its bounded reader reaches EOF.
func TestEmbeddingReplyOverflowIsNotAcceptedAsAValidPrefix(t *testing.T) {
	config := serving(t, func(w http.ResponseWriter, r *http.Request) {
		prefix := `{"data":[{"index":0,"embedding":[1,0]}]}`
		io.WriteString(w, prefix)
		io.CopyN(w, embeddingSpaces{}, int64(inference.MaxEmbeddingReplyBytes-len(prefix)+1))
	})
	config.EmbeddingModel = "test"
	if got, err := inference.NewEmbedder(config).Embed(context.Background(), []string{"input"}); err == nil || got != nil {
		t.Fatal("oversized valid prefix accepted")
	}
}

type embeddingSpaces struct{}

func (embeddingSpaces) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = ' '
	}
	return len(p), nil
}
