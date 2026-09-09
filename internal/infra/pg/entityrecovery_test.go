// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
)

func TestRecoveryRefusesConflictingEntityIdentityWithoutPartialWrites(t *testing.T) {
	for _, retained := range []bool{false, true} {
		t.Run(fmt.Sprint(retained), func(t *testing.T) {
			ctx := context.Background()
			pool := testPool(t)
			schema := tenant(t, pool, fmt.Sprintf("entity_recovery_conflict_%t", retained))
			facts := pg.NewFactStore(pool)
			id := statedAt(t, ctx, pg.NewObservationStore(pool), facts, schema, time.Now().UTC(), "Atlas works at Ensera", domain.Claim{Subject: "Atlas", Predicate: "works_at", Object: "Ensera", Statement: "Atlas works at Ensera", Cardinality: domain.CardinalityMany})
			if !retained {
				if _, err := pool.Exec(ctx, schema.SQL(`DELETE FROM {schema}.fact WHERE fact_id=$1`), id); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := pool.Exec(ctx, schema.SQL(`DELETE FROM {schema}.fact_evidence WHERE fact_id=$1`), id); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.entity SET canonical_name='Different identity',normalized_name='different identity' WHERE scope='p1' AND normalized_name='atlas'`)); err != nil {
				t.Fatal(err)
			}
			before := map[string]string{}
			for _, table := range []string{"entity", "fact", "fact_evidence", "projection_dependency"} {
				before[table] = recoverySnapshot(t, pool, schema, table)
			}
			if _, err := facts.RecoverFacts(ctx, schema, "p1", "", "", uuid.NewString(), 100); !errors.Is(err, pg.ErrInvalidReceipt) {
				t.Fatalf("conflicting identity reused: %v", err)
			}
			for table, snapshot := range before {
				if recoverySnapshot(t, pool, schema, table) != snapshot {
					t.Fatalf("refused recovery changed %s", table)
				}
			}
		})
	}
}
