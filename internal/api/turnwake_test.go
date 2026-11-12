// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package api_test

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/extract"
	"github.com/ensera-ai/taisce/internal/formation"
	"github.com/ensera-ai/taisce/internal/infra/pg"
)

// listening starts a listener for this harness's schema and returns how many times it has woken.
// It is stopped, and its connection given back, before the harness closes the pool.
func listening(t *testing.T, h *harness, wake func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		pg.ListenForTurns(ctx, h.pool, h.schema, wake, func(err error) { t.Logf("listener: %v", err) })
	}()
	t.Cleanup(func() { cancel(); <-done })
}

// TestATurnStoredOnAnIdleInstanceStartsFormingWithoutWaitingForTheTick is the whole journey: a turn
// arrives over the memory API, and a worker that would otherwise sleep for an hour forms it.
//
// The driver's interval is an hour so that the tick cannot be what formed the turn. The driver's
// first pass runs before anything is stored and finds nothing, which is the idle instance; what
// ends its wait is the write.
func TestATurnStoredOnAnIdleInstanceStartsFormingWithoutWaitingForTheTick(t *testing.T) {
	h := newHarness(t, "api_turn_wake")
	ctx, cancel := context.WithCancel(context.Background())

	vocabulary, err := pg.LoadOntology(ctx, h.pool, h.schema)
	if err != nil {
		t.Fatal(err)
	}
	observations := pg.NewObservationStore(h.pool)
	policy := formation.DefaultPolicy()
	policy.Interval = time.Hour
	former := formation.NewFormer(observations, pg.NewFactStore(h.pool), extract.New(scriptedModel{}, vocabulary))
	wake := make(chan struct{}, 1)
	driver := formation.NewDriver(formation.NewWorkerWithPolicy(h.pool, observations, former, policy),
		observations, pg.NewRetentionStore(h.pool, h.schema), pg.NewAuditStore(h.pool, h.schema), h.schema, policy, nil).
		WithWake(wake)
	var woken atomic.Int64
	listening(t, h, func() {
		woken.Add(1)
		select {
		case wake <- struct{}{}:
		default:
		}
	})
	stopped := make(chan struct{})
	go func() { defer close(stopped); _ = driver.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-stopped })

	pending := func() int {
		var n int
		if err := h.pool.QueryRow(context.Background(), h.schema.SQL(
			`SELECT count(*) FROM {schema}.observation WHERE formed_at IS NULL`)).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	// Idle: the listener is connected and the driver has made the pass that connection asks for.
	deadline := time.Now().Add(10 * time.Second)
	for woken.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if woken.Load() == 0 {
		t.Fatal("the listener never connected")
	}
	time.Sleep(300 * time.Millisecond)
	before := woken.Load()

	h.do(t, http.MethodPost, "/v1/observations", map[string]any{
		"data_subject_id": "subject-1", "messages": []map[string]any{{"role": "user", "content": "I work at Ensera."}},
	}, http.StatusCreated, nil)

	deadline = time.Now().Add(10 * time.Second)
	for pending() != 0 && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
	}
	if left := pending(); left != 0 {
		t.Fatalf("a turn stored on an idle instance was still unformed after ten seconds with an hour's tick: %d pending, %d wake-ups since it was stored",
			left, woken.Load()-before)
	}
	if woken.Load() == before {
		t.Fatal("the turn formed and no wake-up arrived: something other than the write ended the driver's wait")
	}
}

// TestABurstOfWritesIsAFewWakeUpsAndAnotherSchemasWritesAreNone holds what a notification costs
// and whom it reaches.
//
// One per stored turn would be a statement per write to tell a worker one thing; and a database
// may hold more than one deployment's schema, whose workers have no business waking for each other.
func TestABurstOfWritesIsAFewWakeUpsAndAnotherSchemasWritesAreNone(t *testing.T) {
	h := newHarness(t, "api_turn_wake_burst")
	other := newHarness(t, "api_turn_wake_other")
	var woken atomic.Int64
	listening(t, h, func() { woken.Add(1) })
	deadline := time.Now().Add(10 * time.Second)
	for woken.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if woken.Load() != 1 {
		t.Fatalf("connecting must wake once, for whatever was stored while nobody listened; got %d", woken.Load())
	}

	store := func(to *harness, n int) {
		for i := 0; i < n; i++ {
			to.do(t, http.MethodPost, "/v1/observations", map[string]any{
				"data_subject_id": "subject-1", "messages": []map[string]any{{"role": "user", "content": "I work at Ensera."}},
			}, http.StatusCreated, nil)
		}
	}
	// Another schema's writes, in the same database: announced on the same channel, and ignored.
	store(other, 5)
	time.Sleep(600 * time.Millisecond)
	if got := woken.Load(); got != 1 {
		t.Fatalf("turns stored in another schema woke this one's worker %d times", got-1)
	}

	const burst = 40
	started := time.Now()
	store(h, burst)
	took := time.Since(started)
	time.Sleep(600 * time.Millisecond)
	got := woken.Load() - 1
	// At most one notification per gap of 200ms, so the ceiling follows from how long the burst
	// took rather than from how many turns it held — plus one for the edge either side.
	ceiling := int64(took/(200*time.Millisecond)) + 2
	if got < 1 || got > ceiling || got >= burst {
		t.Fatalf("%d turns stored over %s produced %d wake-ups; at least one, and no more than %d", burst, took.Round(time.Millisecond), got, ceiling)
	}
}
