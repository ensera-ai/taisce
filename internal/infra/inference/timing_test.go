// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package inference_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/infra/inference"
)

// timings collects what a Config's Timed hook was told.
type timings struct {
	mu    sync.Mutex
	calls []timing
}

type timing struct {
	purpose  inference.Purpose
	answered bool
	took     time.Duration
}

func (c *timings) hook(purpose inference.Purpose, answered bool, took time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, timing{purpose, answered, took})
}

// only requires exactly one timing, for this purpose and this outcome, and returns it.
func (c *timings) only(t *testing.T, what string, purpose inference.Purpose, answered bool) timing {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.calls) != 1 || c.calls[0].purpose != purpose || c.calls[0].answered != answered {
		t.Fatalf("%s: one call for %s was made, answered=%v, and the timings recorded are %+v", what, purpose, answered, c.calls)
	}
	return c.calls[0]
}

// TestEachModelCallIsTimedUnderItsOwnPurposeAndNoOther holds the series a call's duration lands in.
//
// A turn's formation is timed as a whole and makes several calls, so "formation is slow" could not
// be narrowed to which pass, and a slow embedding provider showed only as a slow recall. The value
// of timing them apart is entirely in each landing under its own name: a pass that recorded itself
// as another would send an operator to the wrong provider setting.
//
// The provider here is a stand-in, which is all this needs: what is being checked is which name the
// caller's own code times itself under, and that it waits for the reply before stopping the clock.
func TestEachModelCallIsTimedUnderItsOwnPurposeAndNoOther(t *testing.T) {
	replies := map[inference.Purpose]string{
		inference.PurposeExtraction: `{"claims":[]}`,
		inference.PurposeReport:     `{"title":"Ensera","summary":"A company."}`,
		inference.PurposeCompaction: `{"summary":"Nothing was said."}`,
	}
	const held = 60 * time.Millisecond
	for purpose, call := range generationCalls(t) {
		// Answered: timed once, under its purpose, and for at least as long as the provider took.
		var seen timings
		config := serving(t, func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(held)
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{
				{"finish_reason": "stop", "message": map[string]string{"role": "assistant", "content": replies[purpose]}}}})
		})
		config.Timed = seen.hook
		if err := call(config); err != nil {
			t.Fatalf("%s: %v", purpose, err)
		}
		if got := seen.only(t, "an answer", purpose, true); got.took < held {
			t.Errorf("%s was timed at %s and the provider held the reply for %s: the clock stopped before the answer arrived", purpose, got.took, held)
		}

		// The provider refused: still one timing, and not among the answers. A failure is not a
		// sample of how long an answer takes.
		seen = timings{}
		config = serving(t, func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "overloaded", http.StatusServiceUnavailable)
		})
		config.Timed = seen.hook
		if err := call(config); err == nil {
			t.Fatalf("%s: a provider answering 503 was treated as an answer", purpose)
		}
		seen.only(t, "a refusal by the provider", purpose, false)

		// Answered, then refused here for what it said: the provider did answer, and how often a
		// reply is cut is counted where it is refused.
		seen = timings{}
		config = serving(t, func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{
				{"finish_reason": "length", "message": map[string]string{"role": "assistant", "content": replies[purpose]}}}})
		})
		config.Timed = seen.hook
		if err := call(config); !errors.Is(err, inference.ErrReplyCut) {
			t.Fatalf("%s: a cut reply was not refused as one: %v", purpose, err)
		}
		seen.only(t, "a reply that was cut", purpose, true)
	}
	if len(generationCalls(t)) != len(inference.Purposes()) {
		t.Fatalf("%d generation purposes are declared and %d were exercised", len(inference.Purposes()), len(generationCalls(t)))
	}

	// The embedding, which is the model call a recall waits on.
	var seen timings
	config := serving(t, vectors(t, []map[string]any{{"index": 0, "embedding": []float32{1, 0, 0}}}))
	t.Setenv(inference.EnvEmbeddingModel, "test-embedder")
	config.EmbeddingModel = "test-embedder"
	config.Timed = seen.hook
	embedder := inference.NewEmbedder(config)
	if _, err := embedder.Embed(context.Background(), []string{"where does Ada work"}); err != nil {
		t.Fatal(err)
	}
	seen.only(t, "an embedding", inference.PurposeEmbedding, true)

	// Refused before it was sent: nothing left this process, so there is nothing to time.
	seen.calls = nil
	if _, err := embedder.Embed(context.Background(), []string{"  "}); err == nil {
		t.Fatal("an empty input was embedded")
	}
	if len(seen.calls) != 0 {
		t.Errorf("an embedding that was never sent was timed: %+v", seen.calls)
	}

	// Asked twice within one request: the second is answered from the request's own memo and is
	// not a call, so one request to the provider is one timing.
	ctx := inference.WithQuestionMemo(context.Background())
	for range 2 {
		if _, err := embedder.Embed(ctx, []string{"where does Ada work"}); err != nil {
			t.Fatal(err)
		}
	}
	seen.only(t, "a question embedded twice in one request", inference.PurposeEmbedding, true)

	// A provider that does not answer in the shape asked for: timed, and not as an answer.
	seen = timings{}
	config = serving(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("not json")) })
	config.EmbeddingModel = "test-embedder"
	config.Timed = seen.hook
	if _, err := inference.NewEmbedder(config).Embed(context.Background(), []string{"anything"}); err == nil {
		t.Fatal("an undecodable embedding reply was used")
	}
	seen.only(t, "an embedding reply that could not be read", inference.PurposeEmbedding, false)

	// The set a label is drawn from is the generation purposes and embedding, and nothing else.
	timed := inference.TimedPurposes()
	if len(timed) != len(inference.Purposes())+1 || timed[len(timed)-1] != inference.PurposeEmbedding {
		t.Errorf("the purposes a call is timed under are %v", timed)
	}
}
