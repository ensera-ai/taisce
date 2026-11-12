// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package formation

import (
	"context"
	"encoding/hex"
	"errors"
	"log/slog"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
)

// Driver runs the backlog without anybody asking it to.
//
// Forming happens behind the append, which is only true if something forms. A worker drains a
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
	subjects      *Subjects
	compaction    *Compaction
	notifications *Notifications
	owed          OwedScopes
	worker        *Worker
	observations  *pg.ObservationStore
	retention     *pg.RetentionStore
	audit         *pg.AuditStore
	schema        pg.Schema
	policy        Policy
	log           *slog.Logger
	// wake ends the wait between passes early. Nil in a driver nobody wakes, where it is never
	// ready and the tick is the only thing that ends the wait.
	wake <-chan struct{}
}

// NewDriver builds the driver over a worker that already knows how to drain one scope.
func NewDriver(worker *Worker, observations *pg.ObservationStore, retention *pg.RetentionStore,
	audit *pg.AuditStore, schema pg.Schema, policy Policy, log *slog.Logger, responsive ...func()) *Driver {
	if log == nil {
		log = slog.Default()
	}
	if len(responsive) > 1 {
		panic("one formation responsiveness callback is permitted")
	}
	worker.health = func(ctx context.Context, db pg.HealthExecutor, formed, failed int64) {
		if ctx.Err() != nil {
			return
		}
		bounded, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		if err := pg.RecordFormationHealth(bounded, db, schema, formed, failed); err != nil {
			log.Warn("formation health could not be recorded")
			return
		}
		if len(responsive) == 1 && responsive[0] != nil {
			responsive[0]()
		}
	}
	return &Driver{worker: worker, observations: observations, retention: retention,
		audit: audit, schema: schema, policy: policy, log: log}
}

// Pass is one sweep over every scope that has work waiting.
//
// It returns what it did rather than only an error, because "the driver is running" and "the driver
// is making progress" are different questions and only the second one matters.
type Pass struct {
	Scopes   int
	Formed   int
	Busy     int
	Errored  int
	Reports  int
	Segments int
	// Failed is how many turn attempts failed this pass, and Parked how many of those turns ran out
	// of attempts. Neither is Errored: a refused turn is recorded against the turn and retried, which
	// is the drain doing its job. They are counted so that a model refusing every turn is not a pass
	// that looks idle. Status is the HTTP status of the latest failure that carried one.
	Failed int
	Parked int
	Status int
	// Notified is how many notifications landed this pass, and Owed how many were newly written
	// down. They are counted separately because one is somebody else's endpoint working and the
	// other is this deployment noticing it had news.
	Notified int
	Owed     int
	// Derived is how many projects were given the derived passes although nothing was waiting to be
	// formed in them.
	Derived int
	// More says the pass stopped at its bound in at least one project and left turns waiting there.
	More bool
}

// WithPasses attaches the subject pass, which partitions a scope and writes its reports, and the
// compaction pass, which rolls up each subject's history. Both run per scope after its backlog is
// drained, bounded by the policy, so a deployment writes reports and segments without an operator
// running anything. Either may be nil.
func (d *Driver) WithPasses(subjects *Subjects, compaction *Compaction) *Driver {
	d.subjects, d.compaction = subjects, compaction
	return d
}

// OwedScopes names the projects that owe derived work with no backlog to announce them.
type OwedScopes interface {
	ScopesOwingReports(ctx context.Context, writer string, limit int) ([]string, error)
}

// WithWake lets something end the wait between passes: a signal that a turn was stored.
//
// It changes when a pass runs and nothing about what a pass does, so a wake-up that was spurious
// costs one pass that finds nothing. The tick is kept. A signal can be lost — the process that
// stored the turn died before sending it, the listening connection was down — and a driver that
// waited only for signals would then leave a turn unformed until the next one happened to arrive.
func (d *Driver) WithWake(wake <-chan struct{}) *Driver {
	d.wake = wake
	return d
}

// WithOwedScopes lets the driver reach a project whose backlog is empty and whose reports are not.
// Nil, which is what a deployment without it passes, leaves the driver serving backlogs alone.
func (d *Driver) WithOwedScopes(owed OwedScopes) *Driver {
	d.owed = owed
	return d
}

// WithNotifications attaches the pass that tells a project its memory moved. Nil, or a
// deployment whose operator has named no destinations, means no outbound call is ever made.
func (d *Driver) WithNotifications(notifications *Notifications) *Driver {
	d.notifications = notifications
	return d
}

// Once drains every scope with a backlog, and returns having tried each of them.
//
// A scope that fails does not stop the pass. The failure is already recorded on the turn that caused
// it — an attempt counted, a reason kept, and eventually a parked turn — so stopping here would mean
// one project's bad turn holding up every other project's memory, which is the same mistake at a
// larger scale than the one parking exists to fix.
func (d *Driver) Once(ctx context.Context) (Pass, error) {
	d.worker.heartbeat(ctx, d.worker.pool, 0, 0)
	scopes, err := d.observations.ScopesWithBacklog(ctx, d.schema)
	if err != nil {
		return Pass{}, err
	}

	pass := Pass{Scopes: len(scopes)}
	for _, scope := range scopes {
		if ctx.Err() != nil {
			return pass, ctx.Err()
		}
		drained, err := d.worker.drain(ctx, d.schema, scope, d.policy.TurnsPerScope, false)
		pass.Formed += drained.formed
		pass.More = pass.More || drained.more
		pass.Failed += drained.failed
		pass.Parked += drained.parked
		if drained.status != 0 {
			pass.Status = drained.status
		}

		switch {
		case err == nil && drained.more:
			// Turns are still waiting here, and the worker has moved on so that other projects are
			// reached. The passes that derive from formed turns wait for the backlog to empty, as
			// they always have: each is a model call, and running them after every slice would
			// write a report per slice for a project that is about to change again.
		case err == nil:
			d.projections(ctx, scope, &pass)
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
	// On the health row, with the pass that counted them. A stand-down is the only evidence this
	// deployment produces that the per-scope lock is serialising several workers, and it existed for
	// the length of this function and was then discarded.
	d.recordStandDowns(ctx, pass.Busy)
	if pass.Failed > 0 {
		// Counts and a status, never the reason. The reason is the provider's response body, which
		// is not bounded to the provider's own words: an endpoint may quote the request back, and the
		// request is what somebody said, while this log is read by whoever operates the instance. The
		// reason stays on the turn. One line a pass rather than one a turn, so a backlog failing
		// against a rejected key costs a line every interval instead of one per stored turn.
		attrs := []any{"scopes", pass.Scopes, "failed", pass.Failed, "parked", pass.Parked}
		if pass.Status != 0 {
			attrs = append(attrs, "status", pass.Status)
		}
		d.log.Warn("turns did not form", attrs...)
	}
	// A project with nothing to form can still owe derived work: a report is deleted when the facts
	// under it change, so an erasure or a correction in a quiet project leaves a hole that the loop
	// above never reaches, because that loop is fed by backlogs. Bounded per pass, because
	// each project added here is a partition rebuild and a model call.
	if d.owed != nil && d.subjects != nil && d.policy.DerivedScopesPerPass > 0 {
		drained := make(map[string]bool, len(scopes))
		for _, scope := range scopes {
			drained[scope] = true
		}
		owing, err := d.owed.ScopesOwingReports(ctx, d.subjects.WriterIdentity(), d.policy.DerivedScopesPerPass+len(scopes))
		if err != nil && ctx.Err() == nil {
			pass.Errored++
			d.log.Error("listing the projects owing a report failed", "error", err)
		}
		for _, scope := range owing {
			if drained[scope] || pass.Derived >= d.policy.DerivedScopesPerPass {
				continue
			}
			if ctx.Err() != nil {
				return pass, ctx.Err()
			}
			pass.Derived++
			d.projections(ctx, scope, &pass)
		}
	}

	// Once per pass and bounded, because the queue belongs to the instance rather than to a scope,
	// and because a thousand failing destinations must not be able to stop memory being formed.
	if d.notifications != nil {
		delivered, _, err := d.notifications.Deliver(ctx, d.policy.NotificationsPerPass)
		pass.Notified += delivered
		if err != nil && ctx.Err() == nil {
			pass.Errored++
			d.log.Error("delivering notifications failed", "error", err)
		}
	}
	return pass, nil
}

// projections runs the passes that derive from formed turns: subjects and their reports, then
// compaction. A failure in either is logged and counted; it is not the scope's formation failing.
func (d *Driver) projections(ctx context.Context, scope string, pass *Pass) {
	if d.subjects != nil && d.policy.ReportsPerPass > 0 {
		sp, err := d.subjects.Run(ctx, scope, d.policy.ReportsPerPass)
		pass.Reports += sp.Written
		if err != nil && ctx.Err() == nil {
			pass.Errored++
			d.log.Error("subject pass failed", "scope", scope, "error", err)
		}
	}
	if d.compaction != nil && d.policy.SegmentsPerPass > 0 {
		cp, err := d.compaction.Run(ctx, scope, d.policy.SegmentsPerPass)
		pass.Segments += cp.Written
		if err != nil && ctx.Err() == nil {
			pass.Errored++
			d.log.Error("compaction pass failed", "scope", scope, "error", err)
		}
	}
	// Last, and reading the watermark the passes above left behind rather than being told by them:
	// what is worth telling is what a caller would see if they asked, and that is the watermark.
	if d.notifications != nil {
		freshness, err := d.observations.Freshness(ctx, d.schema, scope)
		if err != nil {
			if ctx.Err() == nil {
				pass.Errored++
				d.log.Error("reading the watermark to notify failed", "scope", scope, "error", err)
			}
			return
		}
		if !freshness.HasFormed {
			return
		}
		owed, err := d.notifications.Owe(ctx, scope, freshness.Formed, freshness.Stored, freshness.Parked)
		pass.Owed += owed
		if err != nil && ctx.Err() == nil {
			pass.Errored++
			d.log.Error("recording what a project is owed failed", "scope", scope, "error", err)
		}
	}
}

// SweepRetention removes what has expired, across every project that has any.
//
// Separate from a formation pass rather than folded into it, because the two answer to different
// clocks: formation runs when there is a backlog, and retention runs whether or not anything was
// written. Folding them together would mean a quiet project never expiring anything.
//
// It reports what it removed rather than only whether it ran. A sweep that deletes quietly is
// indistinguishable from data loss, and the count is what makes "memory disappeared" answerable.
func (d *Driver) SweepRetention(ctx context.Context) error {
	scopes, err := d.observations.ScopesWithRetention(ctx, d.schema)
	if err != nil {
		return err
	}
	for _, scope := range scopes {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		sweep, err := d.retention.Sweep(ctx, scope, 500)
		if err != nil {
			// One project's failure does not stop another's, for the same reason a failing scope
			// does not stop formation: it would make one bad project hold everybody's policy.
			d.log.Error("retention sweep failed", "scope", scope, "error", err)
			continue
		}
		if !sweep.Empty() {
			d.log.Info("retention swept", "scope", scope, "receipt", sweep.ID,
				"observations", sweep.Observations, "deleted", sweep.Deleted)
			// On the ledger as well as in the receipt: the receipt says what went, and the ledger
			// says the instance did it and when, beside every other act against this project. The
			// principal is the system, because nobody asked for this — the project's policy did.
			if err := d.audit.Append(ctx, domain.AuditEntry{
				Operation: domain.AuditRetentionSweep, Principal: "retention",
				PrincipalKind: domain.PrincipalSystem, Project: scope,
				Magnitude: sweep.Observations, Outcome: domain.OutcomeAllowed,
			}); err != nil && ctx.Err() == nil {
				d.log.Error("the audit ledger did not record a retention sweep", "scope", scope, "error", err)
			}
		}
	}
	return nil
}

// recordStandDowns puts a pass's stand-downs on the health row.
//
// Through the worker's pool, which the driver already holds: a stand-down is the absence of a drain,
// so there is no held connection to write it on, and that is exactly the condition being recorded.
//
// A failure here is logged and dropped. This is the deployment reporting on itself, and a formation
// pass that completed its work must not be turned into a failure because the row describing it could
// not be updated — the same reasoning the per-drain health write already follows.
func (d *Driver) recordStandDowns(ctx context.Context, busy int) {
	if busy <= 0 || ctx.Err() != nil {
		return
	}
	bounded, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := pg.RecordFormationStandDowns(bounded, d.worker.pool, d.schema, int64(busy)); err != nil && ctx.Err() == nil {
		d.log.Warn("formation stand-downs could not be recorded", "busy", busy)
	}
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

	// Swept on the first pass rather than after an interval, so an instance restarted after a long
	// stop expires what it should have while it was down.
	var lastSweep time.Time
	var lastSeal time.Time

	for {
		pass, err := d.Once(ctx)
		if err != nil && ctx.Err() == nil {
			// A pass that could not even list its scopes is a database problem rather than a turn
			// problem. Logged and retried on the next tick: exiting would take formation down for a
			// condition that is usually brief, and a driver that exits is one an operator has to
			// notice before memory starts forming again.
			d.log.Error("formation pass failed", "error", err)
		}
		// Busy is in the gate, not only in the line. A pass whose every scope was held by another
		// replica formed nothing, errored on nothing and wrote neither report nor segment — so
		// without it here the replica doing exactly what the topology intends was the one that
		// logged nothing at all. On a single-scope instance, which is the common case, that is
		// every replica but one on every pass. One line per worker per interval is the same volume
		// the holder already produces, and the count is also on the health row the ops centre reads.
		if pass.Formed > 0 || pass.Busy > 0 || pass.Errored > 0 || pass.Reports > 0 || pass.Segments > 0 {
			d.log.Info("formation pass",
				"scopes", pass.Scopes, "formed", pass.Formed,
				"busy", pass.Busy, "errored", pass.Errored,
				"reports", pass.Reports, "segments", pass.Segments)
		}

		// Retention runs on the same tick as formation but on its own schedule: only when the
		// interval since the last sweep has passed, because expiry is a daily-scale concern and
		// running it every few seconds would be a full scan of the expiry index for nothing.
		// The ledger is sealed on the same tick, and the head digest is logged.
		//
		// Logging it is the cheap half of what makes a rebuilt ledger detectable: whatever collects
		// process logs then holds a value the database cannot retroactively agree with. It is not a
		// signed checkpoint and does not pretend to be — it is a digest in a place the database
		// cannot reach, which is the property that matters.
		if time.Since(lastSeal) >= d.policy.SealInterval {
			if seal, err := d.audit.Seal(ctx); err != nil {
				if ctx.Err() == nil {
					d.log.Error("sealing the audit ledger failed", "error", err)
				}
			} else if seal.Entries > 0 {
				d.log.Info("audit ledger sealed",
					"entries", seal.Entries, "from", seal.From, "to", seal.To,
					"head", hex.EncodeToString(seal.Digest))
			}
			lastSeal = time.Now()
		}

		if time.Since(lastSweep) >= d.policy.RetentionInterval {
			if err := d.SweepRetention(ctx); err != nil && ctx.Err() == nil {
				d.log.Error("retention pass failed", "error", err)
			}
			lastSweep = time.Now()
		}

		// A pass that stopped at its bound left turns waiting on purpose, so that other projects
		// were reached. Waiting out the interval now would make the bound a throttle: a project
		// would form eight turns every five seconds however fast the model was. So it goes straight
		// back, and stops only for the context.
		if pass.More {
			if ctx.Err() != nil {
				d.log.Info("formation driver stopped")
				return nil
			}
			continue
		}
		select {
		case <-ctx.Done():
			d.log.Info("formation driver stopped")
			return nil
		case <-ticker.C:
		case <-d.wake:
		}
	}
}
