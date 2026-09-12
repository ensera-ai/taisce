// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

// Package report turns a community of entities into prose about the subject it represents.
//
// A group of entity identifiers is not an answer. A thematic question needs something a person or a
// model can read, and something to search that is not a passage — because a thematic question
// resembles no single passage, which is what makes it thematic.
//
// # What this package is allowed to decide
//
// What material a report is written from, how that material is cut when there is too much of it, and
// what shape the result has. It does not decide whether a report is stored, when it is rebuilt, or
// what an erasure does to it — that is D57, and keeping it out of here is what lets that answer change
// without touching how a report is made. Nothing in this package writes anything anywhere.
//
// It also does not write the prose. A model does, behind a port, for the same reason extraction does:
// the parts carrying a correctness argument — what goes in, what is refused, what the caller receives
// — belong in code that does not vary with the vendor.
package report

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Fact is one asserted relation inside a community, with the words behind it.
//
// The quote travels with the statement because a report written from statements alone is a summary of
// summaries: the extractor already compressed a sentence into a triple, and compressing that again
// loses the thing a reader would check. What the model sees is what was said.
type Fact struct {
	// Source identity is governance metadata. It is never rendered into model input.
	SourceObservationID string
	SourceRevision      int64
	// Entity IDs distinguish equal displayed names. They are projection identities, not protected
	// data-subject keys; empty IDs retain compatibility with caller-supplied named-only material.
	SubjectID string
	ObjectID  string
	Subject   string
	Predicate string
	Object    string
	Statement string
	Quote     string
}

// Size is what one fact costs a context, in characters.
func (f Fact) Size() int {
	return len(f.SubjectLabel()) + len(f.Predicate) + len(f.ObjectLabel()) + len(f.Statement) + len(f.Quote)
}

// Labels carry disambiguation into model input without changing the recorded statement or quote.
func (f Fact) SubjectLabel() string { return entityLabel(f.Subject, f.SubjectID) }
func (f Fact) ObjectLabel() string  { return entityLabel(f.Object, f.ObjectID) }
func entityLabel(name, id string) string {
	if id == "" {
		return name
	}
	return name + " [entity:" + id + "]"
}

// Report is what a community says about itself.
//
// # Why a structure rather than prose
//
// The parts are read separately. A title is what a caller sees in a list, a summary is what goes into
// a bundle, and the findings carry the claims that can be checked against the facts underneath. One
// blob would have to be re-parsed by everything that reads it, and a model asked for a blob returns a
// different shape each time — so the parse would be a guess repeated at every call site.
type Report struct {
	// Loaded from persisted dependency registrations when a child is substituted. Model-returned
	// values are ignored by persistence; only the supplied context determines report provenance.
	SourceObservationIDs []string
	SourceRevisions      map[string]int64
	// Title names the subject, not the community. "The Dublin office move" rather than "Community 7".
	Title string
	// Summary is the paragraph a thematic answer is built from.
	Summary string
	// Importance is how much this subject appears to matter, 0 to 10, WITH the reason for it.
	//
	// The number alone would be a ranking signal nobody could check, and this system does not
	// introduce a ranking stage without a measurement that justifies it. Carried together, it is a
	// claim a reader can disagree with — which is what makes it usable now and replaceable later.
	Importance       float32
	ImportanceReason string
	// Findings are the specific things the subject consists of, each with the reasoning behind it.
	Findings []Finding
}

// Finding is one claim a report makes, with the material behind it.
type Finding struct {
	Summary     string
	Explanation string
}

// Context is everything a report is written from.
//
// # Why child reports and raw facts are both here
//
// A community too large to describe from its facts is described from what its parts already said. Both
// forms appear in one structure because the substitution is a property of a single context — some
// children replaced, some not — rather than two different kinds of request.
type Context struct {
	// Entities are the members of the community, sorted.
	Entities []string
	// Facts are the relations among them.
	Facts []Fact
	// Children are reports of sub-communities that replaced their own raw material.
	Children []Report
	// Substituted is how many children were replaced, and Dropped how many facts did not fit even
	// after every substitution. Reported rather than silent: a summary written from less than the
	// whole subject is a different claim, and whatever reads this has to be able to say so.
	Substituted int
	Dropped     int
}

// Model writes a report from a context. It is a port for the same reason extraction's is.
type Model interface {
	Write(ctx context.Context, c Context) (Report, error)
}

// Writer produces reports. It holds a model and nothing else: there is no state a second report
// depends on, which is what makes a rebuild after an erasure produce the same thing as a first build.
type Writer struct {
	model Model
}

// New builds a writer over a model.
func New(model Model) *Writer { return &Writer{model: model} }

// Model returns the model this writer asks, so a caller can ask it who it is. Reports carry the
// identity of what wrote them, and the only thing that knows it is the model itself.
func (w *Writer) Model() Model { return w.model }

// Write produces the report for one community.
//
// A community with no facts is refused rather than described. There is nothing to write a summary
// from, and a model asked to describe a list of names will produce a paragraph that sounds like a
// subject — which is the one output that cannot be told apart from a real one downstream.
func (w *Writer) Write(ctx context.Context, c Context) (Report, error) {
	if len(c.Facts) == 0 && len(c.Children) == 0 {
		return Report{}, fmt.Errorf("a community with no relations and no sub-reports has nothing to " +
			"write a report from")
	}
	r, err := w.model.Write(ctx, c)
	if err != nil {
		return Report{}, err
	}
	if err := r.validate(); err != nil {
		return Report{}, err
	}
	return r, nil
}

// validate refuses a report that cannot do its job.
//
// A report with no summary has nothing to put in a bundle, and one with no title is unusable in a
// list. An importance outside its range is a model that did not read the scale, and admitting it would
// put a number nobody can interpret next to a subject.
func (r Report) validate() error {
	if strings.TrimSpace(r.Title) == "" {
		return fmt.Errorf("a report with no title cannot be shown in a list of subjects")
	}
	if strings.TrimSpace(r.Summary) == "" {
		return fmt.Errorf("a report with no summary has nothing a thematic answer could be built from")
	}
	if r.Importance < 0 || r.Importance > 10 {
		return fmt.Errorf("importance is %v, which is outside the scale the report is read against",
			r.Importance)
	}
	if r.Importance > 0 && strings.TrimSpace(r.ImportanceReason) == "" {
		return fmt.Errorf("importance %v is asserted with no reason, which is a ranking signal nobody "+
			"can check", r.Importance)
	}
	return nil
}

// BuildContext assembles what a community's report is written from, within a budget.
//
// # Substitution rather than truncation
//
// When the material does not fit, a child's raw facts are replaced by that child's REPORT, largest
// child first. Every subject stays present at lower resolution.
//
// Truncating the facts instead would keep some subjects whole and drop others entirely, and which ones
// are dropped is an artefact of the order they happen to be in rather than of what matters. That
// failure is invisible from the output: the report reads perfectly and is about two thirds of a
// subject.
//
// # Largest first
//
// The largest child frees the most room per substitution, so the fewest children are replaced. Each
// substitution costs resolution — a paragraph instead of the sentences behind it — so making as few as
// possible is making the report as detailed as the budget allows.
//
// # What happens when even that is not enough
//
// Facts are dropped, and the count is reported. There is no arrangement of a fixed budget that fits an
// unbounded community, and a context that silently returned less would make the report a claim about
// the whole subject written from part of it.
func BuildContext(entities []string, facts []Fact, children map[string][]Fact,
	childReports map[string]Report, budget int) Context {

	c := Context{Entities: append([]string(nil), entities...)}
	sort.Strings(c.Entities)

	// Deterministic input, deterministic report. A model given the same facts in a different order
	// writes a different paragraph, and two rebuilds of one community would then differ for no reason
	// anybody could name.
	ordered := append([]Fact(nil), facts...)
	sort.Slice(ordered, func(i, j int) bool { return factKey(ordered[i]) < factKey(ordered[j]) })

	// Which child each fact belongs to, so a substitution removes exactly that child's material.
	owner := map[string]string{}
	for child, owned := range children {
		for _, f := range owned {
			owner[factKey(f)] = child
		}
	}

	replaced := map[string]bool{}
	for {
		kept, size := keep(ordered, owner, replaced)
		if size <= budget || len(replaced) == len(childReports) {
			c.Facts = kept
			c.Substituted = len(replaced)
			c.Children = reportsOf(childReports, replaced)
			// Still over after every substitution: drop from the end, and say how many.
			for len(c.Facts) > 0 && sizeOf(c.Facts, c.Children) > budget {
				c.Facts = c.Facts[:len(c.Facts)-1]
				c.Dropped++
			}
			return c
		}
		next := largestUnreplaced(children, replaced, childReports)
		if next == "" {
			c.Facts = kept
			c.Substituted = len(replaced)
			c.Children = reportsOf(childReports, replaced)
			for len(c.Facts) > 0 && sizeOf(c.Facts, c.Children) > budget {
				c.Facts = c.Facts[:len(c.Facts)-1]
				c.Dropped++
			}
			return c
		}
		replaced[next] = true
	}
}

func keep(facts []Fact, owner map[string]string, replaced map[string]bool) ([]Fact, int) {
	var kept []Fact
	for _, f := range facts {
		if replaced[owner[factKey(f)]] {
			continue
		}
		kept = append(kept, f)
	}
	return kept, sizeOf(kept, nil)
}

func sizeOf(facts []Fact, children []Report) int {
	total := 0
	for _, f := range facts {
		total += f.Size()
	}
	for _, r := range children {
		total += len(r.Title) + len(r.Summary)
		for _, f := range r.Findings {
			total += len(f.Summary) + len(f.Explanation)
		}
	}
	return total
}

// largestUnreplaced picks the child whose raw material is biggest, so one substitution frees the most.
// Ties break on the child's name, so the choice is the same on every rebuild.
func largestUnreplaced(children map[string][]Fact, replaced map[string]bool,
	reports map[string]Report) string {

	best, bestSize := "", -1
	names := make([]string, 0, len(children))
	for name := range children {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if replaced[name] {
			continue
		}
		if _, written := reports[name]; !written {
			// A child with no report of its own cannot be substituted: replacing its facts with
			// nothing would remove the subject rather than compress it.
			continue
		}
		size := sizeOf(children[name], nil)
		if size > bestSize {
			best, bestSize = name, size
		}
	}
	return best
}

func reportsOf(reports map[string]Report, replaced map[string]bool) []Report {
	names := make([]string, 0, len(replaced))
	for name := range replaced {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]Report, 0, len(names))
	for _, name := range names {
		out = append(out, reports[name])
	}
	return out
}

// factKey identifies a fact within a context. The quote is part of it: two relations between the same
// ends from different sentences are two pieces of material, and collapsing them would drop one.
func factKey(f Fact) string {
	return f.SubjectID + "\x00" + f.ObjectID + "\x00" + f.Subject + "\x00" + f.Predicate + "\x00" + f.Object + "\x00" + f.Quote
}
