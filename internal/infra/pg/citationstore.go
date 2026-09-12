// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	MaxCitationEvidence       = 32
	DefaultCitationEvidence   = 8
	MaxCitationTextBytes      = 256 << 10
	MaxCitationStatementBytes = 64 << 10
)

var (
	ErrCitationNotFound  = errors.New("citation not found")
	ErrCitationLimit     = errors.New("citation source exceeds supported text bounds; use the subject export")
	ErrCitationIntegrity = errors.New("citation evidence does not match its source")
	ErrInvalidCitation   = errors.New("invalid citation identifier or page")
)

type CitationCursor struct {
	ObservationID string `json:"source_observation_id"`
	Ordinal       int    `json:"source_ordinal"`
	ByteStart     int    `json:"byte_start"`
}

type CitationInterval struct {
	From           *time.Time `json:"from"`
	Until          *time.Time `json:"until"`
	FromInclusive  bool       `json:"from_inclusive"`
	UntilInclusive bool       `json:"until_inclusive"`
}

type CitationSource struct {
	AuthoredBy       *string   `json:"authored_by,omitempty"`
	ObservationID    string    `json:"source_observation_id"`
	Ordinal          int       `json:"source_ordinal"`
	LogOffset        int64     `json:"log_offset"`
	Role             string    `json:"role"`
	OccurredAt       time.Time `json:"occurred_at"`
	Quote            string    `json:"quote"`
	ByteStart        int       `json:"byte_start"`
	ByteEnd          int       `json:"byte_end"`
	ExtractorVersion string    `json:"extractor_version"`
	Context          string    `json:"context"`
	ContextStart     int       `json:"context_byte_start"`
	ContextEnd       int       `json:"context_byte_end"`
	ContextComplete  bool      `json:"context_complete"`
}

// Citation is the currently retained record and one page of exact supporting sources.
// The stored intervals describe the retained state; historical recall selects earlier versions.
type Citation struct {
	Generation         *GenerationLink    `json:"generation,omitempty"`
	ReinterpretedBy    *GenerationLink    `json:"reinterpreted_by,omitempty"`
	Version            string             `json:"version"`
	Retraction         *RetractionDetails `json:"retraction,omitempty"`
	ID                 string             `json:"id"`
	SupersededBy       *string            `json:"superseded_by,omitempty"`
	SupersessionSource *string            `json:"supersession_source_observation_id,omitempty"`
	Scope              string             `json:"scope"`
	SubjectID          *string            `json:"subject_entity_id"`
	ObjectID           *string            `json:"object_entity_id"`
	Predicate          string             `json:"predicate"`
	Statement          string             `json:"statement"`
	Confidence         float32            `json:"confidence"`
	SourceRole         string             `json:"source_role"`
	RecordedAt         time.Time          `json:"recorded_at"`
	Valid              CitationInterval   `json:"valid"`
	Known              CitationInterval   `json:"known"`
	Status             string             `json:"status"`
	Evidence           []CitationSource   `json:"evidence"`
	Next               *CitationCursor    `json:"next,omitempty"`
}

type CitationStore struct {
	pool   *pgxpool.Pool
	schema Schema
}

func NewCitationStore(pool *pgxpool.Pool, schema Schema) *CitationStore {
	return &CitationStore{pool: pool, schema: schema}
}

func (s *CitationStore) Resolve(ctx context.Context, scope, id string, after *CitationCursor, limit int) (Citation, error) {
	out := Citation{Evidence: make([]CitationSource, 0)}
	factID, err := uuid.Parse(id)
	if err != nil || limit < 1 || limit > MaxCitationEvidence {
		return out, ErrInvalidCitation
	}
	if _, err := NewSchema(scope); err != nil {
		return out, ErrInvalidCitation
	}
	if after != nil {
		if _, err := uuid.Parse(after.ObservationID); err != nil || after.Ordinal < 0 || after.ByteStart < 0 || after.Ordinal > 2147483647 || after.ByteStart > 2147483647 {
			return out, ErrInvalidCitation
		}
	}
	err = pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		var statement *string
		var statementBytes int
		var validOpen, knownOpen bool
		err := tx.QueryRow(ctx, s.schema.SQL(`SELECT version::text,fact_id::text,scope,subject_entity_id::text,object_entity_id::text,predicate,
 CASE WHEN octet_length(statement)<=$3 THEN statement END,octet_length(statement),confidence,source_role,recorded_at,
 lower(valid),upper(valid),lower_inc(valid),upper_inc(valid),lower(known),upper(known),lower_inc(known),upper_inc(known),upper_inf(valid),upper_inf(known),superseded_by::text,supersession_source::text
 FROM {schema}.fact f WHERE scope=$1 AND fact_id=$2 AND (f.retention_until IS NULL OR f.retention_until > now())
   AND EXISTS(SELECT 1 FROM {schema}.fact_evidence e WHERE e.scope=f.scope AND e.fact_id=f.fact_id)`), scope, factID.String(), MaxCitationStatementBytes).Scan(
			&out.Version, &out.ID, &out.Scope, &out.SubjectID, &out.ObjectID, &out.Predicate, &statement, &statementBytes, &out.Confidence, &out.SourceRole, &out.RecordedAt,
			&out.Valid.From, &out.Valid.Until, &out.Valid.FromInclusive, &out.Valid.UntilInclusive, &out.Known.From, &out.Known.Until, &out.Known.FromInclusive, &out.Known.UntilInclusive, &validOpen, &knownOpen, &out.SupersededBy, &out.SupersessionSource)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrCitationNotFound
		}
		if err != nil {
			return err
		}
		if statement == nil || statementBytes > MaxCitationStatementBytes {
			return ErrCitationLimit
		}
		out.Statement = *statement
		out.Status = "current"
		if !validOpen {
			out.Status = "validity_closed"
		}
		if !knownOpen {
			out.Status = "knowledge_closed"
			out.Retraction, err = readRetraction(ctx, tx, s.schema, scope, out.ID)
			if err != nil {
				return err
			}
			if out.Retraction != nil {
				out.Status = "retracted"
			}
		}
		out.Generation, out.ReinterpretedBy, err = generationLinksTx(ctx, tx, s.schema, scope, out.ID)
		if err != nil {
			return err
		}
		if !knownOpen && out.Retraction == nil && out.ReinterpretedBy != nil {
			out.Status = "reinterpreted"
		}
		query := citationEvidenceSQL
		args := []any{scope, factID.String(), limit + 1, domain.MaxMessageBytes, domain.MaxMessageBytes, 256, domain.ContextWindow}
		cursor := ""
		if after != nil {
			cursor = "AND (e.source_observation_id,e.source_ordinal,e.byte_start)>($8::uuid,$9::integer,$10::integer)"
			args = append(args, after.ObservationID, after.Ordinal, after.ByteStart)
		}
		query = strings.ReplaceAll(query, "{cursor}", cursor)
		rows, err := tx.Query(ctx, s.schema.SQL(query), args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		textBytes := len(out.Statement)
		for rows.Next() {
			var source CitationSource
			var quote, version, role *string
			var occurred *time.Time
			var offset *int64
			var messageBytes *int
			var quoteBytes, versionBytes int
			var actual, window []byte
			var windowStart *int
			if err := rows.Scan(&source.ObservationID, &source.Ordinal, &source.ByteStart, &source.ByteEnd, &quoteBytes, &messageBytes, &versionBytes,
				&quote, &version, &role, &occurred, &offset, &actual, &window, &windowStart, &source.AuthoredBy); err != nil {
				return err
			}
			// The extra row only signals continuation. Its contents are validated on its own page.
			if len(out.Evidence) == limit {
				last := out.Evidence[len(out.Evidence)-1]
				out.Next = &CitationCursor{last.ObservationID, last.Ordinal, last.ByteStart}
				break
			}
			if quoteBytes > domain.MaxMessageBytes || versionBytes > 256 || (messageBytes != nil && *messageBytes > domain.MaxMessageBytes) {
				return ErrCitationLimit
			}
			if quote == nil || version == nil || role == nil || occurred == nil || offset == nil || messageBytes == nil || windowStart == nil || !utf8.Valid(actual) || !bytes.Equal(actual, []byte(*quote)) || len(actual) == 0 {
				return ErrCitationIntegrity
			}
			context := domain.RawWindow{Bytes: window, Start: *windowStart, QuoteStart: source.ByteStart, QuoteEnd: source.ByteEnd, MessageBytes: *messageBytes}.Legible()
			cost := len(*quote) + len(context.Text) + len(*version)
			if len(out.Evidence) > 0 && textBytes+cost > MaxCitationTextBytes {
				last := out.Evidence[len(out.Evidence)-1]
				out.Next = &CitationCursor{last.ObservationID, last.Ordinal, last.ByteStart}
				break
			}
			source.Quote = *quote
			source.ExtractorVersion = *version
			source.Role = *role
			source.OccurredAt = *occurred
			source.LogOffset = *offset
			source.Context = context.Text
			source.ContextStart = context.ByteStart
			source.ContextEnd = context.ByteStart + len(context.Text)
			source.ContextComplete = context.Complete
			out.Evidence = append(out.Evidence, source)
			textBytes += cost
		}
		return rows.Err()
	})
	if err != nil {
		return Citation{}, fmt.Errorf("resolve citation: %w", err)
	}
	return out, nil
}

const citationEvidenceSQL = `SELECT e.source_observation_id::text,e.source_ordinal,e.byte_start,e.byte_end,
 octet_length(e.quote),octet_length(m.content),octet_length(e.extractor_version),
 CASE WHEN octet_length(e.quote)<=$4 THEN e.quote END,
 CASE WHEN octet_length(e.extractor_version)<=$6 THEN e.extractor_version END,
 m.role,m.occurred_at,o.log_offset,
 CASE WHEN octet_length(m.content)<=$5 AND e.byte_start>=0 AND e.byte_end<=octet_length(m.content)
 THEN substring(convert_to(m.content,'UTF8') from e.byte_start+1 for e.byte_end-e.byte_start) END,
 CASE WHEN octet_length(m.content)<=$5 AND e.byte_start>=0 AND e.byte_end<=octet_length(m.content)
 THEN substring(convert_to(m.content,'UTF8') from greatest(0,e.byte_start-$7)+1 for e.byte_end+$7-greatest(0,e.byte_start-$7)) END,
 greatest(0,e.byte_start-$7),c.principal_id::text
 FROM {schema}.fact_evidence e
 LEFT JOIN {schema}.observation o ON o.observation_id=e.source_observation_id AND o.scope=e.scope
 LEFT JOIN {schema}.turn_message m ON m.observation_id=o.observation_id AND m.ordinal=e.source_ordinal
 LEFT JOIN {schema}.curated_claim c ON c.scope=e.scope AND c.source_observation_id=e.source_observation_id
 WHERE e.scope=$1 AND e.fact_id=$2 {cursor}
 ORDER BY e.source_observation_id,e.source_ordinal,e.byte_start LIMIT $3`
