// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ParkedMetadata intentionally cannot carry provider errors, subjects or conversation text.
type ParkedMetadata struct {
	ObservationID string    `json:"observation_id"`
	LogOffset     int64     `json:"log_offset"`
	Attempts      int       `json:"attempts"`
	ParkedAt      time.Time `json:"parked_at"`
}

type ParkedPage struct {
	Items     []ParkedMetadata `json:"items"`
	NextAfter *int64           `json:"next_after,omitempty"`
}

// ParkedPage reads at most 201 metadata rows using the project/offset partial index.
// It is a live view: work parked behind a cursor appears on a fresh scan, not a later page.
func (s *ObservationStore) ParkedPage(ctx context.Context, schema Schema, scope string, after int64, limit int) (ParkedPage, error) {
	out := ParkedPage{Items: make([]ParkedMetadata, 0)}
	if _, err := NewSchema(scope); err != nil || after < -1 || limit < 1 || limit > 200 {
		return out, fmt.Errorf("invalid parked page: project required, after >= -1, limit 1..200")
	}
	rows, err := s.pool.Query(ctx, schema.SQL(`SELECT observation_id::text,log_offset,formation_attempts,parked_at
 FROM {schema}.observation WHERE scope=$1 AND parked_at IS NOT NULL AND log_offset>$2
 ORDER BY log_offset LIMIT $3`), scope, after, limit+1)
	if err != nil {
		return out, fmt.Errorf("read parked page: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var item ParkedMetadata
		if err := rows.Scan(&item.ObservationID, &item.LogOffset, &item.Attempts, &item.ParkedAt); err != nil {
			return out, err
		}
		out.Items = append(out.Items, item)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	if len(out.Items) > limit {
		cursor := out.Items[limit-1].LogOffset
		out.NextAfter = &cursor
		out.Items = out.Items[:limit]
	}
	return out, nil
}

var ErrFormationTurnNotFound = errors.New("turn not found in the requested project")

// UnparkAudited is the operator recovery boundary. The opaque principal identifies the CLI
// invocation, not an authenticated human. The database operator is the authority for this command.
// A failed audit insert rolls back recovery, unlike best-effort audit on the request read path.
func (s *ObservationStore) UnparkAudited(ctx context.Context, schema Schema, scope, observationID, principal string) (bool, error) {
	if _, err := NewSchema(scope); err != nil {
		return false, fmt.Errorf("invalid recovery project")
	}
	id, err := uuid.Parse(observationID)
	if err != nil {
		return false, fmt.Errorf("invalid observation UUID")
	}
	actor, err := uuid.Parse(principal)
	if err != nil || actor == uuid.Nil {
		return false, fmt.Errorf("invalid operator invocation UUID")
	}
	changed := false
	var refusal error
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var parked bool
		err := tx.QueryRow(ctx, schema.SQL(`SELECT parked_at IS NOT NULL FROM {schema}.observation
 WHERE scope=$1 AND observation_id=$2 AND kind='turn' FOR UPDATE`), scope, id.String()).Scan(&parked)
		outcome := domain.OutcomeAllowed
		if errors.Is(err, pgx.ErrNoRows) {
			refusal = ErrFormationTurnNotFound
			outcome = domain.OutcomeRefused
		} else if err != nil {
			return err
		}
		if parked {
			if _, err := tx.Exec(ctx, schema.SQL(`UPDATE {schema}.observation
 SET parked_at=NULL,formation_attempts=0,formation_failed_at=NULL,formation_error=NULL
 WHERE scope=$1 AND observation_id=$2`), scope, id.String()); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, schema.SQL(lockScopeWatermarkSQL), scope); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, schema.SQL(advanceFormedWatermarkSQL), scope); err != nil {
				return err
			}
			changed = true
		}
		magnitude := 0
		if changed {
			magnitude = 1
		}
		_, err = tx.Exec(ctx, schema.SQL(`INSERT INTO {schema}.audit_entry
 (operation,principal,principal_kind,project,magnitude,outcome) VALUES($1,$2,$3,$4,$5,$6)`),
			domain.AuditFormationUnpark, actor.String(), domain.PrincipalOperator, scope, magnitude, outcome)
		return err
	})
	if err != nil {
		return false, fmt.Errorf("recover parked turn: %w", err)
	}
	return changed, refusal
}
