//go:build inference

// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package inference_test

import (
	"context"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/infra/inference"
	"github.com/ensera-ai/taisce/internal/report"
	"github.com/ensera-ai/taisce/internal/semantic"
)

// TestEachEmbeddingSurfaceWinsOnlyItsOwnRetrievalQuestion is the executable contract for the embedding surfaces. Each
// fixture compares one question with all three production input shapes. Replacing the intended
// input with either other surface makes the assertion fail, so three indexes cannot pass as three
// copies of one representation.
func TestEachEmbeddingSurfaceWinsOnlyItsOwnRetrievalQuestion(t *testing.T) {
	config, err := inference.ConfigFromEnv()
	if err != nil {
		t.Fatalf("inference is not configured, and this contract requires an embedder: %v", err)
	}
	if config.EmbeddingModel == "" {
		t.Fatalf("%s is not set; this contract requires an embedder", inference.EnvEmbeddingModel)
	}
	entity, err := semantic.Surface("the database migration", []string{"PostgreSQL cutover", "migration 47"}, []string{
		"Marta owns the database migration.",
		"The database migration depends on PostgreSQL.",
	})
	if err != nil {
		t.Fatal(err)
	}
	reportSurface, err := report.EmbeddingSurface(
		"Database modernization is delayed by schema coordination",
		"The PostgreSQL cutover, migration 47, and Marta's delivery work form one program. Progress is blocked by a schema lock, so schema coordination is the theme holding up database work.",
		"The delay spans several related facts rather than one source note.", []byte(`[]`))
	if err != nil {
		t.Fatal(err)
	}

	surfaces := []struct {
		name, input string
	}{
		{"passage", "Marta wrote in the incident log: the schema lock at migration 47 is blocking the PostgreSQL cutover."},
		{"entity", entity},
		{"report", reportSurface},
	}
	probes := []struct {
		question, wants string
	}{
		{"Which stored note says that migration 47 is blocked by a schema lock?", "passage"},
		{"What is the thing also called the PostgreSQL cutover?", "entity"},
		{"What broad theme is holding up our database work?", "report"},
	}
	inputs := make([]string, 0, len(surfaces)+len(probes))
	for _, probe := range probes {
		inputs = append(inputs, probe.question)
	}
	for _, surface := range surfaces {
		inputs = append(inputs, surface.input)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	vectors, err := inference.NewEmbedder(config).Embed(ctx, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if len(vectors) != len(inputs) {
		t.Fatalf("provider returned %d vectors for %d inputs", len(vectors), len(inputs))
	}
	t.Logf("surface-contract/v1 model=%s dimensions=%d", config.EmbeddingModel, len(vectors[0]))
	for i, probe := range probes {
		best, bestScore := "", -2.0
		scores := make(map[string]float64, len(surfaces))
		for j, surface := range surfaces {
			score, err := inference.Similarity(vectors[i], vectors[len(probes)+j])
			if err != nil {
				t.Fatal(err)
			}
			scores[surface.name] = score
			if score > bestScore {
				best, bestScore = surface.name, score
			}
		}
		t.Logf("%s question: passage=%.4f entity=%.4f report=%.4f", probe.wants,
			scores["passage"], scores["entity"], scores["report"])
		if best != probe.wants {
			t.Errorf("%s question selected %s surface: scores=%v", probe.wants, best, scores)
		}
		for _, surface := range surfaces {
			if surface.name != probe.wants && scores[probe.wants] <= scores[surface.name] {
				t.Errorf("substituting %s for %s did not break retrieval: scores=%v", surface.name, probe.wants, scores)
			}
		}
	}
}
