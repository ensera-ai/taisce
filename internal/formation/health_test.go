// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package formation_test

import (
	"context"
	"github.com/google/uuid"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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
	if changed, err := store.UnparkAudited(ctx, schema, "p1", obs.ID, uuid.NewString()); err != nil || !changed {
		t.Fatalf("unpark: %v %v", changed, err)
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

// A pass that stood down on every scope says so, in the log and on the health row.
//
// Two workers against one project is what the chart's default topology runs, and what makes it safe
// is that each scope is drained under an advisory lock: a second worker finds it busy and stands
// down rather than forming the same turn twice. That replica — the one doing exactly what the design
// intends — produced no evidence of itself at all. The count lived for the length of a pass, and the
// one log line carrying it was behind a gate the count was not in, so a pass whose every scope was
// held formed nothing, errored on nothing, wrote neither report nor segment, and logged nothing.
//
// On a single-scope instance, which is what the default project makes and what most deployments are,
// that is every replica but one on every pass. So on the most common topology, four healthy workers
// and one healthy worker beside three that were wedged produced identical evidence.
func TestAPassThatStoodDownOnEveryScopeStillReportsIt(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "formation_stand_down")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	store := pg.NewObservationStore(pool)
	if _, err := store.Append(ctx, schema, turnOf(domain.Message{Role: domain.RoleUser, Content: "I work at Ensera."})); err != nil {
		t.Fatal(err)
	}

	// Hold the scope from outside, the way the replica that got there first does.
	held, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()
	key := schema.String() + "/p1"
	var taken bool
	if err := held.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtext($1)::bigint)`, key).Scan(&taken); err != nil {
		t.Fatal(err)
	}
	if !taken {
		t.Fatal("the lock was already held, so this test asserts nothing")
	}

	logged := &capturingHandler{}
	driver := formation.NewDriver(workerWith(t, pool, schema, scriptedModel{}, impatient()), store,
		pg.NewRetentionStore(pool, schema), pg.NewAuditStore(pool, schema), schema, impatient(),
		slog.New(logged))
	pass, err := driver.Once(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if pass.Busy != 1 || pass.Formed != 0 || pass.Errored != 0 || pass.Segments != 0 || pass.Reports != 0 {
		t.Fatalf("the pass must have stood down and done nothing else, got %+v", pass)
	}

	// On the row the ops centre reads. This is the half that reaches an operator who has no access
	// to the logs, which is the reader the portal exists for.
	status, err := pg.ReadOperationalHealth(ctx, pool, schema)
	if err != nil {
		t.Fatal(err)
	}
	if status.StoodDown != 1 || status.LastStoodDownAt == nil {
		t.Fatalf("a stand-down must be counted and timed on the health row, got %d at %v", status.StoodDown, status.LastStoodDownAt)
	}

	// A second pass adds to it rather than replacing it: the count is the evidence over time.
	if _, err := driver.Once(ctx); err != nil {
		t.Fatal(err)
	}
	if status, err = pg.ReadOperationalHealth(ctx, pool, schema); err != nil || status.StoodDown != 2 {
		t.Fatalf("stand-downs accumulate, got %d %v", status.StoodDown, err)
	}

	// And the driver's own loop emits a line, which it did not when the gate omitted busy: every
	// other counter in that gate is zero on a pass like this one. Driven through Run rather than
	// Once because the gate is in the loop, so asserting it anywhere else would assert nothing
	// about the running deployment.
	looping, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); _ = driver.Run(looping) }()
	deadline := time.After(10 * time.Second)
	for !logged.has("formation pass", "busy", int64(1)) {
		select {
		case <-deadline:
			stop()
			<-done
			t.Fatalf("a pass that stood down on every scope must still log one line: %v", logged.lines())
		case <-time.After(10 * time.Millisecond):
		}
	}
	stop()
	<-done

	// Released: the pass drains, and nothing is added to the stand-down count for a pass that had
	// nothing to stand down for — the time means "a pass last stood down then", and touching it on
	// a pass that did not would make it mean "a pass last ran then", which the heartbeat says.
	if _, err := held.Exec(ctx, `SELECT pg_advisory_unlock(hashtext($1)::bigint)`, key); err != nil {
		t.Fatal(err)
	}
	settled, err := pg.ReadOperationalHealth(ctx, pool, schema)
	if err != nil {
		t.Fatal(err)
	}
	if pass, err := driver.Once(ctx); err != nil || pass.Busy != 0 || pass.Formed != 1 {
		t.Fatalf("a released scope drains, got %+v %v", pass, err)
	}
	after, err := pg.ReadOperationalHealth(ctx, pool, schema)
	if err != nil || after.StoodDown != settled.StoodDown || !after.LastStoodDownAt.Equal(*settled.LastStoodDownAt) {
		t.Fatalf("a pass that stood down for nothing must leave the count and the time alone, got %d at %v against %d at %v",
			after.StoodDown, after.LastStoodDownAt, settled.StoodDown, settled.LastStoodDownAt)
	}
}

// capturingHandler keeps the records a driver logs, so a test can assert that a line was emitted at
// all — which is the whole of what the gate above decides.
type capturingHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *capturingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *capturingHandler) WithAttrs([]slog.Attr) slog.Handler       { return h }
func (h *capturingHandler) WithGroup(string) slog.Handler            { return h }
func (h *capturingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}

func (h *capturingHandler) has(message, key string, want int64) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.records {
		if r.Message != message {
			continue
		}
		found := false
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == key && a.Value.Int64() == want {
				found = true
			}
			return true
		})
		if found {
			return true
		}
	}
	return false
}

func (h *capturingHandler) lines() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, r := range h.records {
		out = append(out, r.Message)
	}
	return out
}
