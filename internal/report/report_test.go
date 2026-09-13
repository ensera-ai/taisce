// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package report_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/report"
)

// scripted returns a fixed report and records what it was asked to write from, so the tests assert
// what goes IN — which is the part with a correctness argument. What a model does with good material
// is #70's business, against a live model; what this package must never do is hand it the wrong
// material and never say so.
type scripted struct {
	seen report.Context
	out  report.Report
	err  error
}

func (s *scripted) Write(_ context.Context, c report.Context) (report.Report, error) {
	s.seen = c
	return s.out, s.err
}

func good() report.Report {
	return report.Report{
		Title:            "The Dublin office move",
		Summary:          "The team moved to the Dublin office and the migration followed it.",
		Importance:       7,
		ImportanceReason: "It is the subject the most relations were recorded about.",
		Findings: []report.Finding{
			{Summary: "The office moved", Explanation: "Recorded twice, a week apart."},
		},
	}
}

func facts(prefix string, n int, size int) []report.Fact {
	out := make([]report.Fact, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, report.Fact{
			Subject:   fmt.Sprintf("%s%d", prefix, i),
			Predicate: "works_at",
			Object:    "Ensera",
			Statement: fmt.Sprintf("%s%d works at Ensera.", prefix, i),
			Quote:     strings.Repeat("x", size),
		})
	}
	return out
}

// ── What a report is refused for ──────────────────────────────────────────────────────────────

// A community with nothing recorded about it has nothing to write a report from, and a model asked to
// describe a list of names produces a paragraph that sounds like a subject — the one output nothing
// downstream can tell apart from a real one.
func TestACommunityWithNothingRecordedIsRefused(t *testing.T) {
	model := &scripted{out: good()}
	_, err := report.New(model).Write(context.Background(), report.Context{
		Entities: []string{"Marta", "Ensera"},
	})
	if err == nil {
		t.Fatal("a community with no relations and no sub-reports was described anyway")
	}
	if model.seen.Entities != nil {
		t.Fatal("the model was asked to write from nothing")
	}
}

// A report that cannot do its job is refused rather than stored. Each of these is a way a model
// answers that parses and is unusable.
func TestAReportThatCannotDoItsJobIsRefused(t *testing.T) {
	for name, mutate := range map[string]func(*report.Report){
		"no title":                   func(r *report.Report) { r.Title = "" },
		"a title of spaces":          func(r *report.Report) { r.Title = "   " },
		"no summary":                 func(r *report.Report) { r.Summary = "" },
		"importance above the scale": func(r *report.Report) { r.Importance = 11 },
		"importance below it":        func(r *report.Report) { r.Importance = -1 },
		"importance with no reason":  func(r *report.Report) { r.ImportanceReason = "" },
	} {
		t.Run(name, func(t *testing.T) {
			r := good()
			mutate(&r)
			_, err := report.New(&scripted{out: r}).Write(context.Background(), report.Context{
				Facts: facts("a", 2, 10),
			})
			if err == nil {
				t.Fatalf("%s was accepted", name)
			}
		})
	}

	// And the whole one is not refused, or the test above would pass for the wrong reason.
	if _, err := report.New(&scripted{out: good()}).Write(context.Background(), report.Context{
		Facts: facts("a", 2, 10),
	}); err != nil {
		t.Fatalf("a usable report was refused: %v", err)
	}
}

// Importance of zero needs no reason: "nothing here matters much" is a complete claim, and demanding a
// justification for it would push a model into writing one.
func TestZeroImportanceNeedsNoReason(t *testing.T) {
	r := good()
	r.Importance, r.ImportanceReason = 0, ""
	if _, err := report.New(&scripted{out: r}).Write(context.Background(), report.Context{
		Facts: facts("a", 2, 10),
	}); err != nil {
		t.Fatalf("a report saying the subject does not matter was refused: %v", err)
	}
}

// ── What a report is written from ─────────────────────────────────────────────────────────────

// The same community produces the same context, whatever order the facts arrive in. A model given the
// same material arranged differently writes a different paragraph, and two rebuilds of one community
// would then differ for no reason anybody could name — which matters most exactly when a rebuild
// follows an erasure and somebody is comparing.
func TestTheSameCommunityBuildsTheSameContext(t *testing.T) {
	material := facts("a", 6, 20)
	forwards := report.BuildContext([]string{"Marta", "Ensera"}, material, nil, nil, 100000)

	reversed := make([]report.Fact, len(material))
	for i := range material {
		reversed[i] = material[len(material)-1-i]
	}
	backwards := report.BuildContext([]string{"Ensera", "Marta"}, reversed, nil, nil, 100000)

	if strings.Join(forwards.Entities, ",") != strings.Join(backwards.Entities, ",") {
		t.Fatalf("the entities came back in a different order: %v vs %v",
			forwards.Entities, backwards.Entities)
	}
	for i := range forwards.Facts {
		if forwards.Facts[i] != backwards.Facts[i] {
			t.Fatalf("fact %d differs between two orderings of one community", i)
		}
	}
}

// ── The roll-up ───────────────────────────────────────────────────────────────────────────────

// An oversized parent substitutes a child's report for that child's raw material rather than
// truncating the facts. Every subject stays present at lower resolution; truncation would keep some
// subjects whole and drop others entirely, and which ones is an artefact of ordering.
func TestAnOversizedParentSubstitutesChildrenRatherThanTruncating(t *testing.T) {
	big := facts("big", 10, 200)   // one large child
	small := facts("small", 4, 50) // one small child
	all := append(append([]report.Fact(nil), big...), small...)

	children := map[string][]report.Fact{"big": big, "small": small}
	reports := map[string]report.Report{
		"big":   {Title: "The big subject", Summary: "What the big child is about."},
		"small": {Title: "The small subject", Summary: "What the small child is about."},
	}

	// Enough for the small child's facts and the big child's report, and nowhere near enough for both
	// children's raw material.
	c := report.BuildContext([]string{"Marta"}, all, children, reports, 1200)

	if c.Substituted != 1 {
		t.Fatalf("expected one substitution, got %d", c.Substituted)
	}
	if c.Dropped != 0 {
		t.Fatalf("%d facts were dropped when a substitution would have fitted", c.Dropped)
	}
	// The large child is the one replaced: it frees the most room, so the fewest children lose their
	// detail.
	if len(c.Children) != 1 || c.Children[0].Title != "The big subject" {
		t.Fatalf("the wrong child was substituted: %+v", c.Children)
	}
	// And the subject that was substituted is still represented — as a report rather than as nothing.
	for _, f := range c.Facts {
		if strings.HasPrefix(f.Subject, "big") {
			t.Fatalf("the substituted child's raw material is still in the context: %+v", f)
		}
	}
	if !strings.HasPrefix(c.Facts[0].Subject, "small") {
		t.Fatalf("the child that fitted lost its detail: %+v", c.Facts[0])
	}
}

// Every subject survives a roll-up, which is the property truncation does not have.
func TestNoSubjectDisappearsInARollUp(t *testing.T) {
	children := map[string][]report.Fact{}
	reports := map[string]report.Report{}
	var all []report.Fact
	for _, name := range []string{"work", "family", "hobby", "trip"} {
		owned := facts(name, 6, 150)
		children[name] = owned
		all = append(all, owned...)
		reports[name] = report.Report{Title: name + " subject", Summary: "About " + name + "."}
	}

	// Far too small for any of the raw material.
	c := report.BuildContext([]string{"Marta"}, all, children, reports, 400)

	present := map[string]bool{}
	for _, f := range c.Facts {
		present[strings.TrimRight(f.Subject, "0123456789")] = true
	}
	for _, r := range c.Children {
		present[strings.TrimSuffix(r.Title, " subject")] = true
	}
	for _, name := range []string{"work", "family", "hobby", "trip"} {
		if !present[name] {
			t.Fatalf("%q vanished from the context entirely: %d substituted, %d dropped, %d facts left",
				name, c.Substituted, c.Dropped, len(c.Facts))
		}
	}
}

// A child with no report of its own cannot be substituted: replacing its facts with nothing would
// remove the subject rather than compress it.
func TestAChildWithNoReportIsNotSubstitutedAway(t *testing.T) {
	unwritten := facts("unwritten", 8, 200)
	written := facts("written", 8, 200)
	all := append(append([]report.Fact(nil), unwritten...), written...)

	c := report.BuildContext([]string{"Marta"},
		all,
		map[string][]report.Fact{"unwritten": unwritten, "written": written},
		map[string]report.Report{"written": {Title: "Written", Summary: "It has a report."}},
		1000)

	if c.Substituted != 1 || len(c.Children) != 1 || c.Children[0].Title != "Written" {
		t.Fatalf("the child without a report was substituted away: %+v", c)
	}
}

// When even every substitution is not enough, facts are dropped and the count is reported. A context
// that silently returned less would make the report a claim about the whole subject written from part
// of it.
func TestWhatWillNotFitIsDroppedAndCounted(t *testing.T) {
	all := facts("a", 20, 500)
	c := report.BuildContext([]string{"Marta"}, all, nil, nil, 1000)

	if c.Dropped == 0 {
		t.Fatal("a context far over budget dropped nothing and said nothing")
	}
	if c.Dropped+len(c.Facts) != len(all) {
		t.Fatalf("%d facts in, %d kept and %d reported dropped", len(all), len(c.Facts), c.Dropped)
	}
	if size := sizeOf(c.Facts); size > 1000 {
		t.Fatalf("the context is %d characters against a budget of 1000", size)
	}
}

// A community that fits is left alone: nothing substituted, nothing dropped, every fact in full. The
// roll-up is what happens when there is too much material, not a stage every report pays for.
func TestACommunityThatFitsIsLeftAlone(t *testing.T) {
	all := facts("a", 5, 30)
	c := report.BuildContext([]string{"Marta"},
		all,
		map[string][]report.Fact{"a": all},
		map[string]report.Report{"a": {Title: "A", Summary: "s"}},
		100000)

	if c.Substituted != 0 || c.Dropped != 0 || len(c.Facts) != len(all) {
		t.Fatalf("a context that fitted was cut anyway: %d substituted, %d dropped, %d of %d facts",
			c.Substituted, c.Dropped, len(c.Facts), len(all))
	}
}

// ── What this package does not do ─────────────────────────────────────────────────────────────

// It writes nothing. Whether a report is stored, when it is rebuilt and what an erasure does to it is
// a decision above this package, and a function that reached out to storage would carry that
// decision inside it where it could not be changed.
func TestWritingAReportTouchesNothingOutsideIt(t *testing.T) {
	model := &scripted{out: good()}
	r, err := report.New(model).Write(context.Background(), report.Context{Facts: facts("a", 3, 10)})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	// The same context, twice, produces the same report from the same material: there is no state a
	// second report depends on, which is what makes a rebuild after an erasure produce the same thing
	// as a first build.
	again, err := report.New(model).Write(context.Background(), report.Context{Facts: facts("a", 3, 10)})
	if err != nil {
		t.Fatalf("write again: %v", err)
	}
	if r.Title != again.Title || r.Summary != again.Summary {
		t.Fatal("two writes of one community produced different reports")
	}
}

func sizeOf(facts []report.Fact) int {
	total := 0
	for _, f := range facts {
		total += f.Size()
	}
	return total
}

// Equal names and quotes do not identify a fact's owner. Substituting Alice's child report must
// retain Bob's raw material, even when they used identical words about the same organization.
func TestChildSubstitutionUsesEntityIdentityRatherThanSpeakerDisplayName(t *testing.T) {
	alice := report.Fact{SubjectID: "alice-entity", ObjectID: "company-entity", Subject: "speaker", Predicate: "works_at", Object: "Ensera", Quote: strings.Repeat("I work at Ensera. ", 20)}
	bob := alice
	bob.SubjectID = "bob-entity"
	childReports := map[string]report.Report{"a": {Title: "Alice", Summary: "one"}, "b": {Title: "Bob", Summary: "two"}}
	got := report.BuildContext([]string{"speaker", "speaker", "Ensera"}, []report.Fact{alice, bob},
		map[string][]report.Fact{"a": {alice}, "b": {bob}}, childReports, bob.Size()+20)
	if got.Substituted != 1 || len(got.Facts) != 1 || got.Facts[0].SubjectID != bob.SubjectID || len(got.Children) != 1 || got.Children[0].Title != "Alice" {
		t.Fatalf("substitution removed the wrong speaker: %+v", got)
	}
	withoutIDs := alice
	withoutIDs.SubjectID = ""
	withoutIDs.ObjectID = ""
	extra := len(alice.SubjectLabel()) - len(alice.Subject) + len(alice.ObjectLabel()) - len(alice.Object)
	if alice.Size()-withoutIDs.Size() != extra {
		t.Fatal("opaque references bypass the material budget")
	}
	cut := report.BuildContext(nil, []report.Fact{alice}, nil, nil, withoutIDs.Size())
	if len(cut.Facts) != 0 || cut.Dropped != 1 {
		t.Fatal("a reference-bearing fact exceeded its budget")
	}
}

// TestAReportForAChildThatOwnsNoFactsStandsInForNothing covers the roll-up's last resort.
//
// A child report only replaces material that child owns. When the context is over budget and the
// only reports on offer belong to children with no facts here, substituting one would add a summary
// while removing nothing — the context would grow. So nothing is substituted, the facts are cut to fit
// from the end, and the cut is counted, which is what lets the report say it was written from part of
// its subject.
func TestAReportForAChildThatOwnsNoFactsStandsInForNothing(t *testing.T) {
	all := facts("a", 20, 500)
	c := report.BuildContext([]string{"Marta"}, all, nil,
		map[string]report.Report{"elsewhere": {Title: "A subject with no facts here", Summary: "It owns nothing in this community."}},
		1000)

	if c.Substituted != 0 || len(c.Children) != 0 {
		t.Fatalf("a report whose child owns nothing here was substituted in: %+v", c.Children)
	}
	if c.Dropped == 0 || c.Dropped+len(c.Facts) != len(all) {
		t.Fatalf("an over-budget context must drop facts and count every one: %d dropped, %d kept, %d given",
			c.Dropped, len(c.Facts), len(all))
	}
}
