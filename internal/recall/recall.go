// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

// Package recall answers a question from entities outward.
//
// Retrieval starts at the entities a question names and returns the facts about them. It does not
// search passages and it does not fan out over the corpus, which is the whole inversion: cost is
// proportional to a neighbourhood rather than to how much has been stored.
//
// This is the thinnest honest form of that: anchor, facts, bundle. Nothing ranks, nothing fuses,
// nothing traverses. Every one of those is a stage that can only be justified by a measurement it
// improves, and adding one before the plain path is measured makes it impossible to say which stage
// is doing the work — or which is doing harm.
package recall

import (
	"context"
	"strings"
	"unicode"

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
	Anchors(ctx context.Context, scopes, terms []string) ([]domain.Anchor, error)
	// FactsAbout returns the currently-valid facts naming any of the given entities, with their
	// evidence, up to limit+1 rows so the caller can tell a full result from a truncated one.
	//
	// `sources` is the set of message roles whose facts may be returned, and it is an argument
	// rather than a default so that no call can omit it and get everything.
	FactsAbout(ctx context.Context, scopes, entityIDs []string, limit int, sources []string) ([]domain.CitedFact, error)
}

// Recaller answers questions against one tenant's memory.
type Recaller struct {
	store Store
	// limit caps the bundle. It is a cap and not a selection: nothing has ranked the facts, so
	// cutting at the limit drops arbitrary members rather than the least useful ones.
	limit int
}

// New builds a recaller. A limit of zero or less takes the default.
func New(store Store, limit int) *Recaller {
	if limit <= 0 {
		limit = 50
	}
	return &Recaller{store: store, limit: limit}
}

// Recall resolves a question to entities and returns the facts naming them.
//
// # The authorised scopes are an argument, never a default
//
// A project is a permission inside one tenant, and a principal granted several recalls across them
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

// Recall answers a question from what the principal said.
func (r *Recaller) Recall(ctx context.Context, scopes []string, question string) (domain.Bundle, error) {
	return r.RecallFrom(ctx, scopes, question, PrincipalSources)
}

// RecallFrom answers a question from the sources the caller admits.
//
// The wider call, for a caller that genuinely wants what a document or a tool result asserted. What
// it gets back carries the role on every fact, so the distinction survives into whatever reads the
// bundle — a merged list with no labels would be the same failure with an extra step.
func (r *Recaller) RecallFrom(ctx context.Context, scopes []string, question string,
	sources []string) (domain.Bundle, error) {
	if len(scopes) == 0 {
		return domain.Bundle{}, nil
	}
	terms := Terms(question)
	if len(terms) == 0 {
		return domain.Bundle{}, nil
	}
	reach := domain.Reach{Terms: len(terms), FactsPerAnchor: map[string]int{}}

	anchors, err := r.store.Anchors(ctx, scopes, terms)
	if err != nil {
		return domain.Bundle{}, err
	}
	reach.Anchored = len(anchors)
	if len(anchors) == 0 {
		// Empty, and saying why. "We have never heard of what you asked about" and "we know that and
		// have nothing to say" are different answers, and a caller that cannot tell them apart
		// cannot decide whether to write something or ask differently.
		return domain.Bundle{Reach: reach}, nil
	}

	ids := make([]string, 0, len(anchors))
	for _, a := range anchors {
		ids = append(ids, a.Entity.ID)
	}

	// One more than the limit, so a full result is distinguishable from a truncated one without a
	// second count query — and without the caller having to guess from the row count whether they
	// are looking at everything.
	facts, err := r.store.FactsAbout(ctx, scopes, ids, r.limit+1, sources)
	if err != nil {
		return domain.Bundle{}, err
	}
	bundle := domain.Bundle{Anchors: anchors, Facts: facts}
	if len(facts) > r.limit {
		bundle.Facts = facts[:r.limit]
		bundle.Truncated = true
	}

	// After the cut, not before. Co-derivation is a statement about what is IN the bundle, and naming
	// a fact the caller cannot see would be a citation that resolves to nothing.
	markCoDerived(bundle.Facts)

	// Counted over what the caller can see, after the cut, for the same reason co-derivation is: a
	// count describing rows nobody received describes something else.
	for _, f := range bundle.Facts {
		if f.AnchoredOn != "" {
			reach.FactsPerAnchor[f.AnchoredOn]++
		}
	}
	bundle.Reach = reach
	return bundle, nil
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
				if i != j {
					facts[i].CoDerivedWith = append(facts[i].CoDerivedWith, facts[j].ID)
				}
			}
		}
	}
}

// NormalizeSubject compares subjects the way entity resolution does, so two facts naming one person
// in different casing are recognised as being about the same subject.
func NormalizeSubject(s string) string { return domain.NormalizeName(s) }
