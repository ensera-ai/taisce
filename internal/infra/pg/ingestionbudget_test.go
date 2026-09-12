// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func assertIngestionAccounting(t *testing.T, pool *pgxpool.Pool, schema pg.Schema, want int) {
	t.Helper()
	var pending, reservations, unfinished, sum, disagreements int
	err := pool.QueryRow(context.Background(), schema.SQL(`SELECT
 (SELECT pending FROM {schema}.ingestion_budget),
 (SELECT count(*) FROM {schema}.ingestion_reservation),
 (SELECT count(*) FROM {schema}.observation WHERE kind='turn' AND formed_at IS NULL),
 (SELECT coalesce(sum(pending),0) FROM {schema}.ingestion_project_usage),
 (SELECT count(*) FROM {schema}.ingestion_project_usage u WHERE pending<>(SELECT count(*) FROM {schema}.ingestion_reservation r WHERE r.scope=u.scope))`)).Scan(&pending, &reservations, &unfinished, &sum, &disagreements)
	if err != nil || pending != want || reservations != want || unfinished != want || sum != want || disagreements != 0 {
		t.Fatalf("inconsistent capacity: pending=%d reservations=%d unfinished=%d sum=%d disagreements=%d want=%d err=%v", pending, reservations, unfinished, sum, disagreements, want, err)
	}
}

func TestBacklogCapacitySurvivesParkingAndReleasesOnFormationOrErasure(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "ingestion_lifecycle")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.ingestion_budget SET max_pending=3,max_pending_per_project=2`)); err != nil {
		t.Fatal(err)
	}
	store := pg.NewObservationStore(pool)
	turn := turnOf(domain.Message{Role: domain.RoleUser, Content: "pending extraction"})
	first, _, err := store.AppendIdempotent(ctx, schema, turn, "ad3e4206-2278-448f-936f-d28b312017ec")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Append(ctx, schema, turn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(ctx, schema, turn); !errors.Is(err, pg.ErrIngestionCapacity) {
		t.Fatalf("project limit: %v", err)
	}
	other := turn
	other.Scope = "p2"
	other.DataSubjectID = "subject-2"
	if _, err := store.Append(ctx, schema, other); err != nil {
		t.Fatal(err)
	}
	key := "7b9cfa94-a642-4c09-82b4-0bb247da1cb6"
	if _, _, err := store.AppendIdempotent(ctx, schema, other, key); !errors.Is(err, pg.ErrIngestionCapacity) {
		t.Fatalf("global limit: %v", err)
	}
	assertIngestionAccounting(t, pool, schema, 3)
	if got, replayed, err := store.AppendIdempotent(ctx, schema, turn, "ad3e4206-2278-448f-936f-d28b312017ec"); err != nil || !replayed || got.ID != first.ID {
		t.Fatalf("full capacity broke retry: %+v %v %v", got, replayed, err)
	}
	claimed(t, pool, schema, first.ID)
	if _, err := store.RecordFormationFailure(ctx, schema, first.ID, "provider unavailable"); err != nil {
		t.Fatal(err)
	}
	if err := store.Park(ctx, schema, first.ID); err != nil {
		t.Fatal(err)
	}
	assertIngestionAccounting(t, pool, schema, 3)
	if err := store.Unpark(ctx, schema, first.ID); err != nil {
		t.Fatal(err)
	}
	assertIngestionAccounting(t, pool, schema, 3)
	for i := 0; i < 2; i++ {
		if err := store.MarkFormed(ctx, schema, first.ID); err != nil {
			t.Fatal(err)
		}
	}
	assertIngestionAccounting(t, pool, schema, 2)
	if got, replayed, err := store.AppendIdempotent(ctx, schema, other, key); err != nil || replayed || got.LogOffset != 1 {
		t.Fatalf("refusal left a receipt or offset hole: %+v %v %v", got, replayed, err)
	}
	assertIngestionAccounting(t, pool, schema, 3)
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.observation SET formed_at=NULL WHERE observation_id=$1`), first.ID); err == nil {
		t.Fatal("reopening a formed observation bypassed capacity")
	}
	assertIngestionAccounting(t, pool, schema, 3)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, schema.SQL(`UPDATE {schema}.observation SET formed_at=now() WHERE observation_id=$1`), second.ID); err != nil {
		tx.Rollback(ctx)
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SET CONSTRAINTS ALL IMMEDIATE`); err != nil {
		tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	assertIngestionAccounting(t, pool, schema, 3)
	if _, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", turn.DataSubjectID, "test"); err != nil {
		t.Fatal(err)
	}
	assertIngestionAccounting(t, pool, schema, 2)
	var retained int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.ingestion_project_usage WHERE scope='p1'`)).Scan(&retained); err != nil || retained != 0 {
		t.Fatalf("erased project's quota state remained: %d %v", retained, err)
	}
}

func TestConcurrentWritersCannotOversubscribeDurableCapacity(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "ingestion_concurrent")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.ingestion_budget SET max_pending=5,max_pending_per_project=3`)); err != nil {
		t.Fatal(err)
	}
	store := pg.NewObservationStore(pool)
	out := make(chan error, 24)
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			turn := turnOf(domain.Message{Role: domain.RoleUser, Content: "queued"})
			turn.Scope = fmt.Sprintf("p%d", i%4)
			_, _, err := store.AppendIdempotent(ctx, schema, turn, uuid.NewString())
			out <- err
		}()
	}
	wg.Wait()
	close(out)
	accepted := 0
	for err := range out {
		if err == nil {
			accepted++
		} else if !errors.Is(err, pg.ErrIngestionCapacity) {
			t.Fatal(err)
		}
	}
	if accepted != 5 {
		t.Fatalf("accepted %d writes, want capacity 5", accepted)
	}
	assertIngestionAccounting(t, pool, schema, 5)
	var excess int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.ingestion_project_usage WHERE pending>3`)).Scan(&excess); err != nil || excess != 0 {
		t.Fatalf("project limit exceeded: %d %v", excess, err)
	}
	for _, table := range []string{"turn_message", "chunk", "observation_retry", "projection_dependency"} {
		var n int
		if err := pool.QueryRow(ctx, schema.SQL("SELECT count(*) FROM {schema}."+table)).Scan(&n); err != nil || n != 5 {
			t.Fatalf("partial %s rows: %d %v", table, n, err)
		}
	}
}

func TestAStaleRepeatableReadSnapshotCannotOversubscribeCapacity(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "ingestion_snapshot")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.ingestion_budget SET max_pending=1,max_pending_per_project=1`)); err != nil {
		t.Fatal(err)
	}
	first, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Rollback(ctx)
	second, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Rollback(ctx)
	for _, tx := range []pgx.Tx{first, second} {
		if _, err := tx.Exec(ctx, schema.SQL(`SELECT pending FROM {schema}.ingestion_budget`)); err != nil {
			t.Fatal(err)
		}
	}
	for i, tx := range []pgx.Tx{first, second} {
		if _, err := tx.Exec(ctx, schema.SQL(`INSERT INTO {schema}.observation(observation_id,scope,log_offset,kind,occurred_at,ingested_at,source_role) VALUES($1,$2,0,'turn',now(),now(),'user')`), uuid.NewString(), fmt.Sprintf("p%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := first.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var databaseError *pgconn.PgError
	if err := second.Commit(ctx); !errors.As(err, &databaseError) || databaseError.Code != "40001" {
		t.Fatalf("stale snapshot was not refused: %v", err)
	}
	assertIngestionAccounting(t, pool, schema, 1)
}

func TestRetentionReleasesCapacityHeldByParkedObservations(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "ingestion_retention")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.ingestion_budget SET max_pending=1,max_pending_per_project=1`)); err != nil {
		t.Fatal(err)
	}
	store := pg.NewObservationStore(pool)
	turn := turnOf(domain.Message{Role: domain.RoleUser, Content: "provider outage"})
	observation, err := store.Append(ctx, schema, turn)
	if err != nil {
		t.Fatal(err)
	}
	claimed(t, pool, schema, observation.ID)
	if _, err := store.RecordFormationFailure(ctx, schema, observation.ID, "provider unavailable"); err != nil {
		t.Fatal(err)
	}
	if err := store.Park(ctx, schema, observation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.observation SET retention_until=now()-interval '1 second' WHERE observation_id=$1`), observation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.NewRetentionStore(pool, schema).Sweep(ctx, "p1", 10); err != nil {
		t.Fatal(err)
	}
	assertIngestionAccounting(t, pool, schema, 0)
	if _, err := store.Append(ctx, schema, turn); err != nil {
		t.Fatalf("retention did not release quota: %v", err)
	}
	assertIngestionAccounting(t, pool, schema, 1)
}

func TestDeferredReservationsUseTheFinalObservationState(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "ingestion_final_state")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	for _, remove := range []bool{false, true} {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		id := uuid.NewString()
		if _, err := tx.Exec(ctx, schema.SQL(`INSERT INTO {schema}.observation(observation_id,scope,log_offset,kind,occurred_at,ingested_at,source_role) VALUES($1,$2,0,'turn',now(),now(),'user')`), id, fmt.Sprintf("p%v", remove)); err != nil {
			tx.Rollback(ctx)
			t.Fatal(err)
		}
		sql := `UPDATE {schema}.observation SET formed_at=now() WHERE observation_id=$1`
		if remove {
			sql = `DELETE FROM {schema}.observation WHERE observation_id=$1`
		}
		if _, err := tx.Exec(ctx, schema.SQL(sql), id); err != nil {
			tx.Rollback(ctx)
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		assertIngestionAccounting(t, pool, schema, 0)
	}
}

func TestInconsistentQuotaCountersRefuseErasureWithoutPartialDataLoss(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "ingestion_inconsistent")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	turn := turnOf(domain.Message{Role: domain.RoleUser, Content: "retained if accounting fails"})
	for _, table := range []string{"ingestion_budget", "ingestion_project_usage"} {
		if _, err := pg.NewObservationStore(pool).Append(ctx, schema, turn); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, schema.SQL("UPDATE {schema}."+table+" SET pending=0")); err != nil {
			t.Fatal(err)
		}
		if _, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", turn.DataSubjectID, "test"); err == nil {
			t.Fatal("inconsistent accounting silently released capacity")
		}
		for _, retainedTable := range []string{"observation", "turn_message", "chunk", "ingestion_reservation"} {
			var n int
			if err := pool.QueryRow(ctx, schema.SQL("SELECT count(*) FROM {schema}."+retainedTable)).Scan(&n); err != nil || n != 1 {
				t.Fatalf("partial erasure of %s: %d %v", retainedTable, n, err)
			}
		}
		if _, err := pool.Exec(ctx, schema.SQL("UPDATE {schema}."+table+" SET pending=1")); err != nil {
			t.Fatal(err)
		}
		if _, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", turn.DataSubjectID, "test"); err != nil {
			t.Fatalf("accounting repair did not restore erasure: %v", err)
		}
		assertIngestionAccounting(t, pool, schema, 0)
	}
}
