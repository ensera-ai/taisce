// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
)

func TestRecoveryValidatesReceiptEndpointBeforeRestoringIdentity(t *testing.T) {
	for _, damage := range []struct{ name, sql string }{
		{"different_id", `UPDATE {schema}.fact_receipt SET subject_identity=jsonb_set(subject_identity,'{entity_id}',to_jsonb(gen_random_uuid()::text))`},
		{"missing_identity", `UPDATE {schema}.fact_receipt SET subject_identity=NULL`},
		{"unexpected_identity", `UPDATE {schema}.fact_receipt SET state=jsonb_set(state,'{subject_entity_id}','null'::jsonb)`},
	} {
		t.Run(damage.name, func(t *testing.T) {
			ctx := context.Background()
			pool := testPool(t)
			schema := tenant(t, pool, "entity_receipt_"+damage.name)
			facts := pg.NewFactStore(pool)
			statedAt(t, ctx, pg.NewObservationStore(pool), facts, schema, time.Now().UTC(), "Atlas works at Ensera", domain.Claim{Subject: "Atlas", Predicate: "works_at", Object: "Ensera", Statement: "Atlas works at Ensera", Cardinality: domain.CardinalityMany})
			if _, err := pool.Exec(ctx, schema.SQL(`DELETE FROM {schema}.fact; DELETE FROM {schema}.entity;`+damage.sql)); err != nil {
				t.Fatal(err)
			}
			if _, err := facts.RecoverFacts(ctx, schema, "p1", "", "", uuid.NewString(), 100); !errors.Is(err, pg.ErrInvalidReceipt) {
				t.Fatalf("damaged endpoint accepted: %v", err)
			}
			var entities, records int
			if err := pool.QueryRow(ctx, schema.SQL(`SELECT (SELECT count(*) FROM {schema}.entity),(SELECT count(*) FROM {schema}.fact)`)).Scan(&entities, &records); err != nil || entities != 0 || records != 0 {
				t.Fatalf("refused page leaked rows: entities=%d records=%d err=%v", entities, records, err)
			}
		})
	}
}

func TestRecoveryPreservesCompatibleSharedEntityAndExistingAliases(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "entity_recovery_shared")
	facts := pg.NewFactStore(pool)
	for _, name := range []string{"Atlas", "Beacon"} {
		text := name + " works at Ensera"
		statedAt(t, ctx, pg.NewObservationStore(pool), facts, schema, time.Now().UTC(), text, domain.Claim{Subject: name, Predicate: "works_at", Object: "Ensera", Statement: text, Cardinality: domain.CardinalityMany})
	}
	statedAt(t, ctx, pg.NewObservationStore(pool), facts, schema, time.Now().UTC(), "Atlas works at ENSERA", domain.Claim{Subject: "Atlas", Predicate: "works_at", Object: "ENSERA", Statement: "Atlas works at ENSERA", Cardinality: domain.CardinalityMany})
	if _, err := pool.Exec(ctx, schema.SQL(`DELETE FROM {schema}.fact`)); err != nil {
		t.Fatal(err)
	}
	before := recoverySnapshot(t, pool, schema, "entity")
	page, err := facts.RecoverFacts(ctx, schema, "p1", "", "", uuid.NewString(), 100)
	if err != nil || page.Restored != 3 {
		t.Fatalf("compatible identities refused: %+v %v", page, err)
	}
	if recoverySnapshot(t, pool, schema, "entity") != before {
		t.Fatal("recovery rewrote compatible entities")
	}
}
