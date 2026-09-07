// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ensera-ai/taisce/internal/domain"
)

// Eraser deletes everything derived from a data subject's observations, and counts what survived.
type Eraser struct{ pool *pgxpool.Pool }

// NewEraser builds an eraser.
func NewEraser(pool *pgxpool.Pool) *Eraser { return &Eraser{pool: pool} }

// projectionKind is a declared kind and the table it lives in, read from `projection_kind`.
type projectionKind struct {
	kind  string
	table string
	idCol string
}

// Erase removes a data subject from a project and returns the receipt.
//
// # Everything in one transaction, including the count
//
// The residual is counted after the deletes and before the commit. Counted afterwards it would be a
// separate read of a moving target: a concurrent formation writing a new projection for the same
// subject would show up as a residual the erasure did not leave, or worse, a concurrent erasure of
// the same subject would make each of them report the other's work as their own clean sweep.
//
// # The order matters and is not obvious
//
// The projections are deleted through their registrations, then counted through the SAME
// registrations, and only then are the observations deleted. Deleting the observations first would
// cascade the registrations away, and the count would then be against a predicate that selects
// nothing — a residual of zero that means "I could not find anything to check" rather than "nothing
// survived".
//
// # What is deliberately NOT deleted
//
// A projection registered to an observation belonging to somebody else as well is kept. An entity is
// the case that matters: `Ensera` is registered by every subject who mentioned it, and removing it
// because one of them left would take a node out of everybody else's graph. So the predicate deletes
// only projections whose every registration belongs to this subject — and the residual is counted
// with that same predicate, so a shared entity surviving is correctly not a residual.
func (e *Eraser) Erase(ctx context.Context, schema Schema, scope, dataSubjectID, reason string) (domain.Erasure, error) {
	if dataSubjectID == "" {
		// An erasure with no subject would match every row whose data_subject_id is NULL, which is
		// every projection that was never about a person. There is no plausible caller for that and
		// one very implausible outcome.
		return domain.Erasure{}, fmt.Errorf("an erasure needs a data subject")
	}

	out := domain.Erasure{
		RequestID:     uuid.New().String(),
		Scope:         scope,
		DataSubjectID: dataSubjectID,
		Reason:        reason,
		Deleted:       map[string]int{},
		Residual:      map[string]int{},
	}

	err := pgx.BeginFunc(ctx, e.pool, func(tx pgx.Tx) error {
		kinds, err := declaredKinds(ctx, tx, schema)
		if err != nil {
			return err
		}
		if len(kinds) == 0 {
			// Nothing declared means nothing would be deleted and the residual would be an empty
			// object — a receipt that proves nothing while looking like a clean sweep.
			return fmt.Errorf("no projection kinds are declared, so an erasure would assert nothing")
		}

		if _, err := tx.Exec(ctx, schema.SQL(openErasureSQL),
			out.RequestID, scope, dataSubjectID, reason); err != nil {
			return fmt.Errorf("open erasure request: %w", err)
		}

		for _, k := range kinds {
			tag, err := tx.Exec(ctx, deleteProjectionSQL(schema, k), scope, dataSubjectID)
			if err != nil {
				return fmt.Errorf("delete %s: %w", k.kind, err)
			}
			out.Deleted[k.kind] = int(tag.RowsAffected())

			var residual int
			if err := tx.QueryRow(ctx, countResidualSQL(schema, k), scope, dataSubjectID).Scan(&residual); err != nil {
				return fmt.Errorf("count residual %s: %w", k.kind, err)
			}
			out.Residual[k.kind] = residual
		}

		// Last, because the cascade takes the registrations with it and the count above needs them.
		// The observation is the root: after this, the turn's messages are gone too, and a rebuild
		// derives only from what survived rather than repairing what did not.
		observations, err := tx.Exec(ctx, schema.SQL(deleteObservationsSQL), scope, dataSubjectID)
		if err != nil {
			return fmt.Errorf("delete observations: %w", err)
		}
		out.Deleted["observation"] = int(observations.RowsAffected())

		residual, err := json.Marshal(out.Residual)
		if err != nil {
			return err
		}
		out.CompletedAt = time.Now().UTC()
		if _, err := tx.Exec(ctx, schema.SQL(closeErasureSQL),
			out.RequestID, out.CompletedAt, residual); err != nil {
			return fmt.Errorf("close erasure request: %w", err)
		}
		return nil
	})
	return out, err
}

// declaredKinds reads the projection registry.
//
// Read per erasure rather than cached, because it is four rows and because a cache would be a copy
// of the one thing that must not be stale: a kind added by a migration and missing from an eraser's
// memory is exactly the silent gap this table exists to close.
func declaredKinds(ctx context.Context, tx pgx.Tx, schema Schema) ([]projectionKind, error) {
	rows, err := tx.Query(ctx, schema.SQL(`
		SELECT kind, projection_table, id_column FROM {schema}.projection_kind ORDER BY kind`))
	if err != nil {
		return nil, fmt.Errorf("read projection kinds: %w", err)
	}
	defer rows.Close()

	var out []projectionKind
	for rows.Next() {
		var k projectionKind
		if err := rows.Scan(&k.kind, &k.table, &k.idCol); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

const openErasureSQL = `
INSERT INTO {schema}.erasure_request (request_id, scope, selector, reason)
VALUES ($1, $2, jsonb_build_object('data_subject_id', $3::text), $4)`

const closeErasureSQL = `
UPDATE {schema}.erasure_request SET completed_at = $2, residual = $3 WHERE request_id = $1`

// A turn's messages go by cascade from the observation, and so do the registrations — which is why
// this runs last.
const deleteObservationsSQL = `
DELETE FROM {schema}.observation WHERE scope = $1 AND data_subject_id = $2`

// deleteProjectionSQL removes the rows of one kind that belong to this subject alone.
//
// The table and column are interpolated because PostgreSQL parameters are values and never
// identifiers. They come from `projection_kind`, whose CHECK constraints permit nothing but
// `[a-z][a-z0-9_]*` — so the names could not have been anything else, rather than being made safe on
// the way out.
//
// `NOT EXISTS` is the sharing rule: a projection with any registration belonging to a different
// subject — or to none — stays. An entity is the case that matters, and removing a shared one would
// take a node out of everybody else's graph to satisfy one person's request.
func deleteProjectionSQL(schema Schema, k projectionKind) string {
	return schema.SQL(fmt.Sprintf(`
DELETE FROM {schema}.%s t
 WHERE t.scope = $1
   AND t.%s::text IN (
        SELECT d.projection_id
          FROM {schema}.projection_dependency d
         WHERE d.scope = $1 AND d.projection_kind = %s AND d.data_subject_id = $2
           AND NOT EXISTS (
                SELECT 1 FROM {schema}.projection_dependency other
                 WHERE other.projection_kind = d.projection_kind
                   AND other.projection_id   = d.projection_id
                   AND other.data_subject_id IS DISTINCT FROM $2))`,
		k.table, k.idCol, quoteLiteral(k.kind)))
}

// countResidualSQL counts, against the same predicate, what is still there.
//
// The same predicate is the whole point. A count with a different one measures something else and
// reports it under the name of this erasure — and it would report zero for all the usual reasons a
// query returns nothing.
func countResidualSQL(schema Schema, k projectionKind) string {
	return schema.SQL(fmt.Sprintf(`
SELECT count(*)
  FROM {schema}.%s t
 WHERE t.scope = $1
   AND t.%s::text IN (
        SELECT d.projection_id
          FROM {schema}.projection_dependency d
         WHERE d.scope = $1 AND d.projection_kind = %s AND d.data_subject_id = $2
           AND NOT EXISTS (
                SELECT 1 FROM {schema}.projection_dependency other
                 WHERE other.projection_kind = d.projection_kind
                   AND other.projection_id   = d.projection_id
                   AND other.data_subject_id IS DISTINCT FROM $2))`,
		k.table, k.idCol, quoteLiteral(k.kind)))
}

// quoteLiteral renders a kind as a SQL string literal.
//
// The kind is also constrained to `[a-z][a-z0-9_]*` at its source, so this cannot encounter a quote
// to double. It doubles them anyway: a defence that depends on a constraint in another file being
// read correctly is one edit away from not being a defence.
func quoteLiteral(s string) string {
	out := make([]byte, 0, len(s)+2)
	out = append(out, '\'')
	for i := 0; i < len(s); i++ {
		if s[i] == '\'' {
			out = append(out, '\'')
		}
		out = append(out, s[i])
	}
	return string(append(out, '\''))
}

// Erasures returns the receipts recorded for a scope, newest first.
//
// A receipt is a row rather than a log line because it is the artefact the product is sold on: an
// erasure in flight has no completion time, and one with a non-zero residual is an erasure that did
// not do what it claimed. Both have to be findable by asking, not by grepping.
func (e *Eraser) Erasures(ctx context.Context, schema Schema, scope string) ([]domain.Erasure, error) {
	rows, err := e.pool.Query(ctx, schema.SQL(selectErasuresSQL), scope)
	if err != nil {
		return nil, fmt.Errorf("read erasures: %w", err)
	}
	defer rows.Close()

	var out []domain.Erasure
	for rows.Next() {
		var record domain.Erasure
		var residual []byte
		var completed *time.Time
		if err := rows.Scan(&record.RequestID, &record.Scope, &record.DataSubjectID,
			&record.Reason, &completed, &residual); err != nil {
			return nil, err
		}
		if completed != nil {
			record.CompletedAt = *completed
		}
		if residual != nil {
			if err := json.Unmarshal(residual, &record.Residual); err != nil {
				return nil, fmt.Errorf("decode residual: %w", err)
			}
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

const selectErasuresSQL = `
SELECT request_id::text, scope, selector->>'data_subject_id', reason, completed_at, residual
  FROM {schema}.erasure_request
 WHERE scope = $1
 ORDER BY requested_at DESC`
