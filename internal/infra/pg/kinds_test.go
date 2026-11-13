// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/domain"
)

// The kinds extraction admits are the kinds the schema admits, and no others.
//
// The set lives in two places for a reason — the schema is the authority, and extraction judges a
// model's kind without a query — and two copies of one set drift the first time somebody adds to
// one. So the copy in memory is held to the constraint as the database states it, read back from the
// catalogue rather than from the migration's text: what matters is the rule in force, not the file
// that once wrote it.
//
// The schema states the set in three places, and each is read: the constraint on a relation's
// object end, the constraint on the column an entity's own kind is stored in, and the condition of
// the trigger that gives that column `thing` for anything else. The last two are one rule in two
// halves — the trigger decides what is not a kind and the check refuses it — so a kind added to one
// and not the other would be stored by the first and refused by the second.
func TestTheKindsExtractionAdmitsAreTheKindsTheSchemaAdmits(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "kinds_are_one_set")

	inMemory := domain.Kinds()
	sort.Strings(inMemory)
	for stated, read := range map[string]string{
		"the check on a relation's object kind": `SELECT pg_get_constraintdef(c.oid) FROM pg_constraint c
		 WHERE c.conname = 'predicate_object_kind_chk' AND c.conrelid = '{schema}.predicate'::regclass`,
		"the check on an entity's kind": `SELECT pg_get_constraintdef(c.oid) FROM pg_constraint c
		 WHERE c.conname = 'entity_kind_chk' AND c.conrelid = '{schema}.entity'::regclass`,
		"the condition of the trigger on an entity's kind": `SELECT pg_get_triggerdef(t.oid) FROM pg_trigger t
		 WHERE t.tgname = 'entity_kind_outside_the_six_is_not_given' AND t.tgrelid = '{schema}.entity'::regclass`,
	} {
		var definition string
		if err := pool.QueryRow(ctx, schema.SQL(read)).Scan(&definition); err != nil {
			t.Fatalf("read %s: %v", stated, err)
		}
		var inSchema []string
		for _, m := range regexp.MustCompile(`'([^']+)'`).FindAllStringSubmatch(definition, -1) {
			inSchema = append(inSchema, m[1])
		}
		sort.Strings(inSchema)
		if len(inSchema) == 0 || strings.Join(inSchema, ",") != strings.Join(inMemory, ",") {
			t.Fatalf("%s admits %v and extraction admits %v; they are one set\n%s", stated, inSchema, inMemory, definition)
		}
	}

	// Each kind is also put to the constraint itself, so the reading of its definition above is not
	// the only thing standing between a typo and a green run.
	for _, kind := range domain.Kinds() {
		if !domain.IsKind(kind) {
			t.Fatalf("%q is listed as a kind and not recognised as one", kind)
		}
		if _, err := pool.Exec(ctx, schema.SQL(`
			INSERT INTO {schema}.predicate (predicate, semantic_type, cardinality, object_kind, description)
			VALUES ($1, 'structure', 'many', $2, 'a relation whose object is of this kind')`),
			"kind_probe_"+kind, kind); err != nil {
			t.Fatalf("the schema refused %q, which extraction admits: %v", kind, err)
		}
	}
}
