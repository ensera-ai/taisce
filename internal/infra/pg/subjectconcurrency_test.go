// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package pg_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
)

func TestConcurrentSubjectReferencesCannotBeClaimedByTwoIdentities(t *testing.T) {
	ctx := context.Background()
	_, _, store := subjectFixture(t, "subject_reference_race")
	actor := uuid.NewString()
	start := make(chan struct{})
	results := make(chan error, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := store.Register(ctx, "p1", actor, subjectRequest())
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	accepted := 0
	for err := range results {
		if err == nil {
			accepted++
		} else if !errors.Is(err, pg.ErrSubjectConflict) {
			t.Fatal(err)
		}
	}
	if accepted != 1 {
		t.Fatal("concurrent requests created competing mappings")
	}
}
func TestSubjectRotationRacingErasureCannotRetainOrResurrectMapping(t *testing.T) {
	ctx := context.Background()
	pool, schema, store := subjectFixture(t, "subject_erasure_race")
	actor := uuid.NewString()
	for i := 0; i < 8; i++ {
		r := subjectRequest()
		first, err := store.Register(ctx, "p1", actor, r)
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		var writeErr, eraseErr error
		go func() {
			defer wg.Done()
			<-start
			_, writeErr = store.Update(ctx, "p1", actor, pg.SubjectUpdate{ID: first.ID, ExpectedVersion: first.Version, SubjectFields: pg.SubjectFields{ExternalReference: "rotated"}})
		}()
		go func() {
			defer wg.Done()
			<-start
			_, eraseErr = pg.NewEraser(pool).Erase(ctx, schema, "p1", first.ID, "requested")
		}()
		close(start)
		wg.Wait()
		if writeErr != nil && !errors.Is(writeErr, pg.ErrSubjectNotFound) {
			t.Fatal(writeErr)
		}
		if eraseErr != nil {
			t.Fatal(eraseErr)
		}
		if _, err := store.Get(ctx, "p1", first.ID, ""); !errors.Is(err, pg.ErrSubjectNotFound) {
			t.Fatal("racing rotation survived erasure")
		}
		if _, err := store.Register(ctx, "p1", actor, r); !errors.Is(err, pg.ErrSubjectConflict) {
			t.Fatal("racing erasure lost retry tombstone")
		}
	}
}
