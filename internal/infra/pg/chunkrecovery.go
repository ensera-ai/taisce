// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const MaxChunkRecoveryBytes = 16 << 20

var ErrChunkRecoveryLimit = errors.New("chunk recovery page exceeds 16 MiB of source text; reduce the page limit")

type ChunkRecoveryCursor struct {
	Offset  int64 `json:"log_offset"`
	Ordinal int   `json:"ordinal"`
}
type ChunkRecoveryPage struct {
	Examined int                  `json:"examined"`
	Restored int                  `json:"restored"`
	Repaired int                  `json:"repaired"`
	Next     *ChunkRecoveryCursor `json:"next,omitempty"`
}

// Source offsets are monotonic within the project. Each page locks surviving source material before
// restoring derived rows; erasure wins cleanly if a selected message disappears before that lock.
func (s *ObservationStore) RecoverChunks(ctx context.Context, schema Schema, scope, source, actor string, after ChunkRecoveryCursor, limit int) (ChunkRecoveryPage, error) {
	out := ChunkRecoveryPage{}
	if _, err := NewSchema(scope); err != nil {
		return out, ErrInvalidRecovery
	}
	if limit < 1 || limit > MaxRecoveryPage || after.Offset < -1 || after.Ordinal < -1 || (after.Offset == -1) != (after.Ordinal == -1) {
		return out, ErrInvalidRecovery
	}
	for _, v := range []string{source, actor} {
		if v != "" {
			if id, err := uuid.Parse(v); err != nil || id == uuid.Nil {
				return out, ErrInvalidRecovery
			}
		}
	}
	if actor == "" {
		return out, ErrInvalidRecovery
	}
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, schema.SQL(`SELECT o.observation_id::text,o.log_offset,m.ordinal,octet_length(m.content)
            FROM {schema}.observation o JOIN {schema}.turn_message m ON m.observation_id=o.observation_id
            WHERE o.scope=$1 AND o.kind='turn' AND ($2::uuid IS NULL OR o.observation_id=$2::uuid)
            AND (o.log_offset,m.ordinal)>($3,$4) ORDER BY o.log_offset,m.ordinal LIMIT $5 FOR SHARE OF o,m`), scope, nullIfEmptyString(source), after.Offset, after.Ordinal, limit+1)
		if err != nil {
			return err
		}
		type entry struct {
			source string
			cursor ChunkRecoveryCursor
			bytes  int
		}
		entries := []entry{}
		for rows.Next() {
			var e entry
			if err := rows.Scan(&e.source, &e.cursor.Offset, &e.cursor.Ordinal, &e.bytes); err != nil {
				rows.Close()
				return err
			}
			entries = append(entries, e)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if len(entries) > limit {
			entries = entries[:limit]
			next := entries[len(entries)-1].cursor
			out.Next = &next
		}
		bytes := 0
		for _, e := range entries {
			bytes += e.bytes
			if bytes > MaxChunkRecoveryBytes {
				return ErrChunkRecoveryLimit
			}
		}
		for _, e := range entries {
			out.Examined++
			restored, repaired, err := recoverMessageChunkTx(ctx, tx, schema, scope, e.source, e.cursor.Ordinal)
			if err != nil {
				return err
			}
			if restored {
				out.Restored++
			}
			if repaired {
				out.Repaired++
			}
		}
		_, err = tx.Exec(ctx, schema.SQL(`INSERT INTO {schema}.audit_entry(operation,principal,principal_kind,project,magnitude,outcome)
            VALUES('formation.recover',$1::uuid,'operator',$2,$3,'allowed')`), actor, scope, out.Restored+out.Repaired)
		return err
	})
	if err != nil {
		return ChunkRecoveryPage{}, err
	}
	return out, nil
}

func recoverMessageChunkTx(ctx context.Context, tx pgx.Tx, schema Schema, scope, source string, ordinal int) (bool, bool, error) {
	var id, text, role string
	var owner *string
	var occurred time.Time
	err := tx.QueryRow(ctx, schema.SQL(`SELECT m.chunk_id::text,m.content,m.role,m.occurred_at,o.data_subject_id
        FROM {schema}.observation o JOIN {schema}.turn_message m ON m.observation_id=o.observation_id
        WHERE o.scope=$1 AND o.observation_id=$2::uuid AND m.ordinal=$3 AND o.kind='turn' FOR SHARE OF o,m`), scope, source, ordinal).Scan(&id, &text, &role, &occurred, &owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	if len(text) > MaxChunkRecoveryBytes {
		return false, false, ErrChunkRecoveryLimit
	}
	tag, err := tx.Exec(ctx, schema.SQL(insertChunkSQL+` ON CONFLICT(scope,chunk_id) DO NOTHING`), id, scope, source, ordinal, text, occurred, role, owner)
	if err != nil {
		return false, false, err
	}
	restored := tag.RowsAffected() > 0
	var matches bool
	err = tx.QueryRow(ctx, schema.SQL(`SELECT (source_observation_id,source_message_ordinal,text,occurred_at,source_role,data_subject_id)
        IS NOT DISTINCT FROM ($3::uuid,$4::int,$5::text,$6::timestamptz,$7::text,$8::text)
        FROM {schema}.chunk WHERE scope=$1 AND chunk_id=$2::uuid FOR UPDATE`), scope, id, source, ordinal, text, occurred, role, owner).Scan(&matches)
	if err != nil {
		return false, false, err
	}
	if !matches {
		return false, false, ErrInvalidReceipt
	}
	tag, err = tx.Exec(ctx, schema.SQL(registerProjectionSQL), source, scope, ProjectionChunk, id, owner)
	if err != nil {
		return false, false, err
	}
	repaired := !restored && tag.RowsAffected() > 0
	var registrationMatches bool
	err = tx.QueryRow(ctx, schema.SQL(`SELECT (scope,data_subject_id,pipeline_version) IS NOT DISTINCT FROM ($3::text,$4::text,'v1'::text)
        FROM {schema}.projection_dependency WHERE source_observation_id=$1::uuid AND projection_kind='chunk' AND projection_id=$2`), source, id, scope, owner).Scan(&registrationMatches)
	if err != nil {
		return false, false, err
	}
	if !registrationMatches {
		return false, false, ErrInvalidReceipt
	}
	return restored, repaired, nil
}
