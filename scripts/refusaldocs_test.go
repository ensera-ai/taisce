// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/domain"
)

// Every document that lists the refusal reasons lists all of them.
//
// A refusal reason is a thing an operator reads about, in four places: the operator overview's table
// of what each one means, the data model's table of which migration added it, the troubleshooting
// page's prose list, and the formation architecture's ordered steps. A reason added to the code and
// not to those is a refusal somebody will meet with no explanation anywhere — and an operator reading
// a list of nine when the service can produce ten has no way to know the list is short.
//
// This drifted within hours of being possible. `unresolvable_object` was added in 0072, merged, and
// all four documents still described nine reasons. The database list was already held against the
// code by TestEveryRefusalReasonTheCodeCanProduceIsOneTheDatabaseAccepts, which is the same class of
// defect one layer in; this is the layer that was left.
//
// Deliberately NOT covered: docs/31-vocabulary-coverage-ap-news.md, which names reasons as part of a
// dated measurement — 2,092 of 7,482 proposals refused as not_current on a named corpus on a named
// day. That document records what was counted, and a reason added later was not in that count. The
// same distinction the version pins needed: a document a reader acts on tracks the code, a document
// that records what happened must not move.
//
// Only one direction is checked, and the first draft of this test is why. It also looked for reasons
// named in a document that the code cannot produce — the mirror of what the database test does — and
// flagged `unresolvable_term` in the data model. That is a table, the closed list of pronouns from
// 0016, not a refusal reason. Prose cannot be pattern-matched for "is this token a reason" when table
// names share the vocabulary, so that direction stays where the values are unambiguous: the CHECK
// constraint, in TestEveryRefusalReasonTheCodeCanProduceIsOneTheDatabaseAccepts.
func TestEveryDocumentListingRefusalReasonsListsAllOfThem(t *testing.T) {
	root := pinsRoot(t)
	reasons := []string{
		domain.ReasonUnmappedRelation, domain.ReasonUnlocatableQuote, domain.ReasonDuplicateClaim,
		domain.ReasonNotAsserted, domain.ReasonUnresolvableSubject, domain.ReasonUnresolvableObject,
		domain.ReasonNotCurrent, domain.ReasonConflictingValue, domain.ReasonNotSpokenByPrincipal,
		domain.ReasonEntityNameLimit,
	}

	for _, name := range []string{
		"docs/operate/overview.md",
		"docs/postgresql/data-model.md",
		"docs/developers/troubleshooting.md",
		"docs/architecture/formation.md",
	} {
		body, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		text := string(body)
		var missing []string
		for _, reason := range reasons {
			if !strings.Contains(text, "`"+reason+"`") {
				missing = append(missing, reason)
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			t.Errorf("%s lists the refusal reasons and does not name %v; an operator reading it has "+
				"no way to know the list is short", name, missing)
		}

	}
}
