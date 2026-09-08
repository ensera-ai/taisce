// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrRebuildBusy = errors.New("a project rebuild is already active")
var ErrRebuildFenced = errors.New("project rebuild runner no longer owns the operation")
var ErrRebuildNotFound = errors.New("project rebuild not found")
var ErrRebuildPoolCapacity = errors.New("project rebuild requires a database pool with at least two connections")

type RebuildJob struct {
	ID              string    `json:"job_id"`
	Version         string    `json:"extractor_version"`
	Through         int64     `json:"through_offset"`
	After           int64     `json:"after_offset"`
	Pending         *int64    `json:"pending_offset,omitempty"`
	Rebuilt         int64     `json:"sources_rebuilt"`
	Skipped         int64     `json:"sources_skipped"`
	Status          string    `json:"status"`
	CancelRequested bool      `json:"cancel_requested"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	lease           string
}

type RebuildJobStore struct{ pool *pgxpool.Pool }

func NewRebuildJobStore(pool *pgxpool.Pool) *RebuildJobStore { return &RebuildJobStore{pool: pool} }

// The connection holds only a session lock while the model runs, never an open transaction.
// A replacement runner changes the persisted lease so a disconnected predecessor cannot publish.
type RebuildJobSession struct {
	store                   *RebuildJobStore
	conn                    *pgxpool.Conn
	schema                  Schema
	scope, key, lease, lock string
}

type GenerationJobGuard struct {
	JobID, LeaseID string
	Offset         int64
}
type RebuildWork struct {
	SourceID, Key   string
	Offset          int64
	CancelRequested bool
}

const readRebuildJobSQL = `SELECT job_id::text,extractor_version,through_offset,after_offset,pending_offset,
    sources_rebuilt,sources_skipped,status,cancel_requested,created_at,updated_at,lease_id::text
    FROM {schema}.fact_rebuild_job WHERE scope=$1 AND job_id=$2::uuid`

func scanRebuildJob(row pgx.Row) (RebuildJob, error) {
	var j RebuildJob
	err := row.Scan(&j.ID, &j.Version, &j.Through, &j.After, &j.Pending, &j.Rebuilt, &j.Skipped, &j.Status, &j.CancelRequested, &j.CreatedAt, &j.UpdatedAt, &j.lease)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrRebuildNotFound
	}
	return j, err
}

func validRebuildJob(scope, key string) error {
	if _, err := NewSchema(scope); err != nil {
		return ErrInvalidGeneration
	}
	id, err := uuid.Parse(key)
	if err != nil || id == uuid.Nil {
		return ErrInvalidGeneration
	}
	return nil
}

func (s *RebuildJobStore) Status(ctx context.Context, schema Schema, scope, key string) (RebuildJob, error) {
	if err := validRebuildJob(scope, key); err != nil {
		return RebuildJob{}, err
	}
	return scanRebuildJob(s.pool.QueryRow(ctx, schema.SQL(readRebuildJobSQL), scope, key))
}

func (s *RebuildJobStore) Acquire(ctx context.Context, schema Schema, scope, key, version, actor string) (*RebuildJobSession, error) {
	if err := validGenerationRequest(scope, key, actor, version); err != nil {
		return nil, err
	}
	if s.pool.Config().MaxConns < 2 {
		return nil, ErrRebuildPoolCapacity
	}
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	session := &RebuildJobSession{store: s, conn: conn, schema: schema, scope: scope, key: key, lease: uuid.NewString(), lock: schema.String() + "/project-rebuild/" + scope}
	var locked bool
	if err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1,0))`, session.lock).Scan(&locked); err != nil || !locked {
		conn.Release()
		if err != nil {
			return nil, err
		}
		return nil, ErrRebuildBusy
	}
	err = pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		j, err := scanRebuildJob(tx.QueryRow(ctx, schema.SQL(readRebuildJobSQL+` FOR UPDATE`), scope, key))
		if errors.Is(err, ErrRebuildNotFound) {
			_, err = tx.Exec(ctx, schema.SQL(`INSERT INTO {schema}.fact_rebuild_job(scope,job_id,extractor_version,through_offset,lease_id)
                VALUES($1,$2::uuid,$3,coalesce((SELECT log_offset FROM {schema}.watermark WHERE scope=$1),-1),$4::uuid)`), scope, key, version, session.lease)
			if err != nil {
				return err
			}
			_, err = tx.Exec(ctx, schema.SQL(`INSERT INTO {schema}.audit_entry(operation,principal,principal_kind,project,magnitude,outcome)
                VALUES('formation.rebuild.start',$1::uuid,'operator',$2,0,'allowed')`), actor, scope)
			return err
		}
		if err != nil {
			return err
		}
		if j.Version != version {
			return ErrGenerationConflict
		}
		_, err = tx.Exec(ctx, schema.SQL(`UPDATE {schema}.fact_rebuild_job SET lease_id=$3::uuid,updated_at=clock_timestamp() WHERE scope=$1 AND job_id=$2::uuid`), scope, key, session.lease)
		return err
	})
	if err != nil {
		session.Close()
		var pgerr *pgconn.PgError
		if errors.As(err, &pgerr) && pgerr.ConstraintName == "one_active_fact_rebuild" {
			err = ErrRebuildBusy
		}
		return nil, err
	}
	return session, nil
}

func (s *RebuildJobSession) Close() {
	if s.conn == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := s.conn.Exec(ctx, `SELECT pg_advisory_unlock(hashtextextended($1,0))`, s.lock); err != nil {
		_ = s.conn.Conn().Close(ctx)
	}
	s.conn.Release()
	s.conn = nil
}

func (s *RebuildJobSession) Guard(offset int64) *GenerationJobGuard {
	return &GenerationJobGuard{JobID: s.key, LeaseID: s.lease, Offset: offset}
}

func sourceRebuildKey(job string, offset int64) string {
	return uuid.NewSHA1(uuid.MustParse(job), []byte("fact-generation/"+strconv.FormatInt(offset, 10))).String()
}

func (s *RebuildJobSession) lockedJob(ctx context.Context, tx pgx.Tx) (RebuildJob, error) {
	j, err := scanRebuildJob(tx.QueryRow(ctx, s.schema.SQL(readRebuildJobSQL+` FOR UPDATE`), s.scope, s.key))
	if err == nil && j.lease != s.lease {
		err = ErrRebuildFenced
	}
	return j, err
}

// Reserving an offset is the cancellation boundary. A pending source may finish; cancellation
// prevents reserving a later one. The offset survives erasure without retaining an observation ID.
func (s *RebuildJobSession) Next(ctx context.Context) (RebuildWork, bool, error) {
	var work RebuildWork
	var found bool
	err := pgx.BeginFunc(ctx, s.store.pool, func(tx pgx.Tx) error {
		j, err := s.lockedJob(ctx, tx)
		if err != nil {
			return err
		}
		if j.Status != "active" {
			return nil
		}
		if j.Pending == nil && j.CancelRequested {
			_, err = tx.Exec(ctx, s.schema.SQL(`UPDATE {schema}.fact_rebuild_job SET status='cancelled',updated_at=clock_timestamp() WHERE scope=$1 AND job_id=$2::uuid`), s.scope, s.key)
			return err
		}
		if j.Pending != nil {
			work.Offset = *j.Pending
			err = tx.QueryRow(ctx, s.schema.SQL(`SELECT observation_id::text FROM {schema}.observation WHERE scope=$1 AND log_offset=$2`), s.scope, work.Offset).Scan(&work.SourceID)
			if errors.Is(err, pgx.ErrNoRows) {
				err = nil
			}
		} else {
			err = tx.QueryRow(ctx, s.schema.SQL(`SELECT o.observation_id::text,o.log_offset FROM {schema}.observation o
                WHERE o.scope=$1 AND o.log_offset>$2 AND o.log_offset<=$3 AND o.kind='turn'
                AND NOT EXISTS(SELECT 1 FROM {schema}.curated_claim c WHERE c.scope=o.scope AND c.source_observation_id=o.observation_id)
                ORDER BY o.log_offset LIMIT 1`), s.scope, j.After, j.Through).Scan(&work.SourceID, &work.Offset)
			if errors.Is(err, pgx.ErrNoRows) {
				_, err = tx.Exec(ctx, s.schema.SQL(`UPDATE {schema}.fact_rebuild_job SET status='completed',after_offset=through_offset,updated_at=clock_timestamp() WHERE scope=$1 AND job_id=$2::uuid`), s.scope, s.key)
				return err
			}
			if err == nil {
				_, err = tx.Exec(ctx, s.schema.SQL(`UPDATE {schema}.fact_rebuild_job SET pending_offset=$3,updated_at=clock_timestamp() WHERE scope=$1 AND job_id=$2::uuid`), s.scope, s.key, work.Offset)
			}
		}
		if err != nil {
			return err
		}
		work.Key = sourceRebuildKey(s.key, work.Offset)
		work.CancelRequested = j.CancelRequested
		found = true
		return nil
	})
	return work, found, err
}

func (s *RebuildJobSession) Complete(ctx context.Context, offset int64, rebuilt bool) error {
	return pgx.BeginFunc(ctx, s.store.pool, func(tx pgx.Tx) error {
		j, err := s.lockedJob(ctx, tx)
		if err != nil {
			return err
		}
		if j.Pending == nil && j.After >= offset {
			return nil
		}
		if j.Pending == nil || *j.Pending != offset {
			return ErrRebuildFenced
		}
		_, err = tx.Exec(ctx, s.schema.SQL(`UPDATE {schema}.fact_rebuild_job SET after_offset=$3,pending_offset=NULL,
            sources_rebuilt=sources_rebuilt+CASE WHEN $4 THEN 1 ELSE 0 END,
            sources_skipped=sources_skipped+CASE WHEN $4 THEN 0 ELSE 1 END,
            status=CASE WHEN cancel_requested THEN 'cancelled' ELSE 'active' END,updated_at=clock_timestamp()
            WHERE scope=$1 AND job_id=$2::uuid`), s.scope, s.key, offset, rebuilt)
		return err
	})
}

func (s *RebuildJobStore) Cancel(ctx context.Context, schema Schema, scope, key, actor string) (RebuildJob, error) {
	if err := validRebuildJob(scope, key); err != nil {
		return RebuildJob{}, err
	}
	if id, err := uuid.Parse(actor); err != nil || id == uuid.Nil {
		return RebuildJob{}, ErrInvalidGeneration
	}
	var result RebuildJob
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		j, err := scanRebuildJob(tx.QueryRow(ctx, schema.SQL(readRebuildJobSQL+` FOR UPDATE`), scope, key))
		if err != nil {
			return err
		}
		if j.Status != "active" {
			result = j
			return nil
		}
		// A disconnected runner cannot drain its pending checkpoint. Take the project lock only
		// if it is free, reconcile any committed publication, and cancel without needing its model.
		var idle bool
		if err = tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1,0))`, schema.String()+"/project-rebuild/"+scope).Scan(&idle); err != nil {
			return err
		}
		if idle && j.Pending != nil {
			var source string
			err = tx.QueryRow(ctx, schema.SQL(`SELECT o.observation_id::text FROM {schema}.observation o
                WHERE o.scope=$1 AND o.log_offset=$2 AND EXISTS(SELECT 1 FROM {schema}.fact_generation g
                WHERE g.scope=o.scope AND g.source_observation_id=o.observation_id AND g.operation_key=$3::uuid)
                FOR UPDATE OF o`), scope, *j.Pending, sourceRebuildKey(key, *j.Pending)).Scan(&source)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			published := source != ""
			if published {
				if err := finishGenerationTx(ctx, tx, schema, scope, source); err != nil {
					return err
				}
			}
			if _, err = tx.Exec(ctx, schema.SQL(`UPDATE {schema}.fact_rebuild_job SET after_offset=pending_offset,pending_offset=NULL,
                sources_rebuilt=sources_rebuilt+CASE WHEN $3 THEN 1 ELSE 0 END,
                sources_skipped=sources_skipped+CASE WHEN $3 THEN 0 ELSE 1 END WHERE scope=$1 AND job_id=$2::uuid`), scope, key, published); err != nil {
				return err
			}
		}
		if _, err = tx.Exec(ctx, schema.SQL(`UPDATE {schema}.fact_rebuild_job SET cancel_requested=true,
            status=CASE WHEN pending_offset IS NULL THEN 'cancelled' ELSE status END,updated_at=clock_timestamp()
            WHERE scope=$1 AND job_id=$2::uuid`), scope, key); err != nil {
			return err
		}
		if !j.CancelRequested {
			if _, err = tx.Exec(ctx, schema.SQL(`INSERT INTO {schema}.audit_entry(operation,principal,principal_kind,project,magnitude,outcome)
            VALUES('formation.rebuild.cancel',$1::uuid,'operator',$2,0,'allowed')`), actor, scope); err != nil {
				return err
			}
		}
		result, err = scanRebuildJob(tx.QueryRow(ctx, schema.SQL(readRebuildJobSQL), scope, key))
		return err
	})
	return result, err
}

// Take the job row before source locks. Cancel only changes the job row; erasure never needs it.
func validateGenerationJobTx(ctx context.Context, tx pgx.Tx, schema Schema, snapshot GenerationSnapshot, key, version string, g *GenerationJobGuard) error {
	j, err := scanRebuildJob(tx.QueryRow(ctx, schema.SQL(readRebuildJobSQL+` FOR UPDATE`), snapshot.Scope, g.JobID))
	if err != nil {
		return err
	}
	if j.Status != "active" || j.lease != g.LeaseID || j.Pending == nil || *j.Pending != g.Offset || g.Offset != snapshot.LogOffset || j.Version != version || sourceRebuildKey(j.ID, g.Offset) != key {
		return ErrRebuildFenced
	}
	return nil
}
