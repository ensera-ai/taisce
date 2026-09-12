// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"context"
	"errors"
	"sort"

	"github.com/jackc/pgx/v5"
)

const MaxEntityNameVariants = 64

var ErrEntityNameLimit = errors.New("entity name variants exceed the supported bound")

// The source and entity are already locked by resolution. Independent sources keep independent
// receipts even when their spelling is identical; deleting one cannot remove the other's support.
func retainEntityNameTx(ctx context.Context, tx pgx.Tx, schema Schema, scope, source, entity, name, normalized string) error {
	if len(name) > 4096 || len(normalized) > 4096 {
		return ErrEntityNameLimit
	}
	var retained string
	err := tx.QueryRow(ctx, schema.SQL(`INSERT INTO {schema}.entity_name_receipt(scope,source_observation_id,entity_id,name,normalized_name)
        VALUES($1,$2::uuid,$3::uuid,$4,$5) ON CONFLICT(scope,source_observation_id,entity_id,name_hash)
        DO UPDATE SET name={schema}.entity_name_receipt.name RETURNING name`), scope, source, entity, name, normalized).Scan(&retained)
	if err != nil {
		return err
	}
	if retained != name {
		return ErrInvalidReceipt
	}
	// Ordinary formation touches only the bounded cache, not every prior supporting source.
	// Full reconstruction belongs to deletion/recovery, where support must actually be recounted.
	var canonical string
	var aliases []string
	if err := tx.QueryRow(ctx, schema.SQL(`SELECT canonical_name,aliases FROM {schema}.entity WHERE scope=$1 AND entity_id=$2::uuid FOR UPDATE`), scope, entity).Scan(&canonical, &aliases); err != nil {
		return err
	}
	if canonical == name {
		return nil
	}
	for _, alias := range aliases {
		if alias == name {
			return nil
		}
	}
	if len(aliases) >= MaxEntityNameVariants {
		return ErrEntityNameLimit
	}
	aliases = append(aliases, name)
	sort.Strings(aliases)
	_, err = tx.Exec(ctx, schema.SQL(`UPDATE {schema}.entity SET aliases=$3 WHERE scope=$1 AND entity_id=$2::uuid`), scope, entity, aliases)
	return err
}

// Arrays are a bounded display/search cache. Source-owned receipts survive entity loss and provide
// deterministic reconstruction. A missing entity requires no cache; its original fact can recover it.
func refreshEntityNamesTx(ctx context.Context, tx pgx.Tx, schema Schema, scope, entity string) (bool, error) {
	var canonical, normalized string
	err := tx.QueryRow(ctx, schema.SQL(`SELECT canonical_name,normalized_name FROM {schema}.entity
        WHERE scope=$1 AND entity_id=$2::uuid AND identity_kind='named' FOR UPDATE`), scope, entity).Scan(&canonical, &normalized)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	rows, err := tx.Query(ctx, schema.SQL(`SELECT DISTINCT name,normalized_name FROM {schema}.entity_name_receipt
        WHERE scope=$1 AND entity_id=$2::uuid AND name<>$3 ORDER BY name,normalized_name LIMIT $4`), scope, entity, canonical, MaxEntityNameVariants+1)
	if err != nil {
		return false, err
	}
	aliases := []string{}
	for rows.Next() {
		var name, norm string
		if err := rows.Scan(&name, &norm); err != nil {
			rows.Close()
			return false, err
		}
		if norm != normalized {
			rows.Close()
			return false, ErrInvalidReceipt
		}
		aliases = append(aliases, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, err
	}
	if len(aliases) > MaxEntityNameVariants {
		return false, ErrEntityNameLimit
	}
	tag, err := tx.Exec(ctx, schema.SQL(`UPDATE {schema}.entity SET aliases=$3 WHERE scope=$1 AND entity_id=$2::uuid
        AND aliases IS DISTINCT FROM $3::text[]`), scope, entity, aliases)
	return tag.RowsAffected() > 0, err
}

// Source locks precede entity locks in formation, recovery and removal. Sorting shared endpoints
// gives overlapping deletion batches the same cache-lock order.
func removeEntityNamesTx(ctx context.Context, tx pgx.Tx, schema Schema, scope, subject string, sources []string) (int, error) {
	rows, err := tx.Query(ctx, schema.SQL(`DELETE FROM {schema}.entity_name_receipt r USING {schema}.observation o
        WHERE r.scope=$1 AND o.scope=r.scope AND o.observation_id=r.source_observation_id
        AND (($2<>'' AND o.data_subject_id=$2) OR r.source_observation_id=ANY($3::uuid[])) RETURNING r.entity_id::text`), scope, subject, sources)
	if err != nil {
		return 0, err
	}
	ids := map[string]bool{}
	count := 0
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids[id] = true
		count++
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	ordered := make([]string, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)
	for _, id := range ordered {
		if _, err := refreshEntityNamesTx(ctx, tx, schema, scope, id); err != nil {
			return 0, err
		}
	}
	return count, nil
}
