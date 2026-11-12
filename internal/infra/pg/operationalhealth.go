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

// RecordFormationStandDowns remembers that a pass found scopes held by another replica.
//
// Separate from RecordFormationHealth because the two are known at different moments. That one is
// called from inside a drain, with what the drain formed and what failed; a stand-down is the
// absence of a drain, so it is only countable once a whole pass has been round every scope. Written
// from the driver's pool rather than a worker's held connection for the same reason: there is no
// held connection, which is the point.
//
// A count and a time, on the row formation already writes. Nothing about which scope or which
// replica: an operator asking whether their workers are serialising is asking about the instance,
// and a scope name on this row would be the one piece of per-project state on an aggregate record.
func RecordFormationStandDowns(ctx context.Context, db HealthExecutor, schema Schema, busy int64) error {
	if busy < 0 {
		return errors.New("negative formation stand-down count")
	}
	if busy == 0 {
		// Nothing stood down, so nothing to say. Not an update that writes a zero: the time means
		// "a pass last stood down then", and touching it on a pass that did not would make it mean
		// "a pass last ran then", which the heartbeat already says.
		return nil
	}
	tag, err := db.Exec(ctx, schema.SQL(`UPDATE {schema}.formation_health
 SET stood_down_count=stood_down_count+$1,last_stood_down_at=clock_timestamp() WHERE singleton`), busy)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("formation health row missing")
	}
	return nil
}

// ErrSchemaBehind is returned when the database has not yet been migrated to the version a binary
// was built for.
var ErrSchemaBehind = errors.New("the schema is older than this binary was built for")

// CheckSchemaBuiltFor refuses a schema older than the version this binary was built to run against.
//
// # What it is for
//
// An upgrade starts new processes and a migration at the same moment and nothing orders them. A
// process that reaches the database first used to report ready, because the checks beside this one
// ask whether tables exist and they do, and then serve against a schema missing whatever its own
// release added. With this in the readiness check it reports not ready instead, takes no traffic,
// and becomes ready on its own when the migration commits — the check is asked again on every
// probe, so nothing has to restart.
//
// # Why it is one-sided
//
// Older than the binary is unambiguous: the binary cannot work without what it was built for. Newer
// than the binary is a process from the previous release still serving after the migration, and
// that is safe by a promise held elsewhere: a release's schema is one the previous release can
// serve on, and scripts/upgrade-check.sh runs the previous release on every migration to keep it so
// (D58). Refusing a newer schema here would take every old process out of service the instant the
// migration commits, before a new one has been probed — a moment with nothing ready, on every
// upgrade, to guard against something that check already refuses.
//
// What that leaves is a process two or more releases old, which the promise does not cover and this
// does not refuse (#257).
//
// A namespace with no record of migrations at all is behind by everything.
func CheckSchemaBuiltFor(ctx context.Context, pool *pgxpool.Pool, schema Schema, builtFor int) error {
	var applied int
	if err := pool.QueryRow(ctx, schema.SQL(
		`SELECT coalesce(max(version), 0) FROM {schema}.schema_migration`)).Scan(&applied); err != nil {
		var missing *pgconn.PgError
		if errors.As(err, &missing) && (missing.Code == "42P01" || missing.Code == "3F000") {
			return fmt.Errorf("%w: nothing has been migrated yet and this binary was built for version %d; "+
				"waiting for the migration", ErrSchemaBehind, builtFor)
		}
		return fmt.Errorf("read the schema version: %w", err)
	}
	if applied < builtFor {
		return fmt.Errorf("%w: it is at version %d and this binary was built for version %d; "+
			"waiting for the migration", ErrSchemaBehind, applied, builtFor)
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
	// StoodDown is how many times a pass found a scope held by another replica, and LastStoodDownAt
	// when that last happened. Together they are the only evidence the deployment produces that the
	// per-scope lock is serialising several workers — a replica standing down is a replica doing
	// what the topology intends, and it was the one that said nothing.
	StoodDown       int64      `json:"stood_down"`
	LastStoodDownAt *time.Time `json:"last_stood_down_at"`
	// FailingProjects is how many scopes hold a turn that has been tried and did not form.
	//
	// The stamps above are one pair for the whole instance, so they cannot answer this: any project
	// forming a turn moves last_progress_at, which reads as recovery while another project fails
	// every attempt. An instance with one busy project and one broken one read as healthy on the
	// page an operator is told to watch.
	//
	// Counted from the backlog rather than from a per-scope row, which needs no schema change and
	// nothing on the formation path: a turn that formed has left the backlog and one the driver gave
	// up on is parked, so a waiting turn stamped with a failure is work still owed that has already
	// gone wrong once. observation_backlog_idx holds exactly those rows, and in a healthy instance
	// it is empty.
	FailingProjects     int64 `json:"failing_projects"`
	DatabaseConnections int64 `json:"database_connections"`
	ClusterConnections  int64 `json:"cluster_connections"`
	MaxConnections      int64 `json:"max_connections"`
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
 h.stood_down_count,h.last_stood_down_at,
 (SELECT count(DISTINCT scope) FROM {schema}.observation
   WHERE formed_at IS NULL AND parked_at IS NULL AND formation_failed_at IS NOT NULL),
 (SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND backend_type='client backend'),
 (SELECT count(*) FROM pg_stat_activity WHERE backend_type='client backend'),
 current_setting('max_connections')::bigint,
 current_setting('superuser_reserved_connections')::bigint
 FROM {schema}.ingestion_budget b CROSS JOIN {schema}.formation_health h WHERE b.singleton AND h.singleton`), int64(FormationHealthMaxAge/time.Second)).Scan(
		&out.Pending, &out.PendingLimit, &out.ProjectPendingLimit, &out.Parked, &out.OldestPendingAt, &out.HeartbeatAt, &out.WorkerResponsive,
		&out.LastProgressAt, &out.LastFailureAt, &out.FormedCount, &out.FailedAttempts,
		&out.StoodDown, &out.LastStoodDownAt, &out.FailingProjects,
		&out.DatabaseConnections, &out.ClusterConnections, &out.MaxConnections, &out.ReservedConnections)
	if err != nil {
		return out, fmt.Errorf("read operational health: %w", err)
	}
	return out, nil
}
