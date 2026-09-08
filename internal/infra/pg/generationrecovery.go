// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// A lost projection is still recoverable knowledge. Restore missing current outcomes under the
// source lock so retirement closes both the fact and its receipt in the publication transaction.
// Otherwise a later recovery could revive an interpretation the generation already replaced.
func restoreMissingGenerationFactsTx(ctx context.Context, tx pgx.Tx, schema Schema, scope, source string) error {
	rows, err := tx.Query(ctx, schema.SQL(`SELECT r.fact_id::text FROM {schema}.fact_receipt r
		WHERE r.scope=$1 AND r.source_observation_id=$2::uuid
		AND r.evidence->>'extractor_version'<>'curated/v1' AND upper_inf((r.state->>'known')::tstzrange)
		AND NOT EXISTS(SELECT 1 FROM {schema}.fact f WHERE f.scope=r.scope AND f.fact_id=r.fact_id)
		ORDER BY r.fact_id LIMIT $3 FOR UPDATE OF r`), scope, source, MaxGenerationClaims+1)
	if err != nil {
		return err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	if len(ids) > MaxGenerationClaims {
		return ErrGenerationLimit
	}
	for _, id := range ids {
		if _, err := restoreFactReceiptTx(ctx, tx, schema, scope, id); err != nil {
			return err
		}
	}
	return nil
}
