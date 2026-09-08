// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestProjectRebuildFencesAReplacedRunnerAtPublication(t *testing.T) {
	ctx := context.Background()
	f := generationSource(t, "job_fencing")
	jobs := pg.NewRebuildJobStore(f.pool)
	key, actor := uuid.NewString(), uuid.NewString()
	old, err := jobs.Acquire(ctx, f.schema, "p1", key, f.version, actor)
	if err != nil {
		t.Fatal(err)
	}
	work, found, err := old.Next(ctx)
	if err != nil || !found {
		t.Fatalf("old runner reserve: %v", err)
	}
	staleGuard := old.Guard(work.Offset)
	old.Close()
	current, err := jobs.Acquire(ctx, f.schema, "p1", key, f.version, actor)
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	if _, err := f.facts.ApplyGeneration(ctx, f.schema, f.snapshot, work.Key, f.version, actor, pg.GenerationPlan{Claims: []domain.Claim{f.claim}, Job: staleGuard}); !errors.Is(err, pg.ErrRebuildFenced) {
		t.Fatalf("stale runner published: %v", err)
	}
	if err := old.Complete(ctx, work.Offset, true); !errors.Is(err, pg.ErrRebuildFenced) {
		t.Fatalf("stale runner advanced progress: %v", err)
	}
	if _, _, err := old.Next(ctx); !errors.Is(err, pg.ErrRebuildFenced) {
		t.Fatalf("stale runner reserved work: %v", err)
	}
	if _, err := f.facts.ApplyGeneration(ctx, f.schema, f.snapshot, uuid.NewString(), f.version, actor, pg.GenerationPlan{Claims: []domain.Claim{f.claim}, Job: current.Guard(work.Offset)}); !errors.Is(err, pg.ErrRebuildFenced) {
		t.Fatalf("job accepted a different source operation key: %v", err)
	}
	if _, err := f.facts.ApplyGeneration(ctx, f.schema, f.snapshot, work.Key, f.version, actor, pg.GenerationPlan{Claims: []domain.Claim{f.claim}, Job: current.Guard(work.Offset)}); err != nil {
		t.Fatal(err)
	}
	if err := current.Complete(ctx, work.Offset, true); err != nil {
		t.Fatal(err)
	}
	if err := current.Complete(ctx, work.Offset, true); err != nil {
		t.Fatal(err)
	}
	if err := current.Complete(ctx, work.Offset+1, true); !errors.Is(err, pg.ErrRebuildFenced) {
		t.Fatalf("unreserved progress accepted: %v", err)
	}
	if _, found, err := current.Next(ctx); err != nil || found {
		t.Fatalf("finished job has more work: found=%v err=%v", found, err)
	}
	state, err := jobs.Status(ctx, f.schema, "p1", key)
	if err != nil || state.Rebuilt != 1 || state.Status != "completed" {
		t.Fatalf("replayed acknowledgement changed progress: %+v %v", state, err)
	}
}

func TestProjectRebuildCancellationSettlesAnOrphanedCheckpoint(t *testing.T) {
	for _, published := range []bool{false, true} {
		name := "job_cancel_orphan"
		if published {
			name = "job_cancel_published"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			f := generationSource(t, name)
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
			if published {
				if _, err := f.facts.ApplyGeneration(ctx, f.schema, f.snapshot, work.Key, f.version, actor, pg.GenerationPlan{Claims: []domain.Claim{f.claim}, Job: session.Guard(work.Offset)}); err != nil {
					t.Fatal(err)
				}
			}
			requested, err := jobs.Cancel(ctx, f.schema, "p1", key, actor)
			if err != nil || requested.Status != "active" || !requested.CancelRequested {
				t.Fatalf("active source was discarded: %+v %v", requested, err)
			}
			session.Close()
			settled, err := jobs.Cancel(ctx, f.schema, "p1", key, actor)
			if err != nil || settled.Status != "cancelled" || settled.Pending != nil || settled.After != work.Offset {
				t.Fatalf("orphaned cancellation requires a model: %+v %v", settled, err)
			}
			if published && (settled.Rebuilt != 1 || settled.Skipped != 0) {
				t.Fatalf("committed source was not reconciled: %+v", settled)
			}
			if !published && (settled.Rebuilt != 0 || settled.Skipped != 1) {
				t.Fatalf("unpublished source was not skipped: %+v", settled)
			}
			if _, err := f.facts.ApplyGeneration(ctx, f.schema, f.snapshot, work.Key, f.version, actor, pg.GenerationPlan{Claims: []domain.Claim{f.claim}, Job: session.Guard(work.Offset)}); !errors.Is(err, pg.ErrRebuildFenced) {
				t.Fatalf("orphaned runner can publish after cancellation: %v", err)
			}
		})
	}
}

func TestProjectRebuildRefusesAConnectionPoolThatWouldDeadlock(t *testing.T) {
	ctx := context.Background()
	f := generationSource(t, "job_pool_bound")
	config := f.pool.Config()
	config.MaxConns = 1
	config.MinConns = 0
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pg.NewRebuildJobStore(pool).Acquire(ctx, f.schema, "p1", uuid.NewString(), f.version, uuid.NewString()); !errors.Is(err, pg.ErrRebuildPoolCapacity) {
		t.Fatalf("one-connection pool was accepted: %v", err)
	}
}

func TestProjectRebuildControlWritesRequireTheirAudit(t *testing.T) {
	ctx := context.Background()
	f := generationSource(t, "job_control_audit")
	jobs := pg.NewRebuildJobStore(f.pool)
	key, actor := uuid.NewString(), uuid.NewString()
	if _, err := f.pool.Exec(ctx, f.schema.SQL(`CREATE FUNCTION {schema}.refuse_job_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'job audit unavailable'; END $$;
        CREATE TRIGGER refuse_job_audit BEFORE INSERT ON {schema}.audit_entry FOR EACH ROW WHEN (NEW.operation IN ('formation.rebuild.start','formation.rebuild.cancel')) EXECUTE FUNCTION {schema}.refuse_job_audit()`)); err != nil {
		t.Fatal(err)
	}
	if session, err := jobs.Acquire(ctx, f.schema, "p1", key, f.version, actor); err == nil {
		session.Close()
		t.Fatal("job creation ignored failed audit")
	}
	if _, err := jobs.Status(ctx, f.schema, "p1", key); !errors.Is(err, pg.ErrRebuildNotFound) {
		t.Fatalf("failed creation left a job: %v", err)
	}
	if _, err := f.pool.Exec(ctx, f.schema.SQL(`ALTER TABLE {schema}.audit_entry DISABLE TRIGGER refuse_job_audit`)); err != nil {
		t.Fatal(err)
	}
	session, err := jobs.Acquire(ctx, f.schema, "p1", key, f.version, actor)
	if err != nil {
		t.Fatal(err)
	}
	session.Close()
	session.Close()
	if _, err := f.pool.Exec(ctx, f.schema.SQL(`ALTER TABLE {schema}.audit_entry ENABLE TRIGGER refuse_job_audit`)); err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.Cancel(ctx, f.schema, "p1", key, actor); err == nil {
		t.Fatal("cancellation ignored failed audit")
	}
	state, err := jobs.Status(ctx, f.schema, "p1", key)
	if err != nil || state.CancelRequested || state.Status != "active" {
		t.Fatalf("failed audit changed cancellation: %+v %v", state, err)
	}
	if _, err := f.pool.Exec(ctx, f.schema.SQL(`DROP TRIGGER refuse_job_audit ON {schema}.audit_entry`)); err != nil {
		t.Fatal(err)
	}
	state, err = jobs.Cancel(ctx, f.schema, "p1", key, actor)
	if err != nil || state.Status != "cancelled" {
		t.Fatalf("idle cancellation did not complete: %+v %v", state, err)
	}
	if _, err := jobs.Cancel(ctx, f.schema, "p1", key, actor); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := f.pool.QueryRow(ctx, f.schema.SQL(`SELECT count(*) FROM {schema}.audit_entry WHERE operation='formation.rebuild.cancel'`)).Scan(&count); err != nil || count != 1 {
		t.Fatalf("cancellation replay duplicated audit: %d %v", count, err)
	}
}

func TestProjectRebuildMetadataIsProjectScopedAndReferencesAreValidated(t *testing.T) {
	ctx := context.Background()
	f := generationSource(t, "job_validation")
	jobs := pg.NewRebuildJobStore(f.pool)
	key, actor := uuid.NewString(), uuid.NewString()
	for _, scope := range []string{"", "invalid project"} {
		if _, err := jobs.Status(ctx, f.schema, scope, key); !errors.Is(err, pg.ErrInvalidGeneration) {
			t.Fatal(err)
		}
	}
	for _, bad := range []string{"bad", uuid.Nil.String()} {
		if _, err := jobs.Status(ctx, f.schema, "p1", bad); !errors.Is(err, pg.ErrInvalidGeneration) {
			t.Fatalf("invalid status key accepted: %v", err)
		}
		if _, err := jobs.Cancel(ctx, f.schema, "p1", bad, actor); !errors.Is(err, pg.ErrInvalidGeneration) {
			t.Fatalf("invalid cancel key accepted: %v", err)
		}
		if _, err := jobs.Cancel(ctx, f.schema, "p1", key, bad); !errors.Is(err, pg.ErrInvalidGeneration) {
			t.Fatalf("invalid actor accepted: %v", err)
		}
	}
	if _, err := jobs.Acquire(ctx, f.schema, "p1", key, "bad-version", actor); !errors.Is(err, pg.ErrInvalidExtractionVersion) {
		t.Fatal(err)
	}
	if _, err := jobs.Cancel(ctx, f.schema, "p1", key, actor); !errors.Is(err, pg.ErrRebuildNotFound) {
		t.Fatal(err)
	}
	session, err := jobs.Acquire(ctx, f.schema, "p1", key, f.version, actor)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if _, err := jobs.Status(ctx, f.schema, "p2", key); !errors.Is(err, pg.ErrRebuildNotFound) {
		t.Fatalf("job metadata crossed projects: %v", err)
	}
}
