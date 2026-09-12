// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestRecoveryStorageFailuresNeverCommitPartialMemory(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "receipt_failures")
	facts := pg.NewFactStore(pool)
	obs := pg.NewObservationStore(pool)
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, place := range []string{"Dublin", "Oslo"} {
		text := "I live in " + place
		statedAt(t, ctx, obs, facts, schema, start, text, domain.Claim{Subject: "I", Predicate: "lives_in", Object: place, Statement: text, Cardinality: domain.CardinalityOne, ValidFrom: start.AddDate(0, i, 0)})
	}
	if _, err := pool.Exec(ctx, schema.SQL(`DELETE FROM {schema}.fact; DELETE FROM {schema}.entity`)); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ table, condition string }{{"entity", "false"}, {"projection_dependency", "projection_kind<>'entity'"}, {"fact", "false"}, {"fact_evidence", "false"}, {"projection_dependency", "projection_kind<>'fact'"}, {"fact_history", "false"}, {"projection_dependency", "projection_kind<>'fact_history'"}} {
		t.Run(tc.table+tc.condition, func(t *testing.T) {
			if _, err := pool.Exec(ctx, schema.SQL("ALTER TABLE {schema}."+tc.table+" ADD CONSTRAINT fail_recovery_write CHECK("+tc.condition+") NOT VALID")); err != nil {
				t.Fatal(err)
			}
			if _, err := facts.RecoverFacts(ctx, schema, "p1", "", "", uuid.NewString(), 100); err == nil {
				t.Fatal("storage refusal committed a recovery page")
			}
			for _, table := range []string{"entity", "fact", "fact_evidence", "fact_history"} {
				if recoverySnapshot(t, pool, schema, table) != "[]" {
					t.Fatalf("failed page left %s", table)
				}
			}
			if _, err := pool.Exec(ctx, schema.SQL("ALTER TABLE {schema}."+tc.table+" DROP CONSTRAINT fail_recovery_write")); err != nil {
				t.Fatal(err)
			}
		})
	}
	page, err := facts.RecoverFacts(ctx, schema, "p1", "", "", uuid.NewString(), 100)
	if err != nil || page.Restored != 2 {
		t.Fatalf("retry did not recover: %+v %v", page, err)
	}
}

func TestRecoveryRejectsDamagedReceiptsAndUnregisteredAdmission(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "receipt_damaged")
	facts := pg.NewFactStore(pool)
	obs := pg.NewObservationStore(pool)
	source, err := obs.Append(ctx, schema, turnOf(domain.Message{Role: domain.RoleUser, Content: "Atlas works at Ensera"}))
	if err != nil {
		t.Fatal(err)
	}
	claim := domain.Claim{Subject: "Atlas", Predicate: "works_at", Object: "Ensera", Statement: "Atlas works at Ensera", Quote: "Atlas works at Ensera", ByteEnd: 21, Cardinality: domain.CardinalityMany}
	// Failing the receipt write must abort the original admission too.
	if _, err := pool.Exec(ctx, schema.SQL(`ALTER TABLE {schema}.fact_receipt ADD CONSTRAINT refuse_receipt CHECK(false)`)); err != nil {
		t.Fatal(err)
	}
	if _, err := facts.Assert(ctx, schema, "p1", source.ID, domain.RoleUser, "subject-1", claim); err == nil {
		t.Fatal("fact without receipt committed")
	}
	if recoverySnapshot(t, pool, schema, "fact") != "[]" {
		t.Fatal("failed receipt retained a fact")
	}
	if _, err := pool.Exec(ctx, schema.SQL(`ALTER TABLE {schema}.fact_receipt DROP CONSTRAINT refuse_receipt`)); err != nil {
		t.Fatal(err)
	}
	// The actual quote is twenty-one bytes; these mutations are privileged storage damage fixtures.
	id, err := facts.Assert(ctx, schema, "p1", source.ID, domain.RoleUser, "subject-1", claim)
	if err != nil {
		t.Fatal(err)
	}
	var state, evidence, left []byte
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT state,evidence,subject_identity FROM {schema}.fact_receipt WHERE fact_id=$1`), id).Scan(&state, &evidence, &left); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`DELETE FROM {schema}.fact; DELETE FROM {schema}.entity`)); err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []string{
		`UPDATE {schema}.fact_receipt SET evidence=jsonb_set(evidence,'{byte_start}','"bad"')`,
		`UPDATE {schema}.fact_receipt SET subject_identity='{}'`,
		`UPDATE {schema}.fact_receipt SET subject_identity=jsonb_set(subject_identity,'{scope}','"p2"')`,
		`UPDATE {schema}.fact_receipt SET subject_identity=jsonb_set(subject_identity,'{entity_id}','"bad"')`,
		`UPDATE {schema}.fact_receipt SET state=jsonb_set(state,'{known}','"bad-range"')`,
	} {
		if _, err := pool.Exec(ctx, schema.SQL(mutation)); err != nil {
			t.Fatal(err)
		}
		if _, err := facts.RecoverFacts(ctx, schema, "p1", "", "", uuid.NewString(), 100); err == nil {
			t.Fatal("damaged receipt accepted")
		}
		if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.fact_receipt SET state=$1,evidence=$2,subject_identity=$3`), state, evidence, left); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := facts.RecoverFacts(ctx, schema, "p1", "", "", uuid.NewString(), 100); err != nil {
		t.Fatal(err)
	}
	// No source: a receipt cannot be written into another project even for named entities.
	if _, err := facts.Assert(ctx, schema, "p2", source.ID, domain.RoleUser, "subject-1", claim); err == nil {
		t.Fatal("foreign source accepted")
	}
	absent, _ := pg.NewSchema("receipt_absent")
	if _, err := facts.RecoverFacts(ctx, absent, "p1", "", "", uuid.NewString(), 100); err == nil {
		t.Fatal("unavailable schema accepted")
	}
}

func TestRecoveryRacingErasureCannotRetainDeletedSources(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "receipt_erasure_race")
	facts := pg.NewFactStore(pool)
	for i := 0; i < 4; i++ {
		text := fmt.Sprintf("Atlas works at company %d", i)
		statedAt(t, ctx, pg.NewObservationStore(pool), facts, schema, time.Now(), text, domain.Claim{Subject: "Atlas", Predicate: "works_at", Object: fmt.Sprintf("company %d", i), Statement: text, Cardinality: domain.CardinalityMany})
	}
	if _, err := pool.Exec(ctx, schema.SQL(`DELETE FROM {schema}.fact`)); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_, err := facts.RecoverFacts(ctx, schema, "p1", "", "", uuid.NewString(), 100)
		errs <- err
	}()
	go func() {
		defer wg.Done()
		<-start
		_, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", "subject-1", "requested")
		errs <- err
	}()
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		var pgerr *pgconn.PgError
		if err != nil && (!errors.As(err, &pgerr) || pgerr.Code != "40P01") {
			t.Fatal(err)
		}
	}
	// A deadlock victim retries its atomic unit, never resumes half a transaction.
	erased, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", "subject-1", "requested")
	if err != nil {
		t.Fatal(err)
	}
	if erased.Residual["fact_receipt"] != 0 {
		t.Fatal("erasure left recovery input")
	}
	for _, table := range []string{"fact", "fact_evidence", "fact_receipt", "fact_receipt_history"} {
		if recoverySnapshot(t, pool, schema, table) != "[]" {
			t.Fatalf("erasure race retained %s", table)
		}
	}
	if p, err := facts.RecoverFacts(ctx, schema, "p1", "", "", uuid.NewString(), 100); err != nil || p.Examined != 0 {
		t.Fatalf("erased memory recovered: %+v %v", p, err)
	}
}

func TestReceiptErasureAlsoRemovesKnowledgeDependingOnAnotherSubject(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "receipt_causal_owner")
	facts := pg.NewFactStore(pool)
	obs := pg.NewObservationStore(pool)
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	first := statedAt(t, ctx, obs, facts, schema, start, "Atlas is in Dublin", domain.Claim{Subject: "Atlas", Predicate: "located_in", Object: "Dublin", Statement: "Atlas is in Dublin", Cardinality: domain.CardinalityOne, ValidFrom: start})
	turn := turnOf(domain.Message{Role: domain.RoleUser, Content: "Atlas moved to Oslo"})
	turn.DataSubjectID = "subject-2"
	source, err := obs.Append(ctx, schema, turn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := facts.Assert(ctx, schema, "p1", source.ID, domain.RoleUser, "subject-2", domain.Claim{Subject: "Atlas", Predicate: "located_in", Object: "Oslo", Statement: "Atlas is in Oslo", Quote: "Atlas moved to Oslo", ByteEnd: 19, Cardinality: domain.CardinalityOne, ValidFrom: start.AddDate(0, 1, 0)}); err != nil {
		t.Fatal(err)
	}
	// The supported state itself is retained exactly, including metadata outside the recall view.
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.fact SET lang='ar',retention_until=clock_timestamp()+interval '1 day' WHERE fact_id=$1`), first); err != nil {
		t.Fatal(err)
	}
	before := recoverySnapshot(t, pool, schema, "fact")
	if _, err := pool.Exec(ctx, schema.SQL(`DELETE FROM {schema}.fact`)); err != nil {
		t.Fatal(err)
	}
	if _, err := facts.RecoverFacts(ctx, schema, "p1", "", "", uuid.NewString(), 100); err != nil {
		t.Fatal(err)
	}
	if recoverySnapshot(t, pool, schema, "fact") != before {
		t.Fatal("recovery lost metadata")
	}
	exported, err := pg.NewExporter(pool, schema).Export(ctx, schema, "p1", "subject-2")
	if err != nil || len(exported.Sections["fact_receipt"]) != 2 || len(exported.Sections["fact_receipt_history"]) != 1 {
		t.Fatalf("causal receipt omitted: %v", err)
	}
	erased, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", "subject-2", "requested")
	if err != nil || erased.Deleted["fact_receipt"] != 2 || erased.Deleted["fact_receipt_history"] != 1 {
		t.Fatalf("causal receipt survived: %+v %v", erased, err)
	}
	p, err := facts.RecoverFacts(ctx, schema, "p1", "", "", uuid.NewString(), 100)
	if err != nil || p.Examined != 0 {
		t.Fatalf("recovery reopened invalidated knowledge: %+v %v", p, err)
	}
}
