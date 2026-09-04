package pg

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/taisce/taisce/internal/domain"
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
			ID:         id.String(),
			LogOffset:  offset,
			Scope:      turn.Scope,
			OccurredAt: turn.OccurredAt,
			IngestedAt: ingested,
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
