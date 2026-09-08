// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestConcurrentSupportRepairAndRetractionPreserveTheHumanWithdrawal(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "support_retraction_race")
	obs, facts := pg.NewObservationStore(pool), pg.NewFactStore(pool)
	for i := 0; i < 8; i++ {
		id := statedAt(t, ctx, obs, facts, schema, time.Now(), "Atlas works at Ensera", domain.Claim{Subject: "Atlas", Predicate: "works_at", Object: "Ensera", Statement: "Atlas works at Ensera", Cardinality: domain.CardinalityMany})
		mutation := recordMutation(t, pool, schema, id)
		var source string
		if err := pool.QueryRow(ctx, schema.SQL(`SELECT source_observation_id::text FROM {schema}.fact_receipt WHERE fact_id=$1::uuid`), id).Scan(&source); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, schema.SQL(`DELETE FROM {schema}.projection_dependency WHERE fact_ref=$1::uuid`), id); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		var recoverErr, retractErr error
		go func() {
			defer wg.Done()
			<-start
			_, recoverErr = facts.RecoverFacts(ctx, schema, "p1", source, "", uuid.NewString(), 1)
		}()
		go func() {
			defer wg.Done()
			<-start
			_, retractErr = pg.NewRecordStore(pool, schema).Retract(ctx, "p1", uuid.NewString(), []pg.RecordMutation{mutation})
		}()
		close(start)
		wg.Wait()
		if recoverErr != nil {
			t.Fatal(recoverErr)
		}
		if retractErr != nil {
			t.Fatal(retractErr)
		}
		citation, err := pg.NewCitationStore(pool, schema).Resolve(ctx, "p1", id, nil, 8)
		if err != nil || citation.Retraction == nil || citation.Known.Until == nil {
			t.Fatal("support repair raced past human withdrawal")
		}
	}
}

func TestConcurrentSupportRepairCannotRecreateErasedMemory(t *testing.T) {
	isDeadlock := func(err error) bool { var db *pgconn.PgError; return errors.As(err, &db) && db.Code == "40P01" }
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "support_erasure_race")
	obs, facts := pg.NewObservationStore(pool), pg.NewFactStore(pool)
	for i := 0; i < 8; i++ {
		id := statedAt(t, ctx, obs, facts, schema, time.Now(), "Atlas works at Ensera", domain.Claim{Subject: "Atlas", Predicate: "works_at", Object: "Ensera", Statement: "Atlas works at Ensera", Cardinality: domain.CardinalityMany})
		if _, err := pool.Exec(ctx, schema.SQL(`DELETE FROM {schema}.fact_evidence WHERE fact_id=$1::uuid`), id); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		var recoverErr, eraseErr error
		go func() {
			defer wg.Done()
			<-start
			_, recoverErr = facts.RecoverFacts(ctx, schema, "p1", "", "", uuid.NewString(), 100)
		}()
		go func() {
			defer wg.Done()
			<-start
			_, eraseErr = pg.NewEraser(pool).Erase(ctx, schema, "p1", "subject-1", "requested")
		}()
		close(start)
		wg.Wait()
		if recoverErr != nil && !isDeadlock(recoverErr) {
			t.Fatal(recoverErr)
		}
		if isDeadlock(eraseErr) {
			_, eraseErr = pg.NewEraser(pool).Erase(ctx, schema, "p1", "subject-1", "retry after atomic abort")
		}
		if eraseErr != nil {
			t.Fatal(eraseErr)
		}
		if _, err := pg.NewCitationStore(pool, schema).Resolve(ctx, "p1", id, nil, 8); !errors.Is(err, pg.ErrCitationNotFound) {
			t.Fatal("erased citation survived repair")
		}
		var remaining int
		if err := pool.QueryRow(ctx, schema.SQL(`SELECT (SELECT count(*) FROM {schema}.fact)+(SELECT count(*) FROM {schema}.fact_evidence)+(SELECT count(*) FROM {schema}.fact_receipt)`)).Scan(&remaining); err != nil || remaining != 0 {
			t.Fatal("repair resurrected erased source material")
		}
	}
}
