//go:build inference

// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

// Whether an entity is separable when it is DESCRIBED rather than assembled.
//
// # What this follows from
//
// Two measurements sit next to each other and disagree, on the same embedder and on the same two
// migrations:
//
//   - as concatenated quotes, they scored 0.6442 against each other and no threshold separated a
//     paraphrase from a near miss;
//   - as written summaries, they were 0.23 apart and every thematic question landed on the right one.
//
// The difference is that a summary NAMES what makes a subject different, where concatenated quotes
// pile up the vocabulary two subjects of one kind have in common. If that is what did it, the same
// treatment applied to a single entity should separate where the surface did not — and if it does not,
// then the effect belongs to communities rather than to summarisation, which is worth knowing before
// anything is built on either.
//
// # Why the erasure objection no longer applies
//
// An earlier design chose concatenated quotes over a written description because a description "cannot be erased by
// counting: deleting one source does not remove that person from prose about them". That argument was dissolved for
// community reports and the same dissolution applies here: the row is registered to every
// observation that contributed to it, deleted when any contributor erases, and regenerated from what
// survives. The count stays exact because the artefact is deleted rather than repaired.
//
// So the only open question is the one this measures.
package inference_test

import (
	"context"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/infra/inference"
	"github.com/ensera-ai/taisce/internal/report"
)

func TestWhetherADescribedEntitySeparatesWhereAnAssembledOneDidNot(t *testing.T) {
	config, err := inference.ConfigFromEnv()
	if err != nil {
		t.Fatalf("inference is not configured, and this target exists to exercise it: %v", err)
	}
	if config.EmbeddingModel == "" {
		t.Fatalf("%s is not set; this measurement is about an embedder", inference.EnvEmbeddingModel)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()

	cases := resolutionSet()
	writer := report.New(inference.NewReporter(config))

	// One description per distinct entity, because writing the same entity twice would measure the
	// model's variance rather than the surface's.
	described := map[string]string{}
	for _, c := range cases {
		if _, done := described[c.canonical]; done {
			continue
		}
		// An entity is a community of one: the same material, the same writer, the same prompt. No
		// second prompt is introduced, so what is being compared is the treatment rather than the
		// wording.
		material := report.Context{Entities: append([]string{c.canonical}, c.aliases...)}
		for _, q := range c.quotes {
			material.Facts = append(material.Facts, report.Fact{
				Subject: c.canonical, Predicate: "mentioned_in", Object: c.canonical,
				Statement: q, Quote: q,
			})
		}
		r, err := writer.Write(ctx, material)
		if err != nil {
			t.Fatalf("describe %q: %v", c.canonical, err)
		}
		described[c.canonical] = r.Title + "\n" + r.Summary
		t.Logf("%-28s %q", c.canonical, r.Title)
		t.Logf("%-28s %s", "", r.Summary)
	}

	inputs := make([]string, 0, len(cases)*3)
	for _, c := range cases {
		inputs = append(inputs,
			c.question,
			measuredSurface(t, c.canonical, c.aliases, c.quotes),
			described[c.canonical])
	}
	vectors, err := inference.NewEmbedder(config).Embed(ctx, inputs)
	if err != nil {
		t.Fatalf("embed: %v", err)
	}

	var assembledSame, assembledDiff, describedSame, describedDiff []float64
	t.Logf("")
	for i, c := range cases {
		assembled, err := inference.Similarity(vectors[i*3], vectors[i*3+1])
		if err != nil {
			t.Fatalf("similarity: %v", err)
		}
		written, err := inference.Similarity(vectors[i*3], vectors[i*3+2])
		if err != nil {
			t.Fatalf("similarity: %v", err)
		}
		kind := "NEAR MISS "
		if c.same {
			kind = "PARAPHRASE"
			assembledSame = append(assembledSame, assembled)
			describedSame = append(describedSame, written)
		} else {
			assembledDiff = append(assembledDiff, assembled)
			describedDiff = append(describedDiff, written)
		}
		t.Logf("%s assembled %.4f  described %.4f  %q vs %q",
			kind, assembled, written, c.question, c.canonical)
	}

	assembledGap := distribution(t, "quotes assembled into a surface", assembledSame, assembledDiff)
	describedGap := distribution(t, "the same material described", describedSame, describedDiff)

	t.Logf("")
	t.Logf("assembled gap %+.4f, described gap %+.4f, change %+.4f",
		assembledGap, describedGap, describedGap-assembledGap)

	switch {
	case describedGap > 0 && assembledGap <= 0:
		t.Logf("DESCRIBING FIXES IT on this embedder: a threshold exists at %+.4f where none existed "+
			"for the assembled surface. What separated communities separates entities, so the effect "+
			"belongs to summarisation rather than to communities — and there is a mechanism to build "+
			"and a measurement to build it against.", describedGap)
	case describedGap > assembledGap:
		t.Logf("DESCRIBING HELPS AND IS NOT ENOUGH: the gap moves %+.4f and is still not positive. "+
			"Summarisation is doing something here, and less of it than it does for a community — "+
			"which is itself a result: the effect is partly that a community has more to distinguish "+
			"it than one entity does.", describedGap-assembledGap)
	default:
		t.Logf("DESCRIBING DOES NOT HELP: %+.4f against %+.4f. Then what separated communities was "+
			"not summarisation but having more distinct material to summarise, and no treatment of a "+
			"single entity's own words is going to separate two things of the same kind.",
			describedGap, assembledGap)
	}
}
