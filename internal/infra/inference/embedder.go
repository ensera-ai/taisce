// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package inference

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// Defensive ceilings bound serialization, response allocation and numeric work. They are not a
// claim about provider throughput or the dimension/index contract of a persisted corpus.
const (
	MaxEmbeddingInputs     = 64
	MaxEmbeddingInputBytes = 64 << 10
	MaxEmbeddingBatchBytes = 1 << 20
	MaxEmbeddingDimensions = 16000
	MaxEmbeddingReplyBytes = 64 << 20
)

// Embedder turns text into a vector over the OpenAI-compatible embeddings interface.
//
// It embeds and nothing more. What text stands for an entity, how much of it, and what is done with
// the distance between two vectors are decisions elsewhere — this is the hop to a model, and the
// reason it is a separate type from the extractor's is that the two are different jobs: extraction is
// a generation that runs once per message, embedding is a forward pass that runs once per entity and
// once per question.
type Embedder struct {
	config Config
	client *http.Client
}

// NewEmbedder builds an embedder against an OpenAI-compatible endpoint.
func NewEmbedder(config Config) *Embedder {
	return &Embedder{
		config: config,
		// Shorter than extraction's, because an embedding is a forward pass rather than a
		// generation: a minute is already far past anything a working model takes, and a call that
		// hangs on the read path is worse here than one that fails, because the read path has a
		// caller waiting on it.
		client: modelClient(time.Minute),
	}
}

// Embed returns one vector per input, in the order the inputs were given.
//
// # Why a batch
//
// Embedding is dominated by the round trip on short inputs, and the callers here have batches by
// nature: every entity in a scope, every community's report. One call per item would make a rebuild
// after an erasure a few thousand round trips.
//
// # Why the order is a promise
//
// The interface returns an index with each vector and nothing requires them to arrive in order. A
// caller matching by position against a reply that came back sorted differently would attach every
// entity's vector to a different entity — silently, and in a way that looks like poor retrieval rather
// than like a defect. So the index is honoured here and the promise is made once.
func (e *Embedder) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	if e.config.EmbeddingModel == "" {
		return nil, fmt.Errorf("%s is not set, so nothing can be embedded; a deployment that does not "+
			"embed is valid, but a caller asking for a vector must not receive zeros", EnvEmbeddingModel)
	}
	if len(inputs) == 0 {
		return nil, nil
	}
	if len(inputs) > MaxEmbeddingInputs {
		return nil, fmt.Errorf("embedding batch exceeds %d inputs", MaxEmbeddingInputs)
	}
	inputBytes := 0
	for i, in := range inputs {
		if len(in) > MaxEmbeddingInputBytes || !utf8.ValidString(in) {
			return nil, fmt.Errorf("input %d is not bounded UTF-8 text", i)
		}
		inputBytes += len(in)
		if inputBytes > MaxEmbeddingBatchBytes {
			return nil, fmt.Errorf("embedding batch exceeds %d input bytes", MaxEmbeddingBatchBytes)
		}
		if strings.TrimSpace(in) == "" {
			// An empty input embeds to whatever the model does with nothing, which is a vector that
			// sits somewhere in the space and matches things for no reason. Refused rather than sent.
			return nil, fmt.Errorf("input %d is empty, and an empty surface is not a thing to find", i)
		}
	}

	body, err := json.Marshal(embeddingRequest{Model: e.config.EmbeddingModel, Input: inputs})
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	// Where embedding goes, which is not necessarily where generation goes: extraction is slow and is
	// where a better model shows, embedding is a cheap forward pass an operator may want on their own
	// hardware. The key travels with the endpoint rather than falling back separately, so a
	// credential is never sent to a host it does not belong to.
	endpoint, key := e.config.EmbeddingsAt()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/embeddings",
		bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call embedder: %w", err)
	}
	defer resp.Body.Close()
	// Bounded like every other reply: a remote system does not decide how much memory this process
	// spends. Larger than a chat reply's because a batch of vectors is genuinely large — a thousand
	// inputs at 1024 dimensions is several megabytes of JSON.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxEmbeddingReplyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read reply: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embedder returned %d", resp.StatusCode)
	}
	if len(raw) > MaxEmbeddingReplyBytes {
		return nil, fmt.Errorf("embedding reply exceeds %d bytes", MaxEmbeddingReplyBytes)
	}

	var reply embeddingResponse
	if err := json.Unmarshal(raw, &reply); err != nil {
		return nil, fmt.Errorf("decode reply: %w", err)
	}
	if len(reply.Data) != len(inputs) {
		// Fewer vectors than inputs would leave a caller matching by position onto the wrong entities.
		return nil, fmt.Errorf("asked for %d embeddings and received %d", len(inputs), len(reply.Data))
	}

	out := make([][]float32, len(inputs))
	for _, item := range reply.Data {
		if item.Index < 0 || item.Index >= len(inputs) {
			return nil, fmt.Errorf("the embedder returned index %d for a batch of %d", item.Index, len(inputs))
		}
		if out[item.Index] != nil {
			return nil, fmt.Errorf("the embedder returned index %d twice", item.Index)
		}
		if len(item.Embedding) == 0 {
			return nil, fmt.Errorf("the embedder returned an empty vector for input %d", item.Index)
		}
		if len(item.Embedding) > MaxEmbeddingDimensions {
			return nil, fmt.Errorf("embedding %d exceeds %d dimensions", item.Index, MaxEmbeddingDimensions)
		}
		var norm float64
		for _, value := range item.Embedding {
			v := float64(value)
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return nil, fmt.Errorf("embedding %d contains a non-finite component", item.Index)
			}
			norm += v * v
		}
		if norm == 0 {
			return nil, fmt.Errorf("embedding %d has no direction", item.Index)
		}
		out[item.Index] = item.Embedding
	}
	for i, v := range out {
		if v == nil {
			return nil, fmt.Errorf("the embedder returned no vector for input %d", i)
		}
		if len(v) != len(out[0]) {
			// One model, one space. Vectors of different lengths in one batch cannot be compared to
			// each other, and comparing them anyway is the kind of defect that shows up as bad
			// retrieval rather than as an error.
			return nil, fmt.Errorf("input %d embedded to %d dimensions and input 0 to %d",
				i, len(v), len(out[0]))
		}
	}
	return out, nil
}

// Similarity is the cosine of the angle between two vectors, in [-1, 1].
//
// Here rather than in a caller because the failure it prevents is arithmetic: a dot product used
// without normalising rewards long vectors, and whether a model returns normalised vectors is a
// property of the model rather than of the interface. Normalising costs one pass and removes the
// question.
//
// Two vectors of different lengths are not comparable, and returning zero would read as "unrelated"
// rather than as "this comparison is meaningless".
func Similarity(a, b []float32) (float64, error) {
	if len(a) != len(b) {
		return 0, fmt.Errorf("a %d-dimensional vector and a %d-dimensional one are not in the same space",
			len(a), len(b))
	}
	if len(a) == 0 {
		return 0, fmt.Errorf("an empty vector has no direction")
	}
	if len(a) > MaxEmbeddingDimensions {
		return 0, fmt.Errorf("comparison exceeds %d dimensions", MaxEmbeddingDimensions)
	}
	var dot, na, nb float64
	for i := range a {
		if math.IsNaN(float64(a[i])) || math.IsInf(float64(a[i]), 0) || math.IsNaN(float64(b[i])) || math.IsInf(float64(b[i]), 0) {
			return 0, fmt.Errorf("a non-finite component has no comparable direction")
		}
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0, fmt.Errorf("a zero vector has no direction")
	}
	// Floating-point accumulation can overshoot an endpoint by a few ulps even for a valid vector.
	return max(-1, min(1, dot/(math.Sqrt(na)*math.Sqrt(nb)))), nil
}

type embeddingRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embeddingResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}
