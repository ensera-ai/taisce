// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const MaxGenerationClaims = 100
const MaxGenerationMessages = 256
const MaxGenerationBytes = 1 << 20

var ErrGenerationConflict = errors.New("source or knowledge changed during generation preparation")
var ErrGenerationLimit = errors.New("generation exceeds its source or output bound")
var ErrInvalidGeneration = errors.New("invalid generation request")
var ErrGenerationSource = errors.New("generation source is unavailable or is authoritative human input")

// GenerationSnapshot is held only during one bounded model attempt. Its digest includes source
// bytes, message groups/roles, attribution, fact revision and the previous extraction pointer.
type GenerationSnapshot struct {
	SourceID           string
	Scope              string
	LogOffset          int64
	Owner              string
	OccurredAt         time.Time
	IngestedAt         time.Time
	Messages           []domain.Message
	Revision           int64
	PreviousVersion    string
	PreviousGeneration string
	Digest             [32]byte
}

func (s *FactStore) ReadGenerationSource(ctx context.Context, schema Schema, scope, source string) (GenerationSnapshot, error) {
	var out GenerationSnapshot
	if _, err := uuid.Parse(source); err != nil {
		return out, ErrInvalidGeneration
	}
	err := pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead}, func(tx pgx.Tx) error {
		var err error
		out, err = readGenerationSourceTx(ctx, tx, schema, scope, source, false)
		return err
	})
	return out, err
}

func readGenerationSourceTx(ctx context.Context, tx pgx.Tx, schema Schema, scope, source string, writing bool) (GenerationSnapshot, error) {
	out := GenerationSnapshot{SourceID: source, Scope: scope}
	lock := " FOR KEY SHARE OF o"
	if writing {
		lock = " FOR UPDATE OF o"
	}
	var kind string
	var curated bool
	err := tx.QueryRow(ctx, schema.SQL(`SELECT coalesce(o.data_subject_id,''),o.occurred_at,o.ingested_at,o.fact_revision,o.kind,o.log_offset,
        EXISTS(SELECT 1 FROM {schema}.curated_claim c WHERE c.scope=o.scope AND c.source_observation_id=o.observation_id)
        FROM {schema}.observation o WHERE o.scope=$1 AND o.observation_id=$2::uuid`+lock), scope, source).Scan(&out.Owner, &out.OccurredAt, &out.IngestedAt, &out.Revision, &kind, &out.LogOffset, &curated)
	if errors.Is(err, pgx.ErrNoRows) {
		return GenerationSnapshot{}, ErrGenerationSource
	}
	if err != nil {
		return out, err
	}
	if curated || kind != KindTurn {
		return GenerationSnapshot{}, ErrGenerationSource
	}
	var count, bytes int64
	err = tx.QueryRow(ctx, schema.SQL(`SELECT count(*),coalesce(sum(octet_length(content)),0) FROM {schema}.turn_message WHERE observation_id=$1::uuid`), source).Scan(&count, &bytes)
	if err != nil {
		return out, err
	}
	if count < 1 || count > MaxGenerationMessages || bytes > MaxGenerationBytes {
		return GenerationSnapshot{}, ErrGenerationLimit
	}
	rows, err := tx.Query(ctx, schema.SQL(`SELECT ordinal,role,content,group_ordinal,occurred_at FROM {schema}.turn_message WHERE observation_id=$1::uuid ORDER BY ordinal FOR SHARE`), source)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var m domain.Message
		if err := rows.Scan(&m.Ordinal, &m.Role, &m.Content, &m.GroupOrdinal, &m.OccurredAt); err != nil {
			rows.Close()
			return out, err
		}
		out.Messages = append(out.Messages, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	err = tx.QueryRow(ctx, schema.SQL(`SELECT extractor_version,coalesce(generation_id::text,'') FROM {schema}.source_extraction WHERE scope=$1 AND source_observation_id=$2::uuid`), scope, source).Scan(&out.PreviousVersion, &out.PreviousGeneration)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	encoded, _ := json.Marshal(out)
	out.Digest = sha256.Sum256(encoded)
	return out, nil
}

func (s GenerationSnapshot) MessageTime(m domain.Message) time.Time {
	if !m.OccurredAt.IsZero() {
		return m.OccurredAt
	}
	if !s.OccurredAt.IsZero() {
		return s.OccurredAt
	}
	return s.IngestedAt
}
