// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"errors"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"testing"
)

func TestSubjectStorageFailuresRollbackIdentityRetryAndAudit(t *testing.T) {
	ctx := context.Background()
	for _, fail := range []struct{ name, ddl string }{
		{"lookup", `ALTER TABLE {schema}.subject_retry RENAME TO unavailable_retries`},
		{"subject", `ALTER TABLE {schema}.data_subject ADD CONSTRAINT injected_failure CHECK(false)`},
		{"retry", `ALTER TABLE {schema}.subject_retry ADD CONSTRAINT injected_failure CHECK(false)`},
		{"audit", `ALTER TABLE {schema}.audit_entry ADD CONSTRAINT injected_failure CHECK(operation<>'subject.register')`},
	} {
		t.Run(fail.name, func(t *testing.T) {
			pool, schema, store := subjectFixture(t, "subject_fail_"+fail.name)
			if _, err := pool.Exec(ctx, schema.SQL(fail.ddl)); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Register(ctx, "p1", uuid.NewString(), subjectRequest()); err == nil {
				t.Fatal("failed registration reported success")
			}
			if fail.name == "lookup" {
				if _, err := pool.Exec(ctx, schema.SQL(`ALTER TABLE {schema}.unavailable_retries RENAME TO subject_retry`)); err != nil {
					t.Fatal(err)
				}
			}
			for _, table := range []string{"data_subject", "subject_retry", "audit_entry"} {
				var n int
				if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.`+table)).Scan(&n); err != nil || n != 0 {
					t.Fatal("failed registration retained " + table)
				}
			}
		})
	}
}

func TestSubjectUpdateAndGovernanceFailuresPreserveAcceptedState(t *testing.T) {
	ctx := context.Background()
	pool, schema, store := subjectFixture(t, "subject_governance_failure")
	actor := uuid.NewString()
	r := subjectRequest()
	first, err := store.Register(ctx, "p1", actor, r)
	if err != nil {
		t.Fatal(err)
	}
	artifactRequest := artifactRequest()
	artifactRequest.DataSubjectID = first.ID
	artifact, err := pg.NewArtifactStore(pool, schema).Put(ctx, "p1", actor, artifactRequest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`ALTER TABLE {schema}.audit_entry ADD CONSTRAINT injected_audit_failure CHECK(operation<>'subject.update') NOT VALID`)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Update(ctx, "p1", actor, pg.SubjectUpdate{ID: first.ID, ExpectedVersion: first.Version, SubjectFields: pg.SubjectFields{ExternalReference: "changed"}}); err == nil {
		t.Fatal("failed update reported success")
	}
	got, err := store.Get(ctx, "p1", first.ID, "")
	if err != nil || got.Version != first.Version || got.ExternalReference != first.ExternalReference {
		t.Fatal("failed update changed reference")
	}
	if _, err := pool.Exec(ctx, schema.SQL(`ALTER TABLE {schema}.subject_retry ADD CONSTRAINT injected_forget_failure CHECK(subject_id IS NOT NULL)`)); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", first.ID, "requested"); err == nil {
		t.Fatal("incomplete retry erasure reported success")
	}
	if _, err := pg.NewArtifactStore(pool, schema).Get(ctx, "p1", artifact.ID, ""); err != nil {
		t.Fatal("failed erasure partially removed source storage")
	}
	var requests int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.erasure_request`)).Scan(&requests); err != nil || requests != 0 {
		t.Fatal("failed erasure left a success receipt")
	}
	if _, err := pool.Exec(ctx, schema.SQL(`ALTER TABLE {schema}.subject_retry DROP CONSTRAINT injected_forget_failure`)); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", first.ID, "retry"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Register(ctx, "p1", actor, r); !errors.Is(err, pg.ErrSubjectConflict) {
		t.Fatal("erased retry became reusable")
	}
}

func TestSubjectRetentionFailureRollsBackExpiredSourcesAndMappingTogether(t *testing.T) {
	ctx := context.Background()
	pool, schema, store := subjectFixture(t, "subject_expiry_failure")
	actor := uuid.NewString()
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.project SET retention=interval '1 microsecond' WHERE scope='p1'`)); err != nil {
		t.Fatal(err)
	}
	first, err := store.Register(ctx, "p1", actor, subjectRequest())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.project SET retention=NULL WHERE scope='p1'`)); err != nil {
		t.Fatal(err)
	}
	request := artifactRequest()
	request.DataSubjectID = first.ID
	artifact, err := pg.NewArtifactStore(pool, schema).Put(ctx, "p1", actor, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.observation SET retention_until=now()-interval '1 second' WHERE observation_id=$1::uuid;`), artifact.SourceObservationID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`ALTER TABLE {schema}.subject_retry ADD CONSTRAINT injected_failure CHECK(subject_id IS NOT NULL)`)); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.NewRetentionStore(pool, schema).Sweep(ctx, "p1", 100); err == nil {
		t.Fatal("incomplete expiry reported success")
	}
	var sources, subjects, artifacts int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT (SELECT count(*) FROM {schema}.observation),(SELECT count(*) FROM {schema}.data_subject),(SELECT count(*) FROM {schema}.agent_artifact)`)).Scan(&sources, &subjects, &artifacts); err != nil || sources != 1 || subjects != 1 || artifacts != 1 {
		t.Fatal("failed expiry partially removed governed bytes")
	}
	limits, err := pg.NewArtifactStore(pool, schema).Limits(ctx, "p1")
	if err != nil || limits.UsedObjects != 1 {
		t.Fatal("failed expiry released committed quota")
	}
	if _, err := pool.Exec(ctx, schema.SQL(`ALTER TABLE {schema}.subject_retry DROP CONSTRAINT injected_failure`)); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.NewRetentionStore(pool, schema).Sweep(ctx, "p1", 100); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeSubjectGovernancePreservesIdentityAndReachesSourceStorage(t *testing.T) {
	ctx := context.Background()
	owner, schema, _ := subjectFixture(t, "subject_runtime")
	config := owner.Config().Copy()
	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET ROLE "+pgx.Identifier{migrate.DataRole}.Sanitize())
		return err
	}
	runtime, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	store := pg.NewSubjectStore(runtime, schema)
	actor := uuid.NewString()
	r := subjectRequest()
	first, err := store.Register(ctx, "p1", actor, r)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{`scope='p2'`, `subject_id='` + uuid.NewString() + `'`, `created_at=created_at+interval '1 hour'`, `inactive_after=now()`} {
		if _, err := runtime.Exec(ctx, schema.SQL(`UPDATE {schema}.data_subject SET `+change+` WHERE scope='p1' AND subject_id=$1`), first.ID); err == nil {
			t.Fatal("runtime changed immutable subject identity or lifetime")
		}
	}
	updated, err := store.Update(ctx, "p1", actor, pg.SubjectUpdate{ID: first.ID, ExpectedVersion: first.Version, SubjectFields: pg.SubjectFields{Label: "Rotated"}})
	if err != nil {
		t.Fatal(err)
	}
	request := artifactRequest()
	request.DataSubjectID = first.ID
	if _, err := pg.NewArtifactStore(runtime, schema).Put(ctx, "p1", actor, request); err != nil {
		t.Fatal("runtime source could not lock subject")
	}
	exported, err := pg.NewExporter(runtime, schema).Export(ctx, schema, "p1", updated.ID)
	if err != nil || len(exported.Sections["data_subject"]) != 1 || len(exported.Sections["agent_artifact"]) != 1 {
		t.Fatal("runtime governance export incomplete")
	}
	if _, err := pg.NewEraser(runtime).Erase(ctx, schema, "p1", first.ID, "requested"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Register(ctx, "p1", actor, r); !errors.Is(err, pg.ErrSubjectConflict) {
		t.Fatal("runtime erasure failed to retain retry tombstone")
	}
}
