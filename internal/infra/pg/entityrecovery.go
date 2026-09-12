// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
)

// Identity must agree with the receipt's endpoint, not just occupy the same UUID. Row locks also
// allow the caller to repair its alias cache without upgrading competing shared locks. Alias
// ownership is checked separately through source-owned name receipts.
func validateReceiptEntitiesTx(ctx context.Context, tx pgx.Tx, schema Schema, scope string, state, left, right []byte) error {
	var saved struct {
		Scope   string  `json:"scope"`
		Subject *string `json:"subject_entity_id"`
		Object  *string `json:"object_entity_id"`
	}
	if json.Unmarshal(state, &saved) != nil || saved.Scope != scope {
		return ErrInvalidReceipt
	}
	for i, identity := range [][]byte{left, right} {
		endpoint := saved.Subject
		if i == 1 {
			endpoint = saved.Object
		}
		if endpoint == nil {
			if len(identity) != 0 && string(identity) != "null" {
				return ErrInvalidReceipt
			}
			continue
		}
		var matches bool
		err := tx.QueryRow(ctx, schema.SQL(`SELECT
            (e.entity_id,e.scope,e.canonical_name,e.normalized_name,e.identity_kind,e.speaker_subject_id)
            IS NOT DISTINCT FROM (s.entity_id,s.scope,s.canonical_name,s.normalized_name,s.identity_kind,s.speaker_subject_id)
            FROM {schema}.entity e CROSS JOIN jsonb_populate_record(NULL::{schema}.entity,$3::jsonb) s
            WHERE e.scope=$1 AND e.entity_id=$2::uuid FOR UPDATE OF e`), scope, *endpoint, identity).Scan(&matches)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && !matches) {
			return ErrInvalidReceipt
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func repairReceiptEntityNamesTx(ctx context.Context, tx pgx.Tx, schema Schema, scope string, state []byte) (bool, error) {
	var endpoints struct {
		Subject *string `json:"subject_entity_id"`
		Object  *string `json:"object_entity_id"`
	}
	if json.Unmarshal(state, &endpoints) != nil {
		return false, ErrInvalidReceipt
	}
	repaired := false
	for _, id := range []*string{endpoints.Subject, endpoints.Object} {
		if id == nil {
			continue
		}
		changed, err := refreshEntityNamesTx(ctx, tx, schema, scope, *id)
		if err != nil {
			return false, err
		}
		repaired = repaired || changed
	}
	return repaired, nil
}
