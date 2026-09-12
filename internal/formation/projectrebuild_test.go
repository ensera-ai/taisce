// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package formation_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/extract"
	"github.com/ensera-ai/taisce/internal/formation"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type projectRebuildFixture struct {
	pool                *pgxpool.Pool
	schema              pg.Schema
	jobs                *pg.RebuildJobStore
	model               *interruptedRebuildModel
	runner              *formation.ProjectRebuilder
	version, key, actor string
}

func newProjectRebuildFixture(t *testing.T, name string) projectRebuildFixture {
	t.Helper()
	p := testPool(t)
	schema := tenant(t, p, name)
	vocabulary, err := pg.LoadOntology(context.Background(), p, schema)
	if err != nil {
		t.Fatal(err)
	}
	model := &interruptedRebuildModel{}
	extractor := extract.New(model, vocabulary)
	jobs := pg.NewRebuildJobStore(p)
	return projectRebuildFixture{p, schema, jobs, model, formation.NewProjectRebuilder(formation.NewRebuilder(pg.NewFactStore(p), extractor), jobs), extractor.PipelineVersion(), uuid.NewString(), uuid.NewString()}
}

func (f projectRebuildFixture) append(t *testing.T) string {
	t.Helper()
	stored, err := pg.NewObservationStore(f.pool).Append(context.Background(), f.schema, domain.Turn{Scope: "p1", DataSubjectID: "subject-1", OccurredAt: time.Now().UTC(), Messages: []domain.Message{{Role: domain.RoleUser, Content: "I work at Ensera and commutes by bicycle."}}})
	if err != nil {
		t.Fatal(err)
	}
	return stored.ID
}

func TestProjectRebuildResumesPagesWithinItsCapturedLogBoundary(t *testing.T) {
	ctx := context.Background()
	f := newProjectRebuildFixture(t, "project_rebuild_pages")
	f.append(t)
	f.append(t)
	first, err := f.runner.RunPage(ctx, f.schema, "p1", f.key, f.actor, 1)
	if err != nil || first.Rebuilt != 1 || first.After != 0 || first.Through != 1 || first.Status != "active" {
		t.Fatalf("first page: %+v %v", first, err)
	}
	later := f.append(t)
	second, err := f.runner.RunPage(ctx, f.schema, "p1", f.key, f.actor, 10)
	if err != nil || second.Rebuilt != 2 || second.After != 1 || second.Status != "completed" || f.model.calls != 2 {
		t.Fatalf("resumed page crossed its boundary: %+v calls=%d err=%v", second, f.model.calls, err)
	}
	var laterGenerations int
	if err := f.pool.QueryRow(ctx, f.schema.SQL(`SELECT count(*) FROM {schema}.fact_generation WHERE source_observation_id=$1`), later).Scan(&laterGenerations); err != nil || laterGenerations != 0 {
		t.Fatalf("late append was rebuilt: %d %v", laterGenerations, err)
	}
	retry, err := f.runner.RunPage(ctx, f.schema, "p1", f.key, f.actor, 10)
	if err != nil || retry.Rebuilt != 2 || f.model.calls != 2 {
		t.Fatalf("completed operation repeated inference: %+v %v", retry, err)
	}
	for _, limit := range []int{0, formation.MaxRebuildPage + 1} {
		if _, err := f.runner.RunPage(ctx, f.schema, "p1", uuid.NewString(), f.actor, limit); !errors.Is(err, pg.ErrGenerationLimit) {
			t.Fatalf("invalid page bound accepted: %v", err)
		}
	}
}

func TestProjectRebuildRetriesPublicationAfterProgressFailureWithoutInference(t *testing.T) {
	ctx := context.Background()
	f := newProjectRebuildFixture(t, "project_rebuild_checkpoint")
	f.append(t)
	if _, err := f.pool.Exec(ctx, f.schema.SQL(`CREATE FUNCTION {schema}.refuse_checkpoint() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'checkpoint interrupted'; END $$;
        CREATE TRIGGER refuse_checkpoint BEFORE UPDATE ON {schema}.fact_rebuild_job FOR EACH ROW WHEN (OLD.pending_offset IS NOT NULL AND NEW.pending_offset IS NULL) EXECUTE FUNCTION {schema}.refuse_checkpoint()`)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.runner.RunPage(ctx, f.schema, "p1", f.key, f.actor, 10); err == nil {
		t.Fatal("checkpoint failure was ignored")
	}
	state, err := f.jobs.Status(ctx, f.schema, "p1", f.key)
	if err != nil || state.Pending == nil || state.Rebuilt != 0 || f.model.calls != 1 {
		t.Fatalf("lost the interrupted source: %+v %v", state, err)
	}
	if _, err := f.pool.Exec(ctx, f.schema.SQL(`DROP TRIGGER refuse_checkpoint ON {schema}.fact_rebuild_job`)); err != nil {
		t.Fatal(err)
	}
	resumed, err := f.runner.RunPage(ctx, f.schema, "p1", f.key, f.actor, 10)
	if err != nil || resumed.Status != "completed" || resumed.Rebuilt != 1 || f.model.calls != 1 {
		t.Fatalf("resume repeated inference: %+v calls=%d err=%v", resumed, f.model.calls, err)
	}
}

func TestProjectRebuildCancellationLetsAdmittedSourceFinishAndStopsTheNext(t *testing.T) {
	ctx := context.Background()
	f := newProjectRebuildFixture(t, "project_rebuild_cancel")
	f.append(t)
	f.append(t)
	f.model.during = func() {
		state, err := f.jobs.Cancel(ctx, f.schema, "p1", f.key, f.actor)
		if err != nil || !state.CancelRequested || state.Pending == nil {
			t.Fatalf("cancellation lost the admitted source: %+v %v", state, err)
		}
	}
	result, err := f.runner.RunPage(ctx, f.schema, "p1", f.key, f.actor, 10)
	if err != nil || result.Status != "cancelled" || result.Rebuilt != 1 || f.model.calls != 1 {
		t.Fatalf("cancellation admitted another source: %+v calls=%d err=%v", result, f.model.calls, err)
	}
	if _, err := f.runner.RunPage(ctx, f.schema, "p1", f.key, f.actor, 10); err != nil || f.model.calls != 1 {
		t.Fatalf("cancelled operation resumed inference: %v", err)
	}
}

func TestProjectRebuildCancelledPendingSourceDoesNotStartInferenceOnResume(t *testing.T) {
	ctx := context.Background()
	f := newProjectRebuildFixture(t, "project_rebuild_cancel_pending")
	f.append(t)
	session, err := f.jobs.Acquire(ctx, f.schema, "p1", f.key, f.version, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := session.Next(ctx); err != nil || !found {
		t.Fatalf("reserve source: %v", err)
	}
	if _, err := f.jobs.Cancel(ctx, f.schema, "p1", f.key, f.actor); err != nil {
		t.Fatal(err)
	}
	session.Close()
	result, err := f.runner.RunPage(ctx, f.schema, "p1", f.key, f.actor, 10)
	if err != nil || result.Status != "cancelled" || result.Rebuilt != 0 || result.Skipped != 1 || f.model.calls != 0 {
		t.Fatalf("cancelled pending source reached inference: %+v calls=%d err=%v", result, f.model.calls, err)
	}
}

func TestProjectRebuildSkipsSourcesErasedDuringModelWork(t *testing.T) {
	ctx := context.Background()
	f := newProjectRebuildFixture(t, "project_rebuild_erasure")
	f.append(t)
	f.append(t)
	f.model.during = func() {
		if erased, err := pg.NewEraser(f.pool).Erase(ctx, f.schema, "p1", "subject-1", "requested"); err != nil || !erased.Clean() {
			t.Fatalf("erasure failed: %v", err)
		}
	}
	result, err := f.runner.RunPage(ctx, f.schema, "p1", f.key, f.actor, 10)
	if err != nil || result.Status != "completed" || result.Rebuilt != 0 || result.Skipped != 1 || f.model.calls != 1 {
		t.Fatalf("erasure stranded or revived the source: %+v %v", result, err)
	}
	var count int
	if err := f.pool.QueryRow(ctx, f.schema.SQL(`SELECT count(*) FROM {schema}.fact_generation`)).Scan(&count); err != nil || count != 0 {
		t.Fatalf("erased source was published: %d %v", count, err)
	}
}

func TestProjectRebuildRejectsOtherRunnersAndChangedConfiguration(t *testing.T) {
	ctx := context.Background()
	f := newProjectRebuildFixture(t, "project_rebuild_configuration")
	f.append(t)
	session, err := f.jobs.Acquire(ctx, f.schema, "p1", f.key, f.version, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.runner.RunPage(ctx, f.schema, "p1", f.key, f.actor, 1); !errors.Is(err, pg.ErrRebuildBusy) {
		t.Fatalf("concurrent runner admitted: %v", err)
	}
	session.Close()
	if other, err := f.jobs.Acquire(ctx, f.schema, "p1", uuid.NewString(), f.version, f.actor); !errors.Is(err, pg.ErrRebuildBusy) {
		if other != nil {
			other.Close()
		}
		t.Fatalf("another active operation admitted: %v", err)
	}
	vocabulary, err := pg.LoadOntology(ctx, f.pool, f.schema)
	if err != nil {
		t.Fatal(err)
	}
	changed := formation.NewProjectRebuilder(formation.NewRebuilder(pg.NewFactStore(f.pool), extract.New(rebuildFailModel{}, vocabulary)), f.jobs)
	if _, err := changed.RunPage(ctx, f.schema, "p1", f.key, f.actor, 1); !errors.Is(err, pg.ErrGenerationConflict) {
		t.Fatalf("configuration changed within an operation: %v", err)
	}
	if f.model.calls != 0 {
		t.Fatal("refused operation spent inference")
	}
}

func TestProjectRebuildCompletesAnEmptyProjectWithoutInference(t *testing.T) {
	ctx := context.Background()
	f := newProjectRebuildFixture(t, "project_rebuild_empty")
	result, err := f.runner.RunPage(ctx, f.schema, "p1", f.key, f.actor, 10)
	if err != nil || result.Status != "completed" || result.Through != -1 || result.Rebuilt != 0 || f.model.calls != 0 {
		t.Fatalf("empty project did not complete: %+v %v", result, err)
	}
}

func TestProjectRebuildErasedPendingSourceSkipsWithoutModel(t *testing.T) {
	ctx := context.Background()
	f := newProjectRebuildFixture(t, "project_rebuild_erased_pending")
	f.append(t)
	session, err := f.jobs.Acquire(ctx, f.schema, "p1", f.key, f.version, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := session.Next(ctx); err != nil || !found {
		t.Fatalf("reserve: %v", err)
	}
	session.Close()
	if _, err := pg.NewEraser(f.pool).Erase(ctx, f.schema, "p1", "subject-1", "requested"); err != nil {
		t.Fatal(err)
	}
	result, err := f.runner.RunPage(ctx, f.schema, "p1", f.key, f.actor, 10)
	if err != nil || result.Status != "completed" || result.Skipped != 1 || f.model.calls != 0 {
		t.Fatalf("erased checkpoint was not skipped: %+v calls=%d err=%v", result, f.model.calls, err)
	}
}

func TestProjectRebuildNeverSendsAuthoritativeSourcesToInference(t *testing.T) {
	ctx := context.Background()
	f := newProjectRebuildFixture(t, "project_rebuild_authoritative")
	if _, err := pg.NewRecordStore(f.pool, f.schema).AssertRecords(ctx, "p1", f.actor, []pg.RecordAssertion{{IdempotencyKey: uuid.NewString(), DataSubjectID: "subject-1", Subject: "I", Predicate: "works_at", Object: "Ensera", Statement: "I work at Ensera"}}); err != nil {
		t.Fatal(err)
	}
	f.append(t)
	result, err := f.runner.RunPage(ctx, f.schema, "p1", f.key, f.actor, 10)
	if err != nil || result.Status != "completed" || result.Rebuilt != 1 || f.model.calls != 1 {
		t.Fatalf("authoritative source was reinterpreted: %+v calls=%d err=%v", result, f.model.calls, err)
	}
}

func TestProjectRebuildRequiresDurableAdmissionBeforeInference(t *testing.T) {
	ctx := context.Background()
	f := newProjectRebuildFixture(t, "project_rebuild_admission_failure")
	f.append(t)
	if _, err := f.pool.Exec(ctx, f.schema.SQL(`CREATE FUNCTION {schema}.refuse_reservation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'reservation unavailable'; END $$;
        CREATE TRIGGER refuse_reservation BEFORE UPDATE ON {schema}.fact_rebuild_job FOR EACH ROW WHEN (OLD.pending_offset IS NULL AND NEW.pending_offset IS NOT NULL) EXECUTE FUNCTION {schema}.refuse_reservation()`)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.runner.RunPage(ctx, f.schema, "p1", f.key, f.actor, 10); err == nil || f.model.calls != 0 {
		t.Fatalf("uncommitted reservation reached inference: calls=%d err=%v", f.model.calls, err)
	}
	state, err := f.jobs.Status(ctx, f.schema, "p1", f.key)
	if err != nil || state.Pending != nil || state.Rebuilt != 0 || state.After != -1 {
		t.Fatalf("failed admission advanced progress: %+v %v", state, err)
	}
}

func TestProjectRebuildLeavesFailedInferencePendingForRetry(t *testing.T) {
	ctx := context.Background()
	f := newProjectRebuildFixture(t, "project_rebuild_failed_inference")
	f.append(t)
	vocabulary, err := pg.LoadOntology(ctx, f.pool, f.schema)
	if err != nil {
		t.Fatal(err)
	}
	runner := formation.NewProjectRebuilder(formation.NewRebuilder(pg.NewFactStore(f.pool), extract.New(rebuildFailModel{}, vocabulary)), f.jobs)
	if _, err := runner.RunPage(ctx, f.schema, "p1", f.key, f.actor, 10); !errors.Is(err, formation.ErrGenerationInference) {
		t.Fatalf("failed inference was not surfaced: %v", err)
	}
	state, err := f.jobs.Status(ctx, f.schema, "p1", f.key)
	if err != nil || state.Pending == nil || state.Rebuilt != 0 || state.After != -1 {
		t.Fatalf("failed inference advanced progress: %+v %v", state, err)
	}
}
