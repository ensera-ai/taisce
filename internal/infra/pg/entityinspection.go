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

// Entity inspection exposes identity behind a record endpoint. It does not infer semantic matches,
// expand the graph, or return generated descriptions. Project authority is reapplied on every page.
const EntityPreviewCharacters = 512

var (
	ErrInvalidEntityPage     = errors.New("invalid entity page")
	ErrEntityNotFound        = errors.New("entity not found")
	ErrInvalidEntityIdentity = errors.New("stored entity identity exceeds the supported inspection bounds")
)

type EntityCursor struct {
	ID string `json:"id"`
}
type EntitySummary struct {
	ID            string    `json:"id"`
	Kind          string    `json:"identity_kind"`
	NamePreview   string    `json:"name_preview"`
	NameBytes     int       `json:"name_bytes"`
	NameTruncated bool      `json:"name_truncated"`
	AliasCount    int       `json:"alias_count"`
	FirstSeenAt   time.Time `json:"first_seen_at"`
}
type EntityPage struct {
	Entities []EntitySummary `json:"entities"`
	Next     *EntityCursor   `json:"next,omitempty"`
}
type EntityIdentity struct {
	ID               string    `json:"id"`
	Kind             string    `json:"identity_kind"`
	Name             string    `json:"canonical_name"`
	Aliases          []string  `json:"aliases"`
	SpeakerSubjectID *string   `json:"speaker_subject_id,omitempty"`
	FirstSeenAt      time.Time `json:"first_seen_at"`
}

// ListEntities is a live inventory ordered by immutable UUID, not an isolation snapshot. Deleting
// the cursor row cannot move earlier rows into the next page; inserts behind it require a restart.
func (s *RecordStore) ListEntities(ctx context.Context, scope string, after *EntityCursor, limit int) (EntityPage, error) {
	out := EntityPage{Entities: []EntitySummary{}}
	if _, err := NewSchema(scope); err != nil || limit < 1 || limit > MaxRecordPage {
		return out, ErrInvalidEntityPage
	}
	cursor := uuid.Nil.String()
	if after != nil {
		id, err := uuid.Parse(after.ID)
		if err != nil || id == uuid.Nil {
			return out, ErrInvalidEntityPage
		}
		cursor = id.String()
	}
	rows, err := s.pool.Query(ctx, s.schema.SQL(`SELECT e.entity_id::text,identity_kind,left(canonical_name,$4),octet_length(canonical_name),char_length(canonical_name)>$4,cardinality(aliases),first_seen_at
  FROM {schema}.entity e WHERE scope=$1 AND e.entity_id>$2::uuid ORDER BY e.entity_id LIMIT $3`), scope, cursor, limit+1, EntityPreviewCharacters)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var e EntitySummary
		if err := rows.Scan(&e.ID, &e.Kind, &e.NamePreview, &e.NameBytes, &e.NameTruncated, &e.AliasCount, &e.FirstSeenAt); err != nil {
			return EntityPage{}, err
		}
		out.Entities = append(out.Entities, e)
	}
	if err := rows.Err(); err != nil {
		return EntityPage{}, err
	}
	if len(out.Entities) > limit {
		out.Entities = out.Entities[:limit]
		out.Next = &EntityCursor{ID: out.Entities[limit-1].ID}
	}
	return out, nil
}

func (s *RecordStore) InspectEntity(ctx context.Context, scope, id string) (EntityIdentity, error) {
	var out EntityIdentity
	if _, err := NewSchema(scope); err != nil {
		return out, ErrInvalidEntityPage
	}
	parsed, err := uuid.Parse(id)
	if err != nil || parsed == uuid.Nil {
		return out, ErrInvalidEntityPage
	}
	// SQL clips transferred fields before Go allocates them, and separately checks that no clipping
	// was necessary. Corrupt/unsupported rows refuse rather than returning a prefix as an identity.
	var compatible bool
	err = s.pool.QueryRow(ctx, s.schema.SQL(`SELECT entity_id::text,identity_kind,left(canonical_name,4096),
  ARRAY(SELECT left(a,4096) FROM unnest(aliases[1:64]) a),left(speaker_subject_id,1048576),first_seen_at,
  octet_length(canonical_name)<=4096 AND cardinality(aliases)<=64
  AND NOT EXISTS(SELECT 1 FROM unnest(aliases[1:64]) a WHERE octet_length(a)>4096)
  AND coalesce(octet_length(speaker_subject_id)<=1048576,true)
  FROM {schema}.entity WHERE scope=$1 AND entity_id=$2::uuid`), scope, parsed.String()).Scan(&out.ID, &out.Kind, &out.Name, &out.Aliases, &out.SpeakerSubjectID, &out.FirstSeenAt, &compatible)
	if errors.Is(err, pgx.ErrNoRows) {
		return EntityIdentity{}, ErrEntityNotFound
	}
	if err != nil {
		return EntityIdentity{}, err
	}
	if !compatible {
		return EntityIdentity{}, ErrInvalidEntityIdentity
	}
	return out, nil
}
