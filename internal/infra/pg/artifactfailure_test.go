// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
)

func TestArtifactInvalidInputsAreRefusedBeforeStorage(t *testing.T) {
	ctx := context.Background()
	pool, schema, store := artifactFixture(t, "artifact_invalid")
	for _, change := range []func(*pg.ArtifactPut){
		func(r *pg.ArtifactPut) { r.ID = "bad" }, func(r *pg.ArtifactPut) { r.ExpectedVersion = "bad" },
		func(r *pg.ArtifactPut) { r.DataSubjectID = "" }, func(r *pg.ArtifactPut) { r.DataSubjectID = "  " },
		func(r *pg.ArtifactPut) { r.DataSubjectID = "a\x00b" }, func(r *pg.ArtifactPut) { r.DataSubjectID = string([]byte{255}) },
		func(r *pg.ArtifactPut) { r.Name = strings.Repeat("a", 257) }, func(r *pg.ArtifactPut) { r.Name = "\x00" },
		func(r *pg.ArtifactPut) { r.Kind = "document" }, func(r *pg.ArtifactPut) { r.Content = nil }, func(r *pg.ArtifactPut) { r.Content = make([]byte, pg.MaxArtifactBytes+1) },
	} {
		r := artifactRequest()
		change(&r)
		if _, err := store.Put(ctx, "p1", uuid.NewString(), r); !errors.Is(err, pg.ErrInvalidArtifact) {
			t.Fatalf("invalid write accepted: %v", err)
		}
	}
	if _, err := store.Get(ctx, "p1", "bad", ""); !errors.Is(err, pg.ErrInvalidArtifact) {
		t.Fatal("invalid read accepted")
	}
	for _, pair := range [][2]string{{"bad", uuid.NewString()}, {uuid.NewString(), "bad"}} {
		if err := store.Delete(ctx, "p1", uuid.NewString(), pair[0], pair[1], ""); !errors.Is(err, pg.ErrInvalidArtifact) {
			t.Fatal("invalid delete accepted")
		}
	}
	var count int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.observation`)).Scan(&count); err != nil || count != 0 {
		t.Fatal("invalid requests wrote source data")
	}
	r := artifactRequest()
	r.Content = []byte{}
	if _, err := store.Put(ctx, "p1", uuid.NewString(), r); err != nil {
		t.Fatalf("empty byte sequence refused: %v", err)
	}
	r.ID = uuid.NewString()
	if _, err := store.Put(ctx, "missing", uuid.NewString(), r); !errors.Is(err, pg.ErrNoSuchProject) {
		t.Fatal("missing project accepted")
	}
}

func TestArtifactStorageFailuresRollbackPayloadRegistrationQuotaAndAudit(t *testing.T) {
	ctx := context.Background()
	for _, failure := range []struct{ name, ddl string }{
		{"source", `ALTER TABLE {schema}.observation ADD CONSTRAINT injected_failure CHECK(kind<>'artifact')`},
		{"artifact", `ALTER TABLE {schema}.agent_artifact ADD CONSTRAINT injected_failure CHECK(false)`},
		{"register", `ALTER TABLE {schema}.projection_dependency ADD CONSTRAINT injected_failure CHECK(projection_kind<>'agent_artifact')`},
		{"retry", `ALTER TABLE {schema}.observation_retry ADD CONSTRAINT injected_failure CHECK(false)`},
		{"audit", `ALTER TABLE {schema}.audit_entry ADD CONSTRAINT injected_failure CHECK(operation<>'artifact.put')`},
	} {
		t.Run(failure.name, func(t *testing.T) {
			pool, schema, store := artifactFixture(t, "artifact_fail_"+failure.name)
			if _, err := pool.Exec(ctx, schema.SQL(failure.ddl)); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Put(ctx, "p1", uuid.NewString(), artifactRequest()); err == nil {
				t.Fatal("failed storage reported success")
			}
			for _, table := range []string{"observation", "agent_artifact", "projection_dependency", "observation_retry", "watermark", "audit_entry"} {
				var n int
				if err := pool.QueryRow(ctx, schema.SQL("SELECT count(*) FROM {schema}."+table)).Scan(&n); err != nil || n != 0 {
					t.Fatalf("failed write retained %s: %d %v", table, n, err)
				}
			}
			limits, err := store.Limits(ctx, "p1")
			if err != nil || limits.UsedBytes != 0 || limits.UsedObjects != 0 {
				t.Fatal("failed write spent quota")
			}
		})
	}
}

func TestFailedArtifactOverwriteDeleteAndPolicyAuditPreserveAcceptedState(t *testing.T) {
	ctx := context.Background()
	pool, schema, store := artifactFixture(t, "artifact_mutation_rollback")
	actor := uuid.NewString()
	r := artifactRequest()
	first, err := store.Put(ctx, "p1", actor, r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`ALTER TABLE {schema}.audit_entry ADD CONSTRAINT injected_failure CHECK(operation NOT IN ('artifact.put','artifact.delete','artifact.configure')) NOT VALID`)); err != nil {
		t.Fatal(err)
	}
	r.ExpectedVersion = first.Version
	r.Content = []byte("replacement")
	if _, err := store.Put(ctx, "p1", actor, r); err == nil {
		t.Fatal("unaudited overwrite committed")
	}
	if err := store.Delete(ctx, "p1", actor, r.ID, first.Version, ""); err == nil {
		t.Fatal("unaudited delete committed")
	}
	limits, _ := store.Limits(ctx, "p1")
	changed := limits
	changed.MaxObjects = 1
	if err := store.ConfigureLimits(ctx, "p1", actor, changed); err == nil {
		t.Fatal("unaudited policy committed")
	}
	current, err := store.Get(ctx, "p1", r.ID, "")
	if err != nil || current.Version != first.Version || current.Bytes != first.Bytes {
		t.Fatal("failed mutation changed accepted bytes")
	}
	after, err := store.Limits(ctx, "p1")
	if err != nil || after != limits {
		t.Fatal("failed mutation changed quota or policy")
	}
	// Registration is a required reverse relationship, not a best-effort companion row.
	if _, err := pool.Exec(ctx, schema.SQL(`DELETE FROM {schema}.projection_dependency WHERE projection_kind='agent_artifact'`)); err == nil {
		t.Fatal("artifact survived without erasure registration")
	}
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.agent_artifact SET data_subject_id='other'`)); err == nil {
		t.Fatal("artifact ownership changed outside the API")
	}
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.projection_dependency SET data_subject_id='other' WHERE projection_kind='agent_artifact'`)); err == nil {
		t.Fatal("registration owner disagreed with bytes")
	}
	if _, err := pool.Exec(ctx, schema.SQL(`ALTER TABLE {schema}.audit_entry DROP CONSTRAINT injected_failure`)); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, "p1", actor, r.ID, first.Version, ""); err != nil {
		t.Fatal(err)
	}
}

func TestArtifactPolicyBoundsMissingProjectsAndDatabaseFailuresAreExplicit(t *testing.T) {
	ctx := context.Background()
	pool, schema, store := artifactFixture(t, "artifact_policy_failures")
	actor := uuid.NewString()
	good := pg.ArtifactLimits{MaxObjectBytes: 1, MaxBytes: 1, MaxObjects: 1, MaxAgeHours: 1}
	for _, change := range []func(*pg.ArtifactLimits){
		func(v *pg.ArtifactLimits) { v.MaxObjectBytes = 0 }, func(v *pg.ArtifactLimits) { v.MaxObjectBytes = pg.MaxArtifactBytes + 1 }, func(v *pg.ArtifactLimits) { v.MaxBytes = 0 }, func(v *pg.ArtifactLimits) { v.MaxBytes = 1<<30 + 1 },
		func(v *pg.ArtifactLimits) { v.MaxObjects = 0 }, func(v *pg.ArtifactLimits) { v.MaxObjects = 100001 }, func(v *pg.ArtifactLimits) { v.MaxAgeHours = 0 }, func(v *pg.ArtifactLimits) { v.MaxAgeHours = 8761 },
	} {
		v := good
		change(&v)
		if err := store.ConfigureLimits(ctx, "p1", actor, v); !errors.Is(err, pg.ErrInvalidArtifact) {
			t.Fatal("invalid policy accepted")
		}
	}
	if err := store.ConfigureLimits(ctx, "missing", actor, good); !errors.Is(err, pg.ErrNoSuchProject) {
		t.Fatal("missing policy updated")
	}
	if _, err := store.Limits(ctx, "missing"); !errors.Is(err, pg.ErrNoSuchProject) {
		t.Fatal("missing policy returned limits")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := store.Put(canceled, "p1", actor, artifactRequest()); err == nil {
		t.Fatal("canceled write succeeded")
	}
	if _, err := store.Get(canceled, "p1", uuid.NewString(), ""); err == nil {
		t.Fatal("canceled read succeeded")
	}
	if _, err := store.List(canceled, "p1", "", "", 1); err == nil {
		t.Fatal("canceled list succeeded")
	}
	if err := store.Delete(canceled, "p1", actor, uuid.NewString(), uuid.NewString(), ""); err == nil {
		t.Fatal("canceled deletion succeeded")
	}
	if err := store.ConfigureLimits(canceled, "p1", actor, good); err == nil {
		t.Fatal("canceled policy update succeeded")
	}
	if _, err := pool.Exec(ctx, schema.SQL(`DROP TABLE {schema}.agent_storage_policy CASCADE`)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Limits(ctx, "p1"); err == nil {
		t.Fatal("missing storage table hidden")
	}
	if err := store.ConfigureLimits(ctx, "p1", actor, good); err == nil {
		t.Fatal("missing storage table updated")
	}
}
