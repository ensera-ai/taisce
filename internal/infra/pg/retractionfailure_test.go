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

// Broken storage must fail closed on reads and writes, with no partially applied withdrawal.
func TestRetractionStorageFailuresAndMissingSupportPreserveTheVersion(t *testing.T) {
	for _, failure := range []string{"evidence", "fact", "instructions", "unsupported"} {
		t.Run(failure, func(t *testing.T) {
			ctx := context.Background()
			pool := testPool(t)
			schema := tenant(t, pool, "retract_failure_"+failure)
			facts := pg.NewFactStore(pool)
			claim := domain.Claim{Subject: "Atlas", Predicate: "works_at", Object: "Ensera", Statement: "Atlas works at Ensera", Cardinality: domain.CardinalityMany}
			id := statedAt(t, ctx, pg.NewObservationStore(pool), facts, schema, time.Now().UTC(), claim.Statement, claim)
			before := recordMutation(t, pool, schema, id)
			citation, err := pg.NewCitationStore(pool, schema).Resolve(ctx, "p1", id, nil, 8)
			if err != nil {
				t.Fatal(err)
			}
			var breakSQL, restoreSQL string
			switch failure {
			case "evidence":
				breakSQL = `ALTER TABLE {schema}.fact_evidence RENAME TO unavailable_evidence`
				restoreSQL = `ALTER TABLE {schema}.unavailable_evidence RENAME TO fact_evidence`
			case "fact":
				breakSQL = `ALTER TABLE {schema}.fact RENAME TO unavailable_fact`
				restoreSQL = `ALTER TABLE {schema}.unavailable_fact RENAME TO fact`
			case "instructions":
				breakSQL = `ALTER TABLE {schema}.record_retraction RENAME TO unavailable_instruction`
				restoreSQL = `ALTER TABLE {schema}.unavailable_instruction RENAME TO record_retraction`
			case "unsupported":
				breakSQL = `DELETE FROM {schema}.fact_evidence`
			}
			if _, err := pool.Exec(ctx, schema.SQL(breakSQL)); err != nil {
				t.Fatal(err)
			}
			if _, err := pg.NewRecordStore(pool, schema).Retract(ctx, "p1", uuid.NewString(), []pg.RecordMutation{before}); err == nil || (failure == "unsupported" && !errors.Is(err, pg.ErrRecordNotFound)) {
				t.Fatalf("unsafe withdrawal accepted: %v", err)
			}
			if failure == "fact" || failure == "instructions" {
				if _, err := facts.Assert(ctx, schema, "p1", citation.Evidence[0].ObservationID, domain.RoleUser, "subject-1", claim); err == nil {
					t.Fatal("formation succeeded without its claim guard or identity store")
				}
			}
			if restoreSQL != "" {
				if _, err := pool.Exec(ctx, schema.SQL(restoreSQL)); err != nil {
					t.Fatal(err)
				}
			}
			if got := recordMutation(t, pool, schema, id); got != before {
				t.Fatal("failed withdrawal changed the version")
			}
		})
	}
}

func TestClosedRecordInspectionRequiresAvailableWithdrawalMetadata(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "retract_read_failure")
	id := statedAt(t, ctx, pg.NewObservationStore(pool), pg.NewFactStore(pool), schema, time.Now().UTC(), "Atlas works at Ensera", domain.Claim{Subject: "Atlas", Predicate: "works_at", Object: "Ensera", Statement: "Atlas works at Ensera", Cardinality: domain.CardinalityMany})
	// A closed knowledge interval is not by itself evidence of a human withdrawal.
	// This fixture tests metadata availability, independently of host/database wall-clock ordering.
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.fact SET known=tstzrange(lower(known),lower(known)+interval '1 microsecond') WHERE fact_id=$1`), id); err != nil {
		t.Fatal(err)
	}
	store, citations := pg.NewRecordStore(pool, schema), pg.NewCitationStore(pool, schema)
	citation, err := citations.Resolve(ctx, "p1", id, nil, 8)
	if err != nil || citation.Retraction != nil || citation.Status != "knowledge_closed" {
		t.Fatalf("invented human withdrawal: %+v %v", citation, err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`ALTER TABLE {schema}.record_retraction RENAME TO unavailable_instruction`)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.History(ctx, "p1", id, nil, 20); err == nil {
		t.Fatal("history hid unavailable withdrawal metadata")
	}
	if _, err := citations.Resolve(ctx, "p1", id, nil, 8); err == nil {
		t.Fatal("citation hid unavailable withdrawal metadata")
	}
}

func TestClaimAssertionRefusesAnInvalidSourceBeforeWriting(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "claim_invalid_source")
	if _, err := pg.NewFactStore(pool).Assert(ctx, schema, "p1", "invalid-source", domain.RoleUser, "subject-1", domain.Claim{Subject: "Atlas", Predicate: "works_at", Object: "Ensera", Statement: "Atlas works at Ensera", Cardinality: domain.CardinalityMany}); err == nil {
		t.Fatal("invalid source created a fact")
	}
	var n int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.entity`)).Scan(&n); err != nil || n != 0 {
		t.Fatalf("invalid source left identities: %d %v", n, err)
	}
}
