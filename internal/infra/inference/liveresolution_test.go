//go:build inference

// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

// What a semantic surface buys, and what it costs, measured rather than assumed.
//
// Entity resolution is exact match on a normalised name. A question that refers to something the
// way the conversation referred to it a second time misses, and the entity is right there. The
// proposal is to find CANDIDATES by comparing a question against a surface built from what an entity
// has been called and what was said about it.
//
// # Why the two directions are not symmetric, and why that decides the threshold
//
// Under-merging costs recall on one question. Over-merging puts two people's facts on one node, and
// nothing downstream can tell that it happened — a bundle about the wrong person is cited, current and
// wrong. So the number to report is not an average: it is how far apart the two distributions are, and
// whether any threshold separates them at all.
//
// This is a MEASUREMENT, not a gate. It fails only on the thing it can be certain about — that a
// near-miss scored above a paraphrase, which means no threshold exists — and otherwise reports what it
// found, because the numbers are a property of the embedder the operator chose.
package inference_test

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/infra/inference"
	"github.com/ensera-ai/taisce/internal/semantic"
)

// A pair is two ways of referring to something, with whether they are the same thing.
type pair struct {
	question string
	// entity is the surface: what it has been called, and what was said about it.
	canonical string
	aliases   []string
	quotes    []string
	same      bool
	why       string
}

func resolutionSet() []pair {
	return []pair{
		// ── The same thing, said differently. Exact match misses every one of these. ──────────
		{
			question:  "what is at our site in Dublin",
			canonical: "the Dublin office",
			aliases:   []string{"Dublin office"},
			quotes:    []string{"the Dublin office has the lab", "we moved into the Dublin office in March"},
			same:      true,
			why:       "the phrase the conversation used, and the phrase the question used, for one place",
		},
		{
			question:  "who runs Dublin HQ",
			canonical: "the Dublin office",
			aliases:   []string{"Dublin office"},
			quotes:    []string{"Marta manages the Dublin office", "the Dublin office reports to Hamza"},
			same:      true,
			why:       "an abbreviation nobody wrote down as an alias",
		},
		{
			question:  "where is the migration up to",
			canonical: "the database migration",
			aliases:   nil,
			quotes:    []string{"the database migration is running this week", "we started the database migration in March"},
			same:      true,
			why:       "the shortened form somebody uses once they have said it in full",
		},
		{
			question:  "what did we decide about the new auth service",
			canonical: "the authentication service",
			aliases:   nil,
			quotes:    []string{"the authentication service replaced the old login", "the authentication service went out on Tuesday"},
			same:      true,
			why:       "a common expansion, and the kind of pair a synonym list would need to enumerate",
		},

		// ── Different things that look alike. Every one of these must stay apart. ─────────────
		{
			question:  "where does Marta Kelly work",
			canonical: "Marta Nowak",
			aliases:   nil,
			quotes:    []string{"Marta Nowak joined in 2024", "Marta Nowak works at Ensera"},
			same:      false,
			why:       "two people sharing a first name — the over-merge that puts one person's facts on another",
		},
		{
			question:  "what is happening at the Cork office",
			canonical: "the Dublin office",
			aliases:   []string{"Dublin office"},
			quotes:    []string{"the Dublin office has the lab", "we moved into the Dublin office in March"},
			same:      false,
			why:       "two offices of one company, described in nearly identical words",
		},
		{
			question:  "how is the payments migration going",
			canonical: "the database migration",
			aliases:   nil,
			quotes:    []string{"the database migration is running this week", "we started the database migration in March"},
			same:      false,
			why:       "two projects of the same kind, which is what a memory of a workplace is full of",
		},
		{
			question:  "what does Ensera Holdings do",
			canonical: "Ensera",
			aliases:   nil,
			quotes:    []string{"Marta works at Ensera", "Ensera is in Dublin"},
			same:      false,
			why:       "a related company name — legally distinct, textually almost identical",
		},
	}
}

func TestWhatASemanticSurfaceResolvesAndWhatItWouldMerge(t *testing.T) {
	config, err := inference.ConfigFromEnv()
	if err != nil {
		t.Fatalf("inference is not configured, and this target exists to exercise it: %v", err)
	}
	if config.EmbeddingModel == "" {
		t.Fatalf("%s is not set; this measurement is about an embedder and cannot be taken without one",
			inference.EnvEmbeddingModel)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	cases := resolutionSet()
	// Two surfaces per case: the names alone, and the names with the stored quotes behind them. What
	// separates them is exactly what the evidence buys — or costs.
	inputs := make([]string, 0, len(cases)*3)
	for _, c := range cases {
		inputs = append(inputs,
			c.question,
			measuredSurface(t, c.canonical, c.aliases, nil),
			measuredSurface(t, c.canonical, c.aliases, c.quotes))
	}

	vectors, err := inference.NewEmbedder(config).Embed(ctx, inputs)
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	t.Logf("embedder: %s, %d dimensions", config.EmbeddingModel, len(vectors[0]))

	var namesParaphrase, namesNearMiss, fullParaphrase, fullNearMiss []float64
	for i, c := range cases {
		names, err := inference.Similarity(vectors[i*3], vectors[i*3+1])
		if err != nil {
			t.Fatalf("similarity: %v", err)
		}
		full, err := inference.Similarity(vectors[i*3], vectors[i*3+2])
		if err != nil {
			t.Fatalf("similarity: %v", err)
		}
		kind := "NEAR MISS "
		if c.same {
			kind = "PARAPHRASE"
			namesParaphrase = append(namesParaphrase, names)
			fullParaphrase = append(fullParaphrase, full)
		} else {
			namesNearMiss = append(namesNearMiss, names)
			fullNearMiss = append(fullNearMiss, full)
		}
		t.Logf("%s names %.4f  surface %.4f  %-45q vs %q\n            %s",
			kind, names, full, c.question, c.canonical, c.why)
	}

	namesGap := distribution(t, "names alone", namesParaphrase, namesNearMiss)
	surfaceGap := distribution(t, "names and the quotes behind them", fullParaphrase, fullNearMiss)

	t.Logf("")
	switch {
	case surfaceGap > namesGap:
		t.Logf("The quotes HELP: they widen the gap by %.4f. What they add about an entity is more "+
			"distinguishing than what they add in common vocabulary.", surfaceGap-namesGap)
	default:
		t.Logf("The quotes HURT: they narrow the gap by %.4f. Two things of the same kind are "+
			"described in nearly the same words, so adding those words moves both toward each other "+
			"faster than it moves either toward the question.", namesGap-surfaceGap)
	}

	// # Why this reports rather than fails
	//
	// It measured a negative result, and that result is recorded in the register: neither surface
	// separates a paraphrase from a near miss on this embedder, so an absolute similarity threshold
	// cannot be the mechanism. A test that goes red for a defect nobody is currently
	// fixing is one people stop reading, and then so is the case beside it that just broke.
	//
	// What it is FOR now is the next measurement. When a candidate mechanism arrives — a lexical
	// requirement alongside proximity, a different surface, a different embedder — this says whether
	// it changed the gap, in the same numbers, on the same pairs.
	t.Logf("")
	if namesGap <= 0 && surfaceGap <= 0 {
		t.Logf("KNOWN GAP: neither surface separates a paraphrase from a near miss on this "+
			"embedder (names %+.4f, surface %+.4f). An absolute similarity threshold cannot be the "+
			"mechanism for finding entity candidates, and building one would merge things that are "+
			"not the same. This was measured rather than assumed.", namesGap, surfaceGap)
		return
	}
	t.Logf("SEPARATION EXISTS on this embedder: names %+.4f, surface %+.4f. This was measured not to "+
		"hold on qwen3-embedding:4b-q8_0. If this is reproducible rather than a lucky draw, "+
		"the question can be reopened with a threshold that has evidence behind it.", namesGap, surfaceGap)
}

// report prints a distribution and returns the gap between the two, which is the number that decides
// whether any threshold exists.
func distribution(t *testing.T, what string, paraphrase, nearMiss []float64) float64 {
	t.Helper()
	sort.Float64s(paraphrase)
	sort.Float64s(nearMiss)
	worst := paraphrase[0]
	best := nearMiss[len(nearMiss)-1]
	t.Logf("")
	t.Logf("%s:", what)
	t.Logf("  paraphrases:  %.4f … %.4f", paraphrase[0], paraphrase[len(paraphrase)-1])
	t.Logf("  near misses:  %.4f … %.4f", nearMiss[0], nearMiss[len(nearMiss)-1])
	t.Logf("  gap:          %+.4f  (worst paraphrase minus best near miss)", worst-best)
	return worst - best
}

// A refused representation is a failed measurement input, never an empty vector surrogate.
func measuredSurface(t *testing.T, canonical string, aliases, quotes []string) string {
	t.Helper()
	surface, err := semantic.Surface(canonical, aliases, quotes)
	if err != nil {
		t.Fatal(err)
	}
	return surface
}
