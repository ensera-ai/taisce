// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestOperationalHealthSeparatesRequiredFormationAndNeverReturnsMemory(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "operational_health")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	if err := pg.CheckServingHealth(ctx, pool, nil, schema, false); err != nil {
		t.Fatal(err)
	}
	if err := pg.CheckServingHealth(ctx, pool, nil, schema, true); err == nil {
		t.Fatal("required missing worker accepted")
	}
	status, err := pg.ReadOperationalHealth(ctx, pool, schema)
	if err != nil || status.Pending != 0 || status.WorkerResponsive || status.OldestPendingAt != nil {
		t.Fatalf("empty health: %+v %v", status, err)
	}
	store := pg.NewObservationStore(pool)
	obs, err := store.Append(ctx, schema, turnOf(domain.Message{Role: domain.RoleUser, Content: "secret-message"}))
	if err != nil {
		t.Fatal(err)
	}
	claimed(t, pool, schema, obs.ID)
	if _, err := store.RecordFormationFailure(ctx, schema, obs.ID, "secret-provider-response"); err != nil {
		t.Fatal(err)
	}
	if err := store.Park(ctx, schema, obs.ID); err != nil {
		t.Fatal(err)
	}
	if err := pg.RecordFormationHealth(ctx, pool, schema, 0, 1); err != nil {
		t.Fatal(err)
	}
	if err := pg.CheckServingHealth(ctx, pool, nil, schema, true); err != nil {
		t.Fatal(err)
	}
	status, err = pg.ReadOperationalHealth(ctx, pool, schema)
	if err != nil || status.Pending != 1 || status.Parked != 1 || status.OldestPendingAt == nil || !status.WorkerResponsive || status.LastFailureAt == nil || status.FailedAttempts != 1 || status.LastProgressAt != nil || status.DatabaseConnections < 1 || status.ClusterConnections < status.DatabaseConnections || status.MaxConnections < 1 {
		t.Fatalf("backlog health: %+v %v", status, err)
	}
	wire, _ := json.Marshal(status)
	for _, forbidden := range []string{"secret", "subject-1", obs.ID, "p1"} {
		if strings.Contains(string(wire), forbidden) {
			t.Fatalf("diagnostics contain memory identifier/content: %s", wire)
		}
	}
	if err := store.Unpark(ctx, schema, obs.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkFormed(ctx, schema, obs.ID); err != nil {
		t.Fatal(err)
	}
	if err := pg.RecordFormationHealth(ctx, pool, schema, 1, 0); err != nil {
		t.Fatal(err)
	}
	status, err = pg.ReadOperationalHealth(ctx, pool, schema)
	if err != nil || status.Pending != 0 || status.Parked != 0 || status.FormedCount != 1 || status.LastProgressAt == nil || status.OldestPendingAt != nil {
		t.Fatalf("recovered health: %+v %v", status, err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.formation_health SET heartbeat_at=now()-interval '10 minutes'`)); err != nil {
		t.Fatal(err)
	}
	if err := pg.CheckServingHealth(ctx, pool, nil, schema, true); err == nil {
		t.Fatal("stale required worker accepted")
	}
	if err := pg.CheckServingHealth(ctx, pool, nil, schema, false); err != nil {
		t.Fatal("API-only service depends on worker")
	}
	for _, delta := range [][2]int64{{-1, 0}, {0, -1}} {
		if err := pg.RecordFormationHealth(ctx, pool, schema, delta[0], delta[1]); err == nil {
			t.Fatal("negative counter accepted")
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := pg.RecordFormationHealth(canceled, pool, schema, 0, 0); err == nil {
		t.Fatal("canceled heartbeat succeeded")
	}
	if _, err := pg.ReadOperationalHealth(canceled, pool, schema); err == nil {
		t.Fatal("canceled operator query succeeded")
	}
	closed, err := pgxpool.NewWithConfig(ctx, pool.Config())
	if err != nil {
		t.Fatal(err)
	}
	closed.Close()
	if err := pg.CheckServingHealth(ctx, closed, nil, schema, false); err == nil {
		t.Fatal("closed memory pool ready")
	}
	if err := pg.CheckServingHealth(ctx, pool, closed, schema, false); err == nil {
		t.Fatal("closed registry pool ready")
	}
	if _, err := pool.Exec(ctx, schema.SQL(`DELETE FROM {schema}.formation_health`)); err != nil {
		t.Fatal(err)
	}
	if err := pg.RecordFormationHealth(ctx, pool, schema, 0, 0); err == nil {
		t.Fatal("missing health row ignored")
	}
	if err := pg.CheckServingHealth(ctx, pool, nil, schema, true); err == nil {
		t.Fatal("missing required health row accepted")
	}
}
