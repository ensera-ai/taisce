// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"testing"

	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRuntimeArtifactWritesUseQuotaWithoutReceivingQuotaAdministration(t *testing.T) {
	ctx := context.Background()
	owner, schema, _ := artifactFixture(t, "artifact_runtime")
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
	store := pg.NewArtifactStore(runtime, schema)
	actor := uuid.NewString()
	r := artifactRequest()
	receipt, err := store.Put(ctx, "p1", actor, r)
	if err != nil {
		t.Fatalf("runtime storage: %v", err)
	}
	if _, err := store.Get(ctx, "p1", r.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Exec(ctx, schema.SQL(`UPDATE {schema}.agent_storage_policy SET used_bytes=0`)); err == nil {
		t.Fatal("runtime can erase quota accounting")
	}
	limits, _ := store.Limits(ctx, "p1")
	if err := store.ConfigureLimits(ctx, "p1", actor, limits); err == nil {
		t.Fatal("runtime can enlarge its own allowance")
	}
	if err := store.Delete(ctx, "p1", actor, receipt.ID, receipt.Version, ""); err != nil {
		t.Fatalf("runtime deletion: %v", err)
	}
	limits, _ = store.Limits(ctx, "p1")
	if limits.UsedBytes != 0 || limits.UsedObjects != 0 {
		t.Fatal("runtime deletion left a reservation")
	}
}
