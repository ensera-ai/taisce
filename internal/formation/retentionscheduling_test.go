// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package formation_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/ensera-ai/taisce/internal/formation"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/google/uuid"
)

func TestRetentionDriverFindsArtifactDeadlinesWithoutAProjectRetentionPolicy(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "fm_artifact_expiry")
	for _, scope := range []string{"p1", "p2"} {
		if err := migrate.ProvisionScope(ctx, pool, schema.String(), scope); err != nil {
			t.Fatal(err)
		}
	}
	store := pg.NewArtifactStore(pool, schema)
	request := pg.ArtifactPut{ID: uuid.NewString(), DataSubjectID: "owner", Kind: "state", Content: []byte("private state")}
	first, err := store.Put(ctx, "p1", uuid.NewString(), request)
	if err != nil {
		t.Fatal(err)
	}
	request.ID = uuid.NewString()
	if _, err := store.Put(ctx, "p2", uuid.NewString(), request); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.observation SET retention_until=now()-interval '1 second' WHERE observation_id=$1::uuid`), first.SourceObservationID); err != nil {
		t.Fatal(err)
	}
	driver := formation.NewDriver(workerWith(t, pool, schema, scriptedModel{}, impatient()), pg.NewObservationStore(pool), pg.NewRetentionStore(pool, schema), pg.NewAuditStore(pool, schema), schema, impatient(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := driver.SweepRetention(ctx); err != nil {
		t.Fatal(err)
	}
	var sources, artifacts int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT (SELECT count(*) FROM {schema}.observation WHERE scope='p1'),(SELECT count(*) FROM {schema}.agent_artifact WHERE scope='p1')`)).Scan(&sources, &artifacts); err != nil || sources != 0 || artifacts != 0 {
		t.Fatalf("scheduled expiry missed retained bytes: sources=%d artifacts=%d error=%v", sources, artifacts, err)
	}
	limits, err := store.Limits(ctx, "p1")
	if err != nil || limits.UsedBytes != 0 || limits.UsedObjects != 0 {
		t.Fatal("scheduled expiry retained allowance")
	}
	if _, err := store.Get(ctx, "p2", request.ID, ""); err != nil {
		t.Fatal("future deadline in another project was swept early")
	}
}
