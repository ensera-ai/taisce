// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// Expired identity bookkeeping remains useful while any attributed source survives. Lock selected
// identities before a second existence check: a concurrent source insert holds KEY SHARE and its
// commit must be visible before cleanup decides the mapping is unused.
func expireUnusedSubjects(ctx context.Context, tx pgx.Tx, schema Schema, scope string, limit int, deleted map[string]int) error {
	rows, err := tx.Query(ctx, schema.SQL(`SELECT s.subject_id FROM {schema}.data_subject s
        WHERE s.scope=$1 AND s.inactive_after<=now() AND NOT EXISTS(SELECT 1 FROM {schema}.observation o WHERE o.scope=s.scope AND o.data_subject_id=s.subject_id)
        ORDER BY s.inactive_after,s.subject_id LIMIT $2 FOR UPDATE OF s SKIP LOCKED`), scope, limit)
	if err != nil {
		return err
	}
	ids := []string{}
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
	if len(ids) == 0 {
		return nil
	}
	tag, err := tx.Exec(ctx, schema.SQL(`DELETE FROM {schema}.data_subject s WHERE s.scope=$1 AND s.subject_id=ANY($2::text[])
        AND NOT EXISTS(SELECT 1 FROM {schema}.observation o WHERE o.scope=s.scope AND o.data_subject_id=s.subject_id)`), scope, ids)
	if err != nil {
		return err
	}
	deleted["data_subject"] = int(tag.RowsAffected())
	return nil
}
