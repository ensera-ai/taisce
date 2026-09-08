// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrInvalidIdempotencyKey = errors.New("idempotency key must be a UUID")
	ErrIdempotencyConflict   = errors.New("idempotency key already identifies a different or erased observation")
)

// AppendIdempotent returns the original receipt for the same logical write. No key means an
// intentional new observation, even if its content repeats. Keys are project-scoped UUIDs; only a
// digest is stored. A tombstone survives erasure, without the payload digest or source identifier.
func (s *ObservationStore) AppendIdempotent(ctx context.Context, schema Schema, turn domain.Turn, key string) (out domain.Observation, replayed bool, err error) {
	if err := turn.Validate(); err != nil {
		return out, false, err
	}
	var keyDigest, requestDigest [32]byte
	if key != "" {
		parsed, err := uuid.Parse(key)
		if err != nil || len(key) != 36 {
			return out, false, ErrInvalidIdempotencyKey
		}
		keyDigest = sha256.Sum256(parsed[:])
	}
	// Canonicalise caller order and explicit time zones, but fingerprint before filling omitted
	// timestamps. Otherwise a retry with no timestamp would acquire a different identity each time.
	turn.Messages = slices.Clone(turn.Messages)
	slices.SortFunc(turn.Messages, func(a, b domain.Message) int { return cmp.Compare(a.Ordinal, b.Ordinal) })
	turn.OccurredAt = turn.OccurredAt.UTC()
	for i := range turn.Messages {
		turn.Messages[i].OccurredAt = turn.Messages[i].OccurredAt.UTC()
	}
	if key != "" {
		encoded, err := json.Marshal(retryRepresentation(turn))
		if err != nil {
			return out, false, fmt.Errorf("encode retry request: %w", err)
		}
		requestDigest = sha256.Sum256(encoded)
	}
	if turn.OccurredAt.IsZero() {
		turn.OccurredAt = time.Now().UTC()
	}
	for i := range turn.Messages {
		if turn.Messages[i].OccurredAt.IsZero() {
			turn.Messages[i].OccurredAt = turn.OccurredAt
		}
	}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if key != "" {
			// This serialises only attempts sharing a key; the normal scope watermark still orders new
			// appends. A hash collision merely serialises unrelated requests; it cannot merge their keys.
			lock := fmt.Sprintf("taisce:observe-retry:%q:%q:%x", schema.String(), turn.Scope, keyDigest)
			if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lock); err != nil {
				return fmt.Errorf("lock observation retry: %w", err)
			}
			var fingerprint []byte
			var observationID *string
			err := tx.QueryRow(ctx, schema.SQL(`SELECT request_digest, observation_id::text
    FROM {schema}.observation_retry WHERE scope=$1 AND key_digest=$2 FOR UPDATE`), turn.Scope, keyDigest[:]).Scan(&fingerprint, &observationID)
			if err == nil {
				if observationID == nil || !bytes.Equal(fingerprint, requestDigest[:]) {
					return ErrIdempotencyConflict
				}
				var subject *string
				if err := tx.QueryRow(ctx, schema.SQL(`SELECT observation_id::text, log_offset, scope, data_subject_id, occurred_at, ingested_at
      FROM {schema}.observation WHERE observation_id=$1::uuid AND scope=$2`), *observationID, turn.Scope).
					Scan(&out.ID, &out.LogOffset, &out.Scope, &subject, &out.OccurredAt, &out.IngestedAt); err != nil {
					return fmt.Errorf("read retry receipt: %w", err)
				}
				if subject != nil {
					out.DataSubjectID = *subject
				}
				replayed = true
				return nil
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("read observation retry: %w", err)
			}
		}
		var err error
		out, err = appendTurnTx(ctx, tx, schema, turn)
		if err != nil {
			return err
		}
		if key != "" {
			if _, err := tx.Exec(ctx, schema.SQL(`INSERT INTO {schema}.observation_retry
    (scope,key_digest,request_digest,observation_id) VALUES ($1,$2,$3,$4::uuid)`), turn.Scope, keyDigest[:], requestDigest[:], out.ID); err != nil {
				return fmt.Errorf("write retry receipt: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return domain.Observation{}, false, err
	}
	return out, replayed, nil
}

// This persisted fingerprint format is independent of Go domain field order or future domain
// additions. Changing it requires a retry-compatibility decision, not a struct refactor.
type retryMessageV1 struct {
	Ordinal      int         `json:"ordinal"`
	Role         domain.Role `json:"role"`
	Content      string      `json:"content"`
	GroupOrdinal int         `json:"group_ordinal"`
	OccurredAt   time.Time   `json:"occurred_at"`
}
type retryTurnV1 struct {
	Version       int              `json:"version"`
	Scope         string           `json:"scope"`
	DataSubjectID string           `json:"data_subject_id"`
	OccurredAt    time.Time        `json:"occurred_at"`
	Messages      []retryMessageV1 `json:"messages"`
}

func retryRepresentation(turn domain.Turn) retryTurnV1 {
	out := retryTurnV1{Version: 1, Scope: turn.Scope, DataSubjectID: turn.DataSubjectID, OccurredAt: turn.OccurredAt}
	for _, m := range turn.Messages {
		out.Messages = append(out.Messages, retryMessageV1{Ordinal: m.Ordinal, Role: m.Role, Content: m.Content, GroupOrdinal: m.GroupOrdinal, OccurredAt: m.OccurredAt})
	}
	return out
}
