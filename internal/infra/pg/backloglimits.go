// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
)

// BacklogLimits is how many turns may wait unformed, and how many are waiting.
type BacklogLimits struct {
	// MaxPending is the most unformed turns the instance holds.
	MaxPending int64 `json:"max_pending"`
	// MaxPendingPerProject is the most any one project holds. Never above MaxPending.
	MaxPendingPerProject int64 `json:"max_pending_per_project"`
	// Pending is how many are waiting now, across the instance.
	Pending int64 `json:"pending"`
}

// ErrInvalidBacklogLimits is returned for limits the budget's own constraints refuse: outside
// 1..1,000,000, or a per-project limit above the instance's.
var ErrInvalidBacklogLimits = errors.New("backlog limits are from 1 to 1000000, and a project's is at most the instance's")

// SetBacklogLimits changes one or both limits and returns what is now in force. A nil limit is
// left as it is.
//
// The bounds are not checked here. The table holds them, as constraints that refuse a hand-written
// UPDATE as readily as this one, and a second copy in Go would be the copy that drifts. A refusal
// by those constraints is returned as ErrInvalidBacklogLimits; a pair that is individually valid
// and jointly not — a per-project limit above an instance limit lowered in the same statement — is
// refused the same way, because the constraint sees the row as it would be.
//
// Lowering a limit below what is already waiting discards nothing. Writes are refused until
// formation has drained the backlog under the new limit, which is the behaviour the budget has
// always had when it was changed by hand.
func (s *ObservationStore) SetBacklogLimits(ctx context.Context, schema Schema, instance, perProject *int64) (BacklogLimits, error) {
	var out BacklogLimits
	err := s.pool.QueryRow(ctx, schema.SQL(`
		UPDATE {schema}.ingestion_budget
		   SET max_pending = COALESCE($1, max_pending),
		       max_pending_per_project = COALESCE($2, max_pending_per_project)
		 WHERE singleton
		RETURNING max_pending, max_pending_per_project, pending`), instance, perProject).
		Scan(&out.MaxPending, &out.MaxPendingPerProject, &out.Pending)
	var refused *pgconn.PgError
	if errors.As(err, &refused) && refused.Code == "23514" {
		return BacklogLimits{}, ErrInvalidBacklogLimits
	}
	if err != nil {
		return BacklogLimits{}, fmt.Errorf("set the backlog limits: %w", err)
	}
	return out, nil
}
