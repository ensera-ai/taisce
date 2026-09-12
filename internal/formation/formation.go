// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

// Package formation turns a stored turn into the facts it supports.
//
// Storing and forming are separate moments, and the separation is not an implementation detail.
// Forming means asking a model what each message asserts, which takes seconds per message — so
// holding a caller through it would make an agent's turn block on its own memory write, and the
// product would be slower than having no memory at all.
//
// What that costs is that a turn is briefly stored and not yet recalled. That is why there are two
// watermarks: "we have your turn" is what an append confirms, and "your turn is in memory" is the
// question a recall actually depends on.
package formation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/extract"
	"github.com/ensera-ai/taisce/internal/infra/pg"
)

// Former runs extraction over one turn and writes what it produced.
type Former struct {
	observations *pg.ObservationStore
	facts        *pg.FactStore
	extractor    *extract.Extractor
}

// NewFormer builds a former over the stores and the extractor it drives.
func NewFormer(observations *pg.ObservationStore, facts *pg.FactStore, extractor *extract.Extractor) *Former {
	return &Former{observations: observations, facts: facts, extractor: extractor}
}

// Report is what one turn produced. Counted rather than returned in full, because the rows
// themselves are already in the database and a caller that wants them can read them there.
type Report struct {
	ObservationID string
	MessagesRead  int
	FactsAsserted int
	// ClaimsRejected is proposals refused by the vocabulary or by the span rule. Recorded, because
	// their rate is the only evidence that either is wrong.
	ClaimsRejected int
	// ClaimsRetracted counts source-backed human withdrawals enforced before projection writes.
	ClaimsRetracted int
	// ClaimsRefusedByRole is claims that would have become facts about the principal, from a message
	// the principal did not speak. Counted and not recorded: unlike the other two this is not a
	// quality signal — it is the policy working exactly as designed, and its rate is already
	// knowable from the roles of the messages in the turn.
	ClaimsRefusedByRole int
}

// Form extracts from every message in a turn and writes the result.
//
// # The role is the MESSAGE's, never the turn's
//
// A turn opened by the user does not make an assistant message three positions later speak for the
// principal. The policy is per message because that is the only granularity at which it is true, and
// passing the turn's role would quietly promote every model sentence in any turn a person started.
//
// # Every message is extracted, including ones whose claims will be refused
//
// A message that cannot speak for the principal costs a model call whose every claim is then
// refused. Skipping it would save that call and produce the same rows — but only because formation
// would be applying the role policy itself, in a second place, using reasoning that has to stay in
// step with the first. The policy lives on the road to the fact table so that a path which forgets
// it does not exist, and that is worth more than the call.
//
// # Not marked formed here
//
// Marking is the caller's, because "extraction ran" and "the watermark may advance" are separable:
// a caller re-running a turn to compare extractor versions wants the first without the second.
func (f *Former) Form(ctx context.Context, schema pg.Schema, observation domain.Observation) (Report, error) {
	report := Report{ObservationID: observation.ID}
	curated, err := f.facts.ReplayCurated(ctx, schema, observation.Scope, observation.ID)
	if curated {
		report.MessagesRead = 1
		if errors.Is(err, pg.ErrRetractedClaim) {
			report.ClaimsRetracted = 1
			return report, nil
		}
		if err == nil {
			report.FactsAsserted = 1
		}
		return report, err
	}
	if err != nil {
		return report, err
	}

	generation, published, err := f.facts.ReplayGeneration(ctx, schema, observation.Scope, observation.ID)
	if err != nil {
		return report, err
	}
	if published {
		report.MessagesRead = generation.Messages
		report.FactsAsserted = generation.Asserted
		report.ClaimsRejected = generation.Rejected
		report.ClaimsRetracted = generation.Retracted
		report.ClaimsRefusedByRole = generation.RefusedRole
		return report, nil
	}

	messages, err := f.observations.Messages(ctx, schema, observation.ID)
	if err != nil {
		return report, err
	}
	version := f.extractor.PipelineVersion()
	if err := f.facts.PinExtraction(ctx, schema, observation.Scope, observation.ID, version); err != nil {
		return report, err
	}
	report.MessagesRead = len(messages)

	for _, message := range messages {
		result, err := f.extractor.Extract(ctx, message)
		if err != nil {
			// A model failure stops the turn. Continuing would form half of it and mark it done,
			// and a half-formed turn is indistinguishable afterwards from one that genuinely
			// asserted only what survived.
			return report, fmt.Errorf("extract message %d: %w", message.Ordinal, err)
		}

		for _, claim := range result.Claims {
			_, err := f.facts.AssertVersioned(ctx, schema, observation.Scope, observation.ID,
				message.Role, observation.DataSubjectID, claim, version)
			switch {
			case err == nil:
				report.FactsAsserted++
			case errors.Is(err, pg.ErrRetractedClaim):
				report.ClaimsRetracted++
			case errors.Is(err, pg.ErrUnboundSpeaker):
				result.Rejected = append(result.Rejected, domain.RejectedClaim{
					Statement: claim.Statement, Predicate: claim.Predicate,
					Quote: claim.Quote, SourceOrdinal: claim.SourceOrdinal, Reason: domain.ReasonUnresolvableSubject,
				})
			case errors.Is(err, pg.ErrNotSpokenByPrincipal):
				report.ClaimsRefusedByRole++
				result.Rejected = append(result.Rejected, domain.RejectedClaim{
					Statement: claim.Statement, Predicate: claim.Predicate,
					Quote: claim.Quote, SourceOrdinal: claim.SourceOrdinal, Reason: domain.ReasonNotSpokenByPrincipal,
				})
			case errors.Is(err, pg.ErrEntityNameLimit):
				// A name this store cannot keep is this claim's problem, not the turn's: the other
				// claims in the message are unaffected and are still asserted.
				result.Rejected = append(result.Rejected, domain.RejectedClaim{
					Statement: claim.Statement, Predicate: claim.Predicate,
					Quote: claim.Quote, SourceOrdinal: claim.SourceOrdinal, Reason: domain.ReasonEntityNameLimit,
				})
			case errors.Is(err, pg.ErrConflictingValue):
				// The constraint refused a second current value from this same message. An outcome
				// with a reason, recorded like the others; retrying it would produce the same row.
				result.Rejected = append(result.Rejected, domain.RejectedClaim{
					Statement: claim.Statement, Predicate: claim.Predicate,
					Quote: claim.Quote, SourceOrdinal: claim.SourceOrdinal, Reason: domain.ReasonConflictingValue,
				})
			default:
				return report, fmt.Errorf("assert claim from message %d: %w", message.Ordinal, err)
			}
		}

		for _, rejected := range result.Rejected {
			if err := f.facts.RejectVersioned(ctx, schema, observation.Scope, observation.ID,
				observation.DataSubjectID, rejected, version); err != nil {
				return report, fmt.Errorf("record rejection from message %d: %w", message.Ordinal, err)
			}
			report.ClaimsRejected++
		}
	}
	return report, nil
}

// Policy bounds what forming will try before it gives up.
//
// Every field here exists because its absence is a way for the backlog to stop making progress
// without anybody being told.
type Policy struct {
	// MaxAttempts is how many times a turn is tried before it is parked. Generous rather than tight,
	// because the failure that matters most is the one that is not the turn's fault.
	MaxAttempts int
	// RetryAfter is the wait before a failed turn is offered again. It doubles per attempt, so this
	// is the first interval rather than the only one.
	RetryAfter time.Duration
	// TurnBudget bounds one attempt. Extraction is a model call per message, so a long turn is a
	// request that never ends.
	TurnBudget time.Duration
	// Interval is how long the driver waits after a pass that found nothing.
	Interval time.Duration
	// RetentionInterval is how often expiry is swept. Separate from Interval because expiry is a
	// daily-scale concern and running it every few seconds would scan an index for nothing.
	RetentionInterval time.Duration
	// ReportsPerPass bounds how many community reports one pass writes per scope, and
	// SegmentsPerPass how many compaction segments: each is a model call, so the bound is what
	// makes a pass's cost predictable.
	ReportsPerPass  int
	SegmentsPerPass int
	// DerivedScopesPerPass bounds how many projects with no backlog are given the derived passes in
	// one tick. Each one is a partition rebuild and a model call, so it is bounded like the two
	// above rather than by however many projects happen to owe a report.
	DerivedScopesPerPass int
	// NotificationsPerPass bounds how many deliveries one pass attempts. Unlike the two above this
	// is not a model call but an outbound request to somebody else's server, and the bound exists
	// for the same reason: a queue of failing destinations must not be able to stop memory forming.
	NotificationsPerPass int
	// SealInterval is how often the audit ledger is sealed. It is the exposure window: an entry
	// written after the last seal is covered by nothing, so this number is the size of the gap
	// somebody could write into unnoticed.
	SealInterval time.Duration
}

// DefaultPolicy is deliberately patient.
//
// Six attempts with a doubling wait from ten seconds means a turn is not parked until roughly ten
// minutes of failing, which is long enough that a provider blip does not park a backlog and short
// enough that a genuinely poisonous turn does not hold a scope for a working day. No latency budget
// has been agreed anywhere in this system, so these are chosen for the shape of the failure rather
// than derived from a target — and that is worth saying out loud rather than presenting them as
// measured.
func DefaultPolicy() Policy {
	return Policy{
		MaxAttempts:          6,
		RetryAfter:           10 * time.Second,
		TurnBudget:           5 * time.Minute,
		Interval:             5 * time.Second,
		RetentionInterval:    time.Hour,
		SealInterval:         5 * time.Minute,
		ReportsPerPass:       4,
		SegmentsPerPass:      4,
		DerivedScopesPerPass: 4,
		// Higher than the model-call bounds because a delivery is a fast outbound request, and
		// still bounded because the queue is shared with the work that matters more.
		NotificationsPerPass: 16,
	}
}

// Worker forms a scope's backlog.
type Worker struct {
	pool         *pgxpool.Pool
	observations *pg.ObservationStore
	former       *Former
	policy       Policy
	health       func(context.Context, pg.HealthExecutor, int64, int64)
}

// NewWorker builds a worker over a pool, a store and a former.
func NewWorker(pool *pgxpool.Pool, observations *pg.ObservationStore, former *Former) *Worker {
	return NewWorkerWithPolicy(pool, observations, former, DefaultPolicy())
}

// NewWorkerWithPolicy is the same worker with the bounds stated, which is what a test needs in order
// to drive a turn to exhaustion without waiting ten minutes.
func NewWorkerWithPolicy(pool *pgxpool.Pool, observations *pg.ObservationStore, former *Former,
	policy Policy) *Worker {
	return &Worker{pool: pool, observations: observations, former: former, policy: policy}
}

// ErrScopeBusy is returned when another worker is already forming this scope's backlog.
var ErrScopeBusy = errors.New("another worker holds this scope")

// Drain forms every unformed turn in a scope, oldest first, and returns what each produced.
//
// # One worker per scope, held by an advisory lock
//
// Two workers taking the same turn would extract it twice and assert every fact twice, and the
// duplicates would be indistinguishable from a person saying the same thing in two conversations.
// The turns are picked one at a time by offset, so nothing weaker than a lock held across the whole
// drain prevents it: a lock inside the transaction that picks a turn is released before the model
// call it was meant to protect.
//
// A scope, not the whole instance. Formation for one project has no reason to wait on another's,
// and an instance-wide lock would make a busy project starve every quiet one beside it.
//
// # Oldest first
//
// The formed watermark is the offset below the lowest unformed turn. Forming newest-first would
// leave the oldest unformed and pin that number at the bottom while everything above it completed —
// a scope reported as entirely behind when it is nearly current.
func (w *Worker) Drain(ctx context.Context, schema pg.Schema, scope string) ([]Report, error) {
	conn, err := w.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire connection: %w", err)
	}
	defer conn.Release()

	// The key is derived from the namespace and the scope together, so two instances sharing a
	// database and using the same project name do not serialise against each other. `hashtext` is 32 bits, so two unrelated scopes can
	// collide and take turns — which costs throughput and never correctness, and is the right way
	// round for a collision to fail.
	key := schema.String() + "/" + scope
	var acquired bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtext($1)::bigint)`, key).Scan(&acquired); err != nil {
		return nil, fmt.Errorf("take scope lock: %w", err)
	}
	if !acquired {
		return nil, ErrScopeBusy
	}
	defer func() {
		// Released explicitly rather than left to the session ending, because the connection goes
		// back to the pool still holding it otherwise — and the next borrower of that connection
		// would hold a lock it knows nothing about.
		_, _ = conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock(hashtext($1)::bigint)`, key)
	}()

	var reports []Report
	for {
		observation, found, err := w.observations.NextUnformed(ctx, schema, scope, w.policy.RetryAfter)
		if err != nil {
			return reports, err
		}
		if !found {
			return reports, nil
		}

		// A budget per turn, applied here rather than in the extractor. Extraction is a model call
		// per message, so a turn with two hundred messages is a request that never ends — and
		// whether to abandon it or keep waiting is a decision about this backlog, which the
		// extractor knows nothing about.
		attemptCtx, cancel := context.WithTimeout(ctx, w.policy.TurnBudget)
		report, err := w.former.Form(attemptCtx, schema, observation)
		cancel()

		if err != nil {
			// A cancelled parent is a shutdown, not a bad turn. Counting it as an attempt would
			// spend a turn's budget on every restart, and a few restarts would park a backlog that
			// nothing was ever wrong with.
			if ctx.Err() != nil {
				return reports, ctx.Err()
			}
			parked, perr := w.recordFailure(ctx, schema, observation, err)
			w.heartbeat(ctx, conn, 0, 1)
			if perr != nil {
				return reports, perr
			}
			if !parked {
				// Left in the backlog with its attempt counted and its wait doubled. Moving on to
				// the next turn rather than returning, because one turn that will not form is not a
				// reason to stop forming the scope — that was the old behaviour and it froze
				// everything behind it.
				continue
			}
			continue
		}

		if err := w.observations.MarkFormed(ctx, schema, observation.ID); err != nil {
			return reports, err
		}
		w.heartbeat(ctx, conn, 1, 0)
		reports = append(reports, report)
	}
}

// recordFailure counts the attempt and parks the turn if it has run out of them.
//
// Parking is a decision to stop trying, recorded and reversible: the observation and its messages
// are untouched, so unparking is one update and re-forming is a rebuild. It advances the watermark
// past the turn, which is what stops one turn nobody can form from freezing a scope — and the count
// of parked turns travels with freshness so that exception is visible rather than hidden.
func (w *Worker) recordFailure(ctx context.Context, schema pg.Schema,
	observation domain.Observation, cause error) (parked bool, err error) {

	attempts, err := w.observations.RecordFormationFailure(ctx, schema, observation.ID, cause.Error())
	if err != nil {
		return false, err
	}
	if attempts < w.policy.MaxAttempts {
		return false, nil
	}
	if err := w.observations.Park(ctx, schema, observation.ID); err != nil {
		return false, err
	}
	return true, nil
}
