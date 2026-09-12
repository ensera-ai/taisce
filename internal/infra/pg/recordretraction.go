// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const MaxRecordMutations = 20
const MaxRetractionSources = 128

var (
	ErrInvalidRecordMutation = errors.New("invalid record mutation")
	ErrRecordConflict        = errors.New("record version changed or is already withdrawn")
	ErrRecordMutationLimit   = errors.New("record mutation exceeds its source bound")
	ErrRetractedClaim        = errors.New("claim was withdrawn for this source message")
)

type RecordMutation struct {
	ID              string `json:"id"`
	ExpectedVersion string `json:"expected_version"`
}

type RetractionDetails struct {
	OperationID              string    `json:"operation_id"`
	PrincipalID              string    `json:"principal_id"`
	RetractedAt              time.Time `json:"retracted_at"`
	ReplacementObservationID *string   `json:"replacement_source_observation_id,omitempty"`
	ReplacementFactID        *string   `json:"replacement_record_id,omitempty"`
}

type RecordRetraction struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	RetractionDetails
}

type RetractionBatch struct {
	Records []RecordRetraction `json:"records"`
}

type retractionSource struct {
	FactID        string
	ObservationID string
	Ordinal       int
}

// The source message and structural identity define the instruction. A paraphrased statement or
// a different valid-from date cannot undo withdrawal; an independent later message can assert it.
// Hashes here are matching keys, not anonymization. Their source ownership governs erasure.
func claimSignature(predicate, subjectKind, subjectKey, objectKind, objectKey string) []byte {
	encoded, _ := json.Marshal([6]string{"claim/v1", predicate, subjectKind, subjectKey, objectKind, objectKey})
	digest := sha256.Sum256(encoded)
	return digest[:]
}

func claimEndpoint(name, subject string, speaker bool) (string, string) {
	if speaker {
		return "speaker", subject
	}
	key := domain.NormalizeName(name)
	if key == "" {
		return "none", ""
	}
	return "named", key
}

// Identity follows the source assertion, so replay and derived-row recovery do not invent a new
// citation. Reinterpreting a retained source is an explicit generation operation, never a retry.
func sourceClaimID(scope, observationID, subject string, claim domain.Claim, leftSpeaker, rightSpeaker bool) (uuid.UUID, error) {
	source, err := uuid.Parse(observationID)
	if err != nil {
		return uuid.Nil, err
	}
	lk, lv := claimEndpoint(claim.Subject, subject, leftSpeaker)
	rk, rv := claimEndpoint(claim.Object, subject, rightSpeaker)
	identity := fmt.Sprintf("taisce:fact/v1:%q:%s:%d:%x", scope, source.String(), claim.SourceOrdinal, claimSignature(claim.Predicate, lk, lv, rk, rv))
	return uuid.NewHash(sha256.New(), uuid.NameSpaceOID, []byte(identity), 8), nil
}

// Formation and retraction serialize only when they address the same source. A hash collision
// delays unrelated work but cannot change its identity or project predicates.
func lockClaimSource(ctx context.Context, tx pgx.Tx, schema Schema, scope, observationID string) error {
	source, err := uuid.Parse(observationID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, fmt.Sprintf("taisce:claim-source:%q:%q:%s", schema.String(), scope, source.String()))
	return err
}

func readRetraction(ctx context.Context, tx pgx.Tx, schema Schema, scope, id string) (*RetractionDetails, error) {
	var out RetractionDetails
	err := tx.QueryRow(ctx, schema.SQL(`SELECT operation_id::text,principal_id::text,retracted_at,replacement_observation_id::text,replacement_fact_id::text FROM {schema}.record_retraction
        WHERE scope=$1 AND target_fact_id=$2::uuid ORDER BY source_observation_id,source_ordinal LIMIT 1`), scope, id).Scan(&out.OperationID, &out.PrincipalID, &out.RetractedAt, &out.ReplacementObservationID, &out.ReplacementFactID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func refuseRetractedClaim(ctx context.Context, tx pgx.Tx, schema Schema, scope, observationID, subject string, claim domain.Claim, leftSpeaker, rightSpeaker bool) error {
	leftKind, leftKey := claimEndpoint(claim.Subject, subject, leftSpeaker)
	rightKind, rightKey := claimEndpoint(claim.Object, subject, rightSpeaker)
	var withdrawn bool
	err := tx.QueryRow(ctx, schema.SQL(`SELECT EXISTS(SELECT 1 FROM {schema}.record_retraction
        WHERE scope=$1 AND source_observation_id=$2::uuid AND source_ordinal=$3 AND claim_signature=$4)`), scope, observationID, claim.SourceOrdinal, claimSignature(claim.Predicate, leftKind, leftKey, rightKind, rightKey)).Scan(&withdrawn)
	if err != nil {
		return err
	}
	if withdrawn {
		return ErrRetractedClaim
	}
	return nil
}

func retractionSources(ctx context.Context, tx pgx.Tx, schema Schema, scope string, ids []string) ([]retractionSource, error) {
	rows, err := tx.Query(ctx, schema.SQL(`SELECT e.fact_id::text,e.source_observation_id::text,e.source_ordinal
        FROM {schema}.fact_evidence e WHERE e.scope=$1 AND e.fact_id=ANY($2::uuid[])
        ORDER BY e.fact_id,e.source_observation_id,e.byte_start LIMIT $3`), scope, ids, MaxRetractionSources+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sources []retractionSource
	seen := map[retractionSource]bool{}
	count := 0
	for rows.Next() {
		count++
		if count > MaxRetractionSources {
			return nil, ErrRecordMutationLimit
		}
		var source retractionSource
		if err := rows.Scan(&source.FactID, &source.ObservationID, &source.Ordinal); err != nil {
			return nil, err
		}
		if !seen[source] {
			sources = append(sources, source)
			seen[source] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return sources, nil
}

// Retract is an atomic bounded batch. Source instructions, closed knowledge, report invalidation,
// new versions and an attributable content-free audit entry either all commit or all roll back.
func (s *RecordStore) Retract(ctx context.Context, scope, principal string, requests []RecordMutation) (RetractionBatch, error) {
	var out RetractionBatch
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		out, err = s.retractTx(ctx, tx, scope, principal, requests)
		return err
	})
	return out, err
}

// The caller owns commit so a correction can withdraw its target and assert its replacement together.
func (s *RecordStore) retractTx(ctx context.Context, tx pgx.Tx, scope, principal string, requests []RecordMutation) (RetractionBatch, error) {

	out := RetractionBatch{Records: []RecordRetraction{}}
	if _, err := NewSchema(scope); err != nil {
		return out, ErrInvalidRecordMutation
	}
	actor, err := uuid.Parse(principal)
	if err != nil || actor == uuid.Nil || len(requests) < 1 || len(requests) > MaxRecordMutations {
		return out, ErrInvalidRecordMutation
	}
	expected := map[string]string{}
	ids := make([]string, 0, len(requests))
	for _, request := range requests {
		id, err := uuid.Parse(request.ID)
		if err != nil || id == uuid.Nil {
			return out, ErrInvalidRecordMutation
		}
		version, err := uuid.Parse(request.ExpectedVersion)
		if err != nil || version == uuid.Nil || expected[id.String()] != "" {
			return out, ErrInvalidRecordMutation
		}
		expected[id.String()] = version.String()
		ids = append(ids, id.String())
	}
	err = func() error {
		sources, err := retractionSources(ctx, tx, s.schema, scope, ids)
		if err != nil {
			return err
		}
		sourceIDs := map[string]bool{}
		for _, source := range sources {
			sourceIDs[source.ObservationID] = true
		}
		ordered := make([]string, 0, len(sourceIDs))
		for sourceID := range sourceIDs {
			ordered = append(ordered, sourceID)
		}
		sort.Strings(ordered)
		for _, sourceID := range ordered {
			if err := lockClaimSource(ctx, tx, s.schema, scope, sourceID); err != nil {
				return err
			}
		}
		rows, err := tx.Query(ctx, s.schema.SQL(`SELECT f.fact_id::text,f.version::text,upper_inf(f.known),f.predicate,
            coalesce(l.identity_kind,'none'),coalesce(CASE WHEN l.identity_kind='speaker' THEN l.speaker_subject_id ELSE l.normalized_name END,''),
            coalesce(r.identity_kind,'none'),coalesce(CASE WHEN r.identity_kind='speaker' THEN r.speaker_subject_id ELSE r.normalized_name END,'')
            FROM {schema}.fact f LEFT JOIN {schema}.entity l ON l.scope=f.scope AND l.entity_id=f.subject_entity_id
            LEFT JOIN {schema}.entity r ON r.scope=f.scope AND r.entity_id=f.object_entity_id
            WHERE f.scope=$1 AND f.fact_id=ANY($2::uuid[]) ORDER BY f.fact_id FOR UPDATE OF f`), scope, ids)
		if err != nil {
			return err
		}
		signatures := map[string][]byte{}
		conflict := false
		for rows.Next() {
			var id, version, predicate, lk, lv, rk, rv string
			var open bool
			if err := rows.Scan(&id, &version, &open, &predicate, &lk, &lv, &rk, &rv); err != nil {
				rows.Close()
				return err
			}
			if !open || version != expected[id] {
				conflict = true
			}
			signatures[id] = claimSignature(predicate, lk, lv, rk, rv)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		// Missing/foreign IDs take precedence, regardless of the states of other supplied records.
		if len(signatures) != len(ids) {
			return ErrRecordNotFound
		}
		if conflict {
			return ErrRecordConflict
		}
		current, err := retractionSources(ctx, tx, s.schema, scope, ids)
		if err != nil {
			return err
		}
		if !slices.Equal(sources, current) {
			return ErrRecordConflict
		}
		supported := map[string]bool{}
		for _, source := range sources {
			supported[source.FactID] = true
		}
		if len(supported) != len(ids) {
			return ErrRecordNotFound
		}
		for _, id := range ids {
			item := RecordRetraction{ID: id, RetractionDetails: RetractionDetails{OperationID: uuid.NewString(), PrincipalID: actor.String()}}
			if err := tx.QueryRow(ctx, s.schema.SQL(`UPDATE {schema}.fact
                SET known=tstzrange(lower(known),greatest(clock_timestamp(),lower(known)+interval '1 microsecond'))
                WHERE scope=$1 AND fact_id=$2::uuid RETURNING version::text,upper(known)`), scope, id).Scan(&item.Version, &item.RetractedAt); err != nil {
				return err
			}
			for _, source := range sources {
				if source.FactID != id {
					continue
				}
				if _, err := tx.Exec(ctx, s.schema.SQL(`INSERT INTO {schema}.record_retraction
                    (scope,target_fact_id,source_observation_id,source_ordinal,claim_signature,operation_id,target_version,principal_id,retracted_at)
                    VALUES($1,$2::uuid,$3::uuid,$4,$5,$6::uuid,$7::uuid,$8::uuid,$9)`), scope, id, source.ObservationID, source.Ordinal, signatures[id], item.OperationID, expected[id], actor.String(), item.RetractedAt); err != nil {
					return err
				}
			}
			out.Records = append(out.Records, item)
		}
		_, err = tx.Exec(ctx, s.schema.SQL(`INSERT INTO {schema}.audit_entry(operation,principal,principal_kind,project,magnitude,outcome)
            VALUES($1,$2,$3,$4,$5,$6)`), domain.AuditRecordRetract, actor.String(), domain.PrincipalCredential, scope, len(ids), domain.OutcomeAllowed)
		return err
	}()
	if err != nil {
		return RetractionBatch{}, fmt.Errorf("retract records: %w", err)
	}
	return out, nil
}
