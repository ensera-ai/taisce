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

	messages, err := f.observations.Messages(ctx, schema, observation.ID)
	if err != nil {
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
			_, err := f.facts.Assert(ctx, schema, observation.Scope, observation.ID,
				message.Role, observation.DataSubjectID, claim)
			switch {
			case err == nil:
				report.FactsAsserted++
			case errors.Is(err, pg.ErrNotSpokenByPrincipal):
				report.ClaimsRefusedByRole++
			default:
				return report, fmt.Errorf("assert claim from message %d: %w", message.Ordinal, err)
			}
		}

		for _, rejected := range result.Rejected {
			if err := f.facts.Reject(ctx, schema, observation.Scope, observation.ID,
				observation.DataSubjectID, rejected); err != nil {
				return report, fmt.Errorf("record rejection from message %d: %w", message.Ordinal, err)
			}
			report.ClaimsRejected++
		}
	}
	return report, nil
}

// Worker forms a scope's backlog.
type Worker struct {
	pool         *pgxpool.Pool
	observations *pg.ObservationStore
	former       *Former
}

// NewWorker builds a worker over a pool, a store and a former.
func NewWorker(pool *pgxpool.Pool, observations *pg.ObservationStore, former *Former) *Worker {
	return &Worker{pool: pool, observations: observations, former: former}
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
// A scope, not a tenant. Formation for one project has no reason to wait on another's, and a
// tenant-wide lock would make a busy project starve every quiet one beside it.
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

	// The key is derived from tenant and scope together, so two tenants using the same project name
	// do not serialise against each other. `hashtext` is 32 bits, so two unrelated scopes can
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
		observation, found, err := w.observations.NextUnformed(ctx, schema, scope)
		if err != nil {
			return reports, err
		}
		if !found {
			return reports, nil
		}
		report, err := w.former.Form(ctx, schema, observation)
		if err != nil {
			// Left unformed on purpose. The turn stays at the head of the backlog and the formed
			// watermark stays below it, so the failure is visible as a scope that stopped advancing
			// rather than as a gap nobody notices.
			return reports, err
		}
		if err := w.observations.MarkFormed(ctx, schema, observation.ID); err != nil {
			return reports, err
		}
		reports = append(reports, report)
	}
}
