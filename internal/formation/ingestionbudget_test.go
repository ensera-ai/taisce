// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package formation_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/formation"
	"github.com/ensera-ai/taisce/internal/infra/inference"
	"github.com/ensera-ai/taisce/internal/infra/pg"
)

func TestProviderOutageCannotGrowTheBacklogByParkingAndRecoveryRestoresAdmission(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "fm_ingestion_outage")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.ingestion_budget SET max_pending=2,max_pending_per_project=2`)); err != nil {
		t.Fatal(err)
	}
	var recovered atomic.Bool
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if !recovered.Load() {
			http.Error(w, "provider temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"{\"claims\":[]}"}}]}`))
	}))
	defer provider.Close()
	model := inference.NewModel(inference.Config{Endpoint: provider.URL + "/v1", Model: "outage-fixture"})
	policy := impatient()
	policy.MaxAttempts = 1
	worker := workerWith(t, pool, schema, model, policy)
	store := pg.NewObservationStore(pool)
	driver := formation.NewDriver(worker, store, pg.NewRetentionStore(pool, schema), pg.NewAuditStore(pool, schema), schema, policy, quietLogger())
	turn := turnOf(domain.Message{Role: domain.RoleUser, Content: "a short conversational message"})
	var observations []domain.Observation
	for i := 0; i < 2; i++ {
		o, err := store.Append(ctx, schema, turn)
		if err != nil {
			t.Fatal(err)
		}
		observations = append(observations, o)
	}
	if _, err := driver.Once(ctx); err != nil {
		t.Fatal(err)
	}
	fresh, err := store.Freshness(ctx, schema, "p1")
	if err != nil || fresh.Parked != 2 {
		t.Fatalf("outage did not reach parking: %+v %v", fresh, err)
	}
	health, err := pg.ReadOperationalHealth(ctx, pool, schema)
	if err != nil || health.FailedAttempts != 2 || health.Parked != 2 || health.Pending != 2 || !health.WorkerResponsive {
		t.Fatalf("outage diagnostics: %+v %v", health, err)
	}
	if _, err := store.Append(ctx, schema, turn); !errors.Is(err, pg.ErrIngestionCapacity) {
		t.Fatalf("parking bypassed capacity: %v", err)
	}
	recovered.Store(true)
	for _, o := range observations {
		if err := store.Unpark(ctx, schema, o.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := driver.Once(ctx); err != nil {
		t.Fatal(err)
	}
	var pending int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT pending FROM {schema}.ingestion_budget`)).Scan(&pending); err != nil || pending != 0 {
		t.Fatalf("recovery did not drain quota: %d %v", pending, err)
	}
	if calls.Load() != 4 {
		t.Fatalf("expected two failed and two recovered HTTP calls, got %d", calls.Load())
	}
	health, err = pg.ReadOperationalHealth(ctx, pool, schema)
	if err != nil || health.FailedAttempts != 2 || health.FormedCount != 2 || health.Pending != 0 || health.LastProgressAt == nil {
		t.Fatalf("recovery diagnostics: %+v %v", health, err)
	}
	if _, err := store.Append(ctx, schema, turn); err != nil {
		t.Fatalf("recovered provider did not restore admission: %v", err)
	}
}
