// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

// Package recall answers a question from entities outward.
//
// Retrieval starts at the entities a question names and returns the facts about them. It does not
// search passages. Question expansion and anchor matches have admission ceilings; graph traversal
// has hop and fanout bounds. Anchor index work still depends on stored name/alias distributions.
package recall

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ensera-ai/taisce/internal/domain"
)

// longestAnchor is the most words an entity name is assumed to have.
//
// Four. Every window up to this length is looked up, so the cost of raising it is a longer candidate
// list on every recall and the cost of lowering it is entities whose names are simply unreachable.
// Names longer than this exist; they are reached by whichever shorter window matches an alias.
const longestAnchor = 4

// Store is the read this package needs. It is an interface here so that what recall DOES is
// separable from where the rows are — and so the anchoring rules can be read without reading SQL.
type Store interface {
	// Anchors resolves candidate terms to entities within the authorised scopes.
	AnchorsForSubject(ctx context.Context, scopes, terms []string, subject string) ([]domain.Anchor, error)
	// FactsAbout returns the currently-valid facts naming any of the given entities, with their
	// evidence, up to limit+1 rows so the caller can tell a full result from a truncated one.
	//
	// `sources` is the set of message roles whose facts may be returned, and it is an argument
	// rather than a default so that no call can omit it and get everything.
	//
	// `at` is the pair of instants the read is taken at. Its zero value is the current read, which
	// is what almost every call wants — so the historical read is available without the ordinary
	// one having to say anything.
	//
	// `hops` is how far from the anchors the traversal may go. One is the ordinary read: facts about
	// what the question named. More is a chain, and it comes back with the relations it followed.
	FactsAboutForSubject(ctx context.Context, scopes, entityIDs []string, limit int, sources []string,
		at domain.AsOf, hops int, subject string) ([]domain.CitedFact, error)
}

// Budget is how much a caller can afford to be given back.
//
// # Why characters and not tokens
//
// A caller's constraint is tokens; ours cannot be. A token count is a property of the tokeniser the
// caller's model uses, which is not the one we would use, and the difference is a few percent that
// varies by model and by text. Reporting a token count we computed with the wrong tokeniser is a
// number that is wrong in a way the caller cannot correct — and it would be believed, because it is
// labelled tokens.
//
// Characters are exact, stable, and convertible by whoever knows their own model. A caller with an
// eight-thousand-token allowance and a model averaging four characters to the token asks for thirty-two
// thousand characters. That is their arithmetic with their number, rather than ours with a guess.
//
// # Why not rows
//
// A row is not a size. One fact carries a two-word quote and another carries a paragraph, so a caller
// converting rows to anything has to assume the worst — which means asking for far less than they can
// afford, every time, to avoid overrunning once.
type Budget struct {
	// Characters bounds what comes back. Zero takes the default.
	Characters int
	// MaxRows bounds the query rather than the answer. It exists so an enormous budget cannot ask
	// the database for an unbounded read, and it is not the unit a caller reasons in.
	MaxRows int
}

// DefaultBudget is deliberately modest. It is a cut nothing has ranked, so a caller who has not said
// what they can afford should be given a small arbitrary slice rather than a large one.
func DefaultBudget() Budget { return Budget{Characters: 16000, MaxRows: 200} }

// Recaller answers questions against one instance's memory.
type Recaller struct {
	store  Store
	budget Budget
	// semantic is nil when the deployment has no embedding revision; then only the exact path runs.
	semantic Semantic
	// surfaces and themes are per-call selections copied in by RecallWithControls; the zero value
	// selects every surface and no forced theme lookup.
	surfaces map[string]bool
	themes   bool
}

// There is deliberately no constructor taking a bare number.
//
// One existed and took a ROW limit. When the unit became characters it kept compiling and changed
// meaning, so every caller passing 50 silently went from fifty facts to fifty characters — which is
// the worst kind of change, because nothing fails and the behaviour is wrong. A compile error is the
// cheaper outcome, so the number now arrives inside a type that names its unit.

// NewWithBudget builds a recaller over an explicit budget.
func NewWithBudget(store Store, b Budget) *Recaller {
	if b.Characters <= 0 {
		b.Characters = DefaultBudget().Characters
	}
	if b.MaxRows <= 0 {
		b.MaxRows = DefaultBudget().MaxRows
	}
	return &Recaller{store: store, budget: b}
}

// Recall resolves a question to entities and returns the facts naming them.
//
// # The authorised scopes are an argument, never a default
//
// A project is a permission inside one instance, and a principal granted several recalls across them
// in one bundle. So the set comes from the caller on every read. There is no "all scopes" and no
// empty-means-everything: an empty set returns nothing, because a read that widens when its
// permission list is missing is the failure that permission list exists to prevent.
//
// # No anchor means an empty bundle, not an error
//
// A question naming nothing we know about is an ordinary outcome — it is most questions, early in a
// scope's life. A caller made to distinguish that from a failure will end up treating failures as
// ordinary.
// PrincipalSources is what a recall returns unless the caller asks for more: facts extracted from
// what the principal themselves said.
//
// # Why this is the default rather than an option
//
// A tool result, a fetched page or a document is written by somebody who is not the person the memory
// is about, and text reaching extraction from those sources has been measured planting a fact that
// passes every structural check the product makes — an admitted relation, a verbatim quote, an exact
// span. Detecting that is undecidable. Knowing who said it is not.
//
// So the ordinary bundle contains what the person said. Everything else is still stored, still
// citable, and still returned to a caller who asks for it — labelled, so a reader can tell a
// statement from something a web page claimed.
var PrincipalSources = []string{string(domain.RoleUser)}

// Recall answers a question from what the principal said, as things stand now, about the entities
// the question names.
func (r *Recaller) Recall(ctx context.Context, scopes []string, question string) (domain.Bundle, error) {
	return r.RecallAsOf(ctx, scopes, question, PrincipalSources, domain.AsOf{}, DirectHop)
}

// DirectHop is the ordinary read: facts about what the question named, and nothing reached through
// them.
//
// # Why one is the default rather than two
//
// A second hop is paid for on every call by every caller, and most questions are answered by the
// first. It also changes what a bundle IS: at one hop every fact is about something the question
// named, and at two some facts are about something else entirely, reached through a relation the
// caller did not ask about. That is genuinely useful for "who does Marta's manager work for" and
// genuinely noise for "where does Marta live", and nothing here can tell which question it was given.
//
// So the caller says. A default that quietly widened would make every bundle bigger, every read
// slower and every answer harder to explain, in exchange for helping the minority of questions that
// need a chain.
const DirectHop = 1

// MaxHops is as far as a traversal goes.
//
// Two, because two is what has a reader: "my manager's employer" is a real question and a chain of
// four is one nobody has asked for. The cost of a third hop is not linear — each level multiplies by
// the fanout cap — so raising this is a decision that arrives with a measurement rather than with a
// larger number.
const MaxHops = 2

// There is deliberately no RecallFrom taking sources and no instants.
//
// One existed and became a delegate the moment the instants arrived: two surfaces where the second
// is the first with a zero value appended. A caller wanting the wider source set writes
// `RecallAsOf(ctx, scopes, question, sources, domain.AsOf{})`, where the zero value says in the call
// what the missing method said in its name.

// RecallAsOf answers a question as it would have been answered at a moment.
//
// # Two instants, because a memory has two histories
//
// What was true of the world, and what this system had been told. They come apart on every
// correction, and only both together answer "why did you tell me that last week" — which is the
// question an audit asks and the one a scalar timestamp cannot answer.
//
// # A past read is not a different product
//
// It anchors the same way, bounds the same scopes, admits the same sources and is cut by the same
// budget. The only difference is which rows are current, so nothing about a historical bundle is
// weaker — and every fact in one carries the date its validity ended, which is what stops a fact that
// held in March from reading as a fact that holds now.
func (r *Recaller) RecallAsOf(ctx context.Context, scopes []string, question string,
	sources []string, at domain.AsOf, hops int) (domain.Bundle, error) {
	return r.RecallForSubjectAsOf(ctx, scopes, question, sources, at, hops, "")
}

// RecallForSubjectAsOf narrows attribution inside the authorised projects. Empty subject preserves
// project-wide recall; a supplied subject is applied by the store to anchors, each hop and evidence.
func (r *Recaller) RecallForSubjectAsOf(ctx context.Context, scopes []string, question string,
	sources []string, at domain.AsOf, hops int, subject string) (domain.Bundle, error) {
	if len(question) > domain.MaxRecallQuestionBytes {
		return domain.Bundle{}, domain.ErrRecallQuestionSize
	}
	hops = clampHops(hops)
	if len(scopes) == 0 {
		return domain.Bundle{}, nil
	}
	terms := Terms(question)
	if err := domain.ValidateRecallTerms(terms); err != nil {
		return domain.Bundle{}, err
	}
	if len(terms) == 0 {
		return domain.Bundle{}, nil
	}
	reach := domain.Reach{Terms: len(terms), FactsPerAnchor: map[string]int{}}

	anchors, err := r.store.AnchorsForSubject(ctx, scopes, terms, subject)
	if err != nil {
		return domain.Bundle{}, err
	}
	if len(anchors) > domain.MaxRecallMatches {
		return domain.Bundle{}, domain.ErrRecallMatches
	}
	exact := len(anchors)
	bundle := domain.Bundle{}
	// Anchors the semantic surface proposed, as opposed to names the question matched. Under a
	// subject filter one survives only if that person has a fact about it.
	semantic := map[string]bool{}
	wants := func(surface string) bool { return r.surfaces == nil || r.surfaces[surface] }
	degrade := func(name string) {
		for _, d := range bundle.Degraded {
			if d == name {
				return
			}
		}
		bundle.Degraded = append(bundle.Degraded, name)
	}
	// Semantic anchors, only when exact anchoring found nothing: an entity named by meaning
	// rather than by a stored name. A proposal is a place to start a walk, marked as such so the
	// caller can see a meaning match was used; it never merges identities.
	// A deployment without semantic surfaces answers exactly as before and says nothing about them,
	// unless the caller selected surfaces by name and so asked for something it cannot do.
	if len(anchors) == 0 && wants(SurfaceFacts) {
		if r.semantic == nil {
			if r.surfaces != nil {
				degrade(domain.DegradedSemanticAnchors)
			}
		} else {
			for _, scope := range scopes {
				proposed, err := r.semantic.EntityAnchors(ctx, scope, question, MaxSemanticAnchors)
				if err != nil {
					degrade(domain.DegradedSemanticAnchors)
					break
				}
				for _, a := range proposed {
					semantic[a.Entity.ID] = true
				}
				anchors = append(anchors, proposed...)
				if len(anchors) >= MaxSemanticAnchors {
					anchors = anchors[:MaxSemanticAnchors]
					break
				}
			}
		}
	}
	if !wants(SurfaceFacts) {
		// Deselecting facts deselects them, whatever the question named. The walk is the facts
		// surface, so it does not run, and neither anchors nor facts come back; the names it matched
		// are still counted, because matching them is true.
		reach.Anchored = exact
		bundle.Reach = reach
		return r.composeRest(ctx, scopes, question, sources, subject, bundle, 0, exact == 0), nil
	}
	if len(anchors) == 0 {
		// Nothing to walk from. "We have never heard of what you asked about" and "we know that and
		// have nothing to say" are different answers, and a caller that cannot tell them apart
		// cannot decide whether to write something or ask differently. Themes and passages may
		// still answer below, which is what makes an unanchored question answerable at all.
		return r.composeUnanchored(ctx, scopes, question, sources, subject, bundle, reach)
	}

	ids := make([]string, 0, len(anchors))
	for _, a := range anchors {
		ids = append(ids, a.Entity.ID)
	}

	// One more than the row bound, so a full result is distinguishable from a truncated one without
	// a second count query. The row bound is a guard on the QUERY — it stops an enormous budget
	// asking the database for an unbounded read — and is not the unit the caller reasons in.
	facts, err := r.store.FactsAboutForSubject(ctx, scopes, ids, r.budget.MaxRows+1, sources, at, hops, subject)
	if err != nil {
		return domain.Bundle{}, err
	}

	bundle.Anchors = anchors
	if len(facts) > r.budget.MaxRows {
		facts = facts[:r.budget.MaxRows]
		bundle.Truncated = true
	}

	// Cut by what the caller can afford. Nothing has ranked these, so the cut drops arbitrary
	// members rather than the least useful ones — which is why the bundle says it was cut, and why
	// the unit matters: arbitrary measured in characters is usable, arbitrary measured in rows is
	// not, because one fact is a two-word quote and another is a paragraph.
	spent := 0
	for _, f := range facts {
		size := factSize(f)
		if size > r.budget.Characters-spent {
			// Never exceed a caller's context allowance, including for the first fact. Truncated
			// distinguishes an empty budget cut from an ordinary question with no matching facts.
			bundle.Truncated = true
			break
		}
		bundle.Facts = append(bundle.Facts, f)
		spent += size
	}
	bundle.Characters = spent

	// After the cut, not before. Co-derivation is a statement about what is IN the bundle, and naming
	// a fact the caller cannot see would be a citation that resolves to nothing.
	markCoDerived(bundle.Facts)

	// Counted over what the caller can see, after the cut, for the same reason co-derivation is: a
	// count describing rows nobody received describes something else.
	for _, f := range bundle.Facts {
		if f.AnchoredOn != "" {
			reach.FactsPerAnchor[f.AnchoredOn]++
		}
		// A citation with no surrounding words is a receipt that lost its legibility, which happens
		// when the message was erased. Said once for the bundle rather than per fact: the caller
		// needs to know some citation is bare, and which ones is visible in the facts themselves.
		if f.Evidence.Context.Text == "" && bundle.Degraded == nil {
			bundle.Degraded = []string{domain.DegradedCitationContext}
		}
	}
	if subject != "" && len(semantic) > 0 {
		// An entity is shared across everybody the project holds. A name the semantic surface
		// proposed, that this person has no fact about, is somebody else's memory — the exact path
		// already refuses such a name, and this is the same rule for the other door.
		kept := make([]domain.Anchor, 0, len(bundle.Anchors))
		for _, a := range bundle.Anchors {
			if !semantic[a.Entity.ID] || reach.FactsPerAnchor[a.Entity.ID] > 0 {
				kept = append(kept, a)
			}
		}
		bundle.Anchors = kept
	}
	// Counted after the semantic anchors, so a bundle answered through one does not also say the
	// question named nothing this memory holds.
	reach.Anchored = len(bundle.Anchors)
	bundle.Reach = reach
	return r.composeRest(ctx, scopes, question, sources, subject, bundle, spent, exact == 0), nil
}

// composeUnanchored answers a question that anchored nothing: reports by theme, then passages, each
// charged to the budget. The reach still says nothing was anchored, because that is still true.
func (r *Recaller) composeUnanchored(ctx context.Context, scopes []string, question string, sources []string,
	subject string, bundle domain.Bundle, reach domain.Reach) (domain.Bundle, error) {
	bundle.Reach = reach
	return r.composeRest(ctx, scopes, question, sources, subject, bundle, 0, true), nil
}

// composeRest adds the report and passage surfaces after the facts, in that order, under what the
// budget has left. Reports run when nothing anchored or the caller asked for themes; passages run
// whenever the budget is not yet spent. A surface that is off, unavailable or refusing is named in
// Degraded and the bundle answers with the rest.
// unanchored is whether the question matched no name exactly; reports answer such a question by
// theme, and a semantic anchor does not change that.
func (r *Recaller) composeRest(ctx context.Context, scopes []string, question string, sources []string,
	subject string, bundle domain.Bundle, spent int, unanchored bool) domain.Bundle {
	wants := func(surface string) bool { return r.surfaces == nil || r.surfaces[surface] }
	degrade := func(name string) {
		for _, d := range bundle.Degraded {
			if d == name {
				return
			}
		}
		bundle.Degraded = append(bundle.Degraded, name)
	}
	if wants(SurfaceReports) && (r.themes || unanchored) {
		if subject != "" {
			// Reports span people and name nobody as their source, so a recall narrowed to one person
			// cannot be answered from one. Said, not silently skipped.
			degrade(domain.DegradedReportsWithheld)
		} else if r.semantic == nil {
			if r.surfaces != nil {
				degrade(domain.DegradedReports)
			}
		} else {
			for _, scope := range scopes {
				hits, err := r.semantic.Reports(ctx, scope, question, MaxReportHits, MaxReportChildren)
				if err != nil {
					degrade(domain.DegradedReports)
					break
				}
				for _, h := range hits {
					size := reportSize(h)
					if size > r.budget.Characters-spent {
						bundle.Truncated = true
						break
					}
					bundle.Reports = append(bundle.Reports, h)
					spent += size
				}
			}
		}
	}
	if wants(SurfacePassages) && spent < r.budget.Characters {
		if r.semantic == nil {
			if r.surfaces != nil {
				degrade(domain.DegradedPassages)
			}
		} else {
			for _, scope := range scopes {
				passages, err := r.semantic.Passages(ctx, scope, question, MaxPassageHits, sources, subject)
				if err != nil {
					degrade(domain.DegradedPassages)
					break
				}
				for _, p := range passages {
					size := passageSize(p)
					if size > r.budget.Characters-spent {
						bundle.Truncated = true
						break
					}
					bundle.Passages = append(bundle.Passages, p)
					spent += size
				}
			}
		}
	}
	bundle.Characters = spent
	return bundle
}

// Terms returns the candidate entity names a question might be naming.
//
// # Why every window rather than one pass of named-entity recognition
//
// A model call per question would put a second inference hop in front of every read, on the path a
// caller is waiting on, to answer something a lookup already answers: an entity is in memory or it
// is not, and its name is a string we already hold. Windowing the question and asking the index is
// exact, has no vocabulary of its own, and costs one query.
//
// # Why the windows are normalised the same way entity names are
//
// The resolution key is `NormalizeName`, so anything that generates lookup candidates has to produce
// them in exactly that shape. Any other normalisation here would silently fail to match entities
// that are in fact stored — the worst kind of miss, because the entity is right there.
//
// Punctuation is dropped at token boundaries and nowhere else, so `work?` reaches `work` while
// `o'brien` and `co-op` survive as written.
// Budget is what the recaller was built with, for the operations that share it.
func (r *Recaller) Budget() Budget { return r.budget }

func Terms(question string) []string {
	words := strings.FieldsFunc(question, func(r rune) bool {
		return unicode.IsSpace(r) || (unicode.IsPunct(r) && r != '\'' && r != '-')
	})
	for i, w := range words {
		words[i] = domain.NormalizeName(strings.Trim(w, "'-"))
	}

	seen := make(map[string]bool, len(words)*longestAnchor)
	var terms []string
	for start := range words {
		for length := 1; length <= longestAnchor && start+length <= len(words); length++ {
			term := strings.Join(words[start:start+length], " ")
			if term == "" || seen[term] {
				continue
			}
			seen[term] = true
			terms = append(terms, term)
		}
	}
	return terms
}

// markCoDerived says which facts in a bundle are not independent of each other.
//
// Two facts from the same message about the same subject are one person saying one thing once. They
// may be restatements at different granularities, or genuinely different claims that happen to share
// a sentence — this does not decide which, because nothing deterministic can.
//
// What it prevents is the reader's error: counting them as corroboration. A model given a bundle with
// two rows saying nearly the same thing treats the repetition as weight, and the repetition is an
// artefact of how one sentence was extracted rather than evidence of anything.
//
// Computed here rather than stored, because it is a property of a BUNDLE and not of a fact: the same
// two facts in a bundle that contains only one of them are independent of nothing.
func markCoDerived(facts []domain.CitedFact) {
	// Keyed on the message, not the turn. Evidence names the message ordinal for the reason a span
	// does: two messages of one turn are two speakers, and facts from different ones are genuinely
	// independent even inside a single exchange.
	type origin struct {
		observation string
		ordinal     int
		subject     string
	}
	groups := map[origin][]int{}
	for i, f := range facts {
		if f.Evidence.SourceObservationID == "" || f.Subject == "" {
			// A fact whose evidence or subject is gone — an erased entity — is independent of
			// nothing rather than co-derived with everything, which is what an empty key would make
			// it.
			continue
		}
		key := origin{f.Evidence.SourceObservationID, f.Evidence.SourceOrdinal, NormalizeSubject(f.Subject)}
		groups[key] = append(groups[key], i)
	}

	for _, members := range groups {
		if len(members) < 2 {
			continue
		}
		for _, i := range members {
			for _, j := range members {
				// By identity, not position: the same fact twice is one fact, never co-derived with
				// itself.
				if i != j && facts[i].ID != facts[j].ID {
					facts[i].CoDerivedWith = append(facts[i].CoDerivedWith, facts[j].ID)
				}
			}
		}
	}
}

// NormalizeSubject compares subjects the way entity resolution does, so two facts naming one person
// in different casing are recognised as being about the same subject.
func NormalizeSubject(s string) string { return domain.NormalizeName(s) }

// factSize counts Unicode code points in the fact text a caller places in context. Quote and
// surrounding context are both charged because both are returned. Source labels and graph paths
// are also text; identifiers, anchors, timestamps and JSON syntax are outside this content budget.
func factSize(f domain.CitedFact) int {
	size := 0
	for _, text := range []string{f.Subject, f.Predicate, f.Object, f.Statement, f.SourceRole, f.Evidence.Quote, f.Evidence.Context.Text} {
		size += utf8.RuneCountInString(text)
	}
	for _, path := range [][]string{f.Path, f.Via} {
		for _, text := range path {
			size += utf8.RuneCountInString(text)
		}
	}
	return size
}
