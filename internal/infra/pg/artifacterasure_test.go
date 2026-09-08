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
	"github.com/jackc/pgx/v5/pgconn"
)

func TestArtifactOverwriteRacingErasureCannotRetainDeletedBytesOrQuota(t *testing.T) {
	ctx := context.Background()
	pool, schema, store := artifactFixture(t, "artifact_erasure_race")
	actor := uuid.NewString()
	isDeadlock := func(err error) bool { var db *pgconn.PgError; return errors.As(err, &db) && db.Code == "40P01" }
	for i := 0; i < 8; i++ {
		r := artifactRequest()
		first, err := store.Put(ctx, "p1", actor, r)
		if err != nil {
			t.Fatal(err)
		}
		r.ExpectedVersion = first.Version
		r.Content = []byte("new sensitive bytes")
		start := make(chan struct{})
		var wg sync.WaitGroup
		var writeErr, eraseErr error
		wg.Add(2)
		go func() { defer wg.Done(); <-start; _, writeErr = store.Put(ctx, "p1", actor, r) }()
		go func() {
			defer wg.Done()
			<-start
			_, eraseErr = pg.NewEraser(pool).Erase(ctx, schema, "p1", r.DataSubjectID, "requested")
		}()
		close(start)
		wg.Wait()
		if writeErr != nil && !errors.Is(writeErr, pg.ErrArtifactNotFound) && !isDeadlock(writeErr) {
			t.Fatal(writeErr)
		}
		if isDeadlock(eraseErr) {
			_, eraseErr = pg.NewEraser(pool).Erase(ctx, schema, "p1", r.DataSubjectID, "retry after atomic deadlock abort")
		}
		if eraseErr != nil {
			t.Fatal(eraseErr)
		}
		var remaining int
		if err := pool.QueryRow(ctx, schema.SQL(`SELECT (SELECT count(*) FROM {schema}.agent_artifact)+(SELECT count(*) FROM {schema}.observation)`)).Scan(&remaining); err != nil || remaining != 0 {
			t.Fatalf("erasure race retained rows: %d %v", remaining, err)
		}
		limits, err := store.Limits(ctx, "p1")
		if err != nil || limits.UsedBytes != 0 || limits.UsedObjects != 0 {
			t.Fatal("erasure race retained quota")
		}
	}
}
