// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ensera-ai/taisce/internal/domain"
)

// ObservationStore appends turns to the log.
type ObservationStore struct{ pool *pgxpool.Pool }

func NewObservationStore(pool *pgxpool.Pool) *ObservationStore { return &ObservationStore{pool: pool} }

// KindTurn is the observation kind a conversation turn is stored under.
const KindTurn = "turn"

// ProjectionChunk is the projection kind a chunk registers as. Erasure walks
// `projection_dependency` rather than a list of tables, so this string is what makes a chunk
// reachable from the observation it derived from.
const ProjectionChunk = "chunk"

// Append writes a turn: the observation, its messages, and one chunk per message, in one
// transaction.
//
// # Why the offset is claimed inside the transaction
//
// The freshness watermark is the highest offset with NO GAP BELOW IT. A sequence issued outside the
// transaction leaves a permanent hole whenever a transaction rolls back, and one hole stalls the
// watermark forever — it would sit below the gap while the log grew above it, reporting a scope as
// hours behind when it is current.
//
// `max(log_offset) + 1` under a row lock on the scope is the price of that guarantee: appends to one
// scope serialise. That is the right trade, because the alternative is a number that cannot mean
// what it says.
//
// # Why a chunk per message rather than per turn
//
// Evidence is a byte span, and a span is only meaningful against the string it indexes. A chunk
// spanning a whole turn would need spans into a concatenation that exists nowhere, so a citation
// would resolve to the wrong words while looking correct.
func (s *ObservationStore) Append(ctx context.Context, schema Schema, turn domain.Turn) (domain.Observation, error) {
	out, _, err := s.AppendIdempotent(ctx, schema, turn, "")
	return out, err
}

// appendTurnTx writes one new observation inside the transaction that also owns its retry receipt.
func appendTurnTx(ctx context.Context, tx pgx.Tx, schema Schema, turn domain.Turn) (domain.Observation, error) {
	// The lock is on the scope's watermark row, which every append to this scope contends for
	// and no append to another scope touches. Taken by upserting it, so the first append to a
	// new scope does not need the row to exist already.
	var offset int64
	if err := tx.QueryRow(ctx, schema.SQL(claimOffsetSQL), turn.Scope, turn.OccurredAt).Scan(&offset); err != nil {
		return domain.Observation{}, fmt.Errorf("claim log offset: %w", err)
	}

	id := uuid.New()
	ingested := time.Now().UTC()
	// The turn's own role, for the observation row: the speaker of its first message. A turn
	// has several, and the per-message roles are what the policy reads; this exists so an
	// operator scanning the log sees who started it without joining.
	lead := turn.Messages[0].Role
	if _, err := tx.Exec(ctx, schema.SQL(insertObservationSQL),
		id, turn.Scope, offset, KindTurn, turn.OccurredAt, ingested,
		string(lead), nullIfEmpty(turn.DataSubjectID)); err != nil {
		return domain.Observation{}, fmt.Errorf("insert observation: %w", err)
	}

	for _, m := range turn.Messages {
		occurred := m.OccurredAt
		if occurred.IsZero() {
			occurred = turn.OccurredAt
		}
		var chunkID string
		if err := tx.QueryRow(ctx, schema.SQL(insertMessageSQL+` RETURNING chunk_id::text`),
			id, m.Ordinal, string(m.Role), m.Content, m.GroupOrdinal, occurred).Scan(&chunkID); err != nil {
			return domain.Observation{}, fmt.Errorf("insert message %d: %w", m.Ordinal, err)
		}

		if _, err := tx.Exec(ctx, schema.SQL(insertChunkSQL),
			chunkID, turn.Scope, id, m.Ordinal, m.Content, occurred,
			string(m.Role), nullIfEmpty(turn.DataSubjectID)); err != nil {
			return domain.Observation{}, fmt.Errorf("insert chunk for message %d: %w", m.Ordinal, err)
		}
		// Registered in the SAME transaction as the row it describes. A projection written now
		// and registered afterwards is a projection erasure cannot see for as long as the gap
		// lasts, and the gap is exactly the window in which a process dies.
		if _, err := tx.Exec(ctx, schema.SQL(registerProjectionSQL),
			id, turn.Scope, ProjectionChunk, chunkID,
			nullIfEmpty(turn.DataSubjectID)); err != nil {
			return domain.Observation{}, fmt.Errorf("register chunk projection: %w", err)
		}
	}

	// The expiry, from the project's own policy, stamped in the same transaction as the write.
	//
	// Stamped rather than computed at sweep time so that the policy which applied when a turn
	// arrived is the policy that governs it, and so an operator can see when a row will go
	// rather than deriving it. Shortening a policy therefore applies to what arrives next;
	// removing what is already held is an erasure with a receipt, not a configuration change
	// that destroys things quietly.
	//
	// A project with no policy leaves this NULL, which means keep — a decision rather than an
	// absence of one, and the sweep reads it as such.
	if _, err := tx.Exec(ctx, schema.SQL(stampRetentionSQL), id, turn.Scope); err != nil {
		return domain.Observation{}, fmt.Errorf("stamp retention: %w", err)
	}

	out := domain.Observation{
		ID:            id.String(),
		LogOffset:     offset,
		Scope:         turn.Scope,
		DataSubjectID: turn.DataSubjectID,
		OccurredAt:    turn.OccurredAt,
		IngestedAt:    ingested,
	}
	return out, nil
}

// The project may not exist — a write to an unprovisioned scope still lands, deliberately, because
// losing memory to a missed provisioning step is the worse failure. No project row means no policy
// means keep, which is the same answer as a project with no policy.
const stampRetentionSQL = `
UPDATE {schema}.observation o
   SET retention_until = now() + p.retention
  FROM {schema}.project p
 WHERE o.observation_id = $1 AND p.scope = $2 AND p.retention IS NOT NULL`

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// claimOffsetSQL takes the scope's next offset and locks the scope for the rest of the transaction.
//
// The watermark row doubles as the lock, which is why this is an upsert rather than a select: it
// serialises appends to one scope, and it advances the row that reports freshness in the same
// statement. A separate advisory lock would need its own key derivation and could disagree with the
// row it is meant to protect.
//
// `watermark_at` is the TURN's time, not now(): the watermark answers "how far through the
// customer's history are we", and stamping it with wall-clock time makes a backfill of last year's
// conversations report as current.
const claimOffsetSQL = `
INSERT INTO {schema}.watermark (scope, log_offset, watermark_at)
VALUES ($1, 0, $2)
ON CONFLICT (scope) DO UPDATE
   SET log_offset   = {schema}.watermark.log_offset + 1,
       watermark_at = greatest({schema}.watermark.watermark_at, excluded.watermark_at),
       updated_at   = now()
RETURNING log_offset`

const insertObservationSQL = `
INSERT INTO {schema}.observation
    (observation_id, scope, log_offset, kind, occurred_at, ingested_at, source_role, data_subject_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`

const insertMessageSQL = `
INSERT INTO {schema}.turn_message
    (observation_id, ordinal, role, content, group_ordinal, occurred_at)
VALUES ($1, $2, $3, $4, $5, $6)`

const insertChunkSQL = `
INSERT INTO {schema}.chunk
    (chunk_id, scope, source_observation_id, source_message_ordinal, text, occurred_at, source_role, data_subject_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`

const registerProjectionSQL = `
INSERT INTO {schema}.projection_dependency
    (source_observation_id, scope, projection_kind, projection_id, data_subject_id)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (source_observation_id, projection_kind, projection_id) DO NOTHING`

// Messages returns a turn's messages in the caller's original order.
func (s *ObservationStore) Messages(ctx context.Context, schema Schema, observationID string) ([]domain.Message, error) {
	rows, err := s.pool.Query(ctx, schema.SQL(selectMessagesSQL), observationID)
	if err != nil {
		return nil, fmt.Errorf("read messages: %w", err)
	}
	defer rows.Close()

	var out []domain.Message
	for rows.Next() {
		var m domain.Message
		var role string
		if err := rows.Scan(&m.Ordinal, &role, &m.Content, &m.GroupOrdinal, &m.OccurredAt); err != nil {
			return nil, err
		}
		m.Role = domain.Role(role)
		out = append(out, m)
	}
	return out, rows.Err()
}

const selectMessagesSQL = `
SELECT ordinal, role, content, group_ordinal, occurred_at
  FROM {schema}.turn_message
 WHERE observation_id = $1
 ORDER BY ordinal`

// ── Formation state ───────────────────────────────────────────────────────────────────────────

// NextUnformed returns the oldest turn in a scope that extraction has not run over.
//
// Oldest first, because forming out of order would let the formed watermark stall behind a turn
// nothing is working on while newer turns complete past it — and a watermark that reports a scope as
// behind when it is nearly current is one people learn to ignore.
// NextUnformed returns the oldest turn in the scope's backlog that is due for an attempt.
//
// The backlog is what is neither formed nor parked. `retryAfter` holds back a turn whose last
// attempt failed recently, so one turn that always fails cannot spin and starve the ones behind it —
// backoff belongs here rather than in a sleep, because the driver may have other scopes to serve
// while this one waits.
func (s *ObservationStore) NextUnformed(ctx context.Context, schema Schema, scope string,
	retryAfter time.Duration) (domain.Observation, bool, error) {
	var out domain.Observation
	var subject *string
	err := s.pool.QueryRow(ctx, schema.SQL(selectNextUnformedSQL), scope,
		fmt.Sprintf("%d milliseconds", retryAfter.Milliseconds())).
		Scan(&out.ID, &out.LogOffset, &out.Scope, &subject, &out.OccurredAt, &out.IngestedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Observation{}, false, nil
	}
	if err != nil {
		return domain.Observation{}, false, fmt.Errorf("find unformed turn: %w", err)
	}
	if subject != nil {
		out.DataSubjectID = *subject
	}
	return out, true, nil
}

// The turn is claimed, not merely read: the attempt is counted here, before the model is called.
//
// Counted on failure instead, an attempt that kills the worker — an out-of-memory, a pod evicted
// mid-extraction, a machine losing power — was never counted at all, so the turn came back on the
// next pass with the same count and could be retried forever. Counting at the claim
// makes every try cost one, whatever happens next, so a turn that reliably kills its worker reaches
// the attempt bound and parks like any other turn that cannot be formed.
//
// The stamp goes on with it, so the backoff below measures from when the turn was last tried rather
// than from when somebody last managed to write down why it failed.
const selectNextUnformedSQL = `
UPDATE {schema}.observation
   SET formation_attempts  = formation_attempts + 1,
       formation_failed_at = now()
 WHERE observation_id = (
   SELECT observation_id
     FROM {schema}.observation
    WHERE scope = $1 AND formed_at IS NULL AND parked_at IS NULL
      -- A turn that just failed is not retried on the very next pass, and the wait doubles with
      -- each attempt. Backoff is expressed where the backlog is chosen rather than as a sleep, so a
      -- scope with one failing turn neither spins on it nor blocks the driver from serving others.
      --
      -- It doubles because the failure that matters most is the one that is not this turn's fault:
      -- a provider outage fails every turn, and a flat retry would burn the attempt budget of the
      -- whole backlog inside a few minutes and park all of it. Doubling makes exhausting the budget
      -- take long enough that an outage has to be a real one.
      AND (formation_failed_at IS NULL
           OR formation_failed_at
              < now() - ($2::interval * power(2, least(formation_attempts - 1, 6))))
    ORDER BY log_offset
    FOR UPDATE SKIP LOCKED
    LIMIT 1)
RETURNING observation_id::text, log_offset, scope, data_subject_id, occurred_at, ingested_at`

// MarkFormed records that extraction has run over a turn, and moves the formed watermark.
//
// # Why the watermark is derived rather than incremented
//
// An increment per completion is correct only if completions happen in order. They do not: two turns
// forming concurrently finish in whichever order their model calls return, and an increment would
// let the number run past a turn that is still unformed. A caller told "current to 40" would then be
// missing turn 12, which is the one failure a freshness number must not have.
//
// So it is recomputed from the backlog: the offset below the lowest unformed turn, or the highest
// stored offset when there is no backlog at all. Two index lookups against a partial index that
// holds the backlog rather than the history.
//
// # Why the scope's watermark row is locked first
//
// The recompute reads the backlog and writes a number derived from it. Two of these interleaving
// would have one overwrite the other's read, and the loser's turn would be missing from a number
// that claims to cover it. The lock is the same row an append takes, so the two serialise — cheap,
// because this is two index probes and not a model call.
func (s *ObservationStore) MarkFormed(ctx context.Context, schema Schema, observationID string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var scope string
		err := tx.QueryRow(ctx, schema.SQL(markFormedSQL), observationID).Scan(&scope)
		if errors.Is(err, pgx.ErrNoRows) {
			// Already formed, or gone. Not an error: forming is retried after a failure that may
			// have happened after the mark, and a retry that fails on "already done" turns a
			// recovered run into a stuck one.
			return nil
		}
		if err != nil {
			return fmt.Errorf("mark formed: %w", err)
		}
		if _, err := tx.Exec(ctx, schema.SQL(lockScopeWatermarkSQL), scope); err != nil {
			return fmt.Errorf("lock scope watermark: %w", err)
		}
		if _, err := tx.Exec(ctx, schema.SQL(advanceFormedWatermarkSQL), scope); err != nil {
			return fmt.Errorf("advance formed watermark: %w", err)
		}
		return nil
	})
}

// Set once and never cleared. Re-forming a turn is a rebuild, which derives from the log rather than
// repairing rows in place.
const markFormedSQL = `
UPDATE {schema}.observation
   SET formed_at = now()
 WHERE observation_id = $1 AND formed_at IS NULL
RETURNING scope`

const lockScopeWatermarkSQL = `SELECT 1 FROM {schema}.watermark WHERE scope = $1 FOR UPDATE`

// NULL when the scope's very first turn is still unformed. Not -1: offsets start at zero, so any
// sentinel is a value in the same domain as the data, and every comparison then carries a clause to
// exclude it that one query eventually forgets.
const advanceFormedWatermarkSQL = `
UPDATE {schema}.watermark
   SET formed_offset = (
        SELECT CASE
                 WHEN backlog.lowest IS NULL THEN stored.highest
                 WHEN backlog.lowest = 0     THEN NULL
                 ELSE backlog.lowest - 1
               END
          FROM (SELECT min(log_offset) AS lowest FROM {schema}.observation
                 -- Parked turns are excluded, which is what lets the watermark advance past one
                 -- the driver gave up on rather than freezing the whole scope behind it. The
                 -- exception that creates is reported as a count, not hidden.
                 WHERE scope = $1 AND formed_at IS NULL AND parked_at IS NULL) backlog,
               (SELECT max(log_offset) AS highest FROM {schema}.observation
                 WHERE scope = $1) stored)
 WHERE scope = $1`

// Freshness reports what a scope has stored and what of it is in memory.
//
// Two numbers, because they answer two questions. An append confirms the first, and the caller
// already knows it. A recall depends on the second, and it is the one being asked about.
func (s *ObservationStore) Freshness(ctx context.Context, schema Schema, scope string) (domain.Freshness, error) {
	out := domain.Freshness{Scope: scope}
	var formed *int64
	err := s.pool.QueryRow(ctx, schema.SQL(selectFreshnessSQL), scope).Scan(&out.Stored, &formed, &out.Parked)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return domain.Freshness{}, fmt.Errorf("read freshness: %w", err)
	}
	out.HasStored = true
	if formed != nil {
		out.Formed, out.HasFormed = *formed, true
	}
	// Read after the watermark rather than joined to it. A project has at most one active rebuild
	// and almost never has any, so a join would put an outer reference to a near-always-empty table
	// in the middle of the query every client polls; a second statement against a unique partial
	// index costs an index probe and leaves the first one alone.
	var rebuild domain.Rebuild
	err = s.pool.QueryRow(ctx, schema.SQL(selectActiveRebuildSQL), scope).Scan(
		&rebuild.ReinterpretedThrough, &rebuild.ReinterpretingThrough, &rebuild.Acknowledged, &rebuild.Skipped)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// No job, so nothing to say. Absent rather than a zeroed structure: a caller testing for
		// presence is asking the right question, and one comparing counts against zero would read a
		// finished rebuild as an active one that has done nothing.
	case err != nil:
		return domain.Freshness{}, fmt.Errorf("read freshness: %w", err)
	default:
		out.Rebuilding = &rebuild
	}
	return out, nil
}

// Only `active`. A cancelled or completed job is not a reason to distrust a bundle, and reporting
// one would make the field mean "a rebuild happened here once", which is history rather than state.
// The partial unique index is what makes this at most one row without an ORDER BY to pick between
// candidates.
const selectActiveRebuildSQL = `
SELECT after_offset, through_offset, sources_rebuilt, sources_skipped
  FROM {schema}.fact_rebuild_job
 WHERE scope = $1 AND status = 'active'`

// The parked count is a subquery against a partial index holding only parked turns, which in a
// healthy scope is empty. It is read on every freshness check because a number with an unstated
// exception is worse than a number with a stated one.
const selectFreshnessSQL = `
SELECT w.log_offset,
       w.formed_offset,
       (SELECT count(*) FROM {schema}.observation o
         WHERE o.scope = w.scope AND o.parked_at IS NOT NULL)
  FROM {schema}.watermark w
 WHERE w.scope = $1`

// ── When forming does not work ────────────────────────────────────────────────────────────────

// RecordFormationFailure remembers why an attempt failed, and returns how many have been made.
//
// The attempt itself was counted when the turn was claimed, so this writes the reason and
// the time and counts nothing: an attempt that never reaches here, because it took the worker with
// it, has still been counted. The error is what makes the failure legible without reading a log. Both live on the observation so that erasure takes them
// with the turn they describe — a provider's error text can contain the message that was sent to it.
func (s *ObservationStore) RecordFormationFailure(ctx context.Context, schema Schema,
	observationID, reason string) (attempts int, err error) {

	// Truncated because the field is fed by a remote system, and an unbounded one lets that system
	// decide how much of our storage it uses. The column constraint enforces the same bound; this
	// makes the write succeed rather than fail on a long provider message.
	const maxReason = 2000
	if len(reason) > maxReason {
		reason = reason[:maxReason]
	}
	err = s.pool.QueryRow(ctx, schema.SQL(recordFormationFailureSQL), observationID, reason).Scan(&attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		// Formed or gone between the attempt and this write. Not an error: it means the work is
		// done or the subject was erased, and failing here would turn either into a stuck driver.
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("record formation failure: %w", err)
	}
	return attempts, nil
}

const recordFormationFailureSQL = `
UPDATE {schema}.observation
   SET formation_failed_at = now(),
       formation_error     = $2
 WHERE observation_id = $1 AND formed_at IS NULL AND parked_at IS NULL
RETURNING formation_attempts`

// Park stops trying to form a turn, and lets the scope move on.
//
// The observation and its messages are untouched, so this is a decision to stop rather than a
// deletion: unparking is one update and re-forming is a rebuild. The watermark is recomputed in the
// same transaction because parking is exactly what allows it to advance, and a park that did not
// advance it would leave the scope frozen with a row that says it should not be.
func (s *ObservationStore) Park(ctx context.Context, schema Schema, observationID string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var scope string
		err := tx.QueryRow(ctx, schema.SQL(parkSQL), observationID).Scan(&scope)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("park turn: %w", err)
		}
		if _, err := tx.Exec(ctx, schema.SQL(lockScopeWatermarkSQL), scope); err != nil {
			return fmt.Errorf("lock scope watermark: %w", err)
		}
		if _, err := tx.Exec(ctx, schema.SQL(advanceFormedWatermarkSQL), scope); err != nil {
			return fmt.Errorf("advance formed watermark: %w", err)
		}
		return nil
	})
}

const parkSQL = `
UPDATE {schema}.observation
   SET parked_at = now()
 WHERE observation_id = $1 AND formed_at IS NULL AND parked_at IS NULL
RETURNING scope`

// Parked reports the turns a scope gave up on, oldest first.
//
// This is what makes the watermark's exception readable rather than merely counted: an operator
// asking why a scope has parked turns gets the offsets, the attempts and the reason.
func (s *ObservationStore) Parked(ctx context.Context, schema Schema, scope string) ([]domain.ParkedTurn, error) {
	rows, err := s.pool.Query(ctx, schema.SQL(selectParkedSQL), scope)
	if err != nil {
		return nil, fmt.Errorf("list parked turns: %w", err)
	}
	defer rows.Close()

	var out []domain.ParkedTurn
	for rows.Next() {
		var p domain.ParkedTurn
		var reason *string
		if err := rows.Scan(&p.ObservationID, &p.LogOffset, &p.Attempts, &reason, &p.ParkedAt); err != nil {
			return nil, fmt.Errorf("scan parked turn: %w", err)
		}
		if reason != nil {
			p.Reason = *reason
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

const selectParkedSQL = `
SELECT observation_id::text, log_offset, formation_attempts, formation_error, parked_at
  FROM {schema}.observation
 WHERE scope = $1 AND parked_at IS NOT NULL
 ORDER BY log_offset`

// Unpark returns a turn to the backlog, clearing what it learned from failing.
//
// Kept beside Park because a decision to stop trying that cannot be reversed is not a decision, it
// is a deletion with extra steps.
func (s *ObservationStore) Unpark(ctx context.Context, schema Schema, observationID string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var scope string
		err := tx.QueryRow(ctx, schema.SQL(unparkSQL), observationID).Scan(&scope)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("unpark turn: %w", err)
		}
		if _, err := tx.Exec(ctx, schema.SQL(lockScopeWatermarkSQL), scope); err != nil {
			return fmt.Errorf("lock scope watermark: %w", err)
		}
		// Recomputed downward: the turn is in the backlog again, so a watermark that advanced past
		// it while it was parked is now claiming a turn is formed that is not.
		if _, err := tx.Exec(ctx, schema.SQL(advanceFormedWatermarkSQL), scope); err != nil {
			return fmt.Errorf("advance formed watermark: %w", err)
		}
		return nil
	})
}

const unparkSQL = `
UPDATE {schema}.observation
   SET parked_at = NULL, formation_attempts = 0, formation_failed_at = NULL, formation_error = NULL
 WHERE observation_id = $1 AND parked_at IS NOT NULL
RETURNING scope`

// ScopesWithBacklog names the scopes that have work waiting.
//
// The driver asks this rather than being told which scopes exist, because a scope is created by the
// first turn written to it and nothing announces that. One index scan over the backlog index.
func (s *ObservationStore) ScopesWithBacklog(ctx context.Context, schema Schema) ([]string, error) {
	rows, err := s.pool.Query(ctx, schema.SQL(
		`SELECT DISTINCT scope FROM {schema}.observation
		  WHERE formed_at IS NULL AND parked_at IS NULL ORDER BY scope`))
	if err != nil {
		return nil, fmt.Errorf("list scopes with backlog: %w", err)
	}
	defer rows.Close()

	var scopes []string
	for rows.Next() {
		var scope string
		if err := rows.Scan(&scope); err != nil {
			return nil, fmt.Errorf("scan scope: %w", err)
		}
		scopes = append(scopes, scope)
	}
	return scopes, rows.Err()
}

// ScopesWithRetention names projects with due stored deadlines. Current policy does not change
// deadlines already stamped on a source, and artifacts can expire under an indefinite project
// policy. One indexed existence check per project avoids rescanning a large expiry backlog merely
// to discover its project on every bounded sweep pass. Unused subject mappings also schedule expiry
// without any observations; source-backed mappings remain available while attributed bytes survive.
func (s *ObservationStore) ScopesWithRetention(ctx context.Context, schema Schema) ([]string, error) {
	rows, err := s.pool.Query(ctx, schema.SQL(
		`SELECT p.scope FROM {schema}.project p WHERE EXISTS(SELECT 1 FROM {schema}.observation o
            WHERE o.scope=p.scope AND o.retention_until<=now()) OR EXISTS(
            SELECT 1 FROM {schema}.data_subject s WHERE s.scope=p.scope AND s.inactive_after<=now()
              AND NOT EXISTS(SELECT 1 FROM {schema}.observation o WHERE o.scope=s.scope AND o.data_subject_id=s.subject_id)) ORDER BY p.scope`))
	if err != nil {
		return nil, fmt.Errorf("list scopes with retention: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var scope string
		if err := rows.Scan(&scope); err != nil {
			return nil, err
		}
		out = append(out, scope)
	}
	return out, rows.Err()
}
