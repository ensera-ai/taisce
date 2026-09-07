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
	if err := turn.Validate(); err != nil {
		return domain.Observation{}, err
	}

	var out domain.Observation
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		// The lock is on the scope's watermark row, which every append to this scope contends for
		// and no append to another scope touches. Taken by upserting it, so the first append to a
		// new scope does not need the row to exist already.
		var offset int64
		if err := tx.QueryRow(ctx, schema.SQL(claimOffsetSQL), turn.Scope, turn.OccurredAt).Scan(&offset); err != nil {
			return fmt.Errorf("claim log offset: %w", err)
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
			return fmt.Errorf("insert observation: %w", err)
		}

		for _, m := range turn.Messages {
			occurred := m.OccurredAt
			if occurred.IsZero() {
				occurred = turn.OccurredAt
			}
			if _, err := tx.Exec(ctx, schema.SQL(insertMessageSQL),
				id, m.Ordinal, string(m.Role), m.Content, m.GroupOrdinal, occurred); err != nil {
				return fmt.Errorf("insert message %d: %w", m.Ordinal, err)
			}

			chunkID := uuid.New()
			if _, err := tx.Exec(ctx, schema.SQL(insertChunkSQL),
				chunkID, turn.Scope, id, m.Ordinal, m.Content, occurred,
				string(m.Role), nullIfEmpty(turn.DataSubjectID)); err != nil {
				return fmt.Errorf("insert chunk for message %d: %w", m.Ordinal, err)
			}
			// Registered in the SAME transaction as the row it describes. A projection written now
			// and registered afterwards is a projection erasure cannot see for as long as the gap
			// lasts, and the gap is exactly the window in which a process dies.
			if _, err := tx.Exec(ctx, schema.SQL(registerProjectionSQL),
				id, turn.Scope, ProjectionChunk, chunkID.String(),
				nullIfEmpty(turn.DataSubjectID)); err != nil {
				return fmt.Errorf("register chunk projection: %w", err)
			}
		}

		out = domain.Observation{
			ID:            id.String(),
			LogOffset:     offset,
			Scope:         turn.Scope,
			DataSubjectID: turn.DataSubjectID,
			OccurredAt:    turn.OccurredAt,
			IngestedAt:    ingested,
		}
		return nil
	})
	return out, err
}

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
func (s *ObservationStore) NextUnformed(ctx context.Context, schema Schema, scope string) (domain.Observation, bool, error) {
	var out domain.Observation
	var subject *string
	err := s.pool.QueryRow(ctx, schema.SQL(selectNextUnformedSQL), scope).
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

const selectNextUnformedSQL = `
SELECT observation_id::text, log_offset, scope, data_subject_id, occurred_at, ingested_at
  FROM {schema}.observation
 WHERE scope = $1 AND formed_at IS NULL
 ORDER BY log_offset
 LIMIT 1`

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
                 WHERE scope = $1 AND formed_at IS NULL) backlog,
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
	err := s.pool.QueryRow(ctx, schema.SQL(selectFreshnessSQL), scope).Scan(&out.Stored, &formed)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Freshness{}, fmt.Errorf("scope %q has no turns", scope)
	}
	if err != nil {
		return domain.Freshness{}, fmt.Errorf("read freshness: %w", err)
	}
	if formed != nil {
		out.Formed, out.HasFormed = *formed, true
	}
	return out, nil
}

const selectFreshnessSQL = `
SELECT log_offset, formed_offset FROM {schema}.watermark WHERE scope = $1`
