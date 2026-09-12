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

func TestOriginalSourceExpiryPreservesIndependentlyAuthoredCorrection(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "correct_original_expiry")
	id := statedAt(t, ctx, pg.NewObservationStore(pool), pg.NewFactStore(pool), schema, time.Now().UTC(), "I work at Ensera", domain.Claim{Subject: "I", Predicate: "works_at", Object: "Ensera", Statement: "I work at Ensera", Cardinality: domain.CardinalityMany})
	original, err := pg.NewCitationStore(pool, schema).Resolve(ctx, "p1", id, nil, 8)
	if err != nil {
		t.Fatal(err)
	}
	out, err := pg.NewRecordStore(pool, schema).Correct(ctx, "p1", uuid.NewString(), []pg.RecordCorrection{{RecordMutation: recordMutation(t, pool, schema, id), Object: "Atlas", Statement: "I work at Atlas"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.observation SET retention_until=clock_timestamp()-interval '1 second' WHERE observation_id=$1`), original.Evidence[0].ObservationID); err != nil {
		t.Fatal(err)
	}
	if swept, err := pg.NewRetentionStore(pool, schema).Sweep(ctx, "p1", 10); err != nil || swept.Observations != 1 {
		t.Fatalf("original expiry: %+v %v", swept, err)
	}
	current, err := pg.NewCitationStore(pool, schema).Resolve(ctx, "p1", out.Records[0].ID, nil, 8)
	if err != nil || current.Statement != "I work at Atlas" || current.Status != "current" || current.Evidence[0].AuthoredBy == nil {
		t.Fatalf("independent correction lost: %+v %v", current, err)
	}
	var instructions, withdrawals int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT (SELECT count(*) FROM {schema}.curated_claim),(SELECT count(*) FROM {schema}.record_retraction)`)).Scan(&instructions, &withdrawals); err != nil || instructions != 1 || withdrawals != 0 {
		t.Fatalf("wrong source ownership: %d %d %v", instructions, withdrawals, err)
	}
}
