// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"errors"
	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
	"testing"
	"time"
)

func TestSubjectRetentionKeepsMappingsUntilAllAttributedSourcesExpire(t *testing.T) {
	ctx := context.Background()
	pool, schema, store := subjectFixture(t, "subject_retention")
	actor := uuid.NewString()
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.project SET retention=interval '1 microsecond' WHERE scope='p1'`)); err != nil {
		t.Fatal(err)
	}
	unusedRequest := subjectRequest()
	unused, err := store.Register(ctx, "p1", actor, unusedRequest)
	if err != nil {
		t.Fatal(err)
	}
	usedRequest := subjectRequest()
	usedRequest.ExternalReference = "used"
	used, err := store.Register(ctx, "p1", actor, usedRequest)
	if err != nil {
		t.Fatal(err)
	}
	if used.InactiveAfter == nil {
		t.Fatal("registry ignored project retention")
	}
	// Changing policy never extends the registry's stamped deadline. A later source can outlive it.
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.project SET retention=NULL WHERE scope='p1'`)); err != nil {
		t.Fatal(err)
	}
	request := artifactRequest()
	request.DataSubjectID = used.ID
	artifact, err := pg.NewArtifactStore(pool, schema).Put(ctx, "p1", actor, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, "p1", unused.ID, ""); !errors.Is(err, pg.ErrSubjectNotFound) {
		t.Fatal("inactive unused mapping remained readable")
	}
	if _, err := store.Get(ctx, "p1", used.ID, ""); err != nil {
		t.Fatal("mapping expired ahead of attributed source")
	}
	page, err := store.List(ctx, "p1", "", 20)
	if err != nil || len(page.Subjects) != 1 || page.Subjects[0].ID != used.ID {
		t.Fatal("inventory does not apply source-aware lifetime")
	}
	if _, err := store.Update(ctx, "p1", actor, pg.SubjectUpdate{ID: unused.ID, ExpectedVersion: unused.Version}); !errors.Is(err, pg.ErrSubjectNotFound) {
		t.Fatal("unused expired entry accepted update")
	}
	if _, err := store.Register(ctx, "p1", actor, unusedRequest); !errors.Is(err, pg.ErrSubjectConflict) {
		t.Fatal("retry revived expired mapping")
	}
	scopes, err := pg.NewObservationStore(pool).ScopesWithRetention(ctx, schema)
	if err != nil || len(scopes) != 1 || scopes[0] != "p1" {
		t.Fatal("registry-only expiry was not scheduled")
	}
	sweep, err := pg.NewRetentionStore(pool, schema).Sweep(ctx, "p1", 1)
	if err != nil || sweep.Observations != 0 || sweep.Deleted["data_subject"] != 1 || sweep.Empty() {
		t.Fatalf("registry-only expiry: %+v %v", sweep, err)
	}
	if _, err := store.Register(ctx, "p1", actor, unusedRequest); !errors.Is(err, pg.ErrSubjectConflict) {
		t.Fatal("expired tombstone allowed replay")
	}
	scopes, err = pg.NewObservationStore(pool).ScopesWithRetention(ctx, schema)
	if err != nil || len(scopes) != 0 {
		t.Fatal("retained attributed source was scheduled for premature registry expiry")
	}
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.observation SET retention_until=now()-interval '1 second' WHERE observation_id=$1::uuid`), artifact.SourceObservationID); err != nil {
		t.Fatal(err)
	}
	sweep, err = pg.NewRetentionStore(pool, schema).Sweep(ctx, "p1", 100)
	if err != nil || sweep.Deleted["data_subject"] != 1 || sweep.Deleted["agent_artifact"] != 1 {
		t.Fatalf("source expiry left mapping: %+v %v", sweep, err)
	}
	if _, err := store.Get(ctx, "p1", used.ID, ""); !errors.Is(err, pg.ErrSubjectNotFound) {
		t.Fatal("mapping outlived final expired source")
	}
	// Indefinite identity bookkeeping is a policy too, and remains until explicit erasure.
	indefinite, err := store.Register(ctx, "p1", actor, subjectRequest())
	if err != nil {
		t.Fatal(err)
	}
	if indefinite.InactiveAfter != nil {
		t.Fatal("indefinite policy acquired a deadline")
	}
	if _, err := pg.NewRetentionStore(pool, schema).Sweep(ctx, "p1", 100); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, "p1", indefinite.ID, ""); err != nil {
		t.Fatal("indefinite identity expired")
	}
}

func TestSubjectExpiryCannotRemoveAnInFlightSourcesMapping(t *testing.T) {
	ctx := context.Background()
	pool, schema, store := subjectFixture(t, "subject_expiry_lock")
	actor := uuid.NewString()
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.project SET retention=interval '1 microsecond' WHERE scope='p1'`)); err != nil {
		t.Fatal(err)
	}
	first, err := store.Register(ctx, "p1", actor, subjectRequest())
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, schema.SQL(`INSERT INTO {schema}.observation(observation_id,scope,log_offset,kind,occurred_at,source_role,data_subject_id) VALUES($1::uuid,'p1',100,'turn',now(),'user',$2)`), uuid.NewString(), first.ID); err != nil {
		t.Fatal(err)
	}
	sweep, err := pg.NewRetentionStore(pool, schema).Sweep(ctx, "p1", 100)
	if err != nil || !sweep.Empty() {
		t.Fatal("expiry deleted an identity locked by source acceptance")
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	sweep, err = pg.NewRetentionStore(pool, schema).Sweep(ctx, "p1", 100)
	if err != nil || !sweep.Empty() {
		t.Fatal("expiry ignored newly committed source")
	}
	if _, err := store.Get(ctx, "p1", first.ID, ""); err != nil {
		t.Fatal("accepted source lost mapping")
	}
}

func TestSubjectExpiryIsBoundedAndPreservesFutureDeadlines(t *testing.T) {
	ctx := context.Background()
	pool, schema, store := subjectFixture(t, "subject_expiry_page")
	actor := uuid.NewString()
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.project SET retention=interval '1 microsecond' WHERE scope='p1'`)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		r := subjectRequest()
		r.ExternalReference = ""
		if _, err := store.Register(ctx, "p1", actor, r); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.project SET retention=interval '1 hour' WHERE scope='p1'`)); err != nil {
		t.Fatal(err)
	}
	future, err := store.Register(ctx, "p1", actor, subjectRequest())
	if err != nil {
		t.Fatal(err)
	}
	sweep, err := pg.NewRetentionStore(pool, schema).Sweep(ctx, "p1", 2)
	if err != nil || sweep.Deleted["data_subject"] != 2 {
		t.Fatal("registry expiry exceeded or ignored its page limit")
	}
	sweep, err = pg.NewRetentionStore(pool, schema).Sweep(ctx, "p1", 2)
	if err != nil || sweep.Deleted["data_subject"] != 1 {
		t.Fatal("registry expiry skipped remainder")
	}
	if _, err := store.Get(ctx, "p1", future.ID, ""); err != nil {
		t.Fatal("future mapping expired early")
	}
}

func TestErasureRemovesRegisteredAndApplicationManagedSourceAttribution(t *testing.T) {
	ctx := context.Background()
	pool, schema, store := subjectFixture(t, "subject_source_erasure")
	actor := uuid.NewString()
	first, err := store.Register(ctx, "p1", actor, subjectRequest())
	if err != nil {
		t.Fatal(err)
	}
	turn := turnOf(domain.Message{Role: domain.RoleUser, Content: "I live in Dublin."})
	turn.DataSubjectID = first.ID
	turn.OccurredAt = time.Now().UTC()
	if _, err := pg.NewObservationStore(pool).Append(ctx, schema, turn); err != nil {
		t.Fatal(err)
	}
	result, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", first.ID, "requested")
	if err != nil || result.Deleted["data_subject"] != 1 || result.Deleted["observation"] != 1 {
		t.Fatal("erasure did not cover source and identity together")
	}
	turn.DataSubjectID = "caller-managed-opaque-reference"
	if _, err := pg.NewObservationStore(pool).Append(ctx, schema, turn); err != nil {
		t.Fatal("optional registry became mandatory for application-managed IDs")
	}
	result, err = pg.NewEraser(pool).Erase(ctx, schema, "p1", turn.DataSubjectID, "requested")
	if err != nil || result.Deleted["data_subject"] != 0 || result.Deleted["observation"] != 1 {
		t.Fatal("unregistered attribution no longer erasable")
	}
}
