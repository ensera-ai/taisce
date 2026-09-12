// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// FormationHealthMaxAge allows one five-minute turn attempt plus scheduling overhead.
// A current heartbeat proves a responsive worker, not inference quality or completion of every scope.
const FormationHealthMaxAge = 330 * time.Second

type HealthExecutor interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

// RecordFormationHealth uses the worker's already-held connection when draining a scope.
// No caller-supplied identifiers or error strings enter this aggregate record.
func RecordFormationHealth(ctx context.Context, db HealthExecutor, schema Schema, formed, failed int64) error {
	if formed < 0 || failed < 0 {
		return errors.New("negative formation health delta")
	}
	tag, err := db.Exec(ctx, schema.SQL(`UPDATE {schema}.formation_health
 SET heartbeat_at=clock_timestamp(),formed_count=formed_count+$1,failed_attempts=failed_attempts+$2,
 last_progress_at=CASE WHEN $1>0 THEN clock_timestamp() ELSE last_progress_at END,
 last_failure_at=CASE WHEN $2>0 THEN clock_timestamp() ELSE last_failure_at END WHERE singleton`), formed, failed)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("formation health row missing")
	}
	return nil
}

// CheckServingHealth performs bounded-by-caller checks using the actual serving identities.
// The registry can be nil for a worker, which does not authenticate client requests.
func CheckServingHealth(ctx context.Context, memory, registry *pgxpool.Pool, schema Schema, requireFormation bool) error {
	var present bool
	if err := memory.QueryRow(ctx, schema.SQL(`SELECT singleton FROM {schema}.ingestion_budget WHERE singleton`)).Scan(&present); err != nil {
		return err
	}
	if registry != nil {
		if _, err := registry.Exec(ctx, `SELECT 1 FROM control.credential LIMIT 0`); err != nil {
			return err
		}
	}
	if requireFormation {
		var alive bool
		if err := memory.QueryRow(ctx, schema.SQL(`SELECT coalesce(heartbeat_at >= clock_timestamp()-($1::bigint*interval '1 second'),false)
 FROM {schema}.formation_health WHERE singleton`), int64(FormationHealthMaxAge/time.Second)).Scan(&alive); err != nil {
			return err
		}
		if !alive {
			return errors.New("formation heartbeat missing or stale")
		}
	}
	return nil
}

type OperationalHealth struct {
	Pending             int64      `json:"pending"`
	PendingLimit        int64      `json:"pending_limit"`
	ProjectPendingLimit int64      `json:"project_pending_limit"`
	Parked              int64      `json:"parked"`
	OldestPendingAt     *time.Time `json:"oldest_pending_at"`
	HeartbeatAt         *time.Time `json:"heartbeat_at"`
	WorkerResponsive    bool       `json:"worker_responsive"`
	LastProgressAt      *time.Time `json:"last_progress_at"`
	LastFailureAt       *time.Time `json:"last_failure_at"`
	FormedCount         int64      `json:"formed_count"`
	FailedAttempts      int64      `json:"failed_attempts"`
	DatabaseConnections int64      `json:"database_connections"`
	ClusterConnections  int64      `json:"cluster_connections"`
	MaxConnections      int64      `json:"max_connections"`
	// ReservedConnections is what the server keeps for superuser recovery. Reported beside
	// MaxConnections because an operator comparing demand against capacity who subtracts nothing is
	// reading a ceiling that is not there, and finds out at the moment they most need to get in.
	ReservedConnections int64 `json:"reserved_connections"`
}

// ConnectionHeadroom is how many client sessions could still be opened before the server refuses.
//
// Reported rather than left to be computed, because the deployment-level failure this exists for is
// exactly the one nobody computes: each process sizes its own pool, the chart chooses how many
// processes, and the substrate sets the ceiling — and nothing compared the three until a full server made it matter. A
// negative result is impossible on a live server and is clamped rather than shown, since a number
// below zero would read as a different kind of fault than "full".
func (h OperationalHealth) ConnectionHeadroom() int64 {
	headroom := h.MaxConnections - h.ReservedConnections - h.ClusterConnections
	if headroom < 0 {
		return 0
	}
	return headroom
}

// ReadOperationalHealth is for an operator connection, never the unauthenticated HTTP probe.
// It returns one fixed-size aggregate snapshot without project labels or stored error text.
func ReadOperationalHealth(ctx context.Context, pool *pgxpool.Pool, schema Schema) (OperationalHealth, error) {
	var out OperationalHealth
	err := pool.QueryRow(ctx, schema.SQL(`SELECT b.pending,b.max_pending,b.max_pending_per_project,
 (SELECT count(*) FROM {schema}.observation WHERE parked_at IS NOT NULL),
 (SELECT ingested_at FROM {schema}.observation WHERE kind='turn' AND formed_at IS NULL ORDER BY ingested_at LIMIT 1),
 h.heartbeat_at,coalesce(h.heartbeat_at>=clock_timestamp()-($1::bigint*interval '1 second'),false),
 h.last_progress_at,h.last_failure_at,h.formed_count,h.failed_attempts,
 (SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND backend_type='client backend'),
 (SELECT count(*) FROM pg_stat_activity WHERE backend_type='client backend'),
 current_setting('max_connections')::bigint,
 current_setting('superuser_reserved_connections')::bigint
 FROM {schema}.ingestion_budget b CROSS JOIN {schema}.formation_health h WHERE b.singleton AND h.singleton`), int64(FormationHealthMaxAge/time.Second)).Scan(
		&out.Pending, &out.PendingLimit, &out.ProjectPendingLimit, &out.Parked, &out.OldestPendingAt, &out.HeartbeatAt, &out.WorkerResponsive,
		&out.LastProgressAt, &out.LastFailureAt, &out.FormedCount, &out.FailedAttempts, &out.DatabaseConnections, &out.ClusterConnections, &out.MaxConnections, &out.ReservedConnections)
	if err != nil {
		return out, fmt.Errorf("read operational health: %w", err)
	}
	return out, nil
}
