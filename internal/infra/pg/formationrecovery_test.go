// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestParkedPagesAreBoundedProjectScopedAndNeverContainProviderText(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "recovery_pages")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	store := pg.NewObservationStore(pool)
	for i := 0; i < 5; i++ {
		turn := turnOf(domain.Message{Role: domain.RoleUser, Content: "private memory"})
		if i == 4 {
			turn.Scope = "p2"
		}
		obs, err := store.Append(ctx, schema, turn)
		if err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			continue
		}
		claimed(t, pool, schema, obs.ID)
		if _, err = store.RecordFormationFailure(ctx, schema, obs.ID, "private provider echo"); err != nil {
			t.Fatal(err)
		}
		if err = store.Park(ctx, schema, obs.ID); err != nil {
			t.Fatal(err)
		}
	}
	first, err := store.ParkedPage(ctx, schema, "p1", -1, 2)
	if err != nil || len(first.Items) != 2 || first.Items[0].LogOffset != 0 || first.Items[1].LogOffset != 2 || first.NextAfter == nil || *first.NextAfter != 2 {
		t.Fatalf("first page: %+v %v", first, err)
	}
	data, _ := json.Marshal(first)
	if strings.Contains(string(data), "private") || strings.Contains(string(data), "reason") || strings.Contains(string(data), "subject") {
		t.Fatalf("content in metadata: %s", data)
	}
	last, err := store.ParkedPage(ctx, schema, "p1", *first.NextAfter, 2)
	if err != nil || len(last.Items) != 1 || last.Items[0].LogOffset != 3 || last.NextAfter != nil {
		t.Fatalf("last page: %+v %v", last, err)
	}
	empty, err := store.ParkedPage(ctx, schema, "p1", 3, 200)
	if err != nil || len(empty.Items) != 0 || empty.Items == nil {
		t.Fatalf("empty page: %+v %v", empty, err)
	}
	for _, v := range []struct {
		scope string
		after int64
		limit int
	}{{"", -1, 1}, {"p1", -2, 1}, {"p1", -1, 0}, {"p1", -1, 201}} {
		if _, err := store.ParkedPage(ctx, schema, v.scope, v.after, v.limit); err == nil {
			t.Fatal("invalid page accepted")
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := store.ParkedPage(canceled, schema, "p1", -1, 1); err == nil {
		t.Fatal("canceled page succeeded")
	}
	// With a larger queue, the ordinary planner must stop at the offset index, not sort it all.
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.ingestion_budget SET max_pending_per_project=3000;
INSERT INTO {schema}.observation(observation_id,scope,log_offset,kind,occurred_at,ingested_at,source_role,formation_attempts,parked_at)
SELECT gen_random_uuid(),'p1',n,'turn',now(),now(),'user',1,now() FROM generate_series(5,2004) n;
ANALYZE {schema}.observation`)); err != nil {
		t.Fatal(err)
	}
	var plan []byte
	if err := pool.QueryRow(ctx, schema.SQL(`EXPLAIN (FORMAT JSON) SELECT observation_id::text,log_offset,formation_attempts,parked_at
FROM {schema}.observation WHERE scope='p1' AND parked_at IS NOT NULL AND log_offset>1000 ORDER BY log_offset LIMIT 2`)).Scan(&plan); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(plan), "observation_parked_idx") || strings.Contains(string(plan), `"Node Type": "Sort"`) {
		t.Fatalf("unbounded parked plan: %s", plan)
	}
}

func TestOperatorRecoveryIsScopedAuditedAndConcurrentRetriesChangeOneTurn(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "recovery_atomic")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	store := pg.NewObservationStore(pool)
	obs, err := store.Append(ctx, schema, turnOf(domain.Message{Role: domain.RoleUser, Content: "private memory"}))
	if err != nil {
		t.Fatal(err)
	}
	claimed(t, pool, schema, obs.ID)
	if _, err = store.RecordFormationFailure(ctx, schema, obs.ID, "secret echo"); err != nil {
		t.Fatal(err)
	}
	if err = store.Park(ctx, schema, obs.ID); err != nil {
		t.Fatal(err)
	}
	if changed, err := store.UnparkAudited(ctx, schema, "p2", obs.ID, uuid.NewString()); changed || !errors.Is(err, pg.ErrFormationTurnNotFound) {
		t.Fatalf("cross-project recovery: %v %v", changed, err)
	}
	var changed atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			did, err := store.UnparkAudited(ctx, schema, "p1", obs.ID, uuid.NewString())
			if err != nil {
				t.Error(err)
			}
			if did {
				changed.Add(1)
			}
		}()
	}
	wg.Wait()
	if changed.Load() != 1 {
		t.Fatalf("changed %d times", changed.Load())
	}
	fresh, err := store.Freshness(ctx, schema, "p1")
	if err != nil || fresh.HasFormed || fresh.Parked != 0 {
		t.Fatalf("freshness after recovery: %+v %v", fresh, err)
	}
	assertIngestionAccounting(t, pool, schema, 1)
	entries, err := pg.NewAuditStore(pool, schema).Recent(ctx, 100)
	if err != nil || len(entries) != 13 {
		t.Fatalf("audit entries=%d err=%v", len(entries), err)
	}
	var magnitude, refused int
	for _, e := range entries {
		if err := e.Validate(); err != nil {
			t.Fatal(err)
		}
		if e.Operation != domain.AuditFormationUnpark || e.PrincipalKind != domain.PrincipalOperator {
			t.Fatalf("wrong audit: %+v", e)
		}
		if _, err := uuid.Parse(e.Principal); err != nil {
			t.Fatal(err)
		}
		magnitude += e.Magnitude
		if e.Outcome == domain.OutcomeRefused {
			refused++
			if e.Project != "p2" {
				t.Fatal("wrong refusal project")
			}
		}
	}
	if magnitude != 1 || refused != 1 {
		t.Fatalf("magnitude=%d refused=%d", magnitude, refused)
	}
	if err := store.MarkFormed(ctx, schema, obs.ID); err != nil {
		t.Fatal(err)
	}
	if did, err := store.UnparkAudited(ctx, schema, "p1", obs.ID, uuid.NewString()); did || err != nil {
		t.Fatalf("completed turn reopened: %v %v", did, err)
	}
	assertIngestionAccounting(t, pool, schema, 0)
}

func TestOperatorRecoveryRollsBackWhenItsAuditCannotBeRecorded(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "recovery_rollback")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	store := pg.NewObservationStore(pool)
	obs, err := store.Append(ctx, schema, turnOf(domain.Message{Role: domain.RoleUser, Content: "pending"}))
	if err != nil {
		t.Fatal(err)
	}
	claimed(t, pool, schema, obs.ID)
	if _, err = store.RecordFormationFailure(ctx, schema, obs.ID, "provider unavailable"); err != nil {
		t.Fatal(err)
	}
	if err = store.Park(ctx, schema, obs.ID); err != nil {
		t.Fatal(err)
	}
	before, err := store.Freshness(ctx, schema, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`ALTER TABLE {schema}.audit_entry ADD CONSTRAINT test_refuse_recovery CHECK(operation<>'formation.unpark')`)); err != nil {
		t.Fatal(err)
	}
	if did, err := store.UnparkAudited(ctx, schema, "p1", obs.ID, uuid.NewString()); did || err == nil {
		t.Fatalf("unaudited recovery: %v %v", did, err)
	}
	after, err := store.Freshness(ctx, schema, "p1")
	if err != nil || before != after {
		t.Fatalf("watermark changed: %+v %+v %v", before, after, err)
	}
	page, err := store.ParkedPage(ctx, schema, "p1", -1, 1)
	if err != nil || len(page.Items) != 1 || page.Items[0].Attempts != 1 {
		t.Fatalf("partial recovery: %+v %v", page, err)
	}
	assertIngestionAccounting(t, pool, schema, 1)
	if _, err := pool.Exec(ctx, schema.SQL(`ALTER TABLE {schema}.audit_entry DROP CONSTRAINT test_refuse_recovery`)); err != nil {
		t.Fatal(err)
	}
	// A row locked by another operation must honor the caller's deadline and leave the turn parked.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, schema.SQL(`SELECT 1 FROM {schema}.observation WHERE observation_id=$1 FOR UPDATE`), obs.ID); err != nil {
		t.Fatal(err)
	}
	deadline, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	if did, err := store.UnparkAudited(deadline, schema, "p1", obs.ID, uuid.NewString()); did || err == nil {
		t.Fatalf("locked recovery did not cancel: %v %v", did, err)
	}
	cancel()
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if did, err := store.UnparkAudited(ctx, schema, "p1", obs.ID, uuid.NewString()); !did || err != nil {
		t.Fatalf("recovery after repair: %v %v", did, err)
	}
	for _, v := range []struct{ scope, id, actor string }{{"", obs.ID, uuid.NewString()}, {"p1", "bad", uuid.NewString()}, {"p1", obs.ID, "subject@example.com"}, {"p1", obs.ID, uuid.Nil.String()}} {
		if did, err := store.UnparkAudited(ctx, schema, v.scope, v.id, v.actor); did || err == nil {
			t.Fatal("invalid recovery arguments accepted")
		}
	}
}

// claimed counts the attempt a worker's claim counts. A turn that was never attempted cannot
// be parked — `observation_parked_chk` refuses it, because giving up on work nobody started is a
// bug rather than a state — so a fixture that parks a turn claims it first, as the worker does.
func claimed(t *testing.T, pool *pgxpool.Pool, schema pg.Schema, id string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), schema.SQL(
		`UPDATE {schema}.observation SET formation_attempts = formation_attempts + 1 WHERE observation_id = $1`), id); err != nil {
		t.Fatal(err)
	}
}
