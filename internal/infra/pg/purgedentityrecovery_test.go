// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
)

// A purge withdraws a node the extractor got wrong, and the claims standing on it go with it.
// Recovery rebuilds a fact from its receipt, and the receipt carries the entity the fact named —
// so without this test nothing says the withdrawal survives the next recovery pass. A rebuild from
// the words may propose the entity again, which is correct; restoring the withdrawn inference
// verbatim from a receipt is not the same thing and is not a decision the operator made twice.
func TestAPurgedEntityIsNotBroughtBackByFactRecovery(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "purged_entity_recovery")
	obs, facts := pg.NewObservationStore(pool), pg.NewFactStore(pool)

	text := "Atlas works at Ensera"
	factID := statedAt(t, ctx, obs, facts, schema, time.Now(), text, domain.Claim{
		Subject: "Atlas", Predicate: "works_at", Object: "Ensera",
		Statement: text, Cardinality: domain.CardinalityMany})

	var entityID string
	if err := pool.QueryRow(ctx, schema.SQL(
		`SELECT object_entity_id::text FROM {schema}.fact WHERE scope='p1' AND fact_id=$1::uuid`),
		factID).Scan(&entityID); err != nil {
		t.Fatal(err)
	}

	purge, err := pg.NewRecordStore(pool, schema).PurgeEntity(ctx, "p1", entityID, "wrongly resolved", true,
		domain.AuditEntry{Operation: domain.AuditEntityPurge, Principal: uuid.NewString(),
			PrincipalKind: domain.PrincipalCredential, Project: "p1", Outcome: domain.OutcomeAllowed})
	if err != nil || !purge.Clean() || purge.Removed["entity"] != 1 || purge.Removed["fact"] != 1 {
		t.Fatalf("the purge did not withdraw the node and its claim: %+v %v", purge, err)
	}

	page, err := facts.RecoverFacts(ctx, schema, "p1", "", "", uuid.NewString(), 100)
	if err != nil {
		t.Fatal(err)
	}

	var entityBack, factBack int
	if err := pool.QueryRow(ctx, schema.SQL(
		`SELECT count(*) FROM {schema}.entity WHERE scope='p1' AND entity_id=$1::uuid`),
		entityID).Scan(&entityBack); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, schema.SQL(
		`SELECT count(*) FROM {schema}.fact WHERE scope='p1' AND fact_id=$1::uuid`),
		factID).Scan(&factBack); err != nil {
		t.Fatal(err)
	}
	if entityBack != 0 || factBack != 0 {
		t.Fatalf("recovery undid the purge: entity rows %d, claim rows %d, page %+v", entityBack, factBack, page)
	}
}

// A purge that left something behind does not report itself clean.
//
// The residual is the whole claim: "removed" is an assertion, and a count of rows still matching the
// predicate that selected the deletions is the measurement behind it. Every other test here purges
// successfully, so only the arm that finds nothing is ever run — and the arm that matters to an
// operator is the other one, which says the operation did not do what it claimed.
func TestAPurgeThatLeftSomethingBehindDoesNotReportItselfClean(t *testing.T) {
	clean := pg.EntityPurge{Residual: map[string]int{"fact": 0, "entity": 0, "fact_receipt": 0}}
	if !clean.Clean() {
		t.Fatal("a purge with nothing left behind must report itself clean")
	}
	for table := range map[string]bool{"fact": true, "entity": true, "fact_receipt": true} {
		left := pg.EntityPurge{Residual: map[string]int{"fact": 0, "entity": 0, "fact_receipt": 0}}
		left.Residual[table] = 1
		if left.Clean() {
			t.Fatalf("a row still matching the purge's own predicate in %s was reported as clean", table)
		}
	}
}
