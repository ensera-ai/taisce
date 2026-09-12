// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/jackc/pgx/v5"
)

const CuratedExtractorVersion = "curated/v1"

type RecordCorrection struct {
	RecordMutation
	Object    string    `json:"object"`
	Statement string    `json:"statement"`
	ValidFrom time.Time `json:"valid_from,omitempty"`
}

type CorrectedRecord struct {
	OriginalID          string `json:"original_id"`
	ID                  string `json:"id"`
	Version             string `json:"version"`
	SourceObservationID string `json:"source_observation_id"`
}

type CorrectionBatch struct {
	Records []CorrectedRecord `json:"records"`
}

// Correct preserves subject and relation. Its independent authored source replaces only a current
// target, never rewrites a transcript, and makes no provider call while holding database locks.
func (s *RecordStore) Correct(ctx context.Context, scope, principal string, requests []RecordCorrection) (CorrectionBatch, error) {
	var out CorrectionBatch
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		out, err = s.correctTx(ctx, tx, scope, principal, requests)
		return err
	})
	if err != nil {
		return CorrectionBatch{}, err
	}
	return out, nil
}

// The caller owns commit, so promoting feedback can withdraw the target, assert the replacement and
// mark the feedback promoted in one transaction. Split out rather than duplicated: a second copy of
// this body would be a second definition of what a correction is, and the day they diverged, one of
// the two would be producing records the rebuild and the retraction ledger did not agree about.
func (s *RecordStore) correctTx(ctx context.Context, tx pgx.Tx, scope, principal string, requests []RecordCorrection) (CorrectionBatch, error) {
	out := CorrectionBatch{Records: []CorrectedRecord{}}
	if len(requests) < 1 || len(requests) > MaxRecordMutations {
		return out, ErrInvalidRecordMutation
	}
	mutations := make([]RecordMutation, len(requests))
	for i, r := range requests {
		if strings.TrimSpace(r.Object) == "" || strings.TrimSpace(r.Statement) == "" || len(r.Object) > 1024 || len(r.Statement) > 16384 || !utf8.ValidString(r.Object) || !utf8.ValidString(r.Statement) || strings.ContainsRune(r.Object, 0) || strings.ContainsRune(r.Statement, 0) || (!r.ValidFrom.IsZero() && (r.ValidFrom.Year() < 1 || r.ValidFrom.Year() > 9999)) {
			return out, ErrInvalidRecordMutation
		}
		mutations[i] = r.RecordMutation
	}
	err := func() error {
		withdrawn, err := s.retractTx(ctx, tx, scope, principal, mutations)
		if err != nil {
			return err
		}
		for i, r := range requests {
			original := withdrawn.Records[i].ID
			var subject, predicate, owner, cardinality, objectKind string
			var open bool
			err := tx.QueryRow(ctx, s.schema.SQL(`SELECT
       CASE WHEN e.identity_kind='speaker' THEN (SELECT term FROM {schema}.speaker_term ORDER BY term LIMIT 1) ELSE e.canonical_name END,
       f.predicate,coalesce(f.data_subject_id,''),f.cardinality,p.object_kind,upper_inf(f.valid)
       FROM {schema}.fact f JOIN {schema}.entity e ON e.scope=f.scope AND e.entity_id=f.subject_entity_id
       JOIN {schema}.predicate p ON p.predicate=f.predicate WHERE f.scope=$1 AND f.fact_id=$2::uuid`), scope, original).Scan(&subject, &predicate, &owner, &cardinality, &objectKind, &open)
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrInvalidRecordMutation
			}
			if err != nil {
				return err
			}
			if !open {
				return ErrRecordConflict
			}
			// One knowledge boundary: the correction is visible exactly when its target ends.
			now := withdrawn.Records[i].RetractedAt
			validFrom := r.ValidFrom
			if validFrom.IsZero() {
				validFrom = now
			}
			claim := domain.Claim{Subject: subject, Predicate: predicate, Object: r.Object, Statement: r.Statement, Quote: r.Statement, ByteEnd: len(r.Statement), Cardinality: domain.Cardinality(cardinality), ObjectType: objectKind, ValidFrom: validFrom}
			id, sourceID, err := authorCuratedTx(ctx, tx, s.schema, scope, principal, owner, claim, now)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, s.schema.SQL(`UPDATE {schema}.record_retraction SET replacement_observation_id=$3::uuid,replacement_fact_id=$4::uuid WHERE scope=$1 AND target_fact_id=$2::uuid`), scope, original, sourceID, id); err != nil {
				return err
			}
			result := CorrectedRecord{OriginalID: original, ID: id, SourceObservationID: sourceID}
			if err := tx.QueryRow(ctx, s.schema.SQL(`SELECT version::text FROM {schema}.fact WHERE scope=$1 AND fact_id=$2::uuid`), scope, id).Scan(&result.Version); err != nil {
				return err
			}
			out.Records = append(out.Records, result)
		}
		if _, err := tx.Exec(ctx, s.schema.SQL(advanceFormedWatermarkSQL), scope); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, s.schema.SQL(`INSERT INTO {schema}.audit_entry(operation,principal,principal_kind,project,magnitude,outcome) VALUES('record.correct',$1,'credential',$2,$3,'allowed')`), principal, scope, len(requests))
		return err
	}()
	if err != nil {
		return CorrectionBatch{}, fmt.Errorf("correct records: %w", err)
	}
	return out, nil
}

// ReplayCurated refuses missing/changed authored instructions instead of letting model extraction
// replace human input. Its immutable source claim and original citation ID commit or fail together.
func (s *FactStore) ReplayCurated(ctx context.Context, schema Schema, scope, observationID string) (bool, error) {
	var kind string
	if err := s.pool.QueryRow(ctx, schema.SQL(`SELECT kind FROM {schema}.observation WHERE scope=$1 AND observation_id=$2::uuid`), scope, observationID).Scan(&kind); err != nil {
		return false, err
	}
	if kind != "curated" {
		return false, nil
	}
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var payload []byte
		var expected, owner string
		var knownAt time.Time
		if err := tx.QueryRow(ctx, schema.SQL(`SELECT c.claim,c.fact_id::text,coalesce(o.data_subject_id,''),o.occurred_at FROM {schema}.curated_claim c JOIN {schema}.observation o ON o.scope=c.scope AND o.observation_id=c.source_observation_id WHERE c.scope=$1 AND c.source_observation_id=$2::uuid AND o.kind='curated'`), scope, observationID).Scan(&payload, &expected, &owner, &knownAt); err != nil {
			return err
		}
		var claim domain.Claim
		if err := json.Unmarshal(payload, &claim); err != nil {
			return err
		}
		// Curated source time is the server-assigned editing event, distinct from supplied validity.
		id, err := assertFactTx(ctx, tx, schema, scope, observationID, domain.RoleUser, owner, claim, CuratedExtractorVersion, knownAt)
		if err != nil {
			return err
		}
		if id != expected {
			return errors.New("curated source identity changed")
		}
		return nil
	})
	return true, err
}
