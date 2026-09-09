// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"context"
	"encoding/hex"
	"errors"
	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"strings"
	"time"
	"unicode/utf8"
)

const MaxMessageWindowBytes = 4096

var ErrInvalidMessageWindow = errors.New("invalid source message window")
var ErrMessageNotFound = errors.New("source message not found")
var ErrMessageChanged = errors.New("source message content changed")
var ErrMessageUnavailable = errors.New("source message is outside its storage contract")

type MessageWindowQuery struct {
	ChunkID          string
	ByteStart, Limit int
	ExpectedDigest   string
}

func (q MessageWindowQuery) Validate() error {
	if id, err := uuid.Parse(q.ChunkID); err != nil || id == uuid.Nil {
		return ErrInvalidMessageWindow
	}
	if q.ByteStart < 0 || q.ByteStart > domain.MaxMessageBytes || q.Limit < utf8.UTFMax || q.Limit > MaxMessageWindowBytes {
		return ErrInvalidMessageWindow
	}
	// A continuation must bind to the message version that supplied the preceding window/preview.
	if q.ByteStart > 0 && q.ExpectedDigest == "" {
		return ErrInvalidMessageWindow
	}
	if q.ExpectedDigest != "" {
		digest, err := hex.DecodeString(q.ExpectedDigest)
		if err != nil || len(digest) != 32 || q.ExpectedDigest != strings.ToLower(q.ExpectedDigest) {
			return ErrInvalidMessageWindow
		}
	}
	return nil
}

type SourceMessageWindow struct {
	ChunkID       string    `json:"chunk_id"`
	SourceID      string    `json:"source_id"`
	Ordinal       int       `json:"ordinal"`
	Role          string    `json:"role"`
	OccurredAt    time.Time `json:"occurred_at"`
	ContentDigest string    `json:"content_digest"`
	MessageBytes  int       `json:"message_bytes"`
	ByteStart     int       `json:"byte_start"`
	ByteEnd       int       `json:"byte_end"`
	Text          string    `json:"text"`
	Complete      bool      `json:"complete"`
	NextByteStart *int      `json:"next_byte_start,omitempty"`
}

// Message reads an authoritative message reference independently of any extracted fact or vector
// generation. SQL bounds the returned bytes before allocation; Go retains whole UTF-8 code points.
// The digest check and content read share one statement snapshot, so they cannot see different text.
func (s *CitationStore) Message(ctx context.Context, scope string, q MessageWindowQuery) (SourceMessageWindow, error) {
	if err := q.Validate(); err != nil {
		return SourceMessageWindow{}, err
	}
	if _, err := NewSchema(scope); err != nil {
		return SourceMessageWindow{}, ErrInvalidMessageWindow
	}
	id := uuid.MustParse(q.ChunkID).String()
	var out SourceMessageWindow
	var window []byte
	err := s.pool.QueryRow(ctx, s.schema.SQL(`SELECT m.chunk_id::text,o.observation_id::text,m.ordinal,m.role,m.occurred_at,octet_length(m.content),
 CASE WHEN octet_length(m.content)<=$4 THEN encode(sha256(convert_to(m.content,'UTF8')),'hex') ELSE '' END,
 CASE WHEN octet_length(m.content)<=$4 THEN substring(convert_to(m.content,'UTF8') FROM $3::integer+1 FOR $5::integer) END
 FROM {schema}.turn_message m JOIN {schema}.observation o USING(observation_id)
 WHERE o.scope=$1 AND m.chunk_id=$2::uuid`), scope, id, q.ByteStart, domain.MaxMessageBytes, q.Limit).Scan(&out.ChunkID, &out.SourceID, &out.Ordinal, &out.Role, &out.OccurredAt, &out.MessageBytes, &out.ContentDigest, &window)
	if errors.Is(err, pgx.ErrNoRows) {
		return SourceMessageWindow{}, ErrMessageNotFound
	}
	if err != nil {
		return SourceMessageWindow{}, err
	}
	if out.MessageBytes > domain.MaxMessageBytes || out.ContentDigest == "" {
		return SourceMessageWindow{}, ErrMessageUnavailable
	}
	if q.ExpectedDigest != "" && q.ExpectedDigest != out.ContentDigest {
		return SourceMessageWindow{}, ErrMessageChanged
	}
	if q.ByteStart > out.MessageBytes || (len(window) > 0 && !utf8.RuneStart(window[0])) {
		return SourceMessageWindow{}, ErrInvalidMessageWindow
	}
	// Only the trailing code point can be partial: PostgreSQL text is UTF-8 and the start was checked.
	for n := 0; !utf8.Valid(window) && len(window) > 0 && n < utf8.UTFMax-1; n++ {
		window = window[:len(window)-1]
	}
	if !utf8.Valid(window) || (len(window) == 0 && q.ByteStart < out.MessageBytes) {
		return SourceMessageWindow{}, ErrMessageUnavailable
	}
	out.ByteStart = q.ByteStart
	out.ByteEnd = q.ByteStart + len(window)
	out.Text = string(window)
	out.Complete = out.ByteStart == 0 && out.ByteEnd == out.MessageBytes
	if out.ByteEnd < out.MessageBytes {
		next := out.ByteEnd
		out.NextByteStart = &next
	}
	return out, nil
}
