// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
)

// Repair support only after ordinary missing-fact recovery declined the row. Lock the retained fact
// and its receipt so correction cannot change the state between validation and registration. A
// missing component is recoverable; an existing conflicting component is an integrity refusal,
// never an invitation to rewrite a caller-visible record or guess at authoritative source bytes.
func repairFactSupportTx(ctx context.Context, tx pgx.Tx, schema Schema, scope, id string) (bool, error) {
	var evidence, state, left, right []byte
	var source, text, owner string
	var factOwner *string
	var ordinal int
	err := tx.QueryRow(ctx, schema.SQL(`SELECT r.evidence,r.source_observation_id::text,r.source_ordinal,m.content,coalesce(o.data_subject_id,''),f.data_subject_id,to_jsonb(f),r.subject_identity,r.object_identity
        FROM {schema}.fact_receipt r JOIN {schema}.fact f ON f.scope=r.scope AND f.fact_id=r.fact_id
        JOIN {schema}.observation o ON o.scope=r.scope AND o.observation_id=r.source_observation_id
        JOIN {schema}.turn_message m ON m.observation_id=o.observation_id AND m.ordinal=r.source_ordinal
        WHERE r.scope=$1 AND r.fact_id=$2::uuid FOR UPDATE OF f,r FOR KEY SHARE OF o,m`), scope, id).Scan(&evidence, &source, &ordinal, &text, &owner, &factOwner, &state, &left, &right)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var saved struct {
		Quote   string `json:"quote"`
		Start   int    `json:"byte_start"`
		End     int    `json:"byte_end"`
		Source  string `json:"source_observation_id"`
		Ordinal int    `json:"source_ordinal"`
		Fact    string `json:"fact_id"`
		Scope   string `json:"scope"`
	}
	if err := json.Unmarshal(evidence, &saved); err != nil {
		return false, ErrInvalidReceipt
	}
	if saved.Start < 0 || saved.End <= saved.Start || saved.End > len(text) || text[saved.Start:saved.End] != saved.Quote || saved.Source != source || saved.Ordinal != ordinal || saved.Fact != id || saved.Scope != scope || (factOwner == nil && owner != "") || (factOwner != nil && *factOwner != owner) {
		return false, ErrInvalidReceipt
	}
	var conflict bool
	if err := validateReceiptEntitiesTx(ctx, tx, schema, scope, state, left, right); err != nil {
		return false, err
	}
	namesRepaired, err := repairReceiptEntityNamesTx(ctx, tx, schema, scope, state)
	if err != nil {
		return false, err
	}
	var evidenceAdded int
	err = tx.QueryRow(ctx, schema.SQL(`WITH expected AS (
        SELECT * FROM jsonb_populate_record(NULL::{schema}.fact_evidence,$1::jsonb)
    ), conflict AS (
        SELECT 1 FROM expected e JOIN {schema}.fact_evidence a
          ON a.fact_id=e.fact_id AND a.source_observation_id=e.source_observation_id AND a.source_ordinal=e.source_ordinal AND a.byte_start=e.byte_start
        WHERE (a.scope,a.quote,a.byte_end,a.extractor_version) IS DISTINCT FROM (e.scope,e.quote,e.byte_end,e.extractor_version)
    ), added AS (
        INSERT INTO {schema}.fact_evidence(fact_id,scope,source_observation_id,source_ordinal,quote,byte_start,byte_end,extractor_version)
        SELECT fact_id,scope,source_observation_id,source_ordinal,quote,byte_start,byte_end,extractor_version FROM expected
        WHERE NOT EXISTS(SELECT 1 FROM conflict) ON CONFLICT DO NOTHING RETURNING 1
    ) SELECT EXISTS(SELECT 1 FROM conflict),(SELECT count(*) FROM added)`), evidence).Scan(&conflict, &evidenceAdded)
	if err != nil {
		return false, err
	}
	if conflict {
		return false, ErrInvalidReceipt
	}
	var historyAdded int
	err = tx.QueryRow(ctx, schema.SQL(`WITH expected AS (
        SELECT scope,history_id,fact_id,valid,known,source_observation_id FROM {schema}.fact_receipt_history WHERE scope=$1 AND fact_id=$2::uuid
    ), conflict AS (
        SELECT 1 FROM expected e JOIN {schema}.fact_history a USING(history_id)
        WHERE (a.scope,a.fact_id,a.valid,a.known,a.source_observation_id) IS DISTINCT FROM (e.scope,e.fact_id,e.valid,e.known,e.source_observation_id)
    ), added AS (
        INSERT INTO {schema}.fact_history(scope,history_id,fact_id,valid,known,source_observation_id)
        SELECT scope,history_id,fact_id,valid,known,source_observation_id FROM expected
        WHERE NOT EXISTS(SELECT 1 FROM conflict) ON CONFLICT DO NOTHING RETURNING 1
    ) SELECT EXISTS(SELECT 1 FROM conflict),(SELECT count(*) FROM added)`), scope, id).Scan(&conflict, &historyAdded)
	if err != nil {
		return false, err
	}
	if conflict {
		return false, ErrInvalidReceipt
	}
	var registrationsAdded int
	err = tx.QueryRow(ctx, schema.SQL(`WITH expected AS (`+factSupportRegistrationsSQL+`), conflict AS (
        SELECT 1 FROM expected e JOIN {schema}.projection_dependency a USING(source_observation_id,projection_kind,projection_id)
        WHERE (a.scope,a.data_subject_id,a.pipeline_version) IS DISTINCT FROM (e.scope,e.data_subject_id,e.pipeline_version)
    ), added AS (
        INSERT INTO {schema}.projection_dependency(source_observation_id,scope,projection_kind,projection_id,data_subject_id,pipeline_version)
        SELECT source_observation_id,scope,projection_kind,projection_id,data_subject_id,pipeline_version FROM expected
        WHERE NOT EXISTS(SELECT 1 FROM conflict) ON CONFLICT DO NOTHING RETURNING 1
    ) SELECT EXISTS(SELECT 1 FROM conflict),(SELECT count(*) FROM added)`), scope, id).Scan(&conflict, &registrationsAdded)
	if err != nil {
		return false, err
	}
	if conflict {
		return false, ErrInvalidReceipt
	}
	return evidenceAdded+historyAdded+registrationsAdded > 0 || namesRepaired, nil
}

// Expected registrations use the live retained endpoints and the source-owned history. A temporal
// transition belongs to both original and causal sources; rebuilding just one registration would
// make erasure by the other subject silently leave their historical contribution behind.
const factSupportRegistrationsSQL = `
    SELECT r.source_observation_id,r.scope,'fact'::text AS projection_kind,r.fact_id::text AS projection_id,o.data_subject_id,r.pipeline_version
    FROM {schema}.fact_receipt r JOIN {schema}.observation o ON o.scope=r.scope AND o.observation_id=r.source_observation_id
    WHERE r.scope=$1 AND r.fact_id=$2::uuid
    UNION
    SELECT r.source_observation_id,r.scope,'entity',endpoint.id::text,o.data_subject_id,r.pipeline_version
    FROM {schema}.fact_receipt r JOIN {schema}.fact f ON f.scope=r.scope AND f.fact_id=r.fact_id
    JOIN {schema}.observation o ON o.scope=r.scope AND o.observation_id=r.source_observation_id
    CROSS JOIN LATERAL unnest(ARRAY[f.subject_entity_id,f.object_entity_id]) endpoint(id)
    WHERE r.scope=$1 AND r.fact_id=$2::uuid AND endpoint.id IS NOT NULL
    UNION
    SELECT r.source_observation_id,r.scope,'fact_history',h.history_id::text,o.data_subject_id,r.pipeline_version
    FROM {schema}.fact_receipt r JOIN {schema}.fact_receipt_history h ON h.scope=r.scope AND h.fact_id=r.fact_id
    JOIN {schema}.observation o ON o.scope=r.scope AND o.observation_id=r.source_observation_id
    WHERE r.scope=$1 AND r.fact_id=$2::uuid
    UNION
    SELECT h.source_observation_id,h.scope,'fact_history',h.history_id::text,o.data_subject_id,r.pipeline_version
    FROM {schema}.fact_receipt r JOIN {schema}.fact_receipt_history h ON h.scope=r.scope AND h.fact_id=r.fact_id
    JOIN {schema}.observation o ON o.scope=h.scope AND o.observation_id=h.source_observation_id
    WHERE r.scope=$1 AND r.fact_id=$2::uuid`
