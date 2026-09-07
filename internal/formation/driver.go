package formation

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/ensera-ai/taisce/internal/infra/pg"
)

// Driver runs the backlog without anybody asking it to.
//
// Forming happens behind the append (D19), which is only true if something forms. A worker drains a
// scope once and returns; this is the thing that decides which scopes, how often, and what to do
// when a pass makes no progress.
//
// # Why it discovers scopes rather than being told them
//
// A scope comes into existence when the first turn is written to it, and nothing announces that. A
// driver configured with a list of scopes would silently ignore every project created after it
// started — which is every project, in a system where creating one is writing to it.
//
// # Why it is safe to run several
//
// Each scope is drained under an advisory lock, so a second driver finds the scope busy and moves on
// to the next one rather than forming the same turn twice. That is what makes the Helm chart's
// several replicas a deployment decision rather than a code change, and it is asserted by a test
// rather than assumed.
type Driver struct {
	worker       *Worker
	observations *pg.ObservationStore
	schema       pg.Schema
	policy       Policy
	log          *slog.Logger
}

// NewDriver builds the driver over a worker that already knows how to drain one scope.
func NewDriver(worker *Worker, observations *pg.ObservationStore, schema pg.Schema,
	policy Policy, log *slog.Logger) *Driver {
	if log == nil {
		log = slog.Default()
	}
	return &Driver{worker: worker, observations: observations, schema: schema, policy: policy, log: log}
}

// Pass is one sweep over every scope that has work waiting.
//
// It returns what it did rather than only an error, because "the driver is running" and "the driver
// is making progress" are different questions and only the second one matters.
type Pass struct {
	Scopes  int
	Formed  int
	Busy    int
	Errored int
}

// Once drains every scope with a backlog, and returns having tried each of them.
//
// A scope that fails does not stop the pass. The failure is already recorded on the turn that caused
// it — an attempt counted, a reason kept, and eventually a parked turn — so stopping here would mean
// one project's bad turn holding up every other project's memory, which is the same mistake at a
// larger scale than the one parking exists to fix.
func (d *Driver) Once(ctx context.Context) (Pass, error) {
	scopes, err := d.observations.ScopesWithBacklog(ctx, d.schema)
	if err != nil {
		return Pass{}, err
	}

	pass := Pass{Scopes: len(scopes)}
	for _, scope := range scopes {
		if ctx.Err() != nil {
			return pass, ctx.Err()
		}
		reports, err := d.worker.Drain(ctx, d.schema, scope)
		pass.Formed += len(reports)

		switch {
		case err == nil:
		case errors.Is(err, ErrScopeBusy):
			// Another replica has it. Not a failure and not worth a log line at anything above
			// debug: in a deployment with several replicas this is the normal case.
			pass.Busy++
		case ctx.Err() != nil:
			return pass, ctx.Err()
		default:
			pass.Errored++
			d.log.Error("draining a scope failed", "scope", scope, "error", err)
		}
	}
	return pass, nil
}

// Run drives passes until the context ends.
//
// It waits between passes rather than polling continuously, and the wait is the same whether the
// last pass did work or not. A driver that spun when there was work would be a busy loop against the
// database on the one path that is already the slowest thing in the system.
func (d *Driver) Run(ctx context.Context) error {
	d.log.Info("formation driver started",
		"schema", d.schema.String(),
		"interval", d.policy.Interval.String(),
		"max_attempts", d.policy.MaxAttempts,
		"turn_budget", d.policy.TurnBudget.String())

	ticker := time.NewTicker(d.policy.Interval)
	defer ticker.Stop()

	for {
		pass, err := d.Once(ctx)
		if err != nil && ctx.Err() == nil {
			// A pass that could not even list its scopes is a database problem rather than a turn
			// problem. Logged and retried on the next tick: exiting would take formation down for a
			// condition that is usually brief, and a driver that exits is one an operator has to
			// notice before memory starts forming again.
			d.log.Error("formation pass failed", "error", err)
		}
		if pass.Formed > 0 || pass.Errored > 0 {
			d.log.Info("formation pass",
				"scopes", pass.Scopes, "formed", pass.Formed,
				"busy", pass.Busy, "errored", pass.Errored)
		}

		select {
		case <-ctx.Done():
			d.log.Info("formation driver stopped")
			return nil
		case <-ticker.C:
		}
	}
}
