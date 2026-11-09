// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"sort"
	"testing"

	"github.com/ensera-ai/taisce/internal/domain"
)

// Every reason the code can produce is a reason the database will accept, and every reason the
// database accepts is one the code can produce.
//
// The reasons are constrained by a CHECK that restates the whole list, and nine migrations have each
// retyped it to add one. That is a trap with no failure attached: a migration written from an older
// list drops the reasons added since, nothing fails while migrating, and the loss only surfaces when
// a refusal of that kind actually happens — at which point formation cannot record it, the error
// parks the turn, and the symptom is turns left unformed with no obvious cause.
//
// This is not hypothetical. Migration 0072 was written from 0052's list and silently dropped
// not_spoken_by_principal (0053) and entity_name_limit (0066). It migrated cleanly. The suite failed
// three formation tests and one generation test, and the reported symptom was "the turn was parked or
// left unformed", which names neither the constraint nor the reasons.
//
// Both directions are checked because each catches a different mistake. Code-without-database is the
// one above: a refusal the service will try to write and cannot. Database-without-code is a reason
// nothing produces any more — dead vocabulary in a constraint, which is harmless to run and
// misleading to read, since the comment on that constraint is where somebody learns what the refusals
// mean.
//
// What this does not cover: whether each reason is used where it should be. That is each refusal's own
// test. This one is about the two lists agreeing.
func TestEveryRefusalReasonTheCodeCanProduceIsOneTheDatabaseAccepts(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "refusal_reasons")

	code := []string{
		domain.ReasonUnmappedRelation, domain.ReasonUnlocatableQuote, domain.ReasonDuplicateClaim,
		domain.ReasonNotAsserted, domain.ReasonUnresolvableSubject, domain.ReasonUnresolvableObject,
		domain.ReasonNotCurrent, domain.ReasonConflictingValue, domain.ReasonNotSpokenByPrincipal,
		domain.ReasonEntityNameLimit,
	}

	// The constraint's own definition is the authority, read back rather than assumed: a list written
	// into this test by hand would agree with whatever its author believed.
	var definition string
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT pg_get_constraintdef(oid)
        FROM pg_constraint WHERE conname = 'rejected_claim_reason_chk'
          AND conrelid = '{schema}.rejected_claim'::regclass`)).Scan(&definition); err != nil {
		t.Fatalf("read the reason constraint: %v", err)
	}

	accepted := map[string]bool{}
	for _, reason := range code {
		if contains(definition, reason) {
			accepted[reason] = true
		}
	}
	var missing []string
	for _, reason := range code {
		if !accepted[reason] {
			missing = append(missing, reason)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("the database refuses %v, which the code can produce. A refusal the service cannot "+
			"record is an error that parks a turn. Constraint: %s", missing, definition)
	}

	// The other direction: a reason in the constraint that nothing produces.
	for _, reason := range quotedValues(definition) {
		if !accepted[reason] {
			t.Errorf("the database accepts %q and no code constant produces it, so the constraint's "+
				"comment describes a refusal that cannot happen", reason)
		}
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, "'"+needle+"'") >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

// quotedValues pulls the 'single quoted' literals out of a constraint definition.
func quotedValues(definition string) []string {
	var out []string
	for i := 0; i < len(definition); i++ {
		if definition[i] != '\'' {
			continue
		}
		for j := i + 1; j < len(definition); j++ {
			if definition[j] == '\'' {
				out = append(out, definition[i+1:j])
				i = j
				break
			}
		}
	}
	return out
}
