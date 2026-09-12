// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
)

func TestOrphanedProjectCancellationFinishesPublishedSourceBookkeeping(t *testing.T) {
	ctx := context.Background()
	f := generationSource(t, "job_orphan_freshness")
	jobs := pg.NewRebuildJobStore(f.pool)
	key, actor := uuid.NewString(), uuid.NewString()
	session, err := jobs.Acquire(ctx, f.schema, "p1", key, f.version, actor)
	if err != nil {
		t.Fatal(err)
	}
	work, found, err := session.Next(ctx)
	if err != nil || !found {
		t.Fatal(err)
	}
	if _, err := f.facts.ApplyGeneration(ctx, f.schema, f.snapshot, work.Key, f.version, actor, pg.GenerationPlan{Claims: []domain.Claim{f.claim}, Job: session.Guard(work.Offset)}); err != nil {
		t.Fatal(err)
	}
	session.Close()
	if _, err := jobs.Cancel(ctx, f.schema, "p1", key, actor); err != nil {
		t.Fatal(err)
	}
	var formed bool
	if err := f.pool.QueryRow(ctx, f.schema.SQL(`SELECT formed_at IS NOT NULL FROM {schema}.observation WHERE observation_id=$1`), work.SourceID).Scan(&formed); err != nil {
		t.Fatal(err)
	}
	if !formed {
		t.Fatal("cancelled job left its published source unfinished")
	}
}

func orphanedPublishedJob(t *testing.T, name string) (generationFixture, *pg.RebuildJobStore, string, string) {
	t.Helper()
	ctx := context.Background()
	f := generationSource(t, name)
	jobs := pg.NewRebuildJobStore(f.pool)
	key, actor := uuid.NewString(), uuid.NewString()
	session, err := jobs.Acquire(ctx, f.schema, "p1", key, f.version, actor)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	work, found, err := session.Next(ctx)
	if err != nil || !found {
		t.Fatal(err)
	}
	if _, err := f.facts.ApplyGeneration(ctx, f.schema, f.snapshot, work.Key, f.version, actor, pg.GenerationPlan{Claims: []domain.Claim{f.claim}, Job: session.Guard(work.Offset)}); err != nil {
		t.Fatal(err)
	}
	return f, jobs, key, actor
}

func TestOrphanedCancellationRollsBackFreshnessWhenItsAuditFails(t *testing.T) {
	ctx := context.Background()
	f, jobs, key, actor := orphanedPublishedJob(t, "job_cancel_freshness_rollback")
	if _, err := f.pool.Exec(ctx, f.schema.SQL(`CREATE FUNCTION {schema}.refuse_cancel_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'cancel audit unavailable'; END $$;
        CREATE TRIGGER refuse_cancel_audit BEFORE INSERT ON {schema}.audit_entry FOR EACH ROW WHEN (NEW.operation='formation.rebuild.cancel') EXECUTE FUNCTION {schema}.refuse_cancel_audit()`)); err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.Cancel(ctx, f.schema, "p1", key, actor); err == nil {
		t.Fatal("orphaned cancellation ignored failed audit")
	}
	state, err := jobs.Status(ctx, f.schema, "p1", key)
	if err != nil || state.Status != "active" || state.Pending == nil || state.Rebuilt != 0 {
		t.Fatalf("failed cancellation advanced progress: %+v %v", state, err)
	}
	var formed bool
	if err := f.pool.QueryRow(ctx, f.schema.SQL(`SELECT formed_at IS NOT NULL FROM {schema}.observation WHERE observation_id=$1`), f.snapshot.SourceID).Scan(&formed); err != nil || formed {
		t.Fatalf("failed cancellation committed freshness: %v", err)
	}
	if _, err := f.pool.Exec(ctx, f.schema.SQL(`DROP TRIGGER refuse_cancel_audit ON {schema}.audit_entry`)); err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.Cancel(ctx, f.schema, "p1", key, actor); err != nil {
		t.Fatal(err)
	}
}

func TestOrphanedCancellationAndErasureSerializeWithoutRevivingMemory(t *testing.T) {
	for i := 0; i < 3; i++ {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			ctx := context.Background()
			f, jobs, key, actor := orphanedPublishedJob(t, fmt.Sprintf("job_cancel_erasure_%d", i))
			start := make(chan struct{})
			var wg sync.WaitGroup
			var cancelErr, eraseErr error
			var erased domain.Erasure
			wg.Add(2)
			go func() { defer wg.Done(); <-start; _, cancelErr = jobs.Cancel(ctx, f.schema, "p1", key, actor) }()
			go func() {
				defer wg.Done()
				<-start
				erased, eraseErr = pg.NewEraser(f.pool).Erase(ctx, f.schema, "p1", "subject-1", "requested")
			}()
			close(start)
			wg.Wait()
			if cancelErr != nil || eraseErr != nil || !erased.Clean() {
				t.Fatalf("cancel/erase failed: cancel=%v erase=%v clean=%v", cancelErr, eraseErr, erased.Clean())
			}
			state, err := jobs.Status(ctx, f.schema, "p1", key)
			if err != nil || state.Status != "cancelled" || state.Pending != nil {
				t.Fatalf("erasure stranded cancellation: %+v %v", state, err)
			}
			if generationState(t, f, "fact") != "[]" || generationState(t, f, "fact_generation") != "[]" {
				t.Fatal("cancellation revived erased memory")
			}
		})
	}
}
