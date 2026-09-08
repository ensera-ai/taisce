// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/api"
	"github.com/ensera-ai/taisce/internal/infra/pg"
)

func TestReadinessBoundsDatabaseFailureAndProbeAmplificationWhileLivenessWorks(t *testing.T) {
	h := newHarness(t, "api_readiness")
	ctx := context.Background()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, h.schema.SQL(`LOCK {schema}.ingestion_budget IN ACCESS EXCLUSIVE MODE`)); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	entered := make(chan struct{})
	ready := api.NewReadiness(func(ctx context.Context) error {
		if calls.Add(1) == 1 {
			close(entered)
		}
		return pg.CheckServingHealth(ctx, h.pool, h.pool, h.schema, false)
	})
	handler := api.HealthHandler(ready)
	first := httptest.NewRecorder()
	done := make(chan struct{})
	started := time.Now()
	go func() {
		defer close(done)
		handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/ready", nil))
	}()
	<-entered
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := httptest.NewRecorder()
			handler.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/ready", nil))
			if r.Code != 503 {
				t.Errorf("concurrent probe=%d", r.Code)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("probe fanout issued %d dependency checks", calls.Load())
	}
	live := httptest.NewRecorder()
	handler.ServeHTTP(live, httptest.NewRequest(http.MethodGet, "/health", nil))
	if live.Code != 200 {
		t.Fatal("liveness depends on locked database")
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("readiness exceeded its failure budget")
	}
	if first.Code != 503 || time.Since(started) > 2*time.Second || first.Body.String() != "{\"status\":\"unavailable\"}\n" || first.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("failure response=%d %s", first.Code, first.Body.String())
	}
	for i := 0; i < 20; i++ {
		r := httptest.NewRecorder()
		handler.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/ready", nil))
		if r.Code != 503 {
			t.Fatal("failure cache lost")
		}
	}
	if calls.Load() != 1 {
		t.Fatal("failure probes were not cached")
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1050 * time.Millisecond)
	r := httptest.NewRecorder()
	handler.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if r.Code != 200 || calls.Load() != 2 {
		t.Fatalf("database recovery not observed: %d checks=%d", r.Code, calls.Load())
	}
}

func TestCanceledProbeCannotPoisonReadinessAndAbsentConfigurationFailsClosed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ready := api.NewReadiness(func(ctx context.Context) error { return ctx.Err() })
	r := httptest.NewRecorder()
	ready.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/ready", nil).WithContext(ctx))
	if r.Code != 200 {
		t.Fatal("client cancellation poisoned dependency check")
	}
	for path, want := range map[string]int{"/health": 200, "/ready": 503, "/v1/observations": 404, "/metrics": 404} {
		r := httptest.NewRecorder()
		api.HealthHandler(nil).ServeHTTP(r, httptest.NewRequest(http.MethodGet, path, nil))
		if r.Code != want {
			t.Fatalf("%s: %d want %d", path, r.Code, want)
		}
	}
}
