// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
)

func TestCorrectionInputBoundsAndHistoricalTargetsNeverMutate(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "correct_invalid")
	id := statedAt(t, ctx, pg.NewObservationStore(pool), pg.NewFactStore(pool), schema, time.Now().UTC(), "Atlas works at Ensera", domain.Claim{Subject: "Atlas", Predicate: "works_at", Object: "Ensera", Statement: "Atlas works at Ensera", Cardinality: domain.CardinalityMany})
	store := pg.NewRecordStore(pool, schema)
	actor := uuid.NewString()
	original := recordMutation(t, pool, schema, id)
	request := pg.RecordCorrection{RecordMutation: original, Object: "Orbit", Statement: "Atlas works at Orbit"}
	invalid := []pg.RecordCorrection{{RecordMutation: original, Object: "", Statement: "claim"}, {RecordMutation: original, Object: "A", Statement: " "}, {RecordMutation: original, Object: strings.Repeat("a", 1025), Statement: "claim"}, {RecordMutation: original, Object: "A", Statement: strings.Repeat("a", 16385)}, {RecordMutation: original, Object: "\xff", Statement: "claim"}, {RecordMutation: original, Object: "A", Statement: "\xff"}, {RecordMutation: original, Object: "A\x00", Statement: "claim"}, {RecordMutation: original, Object: "A", Statement: "claim\x00"}, {RecordMutation: original, Object: "A", Statement: "claim", ValidFrom: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)}}
	for _, r := range invalid {
		if _, err := store.Correct(ctx, "p1", actor, []pg.RecordCorrection{r}); !errors.Is(err, pg.ErrInvalidRecordMutation) {
			t.Fatalf("invalid input accepted: %v", err)
		}
	}
	for _, batch := range [][]pg.RecordCorrection{nil, make([]pg.RecordCorrection, 21), {request, request}} {
		if _, err := store.Correct(ctx, "p1", actor, batch); !errors.Is(err, pg.ErrInvalidRecordMutation) {
			t.Fatalf("invalid batch: %v", err)
		}
	}
	if recordMutation(t, pool, schema, id) != original {
		t.Fatal("invalid correction changed memory")
	}
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.fact SET valid=tstzrange(lower(valid),clock_timestamp()+interval '1 second') WHERE fact_id=$1`), id); err != nil {
		t.Fatal(err)
	}
	request.RecordMutation = recordMutation(t, pool, schema, id)
	if _, err := store.Correct(ctx, "p1", actor, []pg.RecordCorrection{request}); !errors.Is(err, pg.ErrRecordConflict) {
		t.Fatalf("historical target corrected: %v", err)
	}
	if recordMutation(t, pool, schema, id) != request.RecordMutation {
		t.Fatal("historical refusal withdrew target")
	}
}

func TestCuratedReplayRefusesChangedIdentityWithoutWriting(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "curated_identity_guard")
	facts := pg.NewFactStore(pool)
	id := statedAt(t, ctx, pg.NewObservationStore(pool), facts, schema, time.Now().UTC(), "Atlas works at Ensera", domain.Claim{Subject: "Atlas", Predicate: "works_at", Object: "Ensera", Statement: "Atlas works at Ensera", Cardinality: domain.CardinalityMany})
	corrected, err := pg.NewRecordStore(pool, schema).Correct(ctx, "p1", uuid.NewString(), []pg.RecordCorrection{{RecordMutation: recordMutation(t, pool, schema, id), Object: "Orbit", Statement: "Atlas works at Orbit"}})
	if err != nil {
		t.Fatal(err)
	}
	source := corrected.Records[0].SourceObservationID
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.curated_claim SET claim=jsonb_set(claim,'{Object}','"Unexpected"') WHERE source_observation_id=$1`), source); err != nil {
		t.Fatal(err)
	}
	if found, err := facts.ReplayCurated(ctx, schema, "p1", source); !found || err == nil {
		t.Fatal("identity drift accepted")
	}
	var n int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.fact`)).Scan(&n); err != nil || n != 2 {
		t.Fatal("rejected identity drift committed a projection")
	}
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.curated_claim SET claim='{"ValidFrom":"invalid-date"}'::jsonb WHERE source_observation_id=$1`), source); err != nil {
		t.Fatal(err)
	}
	if _, err := facts.ReplayCurated(ctx, schema, "p1", source); err == nil {
		t.Fatal("invalid authored source accepted")
	}
	if found, err := facts.ReplayCurated(ctx, schema, "p2", source); found || err == nil {
		t.Fatal("foreign curated source resolved")
	}
}
