// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package formation_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/formation"
	"github.com/ensera-ai/taisce/internal/infra/pg"
)

func TestFormationHealthTracksActualAttemptsAndRecovery(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "formation_health")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	store := pg.NewObservationStore(pool)
	obs, err := store.Append(ctx, schema, turnOf(domain.Message{Role: domain.RoleUser, Content: "I work at Ensera."}))
	if err != nil {
		t.Fatal(err)
	}
	var responsive atomic.Int32
	policy := impatient()
	policy.MaxAttempts = 1
	driver := formation.NewDriver(workerWith(t, pool, schema, &failingModel{}, policy), store, pg.NewRetentionStore(pool, schema), pg.NewAuditStore(pool, schema), schema, policy, quietLogger(), func() { responsive.Add(1) })
	if _, err := driver.Once(ctx); err != nil {
		t.Fatal(err)
	}
	status, err := pg.ReadOperationalHealth(ctx, pool, schema)
	if err != nil || status.FailedAttempts != 1 || status.FormedCount != 0 || !status.WorkerResponsive || responsive.Load() == 0 {
		t.Fatalf("failed attempt health: %+v %v", status, err)
	}
	// Reset the retry delay using the supported parked/recovery transition.
	if err := store.Park(ctx, schema, obs.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.Unpark(ctx, schema, obs.ID); err != nil {
		t.Fatal(err)
	}
	working := formation.NewDriver(workerWith(t, pool, schema, scriptedModel{}, impatient()), store, pg.NewRetentionStore(pool, schema), pg.NewAuditStore(pool, schema), schema, impatient(), quietLogger())
	if pass, err := working.Once(ctx); err != nil || pass.Formed != 1 {
		t.Fatalf("recovery pass: %+v %v", pass, err)
	}
	status, err = pg.ReadOperationalHealth(ctx, pool, schema)
	if err != nil || status.FailedAttempts != 1 || status.FormedCount != 1 || status.Pending != 0 || status.LastProgressAt == nil {
		t.Fatalf("successful attempt health: %+v %v", status, err)
	}
}
