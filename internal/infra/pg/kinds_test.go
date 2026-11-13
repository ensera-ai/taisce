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
// It is the constraint on a relation's object end that is read, because that is where the schema
// closes the set today. The column an entity's own kind is stored in carries no constraint yet
// (#289), which is why this test cannot be about that column.
func TestTheKindsExtractionAdmitsAreTheKindsTheSchemaAdmits(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "kinds_are_one_set")

	var definition string
	if err := pool.QueryRow(ctx, schema.SQL(`
		SELECT pg_get_constraintdef(c.oid)
		  FROM pg_constraint c
		 WHERE c.conname = 'predicate_object_kind_chk'
		   AND c.conrelid = '{schema}.predicate'::regclass`)).Scan(&definition); err != nil {
		t.Fatalf("read the constraint that closes a relation's object kind: %v", err)
	}

	var inSchema []string
	for _, m := range regexp.MustCompile(`'([^']+)'`).FindAllStringSubmatch(definition, -1) {
		inSchema = append(inSchema, m[1])
	}
	inMemory := domain.Kinds()
	sort.Strings(inSchema)
	sort.Strings(inMemory)
	if len(inSchema) == 0 || strings.Join(inSchema, ",") != strings.Join(inMemory, ",") {
		t.Fatalf("the schema admits %v and extraction admits %v; they are one set\nconstraint: %s", inSchema, inMemory, definition)
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
