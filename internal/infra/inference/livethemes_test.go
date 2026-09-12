//go:build inference

// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

// Whether a thematic question finds the right subject, measured before an index is built for it.
//
// A thematic question names no entity and resembles no single passage, so it has to be answered by
// searching something written about a subject. That is the argument for a third index — and it is an
// argument for having something to search, not evidence that searching it works.
//
// # Why this is measured before the index rather than after
//
// The entity surface was measured after the same argument had been made for it, and the distributions
// overlapped: a surface built from what was said about a thing turned out to be partly a surface about
// the KIND of thing it is. A report is that shape at a larger scale — prose about a subject,
// written in the vocabulary of subjects of that kind — so two reports about two migrations may be no
// more separable than two surfaces about two migrations were.
//
// The reports here are written by the live model from fixture material, so what is measured is what
// the system would actually store.
package inference_test

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/infra/inference"
	"github.com/ensera-ai/taisce/internal/report"
)

// subject is one community's material, and the questions that should and should not find it.
type subject struct {
	name     string
	entities []string
	facts    []report.Fact
}

func themeSet() []subject {
	return []subject{
		{
			name:     "the database migration",
			entities: []string{"the database migration", "Marta", "the schema", "Postgres"},
			facts: []report.Fact{
				{Subject: "Marta", Predicate: "responsible_for", Object: "the database migration",
					Statement: "Marta is responsible for the database migration.",
					Quote:     "Marta is running the database migration"},
				{Subject: "the database migration", Predicate: "depends_on", Object: "Postgres",
					Statement: "The database migration depends on Postgres.",
					Quote:     "the database migration needs the Postgres upgrade first"},
				{Subject: "the database migration", Predicate: "blocked_by", Object: "the schema",
					Statement: "The database migration is blocked by the schema.",
					Quote:     "we are stuck on the schema before the database migration can go"},
			},
		},
		{
			name:     "the payments migration",
			entities: []string{"the payments migration", "Tomas", "the card vault", "Stripe"},
			facts: []report.Fact{
				{Subject: "Tomas", Predicate: "responsible_for", Object: "the payments migration",
					Statement: "Tomas is responsible for the payments migration.",
					Quote:     "Tomas is running the payments migration"},
				{Subject: "the payments migration", Predicate: "depends_on", Object: "Stripe",
					Statement: "The payments migration depends on Stripe.",
					Quote:     "the payments migration waits on the Stripe contract"},
				{Subject: "the payments migration", Predicate: "blocked_by", Object: "the card vault",
					Statement: "The payments migration is blocked by the card vault.",
					Quote:     "the card vault has to move before the payments migration can finish"},
			},
		},
		{
			name:     "the cycling club",
			entities: []string{"the cycling club", "Saturday", "the coast road", "Marta"},
			facts: []report.Fact{
				{Subject: "Marta", Predicate: "committed_to", Object: "the cycling club",
					Statement: "Marta rides with the cycling club.",
					Quote:     "Marta rides with the cycling club on Saturdays"},
				{Subject: "the cycling club", Predicate: "occurred_on", Object: "Saturday",
					Statement: "The cycling club meets on Saturday.",
					Quote:     "the cycling club goes out on Saturday mornings"},
				{Subject: "the cycling club", Predicate: "located_in", Object: "the coast road",
					Statement: "The cycling club rides the coast road.",
					Quote:     "the cycling club takes the coast road"},
			},
		},
	}
}

// probe is a thematic question and the subject it should find.
type probe struct {
	question string
	wants    string
	why      string
}

func themeProbes() []probe {
	return []probe{
		{
			question: "what is holding up the database work",
			wants:    "the database migration",
			why:      "names the subject in different words, which is what makes it thematic",
		},
		{
			question: "what is going on with card payments",
			wants:    "the payments migration",
			why:      "names the subject by its domain rather than by its name",
		},
		{
			question: "what do I do at weekends",
			wants:    "the cycling club",
			why:      "names nothing in the material at all — no entity, no relation, no quote",
		},
	}
}

func TestWhetherAThematicQuestionFindsTheRightSubject(t *testing.T) {
	config, err := inference.ConfigFromEnv()
	if err != nil {
		t.Fatalf("inference is not configured, and this target exists to exercise it: %v", err)
	}
	if config.EmbeddingModel == "" {
		t.Fatalf("%s is not set; this measurement is about an embedder", inference.EnvEmbeddingModel)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	// The reports are written by the model from the material, because what has to be measured is what
	// the system would store rather than prose written here to be measurable.
	writer := report.New(inference.NewReporter(config))
	subjects := themeSet()
	written := make([]report.Report, len(subjects))
	for i, s := range subjects {
		c := report.BuildContext(s.entities, s.facts, nil, nil, 100000)
		r, err := writer.Write(ctx, c)
		if err != nil {
			t.Fatalf("write %q: %v", s.name, err)
		}
		written[i] = r
		t.Logf("%-26s %q", s.name, r.Title)
		t.Logf("%-26s %s", "", r.Summary)
	}

	probes := themeProbes()
	inputs := make([]string, 0, len(probes)+len(written))
	for _, p := range probes {
		inputs = append(inputs, p.question)
	}
	for _, r := range written {
		// Title and summary, which is what a thematic search would hold: the findings are the
		// checkable detail behind an answer rather than what a question is matched against.
		inputs = append(inputs, r.Title+"\n"+r.Summary)
	}

	vectors, err := inference.NewEmbedder(config).Embed(ctx, inputs)
	if err != nil {
		t.Fatalf("embed: %v", err)
	}

	var right, wrong []float64
	for i, p := range probes {
		t.Logf("")
		t.Logf("%q — %s", p.question, p.why)
		best, bestScore := "", -2.0
		for j, s := range subjects {
			score, err := inference.Similarity(vectors[i], vectors[len(probes)+j])
			if err != nil {
				t.Fatalf("similarity: %v", err)
			}
			mark := " "
			if s.name == p.wants {
				mark = "*"
				right = append(right, score)
			} else {
				wrong = append(wrong, score)
			}
			if score > bestScore {
				best, bestScore = s.name, score
			}
			t.Logf("   %s %.4f  %s", mark, score, s.name)
		}
		if best != p.wants {
			t.Errorf("the nearest subject to %q is %q, and the question is about %q",
				p.question, best, p.wants)
		}
	}

	sort.Float64s(right)
	sort.Float64s(wrong)
	gap := right[0] - wrong[len(wrong)-1]
	t.Logf("")
	t.Logf("right subject:  %.4f … %.4f", right[0], right[len(right)-1])
	t.Logf("wrong subject:  %.4f … %.4f", wrong[0], wrong[len(wrong)-1])
	t.Logf("gap:            %+.4f  (weakest right minus strongest wrong)", gap)

	// Two questions are asked here and they have different answers.
	//
	// **Does the nearest subject win?** That is a RELATIVE comparison — pick the best of what exists —
	// and it is what a thematic search actually does. It is asserted above, per question.
	//
	// **Does an absolute threshold exist?** That is what an index needs to say "nothing here is about
	// your question" rather than always returning its nearest row. The entity surface failed exactly
	// this, and it is the number below.
	t.Logf("")
	if gap <= 0 {
		t.Logf("KNOWN GAP: the right subject is nearest per question, and the distributions still "+
			"overlap across questions (%+.4f). A thematic search can rank what it has; it cannot yet "+
			"say that nothing it holds is about the question — the same shape the entity surface showed.", gap)
		return
	}
	t.Logf("Separation on this embedder: %+.4f. Measured at +0.0943 on qwen3-embedding:4b-q8_0 and "+
		"which is what justifies building the index. Three questions against three "+
		"subjects is a gap rather than a distribution, so it is not yet a threshold to ship.", gap)
}
