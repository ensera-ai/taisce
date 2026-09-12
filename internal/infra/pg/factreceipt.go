// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const MaxRecoveryPage = 100

var ErrInvalidRecovery = errors.New("recovery requires a valid cursor, source and page size")
var ErrInvalidReceipt = errors.New("formation receipt no longer matches its surviving source")

// RecoveryPage is a live UUID page. Repeating a page preserves retained records, and an interrupted
// transaction advances nothing. A new full pass finds concurrent additions below a previous cursor.
type RecoveryPage struct {
	Examined int    `json:"examined"`
	Restored int    `json:"restored"`
	Repaired int    `json:"repaired"`
	Next     string `json:"next,omitempty"`
}

// RecoverFacts restores one bounded page without a model call or a delete of available memory.
// It does not claim to regenerate embeddings, summaries, or interpretations under new model rules.
func (s *FactStore) RecoverFacts(ctx context.Context, schema Schema, scope, source, after, principal string, limit int) (RecoveryPage, error) {
	out := RecoveryPage{}
	if limit < 1 || limit > MaxRecoveryPage {
		return out, ErrInvalidRecovery
	}
	for _, v := range []string{source, after, principal} {
		if v != "" {
			if _, err := uuid.Parse(v); err != nil {
				return out, ErrInvalidRecovery
			}
		}
	}
	if principal == "" {
		return out, ErrInvalidRecovery
	}
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, schema.SQL(`SELECT fact_id::text,source_observation_id::text FROM {schema}.fact_receipt
            WHERE scope=$1 AND ($2::uuid IS NULL OR source_observation_id=$2::uuid)
            AND ($3::uuid IS NULL OR fact_id>$3::uuid) ORDER BY fact_id LIMIT $4`), scope, nullIfEmptyString(source), nullIfEmptyString(after), limit+1)
		if err != nil {
			return err
		}
		type entry struct{ id, source string }
		var entries []entry
		for rows.Next() {
			var e entry
			if err := rows.Scan(&e.id, &e.source); err != nil {
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
			out.Next = entries[len(entries)-1].id
		}
		// Curation also takes source locks in sorted order. Use the same order across a page.
		sources := make([]string, 0, len(entries))
		for _, e := range entries {
			sources = append(sources, e.source)
		}
		sort.Strings(sources)
		for _, v := range sources {
			if err := lockClaimSource(ctx, tx, schema, scope, v); err != nil {
				return err
			}
		}
		for _, e := range entries {
			restored, err := restoreFactReceiptTx(ctx, tx, schema, scope, e.id)
			if err != nil {
				return err
			}
			out.Examined++
			if restored {
				out.Restored++
			} else {
				repaired, err := repairFactSupportTx(ctx, tx, schema, scope, e.id)
				if err != nil {
					return err
				}
				if repaired {
					out.Repaired++
				}
			}
		}
		_, err = tx.Exec(ctx, schema.SQL(`INSERT INTO {schema}.audit_entry(operation,principal,principal_kind,project,magnitude,outcome)
            VALUES('formation.recover',$1,'operator',$2,$3,'allowed')`), principal, scope, out.Restored+out.Repaired)
		return err
	})
	if err != nil {
		return RecoveryPage{}, fmt.Errorf("recover facts: %w", err)
	}
	return out, nil
}

// Receipt state is a typed PostgreSQL row, not a model response. Source byte verification prevents
// recovery from a changed transcript. Existing rows win; recovery never restamps current memory.
func restoreFactReceiptTx(ctx context.Context, tx pgx.Tx, schema Schema, scope, id string) (bool, error) {
	var state, evidence, left, right []byte
	var source, owner, pipeline, text string
	var ordinal int
	err := tx.QueryRow(ctx, schema.SQL(`SELECT r.state,r.evidence,r.subject_identity,r.object_identity,r.source_observation_id::text,
        coalesce(o.data_subject_id,''),r.pipeline_version,m.content,r.source_ordinal
        FROM {schema}.fact_receipt r JOIN {schema}.observation o ON o.scope=r.scope AND o.observation_id=r.source_observation_id
        JOIN {schema}.turn_message m ON m.observation_id=o.observation_id AND m.ordinal=r.source_ordinal
        WHERE r.scope=$1 AND r.fact_id=$2::uuid AND NOT EXISTS(SELECT 1 FROM {schema}.fact f WHERE f.scope=r.scope AND f.fact_id=r.fact_id) FOR UPDATE OF r FOR KEY SHARE OF o,m`), scope, id).Scan(&state, &evidence, &left, &right, &source, &owner, &pipeline, &text, &ordinal)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var e struct {
		Quote   string `json:"quote"`
		Start   int    `json:"byte_start"`
		End     int    `json:"byte_end"`
		Source  string `json:"source_observation_id"`
		Ordinal int    `json:"source_ordinal"`
		Fact    string `json:"fact_id"`
		Scope   string `json:"scope"`
	}
	if err := json.Unmarshal(evidence, &e); err != nil {
		return false, ErrInvalidReceipt
	}
	if e.Start < 0 || e.End <= e.Start || e.End > len(text) || text[e.Start:e.End] != e.Quote || e.Source != source || e.Ordinal != ordinal || e.Fact != id || e.Scope != scope {
		return false, ErrInvalidReceipt
	}
	for _, identity := range [][]byte{left, right} {
		if len(identity) == 0 || string(identity) == "null" {
			continue
		}
		var identityID, identityScope string
		if err := tx.QueryRow(ctx, `SELECT $1::jsonb->>'entity_id',$1::jsonb->>'scope'`, identity).Scan(&identityID, &identityScope); err != nil {
			return false, err
		}
		if identityScope != scope {
			return false, ErrInvalidReceipt
		}
		if _, err := tx.Exec(ctx, schema.SQL(`INSERT INTO {schema}.entity(entity_id,scope,canonical_name,normalized_name,entity_type,resolution_version,first_seen_at,identity_kind,speaker_subject_id)
            SELECT entity_id,scope,canonical_name,normalized_name,entity_type,resolution_version,first_seen_at,identity_kind,speaker_subject_id
            FROM jsonb_populate_record(NULL::{schema}.entity,$1::jsonb) ON CONFLICT(entity_id) DO NOTHING`), identity); err != nil {
			return false, err
		}
		if _, err := tx.Exec(ctx, schema.SQL(`INSERT INTO {schema}.projection_dependency(source_observation_id,scope,projection_kind,projection_id,data_subject_id,pipeline_version)
            VALUES($1::uuid,$2,'entity',$3,$4,$5) ON CONFLICT DO NOTHING`), source, scope, identityID, nullIfEmpty(owner), pipeline); err != nil {
			return false, err
		}
	}
	if err := validateReceiptEntitiesTx(ctx, tx, schema, scope, state, left, right); err != nil {
		return false, err
	}
	if _, err := repairReceiptEntityNamesTx(ctx, tx, schema, scope, state); err != nil {
		return false, err
	}
	// A successor may be in another page. Keep the source-owned link in the receipt and attach
	// the live FK as soon as that successor is available; intervals are exact on every page.
	if _, err := tx.Exec(ctx, schema.SQL(`INSERT INTO {schema}.fact(fact_id,scope,subject_entity_id,object_entity_id,predicate,statement,valid,known,confidence,data_subject_id,cardinality,source_role,recorded_at,superseded_by,supersession_source,version,lang,retention_until)
        SELECT f.fact_id,f.scope,f.subject_entity_id,f.object_entity_id,f.predicate,f.statement,f.valid,f.known,f.confidence,f.data_subject_id,f.cardinality,f.source_role,f.recorded_at,
        CASE WHEN EXISTS(SELECT 1 FROM {schema}.fact s WHERE s.scope=$2 AND s.fact_id=f.superseded_by) THEN f.superseded_by END,f.supersession_source,f.version,f.lang,f.retention_until
        FROM jsonb_populate_record(NULL::{schema}.fact,$1::jsonb) f`), state, scope); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, schema.SQL(`INSERT INTO {schema}.fact_evidence(fact_id,scope,source_observation_id,source_ordinal,quote,byte_start,byte_end,extractor_version)
        SELECT fact_id,scope,source_observation_id,source_ordinal,quote,byte_start,byte_end,extractor_version FROM jsonb_populate_record(NULL::{schema}.fact_evidence,$1::jsonb)`), evidence); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, schema.SQL(`INSERT INTO {schema}.projection_dependency(source_observation_id,scope,projection_kind,projection_id,data_subject_id,pipeline_version)
        VALUES($1::uuid,$2,'fact',$3,$4,$5) ON CONFLICT DO NOTHING`), source, scope, id, nullIfEmpty(owner), pipeline); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, schema.SQL(`INSERT INTO {schema}.fact_history(history_id,scope,fact_id,valid,known,source_observation_id)
        SELECT history_id,scope,fact_id,valid,known,source_observation_id FROM {schema}.fact_receipt_history WHERE scope=$1 AND fact_id=$2::uuid`), scope, id); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, schema.SQL(`INSERT INTO {schema}.projection_dependency(source_observation_id,scope,projection_kind,projection_id,data_subject_id,pipeline_version)
        SELECT $3::uuid,$1,'fact_history',history_id::text,$4,$5 FROM {schema}.fact_receipt_history WHERE scope=$1 AND fact_id=$2::uuid
        UNION SELECT h.source_observation_id,h.scope,'fact_history',h.history_id::text,o.data_subject_id,$5
        FROM {schema}.fact_receipt_history h JOIN {schema}.observation o ON o.scope=h.scope AND o.observation_id=h.source_observation_id
        WHERE h.scope=$1 AND h.fact_id=$2::uuid ON CONFLICT DO NOTHING`), scope, id, source, nullIfEmpty(owner), pipeline); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, schema.SQL(`UPDATE {schema}.fact f SET superseded_by=r.superseded_by FROM {schema}.fact_receipt r
        WHERE r.scope=$1 AND r.superseded_by=$2::uuid AND f.scope=r.scope AND f.fact_id=r.fact_id AND f.superseded_by IS NULL`), scope, id); err != nil {
		return false, err
	}
	return true, nil
}

const retainFactReceiptSQL = `INSERT INTO {schema}.fact_receipt(scope,fact_id,source_observation_id,source_ordinal,state,evidence,subject_identity,object_identity,pipeline_version,supersession_source,superseded_by)
 SELECT f.scope,f.fact_id,e.source_observation_id,e.source_ordinal,to_jsonb(f),to_jsonb(e),
        to_jsonb(l)-'aliases'-'normalized_aliases',to_jsonb(r)-'aliases'-'normalized_aliases',d.pipeline_version,f.supersession_source,f.superseded_by
 FROM {schema}.fact f JOIN {schema}.fact_evidence e ON e.scope=f.scope AND e.fact_id=f.fact_id
 JOIN {schema}.projection_dependency d ON d.scope=f.scope AND d.fact_ref=f.fact_id AND d.source_observation_id=e.source_observation_id
 LEFT JOIN {schema}.entity l ON l.scope=f.scope AND l.entity_id=f.subject_entity_id
 LEFT JOIN {schema}.entity r ON r.scope=f.scope AND r.entity_id=f.object_entity_id
 WHERE f.scope=$1 AND f.fact_id=$2::uuid ON CONFLICT(scope,fact_id) DO NOTHING`

// receiptOwnershipSQLFor is the ownership predicate over any subquery of owned observation ids, so
// an erasure by source counts receipts exactly as an erasure by subject does, and both governance
// readers — the export and the erasure — ask the question in exactly one way. The table names are a
// fixed internal set.
func receiptOwnershipSQLFor(table, owned string) string {
	if table == "fact_receipt_history" {
		return `r.scope=$1 AND (r.source_observation_id IN (` + owned + `) OR EXISTS(SELECT 1 FROM {schema}.fact_receipt p WHERE p.scope=r.scope AND p.fact_id=r.fact_id AND (p.source_observation_id IN (` + owned + `) OR p.supersession_source IN (` + owned + `))))`
	}
	return `r.scope=$1 AND (r.source_observation_id IN (` + owned + `) OR r.supersession_source IN (` + owned + `))`
}
