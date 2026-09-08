// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// A published generation is an accepted outcome, not permission to ask the model again. This also
// fences an old in-flight extraction: ordinary writes refuse a source with a generation pointer.
func (s *FactStore) ReplayGeneration(ctx context.Context, schema Schema, scope, source string) (GenerationResult, bool, error) {
	var out GenerationResult
	var found bool
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockClaimSource(ctx, tx, schema, scope, source); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, schema.SQL(`SELECT g.generation_id::text,g.source_observation_id::text,g.extractor_version,g.applied_at,g.messages_read,g.claims_rejected,g.claims_refused_by_role,g.claims_retracted
            FROM {schema}.source_extraction p JOIN {schema}.fact_generation g ON g.scope=p.scope AND g.generation_id=p.generation_id
            WHERE p.scope=$1 AND p.source_observation_id=$2::uuid`), scope, source).Scan(&out.ID, &out.SourceID, &out.Version, &out.AppliedAt, &out.Messages, &out.Rejected, &out.RefusedRole, &out.Retracted)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		out.Replayed = true
		rows, err := tx.Query(ctx, schema.SQL(`SELECT fact_id::text FROM {schema}.fact_generation_record WHERE scope=$1 AND generation_id=$2::uuid AND disposition='admitted' ORDER BY fact_id LIMIT $3`), scope, out.ID, MaxGenerationClaims+1)
		if err != nil {
			return err
		}
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if len(ids) > MaxGenerationClaims {
			return ErrGenerationLimit
		}
		for _, id := range ids {
			if _, err := restoreFactReceiptTx(ctx, tx, schema, scope, id); err != nil {
				return err
			}
			if _, err := repairFactSupportTx(ctx, tx, schema, scope, id); err != nil {
				return err
			}
			withdrawn, err := readRetraction(ctx, tx, schema, scope, id)
			if err != nil {
				return err
			}
			if withdrawn != nil {
				out.Retracted++
			} else {
				out.Asserted++
			}
		}
		return nil
	})
	if err != nil {
		return GenerationResult{}, false, err
	}
	return out, found, nil
}

// Availability switches in ApplyGeneration; completing backlog bookkeeping is retryable afterwards.
// Keeping the watermark lock out of model preparation and fact publication avoids holding a whole
// project's ingestion queue while a source is reinterpreted. The same operation key finishes this
// step without another model call if the process or its output stream fails after publication.
func (s *FactStore) FinishGeneration(ctx context.Context, schema Schema, scope, source string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		return finishGenerationTx(ctx, tx, schema, scope, source)
	})
}

// Orphaned job cancellation uses the same bookkeeping in its control transaction. Its job row has
// no erasure foreign key and this path holds no fact/source advisory lock across the watermark.
func finishGenerationTx(ctx context.Context, tx pgx.Tx, schema Schema, scope, source string) error {
	tag, err := tx.Exec(ctx, schema.SQL(`UPDATE {schema}.observation o SET formed_at=coalesce(o.formed_at,clock_timestamp()),parked_at=NULL
            WHERE o.scope=$1 AND o.observation_id=$2::uuid AND EXISTS(SELECT 1 FROM {schema}.source_extraction p WHERE p.scope=o.scope AND p.source_observation_id=o.observation_id AND p.generation_id IS NOT NULL)`), scope, source)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrGenerationSource
	}
	if _, err := tx.Exec(ctx, schema.SQL(lockScopeWatermarkSQL), scope); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, schema.SQL(advanceFormedWatermarkSQL), scope)
	return err
}
