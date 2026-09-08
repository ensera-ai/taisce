// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"context"
	"github.com/jackc/pgx/v5"
)

type ArtifactLimits struct {
	MaxObjectBytes int64 `json:"max_object_bytes"`
	MaxBytes       int64 `json:"max_bytes"`
	MaxObjects     int64 `json:"max_objects"`
	MaxAgeHours    int   `json:"max_age_hours"`
	UsedBytes      int64 `json:"used_bytes"`
	UsedObjects    int64 `json:"used_objects"`
}

func (v ArtifactLimits) Validate() error {
	if v.MaxObjectBytes < 1 || v.MaxObjectBytes > MaxArtifactBytes || v.MaxBytes < v.MaxObjectBytes || v.MaxBytes > 1<<30 || v.MaxObjects < 1 || v.MaxObjects > 100000 || v.MaxAgeHours < 1 || v.MaxAgeHours > 8760 {
		return ErrInvalidArtifact
	}
	return nil
}
func (s *ArtifactStore) Limits(ctx context.Context, scope string) (ArtifactLimits, error) {
	var out ArtifactLimits
	err := s.pool.QueryRow(ctx, s.schema.SQL(`SELECT max_object_bytes,max_bytes,max_objects,max_age_hours,used_bytes,used_objects FROM {schema}.agent_storage_policy WHERE scope=$1`), scope).Scan(&out.MaxObjectBytes, &out.MaxBytes, &out.MaxObjects, &out.MaxAgeHours, &out.UsedBytes, &out.UsedObjects)
	if err == pgx.ErrNoRows {
		return out, ErrNoSuchProject
	}
	return out, err
}

// ConfigureLimits requires an operator connection. Lowering limits preserves existing objects;
// shrinking/deleting remains possible while above a new allowance. TTL changes affect new roots.
func (s *ArtifactStore) ConfigureLimits(ctx context.Context, scope, principal string, v ArtifactLimits) error {
	if err := v.Validate(); err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, s.schema.SQL(`UPDATE {schema}.agent_storage_policy SET max_object_bytes=$2,max_bytes=$3,max_objects=$4,max_age_hours=$5 WHERE scope=$1`), scope, v.MaxObjectBytes, v.MaxBytes, v.MaxObjects, v.MaxAgeHours)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrNoSuchProject
		}
		_, err = tx.Exec(ctx, s.schema.SQL(`INSERT INTO {schema}.audit_entry(operation,principal,principal_kind,project,magnitude,outcome) VALUES('artifact.configure',$1,'operator',$2,1,'allowed')`), principal, scope)
		return err
	})
}
