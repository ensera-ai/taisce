// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
)

// The vocabulary key a fact references cannot move while the fact carries it.
//
// Two statements, and they were in different states. Redeclaring a relation's cardinality cascaded:
// one statement rewrote the cardinality on every fact that carried the relation, with no fact deleted
// and no count changed — and the copy on each fact is what supersession rests on, so it changed what
// the facts already stored had meant, retroactively and invisibly. Renaming was already refused, by
// the older reference to the relation's name, which is not what the issue behind this expected and is
// why both halves are here: one is the door that was open, the other is the guard that was holding
// and now says so rather than depending on which reference happens to look first.
//
// Written against the administrative connection on purpose. That is the only identity that can reach
// this table at all, so it is the one the rule has to hold against; a rule that only holds for
// identities with no privileges is not a rule. And it is asserted as a refusal by the substrate
// rather than as an operation this system does not offer, because not offering something is a
// convention, and the statement written under pressure does not consult conventions.
func TestTheVocabularyKeyAFactReferencesCannotMoveWhileTheFactCarriesIt(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "vocab_key_immutable")
	obs, facts := pg.NewObservationStore(pool), pg.NewFactStore(pool)

	statedAt(t, ctx, obs, facts, schema, time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC),
		"I work at Ensera", domain.Claim{Subject: "Atlas", Predicate: "works_at",
			Cardinality: domain.CardinalityMany, Object: "Ensera", Statement: "Atlas works at Ensera"})

	// Each statement is held against the rule that refuses it, by SQLSTATE and by name, so this
	// passes for the reason it is about rather than because the statement happened to be wrong.
	refused := func(statement, code, constraint string) {
		t.Helper()
		_, err := pool.Exec(ctx, schema.SQL(statement))
		if err == nil {
			t.Fatalf("the substrate permitted %q while facts carry the relation", statement)
		}
		var db *pgconn.PgError
		if !errors.As(err, &db) || db.Code != code || db.ConstraintName != constraint {
			t.Fatalf("expected %s from %s, got: %v", code, constraint, err)
		}
	}
	// The rename is refused by the reference to the relation's NAME. That guard was always there and
	// the cascade was what defeated it: the cascade rewrote fact.predicate before this constraint was
	// checked, so by the time it looked, the rows it protects had already moved to the new spelling.
	// 23503 from the reference to the relation's NAME. That guard was always there and the cascade
	// was what defeated it: the cascade rewrote fact.predicate before this constraint was checked, so
	// by the time it looked, the rows it protects had already moved to the new spelling.
	refused(`UPDATE {schema}.predicate SET predicate='employed_by' WHERE predicate='works_at'`,
		"23503", "fact_predicate_fk")
	// The same door, less obviously: redeclaring the cardinality rewrites the copy on every fact
	// that carried the relation, which changes what supersession meant for facts already formed.
	// 23001 — a restrict violation — from the reference that carries cardinality, which is the
	// setting this migration changed. Only this reference sees a cardinality change; the name is
	// unchanged, so the name's reference has nothing to say about it.
	refused(`UPDATE {schema}.predicate SET cardinality='one' WHERE predicate='works_at'`,
		"23001", "fact_cardinality_matches_vocabulary")

	// Nothing moved. A failed rename that had already rewritten some rows would be worse than one
	// that succeeded, because the vocabulary and the facts would disagree.
	var carried, admitted, renamed int
	if err := pool.QueryRow(ctx, schema.SQL(
		`SELECT (SELECT count(*) FROM {schema}.fact WHERE predicate='works_at' AND cardinality='many'),
		        (SELECT count(*) FROM {schema}.predicate WHERE predicate='works_at'),
		        (SELECT count(*) FROM {schema}.predicate WHERE predicate='employed_by')`)).
		Scan(&carried, &admitted, &renamed); err != nil {
		t.Fatalf("read the vocabulary and the facts: %v", err)
	}
	if carried != 1 || admitted != 1 || renamed != 0 {
		t.Fatalf("after two refused statements: %d facts carry the relation, %d vocabulary rows name it, %d name the new spelling",
			carried, admitted, renamed)
	}

	// What this does not cover, stated where somebody relying on it will read it: the constraint
	// protects a relation FACTS CARRY. A relation nothing has ever been formed under can still be
	// renamed over the administrative connection, because no row references it — and nothing is lost
	// when that happens, which is why this is a limit rather than a gap. No path in this system does
	// it either way.
	if _, err := pool.Exec(ctx, schema.SQL(
		`UPDATE {schema}.predicate SET predicate='familiar_with' WHERE predicate='knows_about'`)); err != nil {
		t.Fatalf("a relation no fact carries was refused as well, which is not what this constraint says: %v", err)
	}
}
