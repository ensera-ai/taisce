// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Reconcile only unpublished facts from this transaction's generation. Another source's facts,
// including human assertions, are never shortened by reinterpretation. The exclusion constraint
// refuses an overlapping earlier interval; the next later assertion bounds the new fact's end.
//
// "Next" means the same speaker's next. The slot is the subject, the relation and the data subject
// the statement belongs to, exactly as the ordinary path and the constraint define it. Without the
// data subject here a rebuild of one person's source bounded their fact at another person's later
// statement about the same name — writing back, on the path that exists to repair things, the loss
// the ordinary path no longer makes.
type generationBoundary struct {
	Until             *time.Time
	Successor, Source *string
}

func generationValidityTx(ctx context.Context, tx pgx.Tx, schema Schema, scope, source, generation, subject, predicate, dataSubject, successor string, start time.Time) (generationBoundary, error) {
	_, err := tx.Exec(ctx, schema.SQL(`UPDATE {schema}.fact f SET valid=tstzrange(lower(f.valid),$6),superseded_by=$7::uuid,supersession_source=$2::uuid
        WHERE f.scope=$1 AND f.subject_entity_id=$4::uuid AND f.predicate=$5 AND upper_inf(f.known)
        AND f.data_subject_id IS NOT DISTINCT FROM $8::text
        AND lower(f.valid)<$6 AND f.valid @> $6::timestamptz
        AND EXISTS(SELECT 1 FROM {schema}.fact_generation_record g WHERE g.scope=f.scope AND g.fact_id=f.fact_id AND g.generation_id=$3::uuid AND g.disposition='admitted')`), scope, source, generation, subject, predicate, start, successor, nullIfEmpty(dataSubject))
	if err != nil {
		return generationBoundary{}, err
	}
	var out generationBoundary
	err = tx.QueryRow(ctx, schema.SQL(`SELECT lower(f.valid),f.fact_id::text,r.source_observation_id::text FROM {schema}.fact f
        LEFT JOIN {schema}.fact_receipt r ON r.scope=f.scope AND r.fact_id=f.fact_id
        WHERE f.scope=$1 AND f.subject_entity_id=$2::uuid AND f.predicate=$3 AND upper_inf(f.known) AND lower(f.valid)>$4
        AND f.data_subject_id IS NOT DISTINCT FROM $5::text
        ORDER BY lower(f.valid),f.fact_id LIMIT 1 FOR SHARE OF f`), scope, subject, predicate, start, nullIfEmpty(dataSubject)).Scan(&out.Until, &out.Successor, &out.Source)
	if errors.Is(err, pgx.ErrNoRows) {
		return generationBoundary{}, nil
	}
	if err != nil {
		return generationBoundary{}, err
	}
	if out.Source == nil {
		return generationBoundary{}, ErrInvalidReceipt
	}
	return out, nil
}
