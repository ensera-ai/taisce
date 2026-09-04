package migrate

import (
	"context"
	"testing"
)

// ── #5's claim: the vocabulary is closed, and closed by the database ───────────────────────────
//
// The refusal is asserted through a direct INSERT rather than through the write path, because the
// point is that NO path can store an unknown relation — including one written by hand during an
// incident, which is exactly when a convention enforced in application code is bypassed.
func TestARelationOutsideTheVocabularyCannotBeStored(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	tenant := freshTenant(t, pool)
	schema, _ := NewSchema(tenant)

	// `employed_by` is a second spelling of `works_at`. It is the realistic failure — not a typo,
	// but a synonym an extractor would produce and no traversal would ever match.
	_, err := pool.Exec(ctx, schema.SQL(`
		INSERT INTO {schema}.fact (fact_id, scope, predicate, statement, valid)
		VALUES (gen_random_uuid(), 'p1', 'employed_by', 'Marta is employed by Ensera.',
		        tstzrange(now(), NULL))`))
	if err == nil {
		t.Fatal("a relation outside the vocabulary was stored; the set is documented, not closed")
	}

	// And the admitted spelling of the same relation goes in, so the refusal is the vocabulary
	// working rather than the table being unwritable.
	if _, err := pool.Exec(ctx, schema.SQL(`
		INSERT INTO {schema}.fact (fact_id, scope, predicate, statement, valid)
		VALUES (gen_random_uuid(), 'p1', 'works_at', 'Marta works at Ensera.',
		        tstzrange(now(), NULL))`)); err != nil {
		t.Fatalf("the admitted relation must be storable: %v", err)
	}
}

// A predicate cannot be retired while facts still use it.
//
// The alternative is a cascade, which would delete the memory in order to permit the vocabulary
// change — silently trading away the thing the system exists to keep for a schema edit.
func TestRetiringAPredicateInUseIsRefused(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	tenant := freshTenant(t, pool)
	schema, _ := NewSchema(tenant)

	if _, err := pool.Exec(ctx, schema.SQL(`
		INSERT INTO {schema}.fact (fact_id, scope, predicate, statement, valid)
		VALUES (gen_random_uuid(), 'p1', 'lives_in', 'The user lives in Dublin.',
		        tstzrange(now(), NULL))`)); err != nil {
		t.Fatalf("seed fact: %v", err)
	}
	if _, err := pool.Exec(ctx,
		schema.SQL(`DELETE FROM {schema}.predicate WHERE predicate = 'lives_in'`)); err == nil {
		t.Fatal("a predicate with facts behind it was retired, taking the facts with it")
	}
}

// The declared semantic types and the seeded rows cannot drift apart.
//
// A type listed in the CHECK with no predicate in it is a category the extractor is told about and
// can never emit into; a predicate is impossible in the other direction because the CHECK refuses
// it. So this asserts the direction the database cannot.
func TestEverySemanticTypeHasAPredicateInIt(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	tenant := freshTenant(t, pool)
	schema, _ := NewSchema(tenant)

	// Read the permitted set out of the constraint itself rather than restating it here: a list
	// repeated in a test is a second place for it to be wrong.
	rows, err := pool.Query(ctx, schema.SQL(`
		WITH declared AS (
			SELECT (regexp_matches(pg_get_constraintdef(oid), '''([a-z_]+)''::text', 'g'))[1]
			           AS semantic_type
			  FROM pg_constraint
			 WHERE conname = 'predicate_semantic_type_chk'
			   AND conrelid = '{schema}.predicate'::regclass
		)
		SELECT d.semantic_type, count(p.predicate)
		  FROM declared d
		  LEFT JOIN {schema}.predicate p USING (semantic_type)
		 GROUP BY d.semantic_type`))
	if err != nil {
		t.Fatalf("read declared semantic types: %v", err)
	}
	defer rows.Close()

	declared := 0
	for rows.Next() {
		var name string
		var n int
		if err := rows.Scan(&name, &n); err != nil {
			t.Fatalf("scan: %v", err)
		}
		declared++
		if n == 0 {
			t.Errorf("semantic type %s is declared and has no predicate in it", name)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if declared == 0 {
		t.Fatal("no semantic types were read from the constraint, so this test asserted nothing")
	}
}

// The size of the vocabulary is itself the decision.
//
// Too small and real relations have nowhere to go, so the unmapped rate rises and memory is lost.
// Too large and the near-duplicates are back — with thirty synonyms for one relation, spread over a
// set nobody can hold in their head, the split is harder to see than it was with an open vocabulary.
// The bound is asserted so that growing it is a deliberate act with a reason attached.
func TestTheVocabularyStaysWithinItsDeclaredBounds(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	tenant := freshTenant(t, pool)
	schema, _ := NewSchema(tenant)

	var n int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.predicate`)).Scan(&n); err != nil {
		t.Fatalf("count predicates: %v", err)
	}
	if n < 30 || n > 50 {
		t.Fatalf("the vocabulary has %d relations; the decision is 30 to 50", n)
	}
}
