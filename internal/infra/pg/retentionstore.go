// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ensera-ai/taisce/internal/domain"
)

// Retention is erasure with a clock instead of a request.
//
// # Why the observation expires and takes everything with it
//
// The log is authoritative and every projection derives from it. Expiring a fact on its own schedule
// would let it outlive the message that produced it, and its citation would then resolve against
// nothing — a fact whose receipt cannot be produced, which is precisely what this product does not
// offer. So the observation is what expires, and the projections registered against it go with it.
//
// # Why the date is stamped at write time and not computed at sweep time
//
// Stamped, the policy that applied when a turn arrived is the policy that governs it, and the date is
// auditable: an operator can see when a row will go rather than deriving it from the current setting.
//
// Computed at sweep time, shortening a policy would retroactively delete data that was within policy
// yesterday — a configuration change that destroys memory, discovered by whoever changed it. That is
// the same failure the memory surfaces refuse, and it gets the same answer: shortening a policy
// applies to what arrives next, and removing what is already held is an erasure with a receipt.
//
// # Why a sweep produces a receipt
//
// A retention sweep that deletes quietly is indistinguishable from data loss. It produces what an
// erasure produces — what went, per kind — so that "memory disappeared" is answerable rather than
// alarming.
type RetentionStore struct {
	pool   *pgxpool.Pool
	schema Schema
}

func NewRetentionStore(pool *pgxpool.Pool, schema Schema) *RetentionStore {
	return &RetentionStore{pool: pool, schema: schema}
}

// Sweep removes what has expired in one project, and reports what went.
//
// The same walk erasure makes, keyed on the observation rather than on a data subject: read the
// registered projections, delete them, delete the observation, count. One transaction per sweep, so a
// crash midway leaves the whole thing undone rather than a fact whose evidence is gone.
func (s *RetentionStore) Sweep(ctx context.Context, scope string, limit int) (domain.RetentionSweep, error) {
	if limit <= 0 {
		limit = 100
	}
	out := domain.RetentionSweep{Scope: scope, Deleted: map[string]int{}, SweptAt: time.Now().UTC()}

	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		// Locked as it is selected, so two sweeps — or a sweep and a driver — cannot both take the
		// same turn. SKIP LOCKED rather than waiting: the other holder is already deleting it.
		rows, err := tx.Query(ctx, s.schema.SQL(`
			SELECT observation_id::text FROM {schema}.observation
			 WHERE scope = $1 AND retention_until IS NOT NULL AND retention_until <= now()
			 ORDER BY retention_until,log_offset
			 LIMIT $2
			 FOR UPDATE SKIP LOCKED`), scope, limit)
		if err != nil {
			return fmt.Errorf("find expired observations: %w", err)
		}
		var expired []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			expired = append(expired, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if len(expired) == 0 {
			if err := expireUnusedSubjects(ctx, tx, s.schema, scope, limit, out.Deleted); err != nil {
				return err
			}
			return recordSweepTx(ctx, tx, s.schema, &out)
		}
		kinds, err := declaredKinds(ctx, tx, s.schema)
		if err != nil {
			return err
		}
		for _, k := range kinds {
			shared := ""
			if k.survivesSharing {
				shared = ` AND NOT EXISTS (SELECT 1 FROM {schema}.projection_dependency other
                    WHERE other.scope=d.scope AND other.projection_kind=d.projection_kind
                      AND other.projection_id=d.projection_id
                      AND NOT (other.source_observation_id=ANY($1::uuid[])))`
			}
			// Interpolated from `projection_kind`, whose constraints permit nothing that is not an
			// identifier — the same safety erasure relies on.
			tag, err := tx.Exec(ctx, s.schema.SQL(fmt.Sprintf(`
				DELETE FROM {schema}.%s p
				 WHERE p.%s::text IN (
				       SELECT d.projection_id FROM {schema}.projection_dependency d
				        WHERE d.source_observation_id = ANY($1::uuid[])
				          AND d.projection_kind = '%s'%s)`, k.table, k.idCol, k.kind, shared)), expired)
			if err != nil {
				return fmt.Errorf("expire %s: %w", k.kind, err)
			}
			out.Deleted[k.kind] = int(tag.RowsAffected())
		}

		// Count registered aggregate projections before changing a source-owned name input can
		// invalidate them through its trigger.
		names, err := removeEntityNamesTx(ctx, tx, s.schema, scope, "", expired)
		if err != nil {
			return err
		}
		out.Deleted["entity_name_receipt"] = names

		// A jointly supported fact can remain, but its expired source quote cannot.
		if _, err := tx.Exec(ctx, s.schema.SQL(`DELETE FROM {schema}.fact_evidence WHERE source_observation_id=ANY($1::uuid[])`), expired); err != nil {
			return fmt.Errorf("expire evidence: %w", err)
		}
		if _, err := tx.Exec(ctx, s.schema.SQL(
			`DELETE FROM {schema}.projection_dependency WHERE source_observation_id = ANY($1::uuid[])`),
			expired); err != nil {
			return fmt.Errorf("expire projection registrations: %w", err)
		}
		if _, err := tx.Exec(ctx, s.schema.SQL(
			`DELETE FROM {schema}.turn_message WHERE observation_id = ANY($1::uuid[])`), expired); err != nil {
			return fmt.Errorf("expire messages: %w", err)
		}
		tag, err := tx.Exec(ctx, s.schema.SQL(
			`DELETE FROM {schema}.observation WHERE observation_id = ANY($1::uuid[])`), expired)
		if err != nil {
			return fmt.Errorf("expire observations: %w", err)
		}
		out.Deleted["observation"] = int(tag.RowsAffected())
		out.Observations = len(expired)
		if err := expireUnusedSubjects(ctx, tx, s.schema, scope, limit, out.Deleted); err != nil {
			return err
		}
		return recordSweepTx(ctx, tx, s.schema, &out)
	})
	if err != nil {
		return domain.RetentionSweep{}, err
	}
	return out, nil
}

// recordSweepTx writes the receipt for what this sweep deleted, in the transaction that deleted it.
//
// In the transaction deliberately: a receipt written afterwards is one a crash can lose, and a
// sweep that deleted without recording it is the defect this fixes. A sweep that found
// nothing writes nothing, because a receipt per quiet tick would bury the ones that say something.
func recordSweepTx(ctx context.Context, tx pgx.Tx, schema Schema, out *domain.RetentionSweep) error {
	if out.Empty() {
		return nil
	}
	deleted, err := json.Marshal(out.Deleted)
	if err != nil {
		return fmt.Errorf("encode what the sweep deleted: %w", err)
	}
	if err := tx.QueryRow(ctx, schema.SQL(
		`INSERT INTO {schema}.retention_sweep (scope, observations, deleted)
		 VALUES ($1, $2, $3) RETURNING sweep_id::text`),
		out.Scope, out.Observations, deleted).Scan(&out.ID); err != nil {
		return fmt.Errorf("record the retention sweep: %w", err)
	}
	return nil
}
