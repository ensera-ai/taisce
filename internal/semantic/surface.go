// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

// Package semantic builds the text an entity is found by.
//
// An entity is reached today by exact name match after normalisation — casing and whitespace and
// nothing else (D6). That is deliberate: merging "the Dublin office" with "our Dublin site" needs
// either a similarity threshold nobody can justify or a model call per entity, and over-merging puts
// two people's facts on one node, which is a governance failure rather than a recall cost.
//
// What it costs is a question that names something the way the conversation named it a second time.
// The entity is right there and the question misses it.
//
// # What this package is allowed to decide
//
// What text stands for an entity, and how much of it. It does not resolve, does not merge, and does
// not decide that two entities are one thing — that is still normalised name equality and nothing
// else. This produces a surface a question can be compared against, which is a way of finding
// CANDIDATES, and the irreversible direction stays closed.
//
// It has no I/O and no model: a caller supplies the aliases and the stored quotes, and gets back the
// string to embed.
package semantic

import (
	"sort"
	"strings"
)

// MEASURED NOT TO SEPARATE, AND KEPT ANYWAY — see D59 and #105.
//
// A surface built this way was compared against questions on a live embedder, and the distributions
// overlap: the strongest near miss outscored the weakest paraphrase, both with the quotes and without
// them. So nothing resolves entities by proximity today, and this package builds no index.
//
// It is kept because it is the input to the measurement that would say whether a candidate mechanism
// works — the surface is what #105 varies, and deleting it would mean rebuilding it to ask the
// question again.

// SurfaceBudget is how many characters of an entity's surface are embedded.
//
// # Why there is a bound
//
// A hub entity has thousands of quotes behind it, and embedding all of them would make one entity's
// surface cost more than every other entity's together — and produce a vector that is an average of
// everything anybody ever said near that name, which resembles nothing in particular.
//
// # Why this number
//
// About two thousand characters is a dozen or so sentences: enough that an entity mentioned in several
// contexts is represented by more than one of them, and short enough that the surface still points at
// a thing rather than at a topic. It is a constant rather than a setting because nothing has asked to
// vary it, and the measurement that would justify changing it is the one #10 exists to take.
const SurfaceBudget = 2000

// Surface is the text an entity is found by: what it has been called, and what was said about it.
//
// # Why the stored quotes rather than a written description
//
// A model-written description of a person, aggregated from many sources, cannot be erased by counting:
// deleting one source does not remove that person from prose about them, and there is nothing to count
// afterwards. The quotes are already stored, already scoped, already registered and already erased
// with the facts they belong to — so a surface built from them inherits the guarantee instead of
// needing an exception to it.
//
// It also cannot drift from the corpus, because it IS the corpus: two rebuilds of one entity produce
// the same text, and a rebuild after an erasure produces exactly the text the surviving quotes give.
//
// # Why the names come first
//
// A question usually contains the name. Putting the surface forms at the front means the thing being
// described is stated before the evidence for it, which is the order a sentence about anything takes
// — and when the budget cuts, it cuts evidence rather than identity.
func Surface(canonical string, aliases []string, quotes []string) string {
	var b strings.Builder
	b.WriteString(canonical)

	for _, alias := range distinct(aliases, canonical) {
		b.WriteString(" | ")
		b.WriteString(alias)
	}

	// Sorted, so two rebuilds of one entity produce one string. Quotes arrive from a query whose order
	// is a property of the plan rather than of the entity, and a surface that depended on it would
	// embed differently on a rebuild for no reason anybody could name.
	for _, quote := range distinct(quotes, "") {
		addition := len(quote) + 1
		if b.Len()+addition > SurfaceBudget {
			// Stop at a whole quote rather than cutting one. Half a sentence embeds as half a
			// sentence, which is a worse representation than the sentence not being there.
			break
		}
		b.WriteString("\n")
		b.WriteString(quote)
	}
	return b.String()
}

// distinct returns the unique members, sorted, without a value the caller has already used.
//
// Deduplicated because a relation asserted five times stores five identical quotes, and a surface
// repeating one sentence five times is a surface mostly about that sentence.
func distinct(values []string, exclude string) []string {
	seen := map[string]struct{}{}
	if exclude != "" {
		seen[strings.TrimSpace(exclude)] = struct{}{}
	}
	var out []string
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, already := seen[v]; already {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
