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

func TestRecoveryRepairsMissingEvidenceAndHistoryWithoutReplacingRetainedFacts(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "receipt_retained_support")
	obs, facts := pg.NewObservationStore(pool), pg.NewFactStore(pool)
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, city := range []string{"Dublin", "Oslo"} {
		text := "I live in " + city
		statedAt(t, ctx, obs, facts, schema, start.AddDate(0, i, 0), text, domain.Claim{Subject: "I", Predicate: "lives_in", Object: city, Statement: text, Cardinality: domain.CardinalityOne, ValidFrom: start.AddDate(0, i, 0)})
	}
	beforeFacts := recoverySnapshot(t, pool, schema, "fact")
	beforeEvidence := recoverySnapshot(t, pool, schema, "fact_evidence")
	beforeHistory := recoverySnapshot(t, pool, schema, "fact_history")
	if _, err := pool.Exec(ctx, schema.SQL(`DELETE FROM {schema}.fact_evidence; DELETE FROM {schema}.fact_history; DELETE FROM {schema}.projection_dependency WHERE projection_kind IN ('fact','entity')`)); err != nil {
		t.Fatal(err)
	}
	page, err := facts.RecoverFacts(ctx, schema, "p1", "", "", uuid.NewString(), 100)
	if err != nil || page.Restored != 0 || page.Repaired != 2 {
		t.Fatalf("retained-fact repair: %+v %v", page, err)
	}
	if recoverySnapshot(t, pool, schema, "fact_evidence") != beforeEvidence {
		t.Fatal("retained fact evidence was not repaired")
	}
	if recoverySnapshot(t, pool, schema, "fact_history") != beforeHistory {
		t.Fatal("retained fact history was not repaired")
	}
	if recoverySnapshot(t, pool, schema, "fact") != beforeFacts {
		t.Fatal("support repair changed retained fact state")
	}

	var historyRegistrations int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.projection_dependency WHERE projection_kind='fact_history'`)).Scan(&historyRegistrations); err != nil || historyRegistrations != 2 {
		t.Fatal("history lost original or causal erasure registration")
	}
	erased, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", "subject-1", "requested")
	if err != nil || erased.Deleted["fact"] != 2 || erased.Residual["fact_history"] != 0 {
		t.Fatal("repaired support was not registered for erasure")
	}
}

func TestSupportRecoveryPreservesWithdrawalsAndDoesNothingOnARepeatedPage(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "receipt_support_withdrawn")
	obs, facts := pg.NewObservationStore(pool), pg.NewFactStore(pool)
	id := statedAt(t, ctx, obs, facts, schema, time.Now(), "Atlas works at Ensera", domain.Claim{Subject: "Atlas", Predicate: "works_at", Object: "Ensera", Statement: "Atlas works at Ensera", Cardinality: domain.CardinalityMany})
	if _, err := pg.NewRecordStore(pool, schema).Retract(ctx, "p1", uuid.NewString(), []pg.RecordMutation{recordMutation(t, pool, schema, id)}); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT to_jsonb(f)::text FROM {schema}.fact f WHERE fact_id=$1::uuid`), id).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`DELETE FROM {schema}.fact_evidence`)); err != nil {
		t.Fatal(err)
	}
	page, err := facts.RecoverFacts(ctx, schema, "p1", "", "", uuid.NewString(), 100)
	if err != nil || page.Repaired != 1 || page.Restored != 0 {
		t.Fatalf("repair: %+v %v", page, err)
	}
	var after string
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT to_jsonb(f)::text FROM {schema}.fact f WHERE fact_id=$1::uuid`), id).Scan(&after); err != nil || before != after {
		t.Fatal("support repair changed withdrawn record, intervals or version")
	}
	citation, err := pg.NewCitationStore(pool, schema).Resolve(ctx, "p1", id, nil, 8)
	if err != nil || citation.Retraction == nil || citation.Known.Until == nil || len(citation.Evidence) != 1 {
		t.Fatal("withdrawal was reactivated or lost citation")
	}
	var beforeRevision, afterRevision int64
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT sum(fact_revision) FROM {schema}.observation`)).Scan(&beforeRevision); err != nil {
		t.Fatal(err)
	}
	page, err = facts.RecoverFacts(ctx, schema, "p1", "", "", uuid.NewString(), 100)
	if err != nil || page.Restored != 0 || page.Repaired != 0 {
		t.Fatal("repeated repair changed retained support")
	}
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT sum(fact_revision) FROM {schema}.observation`)).Scan(&afterRevision); err != nil || beforeRevision != afterRevision {
		t.Fatal("no-op recovery invalidated report inputs")
	}

}

func TestSupportRecoveryNeverRewritesConflictingEvidenceOrOwnership(t *testing.T) {
	ctx := context.Background()
	for _, kind := range []string{"evidence", "ownership", "pipeline", "history", "source"} {
		t.Run(kind, func(t *testing.T) {
			pool := testPool(t)
			schema := tenant(t, pool, "receipt_support_conflict_"+kind)
			obs, facts := pg.NewObservationStore(pool), pg.NewFactStore(pool)
			start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			var first string
			for i, city := range []string{"Dublin", "Oslo"} {
				text := "I live in " + city
				id := statedAt(t, ctx, obs, facts, schema, start.AddDate(0, i, 0), text, domain.Claim{Subject: "I", Predicate: "lives_in", Object: city, Statement: text, Cardinality: domain.CardinalityOne, ValidFrom: start.AddDate(0, i, 0)})
				if i == 0 {
					first = id
				}
			}
			var ddl string
			switch kind {
			case "evidence":
				ddl = `UPDATE {schema}.fact_evidence SET quote='corrupted' WHERE fact_id=$1::uuid`
			case "ownership":
				ddl = `UPDATE {schema}.projection_dependency SET data_subject_id='different-owner' WHERE fact_ref=$1::uuid`
			case "pipeline":
				ddl = `UPDATE {schema}.projection_dependency SET pipeline_version='different-pipeline' WHERE fact_ref=$1::uuid`
			case "history":
				ddl = `UPDATE {schema}.fact_history SET known=tstzrange(lower(known)-interval '1 hour',upper(known)) WHERE fact_id=$1::uuid`
			case "source":
				ddl = `UPDATE {schema}.turn_message SET content='changed source bytes' WHERE observation_id IN (SELECT source_observation_id FROM {schema}.fact_receipt WHERE fact_id=$1::uuid)`
			}
			if _, err := pool.Exec(ctx, schema.SQL(ddl), first); err != nil {
				t.Fatal(err)
			}
			before := recoverySnapshot(t, pool, schema, "fact")
			support := recoverySnapshot(t, pool, schema, "fact_evidence")
			history := recoverySnapshot(t, pool, schema, "fact_history")
			if _, err := facts.RecoverFacts(ctx, schema, "p1", "", "", uuid.NewString(), 100); !errors.Is(err, pg.ErrInvalidReceipt) {
				t.Fatalf("conflicting %s accepted: %v", kind, err)
			}
			if recoverySnapshot(t, pool, schema, "fact") != before || recoverySnapshot(t, pool, schema, "fact_evidence") != support || recoverySnapshot(t, pool, schema, "fact_history") != history {
				t.Fatal("refused repair rewrote retained material")
			}
		})
	}
}

func TestSupportRecoveryFailuresRollBackTheEntirePage(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "receipt_support_rollback")
	obs, facts := pg.NewObservationStore(pool), pg.NewFactStore(pool)
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, city := range []string{"Dublin", "Oslo"} {
		text := "I live in " + city
		statedAt(t, ctx, obs, facts, schema, start.AddDate(0, i, 0), text, domain.Claim{Subject: "I", Predicate: "lives_in", Object: city, Statement: text, Cardinality: domain.CardinalityOne, ValidFrom: start.AddDate(0, i, 0)})
	}
	if _, err := pool.Exec(ctx, schema.SQL(`DELETE FROM {schema}.fact_evidence;DELETE FROM {schema}.fact_history;DELETE FROM {schema}.projection_dependency WHERE projection_kind IN ('fact','entity')`)); err != nil {
		t.Fatal(err)
	}
	before := recoverySnapshot(t, pool, schema, "fact")
	for _, failure := range []struct{ table, condition string }{{"fact_evidence", "false"}, {"fact_history", "false"}, {"projection_dependency", "projection_kind<>'fact'"}, {"audit_entry", "operation<>'formation.recover'"}} {
		if _, err := pool.Exec(ctx, schema.SQL(`ALTER TABLE {schema}.`+failure.table+` ADD CONSTRAINT refuse_support_recovery CHECK(`+failure.condition+`) NOT VALID`)); err != nil {
			t.Fatal(err)
		}
		if _, err := facts.RecoverFacts(ctx, schema, "p1", "", "", uuid.NewString(), 100); err == nil {
			t.Fatal("failed support repair reported success")
		}
		if recoverySnapshot(t, pool, schema, "fact") != before || recoverySnapshot(t, pool, schema, "fact_evidence") != "[]" || recoverySnapshot(t, pool, schema, "fact_history") != "[]" {
			t.Fatal("failed support repair left a partial page")
		}
		if _, err := pool.Exec(ctx, schema.SQL(`ALTER TABLE {schema}.`+failure.table+` DROP CONSTRAINT refuse_support_recovery`)); err != nil {
			t.Fatal(err)
		}
	}
	if page, err := facts.RecoverFacts(ctx, schema, "p1", "", "", uuid.NewString(), 100); err != nil || page.Repaired != 2 {
		t.Fatal("failed page was not retryable")
	}
}
