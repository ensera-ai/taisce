//go:build inference

// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

// Whether what distinguishes an entity is its neighbourhood.
//
// # The sequence this is the third step of
//
//  1. An entity assembled from the quotes that mention it does not separate a paraphrase from a near
//     miss (gap −0.0313).
//  2. A community summarised from its facts does separate, and by a margin (gap +0.0943).
//  3. The same material DESCRIBED rather than assembled does not help — measured at −0.0360, slightly
//     worse. So summarisation is not the mechanism.
//
// Two things differed between 1 and 2 and only one of them has been ruled out. The community's
// material was not merely written up: it was RICHER. A community carried Postgres, a schema, Marta and
// a blocking relation; an entity carried two sentences that mention it, which are mostly about the
// kind of thing it is.
//
// # What that predicts
//
// The Dublin office is distinguished from the Cork office by who works there and what happened there.
// The database migration is distinguished from the payments migration by Postgres and a schema against
// Stripe and a card vault. None of that is in the sentences that name the entity — it is in the
// entity's NEIGHBOURHOOD, which is the one thing this system holds that a passage store does not.
//
// So: same treatment as step 1, same embedder, same questions, and a surface that carries the
// relations the entity stands in rather than only the sentences that mention it. No model call is
// involved, which is itself the point — if this is where the discrimination lives, it is free.
package inference_test

import (
	"context"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/infra/inference"
)

// neighbourhood is an entity with the relations it stands in, which is what the fact table holds.
type neighbourhood struct {
	question  string
	canonical string
	aliases   []string
	// mentions are the sentences that name the entity — step 1's material.
	mentions []string
	// relations are what the entity is connected to, rendered as the surface would render them.
	relations []string
	same      bool
}

func neighbourhoodSet() []neighbourhood {
	dublin := []string{
		"the Dublin office located_in Dublin",
		"Marta works_at the Dublin office",
		"the Dublin office has the lab",
		"the lab part_of the Dublin office",
	}
	database := []string{
		"the database migration depends_on Postgres",
		"the database migration blocked_by the schema",
		"Marta responsible_for the database migration",
	}
	return []neighbourhood{
		{
			question: "what is at our site in Dublin", canonical: "the Dublin office",
			aliases:   []string{"Dublin office"},
			mentions:  []string{"the Dublin office has the lab", "we moved into the Dublin office in March"},
			relations: dublin, same: true,
		},
		{
			question: "who runs Dublin HQ", canonical: "the Dublin office",
			aliases:   []string{"Dublin office"},
			mentions:  []string{"Marta manages the Dublin office", "the Dublin office reports to Hamza"},
			relations: dublin, same: true,
		},
		{
			question: "where is the migration up to", canonical: "the database migration",
			mentions: []string{"the database migration is running this week",
				"we started the database migration in March"},
			relations: database, same: true,
		},
		{
			question: "what is happening at the Cork office", canonical: "the Dublin office",
			aliases:   []string{"Dublin office"},
			mentions:  []string{"the Dublin office has the lab", "we moved into the Dublin office in March"},
			relations: dublin, same: false,
		},
		{
			question: "how is the payments migration going", canonical: "the database migration",
			mentions: []string{"the database migration is running this week",
				"we started the database migration in March"},
			relations: database, same: false,
		},
		{
			// The one the questions above do not cover: a person, whose neighbourhood is where the
			// discrimination would have to come from if it comes from anywhere.
			question: "where does Marta Kelly work", canonical: "Marta Nowak",
			mentions:  []string{"Marta Nowak joined in 2024", "Marta Nowak works at Ensera"},
			relations: []string{"Marta Nowak works_at Ensera", "Marta Nowak joined_on 2024"},
			same:      false,
		},
	}
}

func TestWhetherAnEntitysNeighbourhoodIsWhatDistinguishesIt(t *testing.T) {
	config, err := inference.ConfigFromEnv()
	if err != nil {
		t.Fatalf("inference is not configured, and this target exists to exercise it: %v", err)
	}
	if config.EmbeddingModel == "" {
		t.Fatalf("%s is not set; this measurement is about an embedder", inference.EnvEmbeddingModel)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	cases := neighbourhoodSet()
	inputs := make([]string, 0, len(cases)*3)
	for _, c := range cases {
		inputs = append(inputs,
			c.question,
			measuredSurface(t, c.canonical, c.aliases, c.mentions),
			measuredSurface(t, c.canonical, c.aliases, append(append([]string(nil), c.mentions...), c.relations...)))
	}

	vectors, err := inference.NewEmbedder(config).Embed(ctx, inputs)
	if err != nil {
		t.Fatalf("embed: %v", err)
	}

	var mentionsSame, mentionsDiff, neighbourSame, neighbourDiff []float64
	for i, c := range cases {
		mentions, err := inference.Similarity(vectors[i*3], vectors[i*3+1])
		if err != nil {
			t.Fatalf("similarity: %v", err)
		}
		neighbours, err := inference.Similarity(vectors[i*3], vectors[i*3+2])
		if err != nil {
			t.Fatalf("similarity: %v", err)
		}
		kind := "NEAR MISS "
		if c.same {
			kind = "PARAPHRASE"
			mentionsSame = append(mentionsSame, mentions)
			neighbourSame = append(neighbourSame, neighbours)
		} else {
			mentionsDiff = append(mentionsDiff, mentions)
			neighbourDiff = append(neighbourDiff, neighbours)
		}
		t.Logf("%s mentions %.4f  with neighbourhood %.4f  %q vs %q",
			kind, mentions, neighbours, c.question, c.canonical)
	}

	mentionsGap := distribution(t, "sentences that mention the entity", mentionsSame, mentionsDiff)
	neighbourGap := distribution(t, "those, plus the relations it stands in", neighbourSame, neighbourDiff)

	t.Logf("")
	t.Logf("mentions gap %+.4f, neighbourhood gap %+.4f, change %+.4f",
		mentionsGap, neighbourGap, neighbourGap-mentionsGap)

	switch {
	case neighbourGap > 0 && mentionsGap <= 0:
		t.Logf("THE NEIGHBOURHOOD IS THE MECHANISM: a threshold exists at %+.4f where none existed "+
			"without it, and it costs no model call. What distinguishes two things of one kind is what "+
			"they are connected to, which is the one thing this system holds that a passage store does "+
			"not. There is a design to build.", neighbourGap)
	case neighbourGap > mentionsGap:
		t.Logf("THE NEIGHBOURHOOD HELPS AND IS NOT ENOUGH: %+.4f against %+.4f. It is the right "+
			"direction and something else is still needed, which means a surface is not the whole "+
			"answer and the mechanism has to include something other than proximity.",
			neighbourGap, mentionsGap)
	default:
		t.Logf("THE NEIGHBOURHOOD DOES NOT HELP EITHER: %+.4f against %+.4f. Three treatments of an "+
			"entity's own material have now failed to separate two things of one kind on this "+
			"embedder, which is enough to stop trying surfaces and say so.",
			neighbourGap, mentionsGap)
	}
}
