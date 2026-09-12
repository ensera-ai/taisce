// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	DefaultRecordPage       = 20
	MaxRecordPage           = 100
	RecordPreviewCharacters = 512
	MaxRecordSubjectBytes   = 1 << 20
)

var (
	ErrInvalidRecordPage = errors.New("invalid record page")
	ErrRecordNotFound    = errors.New("record not found")
)

// RecordStore provides bounded inspection of retained facts. It does not infer new claims or
// verify source text; a client follows the record ID through citation resolution for that evidence.
type RecordStore struct {
	pool   *pgxpool.Pool
	schema Schema
}

func NewRecordStore(pool *pgxpool.Pool, schema Schema) *RecordStore {
	return &RecordStore{pool: pool, schema: schema}
}

// RecordCursor is a position, never an authorization capability. Project and subject predicates
// are reapplied on every page, even if a caller manufactures or reuses a cursor.
type RecordCursor struct {
	ID string `json:"id"`
}

type RecordSummary struct {
	Version          string    `json:"version"`
	ID               string    `json:"id"`
	SubjectID        *string   `json:"subject_entity_id"`
	ObjectID         *string   `json:"object_entity_id"`
	Predicate        string    `json:"predicate"`
	StatementPreview string    `json:"statement_preview"`
	StatementBytes   int       `json:"statement_bytes"`
	PreviewTruncated bool      `json:"preview_truncated"`
	SourceRole       string    `json:"source_role"`
	RecordedAt       time.Time `json:"recorded_at"`
	Status           string    `json:"status"`
}

type RecordPage struct {
	Records []RecordSummary `json:"records"`
	Next    *RecordCursor   `json:"next,omitempty"`
}

// List orders by immutable UUID, so closing validity or deleting the cursor row cannot move a
// returned record into a later page. This is a live inventory: inserts behind the cursor require
// restarting the browse. OFFSET would both rescan earlier rows and shift pages after deletion.
func (s *RecordStore) List(ctx context.Context, scope, subject string, after *RecordCursor, limit int) (RecordPage, error) {
	out := RecordPage{Records: make([]RecordSummary, 0)}
	if _, err := NewSchema(scope); err != nil || limit < 1 || limit > MaxRecordPage || len(subject) > MaxRecordSubjectBytes || !utf8.ValidString(subject) || (subject != "" && strings.TrimSpace(subject) == "") {
		return out, ErrInvalidRecordPage
	}
	cursor := uuid.Nil.String()
	comparison := ">="
	if after != nil {
		id, err := uuid.Parse(after.ID)
		if err != nil {
			return out, ErrInvalidRecordPage
		}
		cursor = id.String()
		comparison = ">"
	}
	query := recordListSQL
	args := []any{scope, cursor, limit + 1, RecordPreviewCharacters}
	if subject != "" {
		query = subjectRecordListSQL
		args = append(args, subject)
	}
	query = strings.ReplaceAll(query, "{comparison}", comparison)
	rows, err := s.pool.Query(ctx, s.schema.SQL(query), args...)
	if err != nil {
		return out, fmt.Errorf("list records: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		if len(out.Records) == limit {
			out.Next = &RecordCursor{ID: out.Records[len(out.Records)-1].ID}
			break
		}
		var record RecordSummary
		if err := rows.Scan(&record.Version, &record.ID, &record.SubjectID, &record.ObjectID, &record.Predicate, &record.StatementPreview, &record.StatementBytes, &record.SourceRole, &record.RecordedAt, &record.Status); err != nil {
			return RecordPage{}, fmt.Errorf("read record: %w", err)
		}
		record.PreviewTruncated = record.StatementBytes > len(record.StatementPreview)
		out.Records = append(out.Records, record)
	}
	if err := rows.Err(); err != nil {
		return RecordPage{}, fmt.Errorf("list records: %w", err)
	}
	return out, nil
}

// The page is selected before reading statement text. The subject path first deduplicates indexed
// provenance references, so one shared fact appears once even with several supporting observations.
const recordListSQL = `WITH page AS MATERIALIZED (
 SELECT fact_id FROM {schema}.fact WHERE scope=$1 AND fact_id {comparison} $2::uuid
   AND (retention_until IS NULL OR retention_until > now())
 ORDER BY fact_id LIMIT $3
)
SELECT f.version::text,f.fact_id::text,f.subject_entity_id::text,f.object_entity_id::text,f.predicate,
 left(f.statement,$4),octet_length(f.statement),f.source_role,f.recorded_at,
 CASE WHEN NOT upper_inf(f.known) THEN CASE WHEN EXISTS(SELECT 1 FROM {schema}.record_retraction rr WHERE rr.scope=f.scope AND rr.target_fact_id=f.fact_id) THEN 'retracted' ELSE 'knowledge_closed' END WHEN NOT upper_inf(f.valid) THEN 'validity_closed' ELSE 'current' END
 FROM page p JOIN {schema}.fact f ON f.scope=$1 AND f.fact_id=p.fact_id
 ORDER BY f.fact_id`

const subjectRecordListSQL = `WITH page AS MATERIALIZED (
 SELECT DISTINCT fact_ref FROM {schema}.projection_dependency
 WHERE scope=$1 AND data_subject_id=$5 AND projection_kind='fact' AND fact_ref {comparison} $2::uuid
 ORDER BY fact_ref LIMIT $3
)
SELECT f.version::text,f.fact_id::text,f.subject_entity_id::text,f.object_entity_id::text,f.predicate,
 left(f.statement,$4),octet_length(f.statement),f.source_role,f.recorded_at,
 CASE WHEN NOT upper_inf(f.known) THEN CASE WHEN EXISTS(SELECT 1 FROM {schema}.record_retraction rr WHERE rr.scope=f.scope AND rr.target_fact_id=f.fact_id) THEN 'retracted' ELSE 'knowledge_closed' END WHEN NOT upper_inf(f.valid) THEN 'validity_closed' ELSE 'current' END
 FROM page p JOIN {schema}.fact f ON f.scope=$1 AND f.fact_id=p.fact_ref ORDER BY f.fact_id`

type RecordTemporalState struct {
	Valid CitationInterval `json:"valid"`
	Known CitationInterval `json:"known"`
}

type RecordHistoryVersion struct {
	ID string `json:"history_id"`
	RecordTemporalState
	EndedByObservationID string `json:"ended_by_observation_id"`
}

type RecordHistoryCursor struct {
	KnownUntil time.Time `json:"known_until"`
	ID         string    `json:"history_id"`
}

type RecordHistoryPage struct {
	Version    string                 `json:"version"`
	Retraction *RetractionDetails     `json:"retraction,omitempty"`
	ID         string                 `json:"id"`
	Current    RecordTemporalState    `json:"current"`
	Previous   []RecordHistoryVersion `json:"previous"`
	Next       *RecordHistoryCursor   `json:"next,omitempty"`
}

// History reads the current state and its archive in one snapshot. Across pages history is live:
// erasure may remove a record and a later supersession may add a newer version before the cursor.
// The archive contains intervals, not copies of entity labels or evidence that never changed here.
func (s *RecordStore) History(ctx context.Context, scope, id string, before *RecordHistoryCursor, limit int) (RecordHistoryPage, error) {
	out := RecordHistoryPage{Previous: make([]RecordHistoryVersion, 0)}
	factID, err := uuid.Parse(id)
	if err != nil {
		return out, ErrInvalidRecordPage
	}
	if _, err := NewSchema(scope); err != nil || limit < 1 || limit > MaxRecordPage {
		return out, ErrInvalidRecordPage
	}
	out.ID = factID.String()
	args := []any{scope, out.ID, limit + 1}
	cursor := ""
	if before != nil {
		historyID, err := uuid.Parse(before.ID)
		if err != nil || before.KnownUntil.IsZero() || before.KnownUntil.Year() < 1 || before.KnownUntil.Year() > 9999 {
			return out, ErrInvalidRecordPage
		}
		args = append(args, before.KnownUntil.UTC(), historyID.String())
		cursor = "AND (upper(known),history_id)<($4::timestamptz,$5::uuid)"
	}
	err = pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		current := &out.Current
		err := tx.QueryRow(ctx, s.schema.SQL(`SELECT version::text,lower(valid),upper(valid),lower_inc(valid),upper_inc(valid),lower(known),upper(known),lower_inc(known),upper_inc(known)
 FROM {schema}.fact WHERE scope=$1 AND fact_id=$2 AND (retention_until IS NULL OR retention_until > now())`), scope, out.ID).Scan(
			&out.Version, &current.Valid.From, &current.Valid.Until, &current.Valid.FromInclusive, &current.Valid.UntilInclusive,
			&current.Known.From, &current.Known.Until, &current.Known.FromInclusive, &current.Known.UntilInclusive)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrRecordNotFound
		}
		if err != nil {
			return err
		}
		if current.Known.Until != nil {
			out.Retraction, err = readRetraction(ctx, tx, s.schema, scope, out.ID)
			if err != nil {
				return err
			}
		}
		rows, err := tx.Query(ctx, s.schema.SQL(strings.ReplaceAll(recordHistorySQL, "{cursor}", cursor)), args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			if len(out.Previous) == limit {
				last := out.Previous[len(out.Previous)-1]
				out.Next = &RecordHistoryCursor{KnownUntil: *last.Known.Until, ID: last.ID}
				break
			}
			var version RecordHistoryVersion
			if err := rows.Scan(&version.ID, &version.Valid.From, &version.Valid.Until, &version.Valid.FromInclusive, &version.Valid.UntilInclusive,
				&version.Known.From, &version.Known.Until, &version.Known.FromInclusive, &version.Known.UntilInclusive, &version.EndedByObservationID); err != nil {
				return err
			}
			out.Previous = append(out.Previous, version)
		}
		return rows.Err()
	})
	if err != nil {
		return RecordHistoryPage{}, fmt.Errorf("read record history: %w", err)
	}
	return out, nil
}

const recordHistorySQL = `SELECT history_id::text,lower(valid),upper(valid),lower_inc(valid),upper_inc(valid),
 lower(known),upper(known),lower_inc(known),upper_inc(known),source_observation_id::text
 FROM {schema}.fact_history h WHERE h.scope=$1 AND h.fact_id=$2 {cursor}
   AND EXISTS (SELECT 1 FROM {schema}.fact f WHERE f.scope=h.scope AND f.fact_id=h.fact_id
                 AND (f.retention_until IS NULL OR f.retention_until > now()))
 ORDER BY upper(known) DESC,history_id DESC LIMIT $3`
