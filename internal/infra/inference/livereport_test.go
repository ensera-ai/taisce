//go:build inference

// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

// The report prompt's live measurement.
//
// A prompt is data, and editing it is a change to behaviour that has to be measured rather than
// reviewed (rule 14). The extraction prompt has a corpus of nineteen cases; this is the report
// prompt's first case, and it exists so the wording is not shipped having never been asked of a model.
//
// What it asserts is the one property the prompt is mostly about: a report says what the material said
// and does not add. A fluent report with three plausible invented details is worse than no report,
// because nothing downstream can tell which details were given and which were supplied.
package inference_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/infra/inference"
	"github.com/ensera-ai/taisce/internal/report"
)

func TestALiveReportSaysWhatTheMaterialSaidAndNothingElse(t *testing.T) {
	config, err := inference.ConfigFromEnv()
	if err != nil {
		t.Fatalf("inference is not configured, and this target exists to exercise it: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	material := report.Context{
		Entities: []string{"Marta", "Ensera", "Dublin", "the migration"},
		Facts: []report.Fact{
			{Subject: "Marta", Predicate: "works_at", Object: "Ensera",
				Statement: "Marta works at Ensera.", Quote: "Marta works at Ensera"},
			{Subject: "Marta", Predicate: "lives_in", Object: "Dublin",
				Statement: "Marta lives in Dublin.", Quote: "Marta moved to Dublin last month"},
			{Subject: "Ensera", Predicate: "located_in", Object: "Dublin",
				Statement: "Ensera is in Dublin.", Quote: "the Ensera office is in Dublin"},
			{Subject: "Marta", Predicate: "responsible_for", Object: "the migration",
				Statement: "Marta is responsible for the migration.",
				Quote:     "Marta is running the migration"},
		},
	}

	r, err := report.New(inference.NewReporter(config)).Write(ctx, material)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Logf("title:      %s", r.Title)
	t.Logf("summary:    %s", r.Summary)
	t.Logf("importance: %v — %s", r.Importance, r.ImportanceReason)
	for _, f := range r.Findings {
		t.Logf("finding:    %s — %s", f.Summary, f.Explanation)
	}

	// It is about the subject rather than about the group. A report that names none of the things it
	// was given has described something else.
	written := strings.ToLower(r.Title + " " + r.Summary)
	named := 0
	for _, entity := range []string{"marta", "ensera", "dublin", "migration"} {
		if strings.Contains(written, entity) {
			named++
		}
	}
	if named < 2 {
		t.Fatalf("the report names %d of the four things it was given: %q / %q", named, r.Title, r.Summary)
	}

	// And it did not fill gaps. None of these is in the material, and every one of them is what a
	// model reaches for when asked to describe a person from four relations: a job title, a company
	// it has heard of, a date, a relationship.
	whole := strings.ToLower(written + " " + findingsText(r))
	for _, invented := range []string{"london", "engineer", "manager", "ceo", "google", "microsoft",
		"2019", "2020", "husband", "wife", "family"} {
		if strings.Contains(whole, invented) {
			t.Errorf("the report contains %q, which is in none of the material — the grounding rule "+
				"is not holding, and a fluent report with invented details is worse than none", invented)
		}
	}

	// The importance carries its reason, which is what makes it a claim somebody can disagree with
	// rather than a ranking signal nobody can check.
	if r.Importance > 0 && len(r.ImportanceReason) < 20 {
		t.Errorf("importance %v is asserted with %q, which is not a reason", r.Importance, r.ImportanceReason)
	}
	if len(r.Findings) == 0 {
		t.Error("a report with four relations behind it found nothing specific to say")
	}
}

func findingsText(r report.Report) string {
	var b strings.Builder
	for _, f := range r.Findings {
		b.WriteString(f.Summary)
		b.WriteString(" ")
		b.WriteString(f.Explanation)
		b.WriteString(" ")
	}
	return b.String()
}
