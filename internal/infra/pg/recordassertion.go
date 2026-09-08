// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// An assertion is application-authored input, not an inference proposal. The closed vocabulary
// supplies cardinality and object type; a client cannot override either or invent model confidence.
type RecordAssertion struct {
	IdempotencyKey string    `json:"idempotency_key"`
	DataSubjectID  string    `json:"data_subject_id,omitempty"`
	Subject        string    `json:"subject"`
	Predicate      string    `json:"predicate"`
	Object         string    `json:"object"`
	Statement      string    `json:"statement"`
	ValidFrom      time.Time `json:"valid_from,omitempty"`
}

type AssertedRecord struct {
	ID                  string `json:"id"`
	Version             string `json:"version"`
	SourceObservationID string `json:"source_observation_id"`
	Replayed            bool   `json:"replayed"`
}

type AssertionBatch struct {
	Records []AssertedRecord `json:"records"`
}

func validAssertionText(value string, max int, optional bool) bool {
	return (value == "" && optional) || (strings.TrimSpace(value) != "" && len(value) <= max && utf8.ValidString(value) && !strings.ContainsRune(value, 0))
}

// AssertRecords atomically creates 1–20 authored sources or resolves their prior retry receipts.
// Keys are namespaced within the existing source retry table, whose erasure tombstones prevent
// queued retries from recreating deleted content. Fingerprints omit the server-assigned event time.
func (s *RecordStore) AssertRecords(ctx context.Context, scope, principal string, requests []RecordAssertion) (AssertionBatch, error) {
	out := AssertionBatch{Records: []AssertedRecord{}}
	if len(requests) < 1 || len(requests) > MaxRecordMutations {
		return out, ErrInvalidRecordMutation
	}
	type prepared struct {
		request          RecordAssertion
		key, fingerprint [32]byte
		order            string
	}
	entries := make([]prepared, len(requests))
	seen := map[string]bool{}
	for i, r := range requests {
		key, err := uuid.Parse(r.IdempotencyKey)
		if err != nil || len(r.IdempotencyKey) != 36 || seen[key.String()] || !validAssertionText(r.Subject, 1024, false) || !validAssertionText(r.Object, 1024, false) || !validAssertionText(r.Predicate, 128, false) || !validAssertionText(r.Statement, 16384, false) || !validAssertionText(r.DataSubjectID, 1024, true) || (!r.ValidFrom.IsZero() && (r.ValidFrom.Year() < 1 || r.ValidFrom.Year() > 9999)) {
			return out, ErrInvalidRecordMutation
		}
		seen[key.String()] = true
		r.ValidFrom = r.ValidFrom.UTC()
		// An explicit persisted representation avoids depending on Go struct field order.
		payload, err := json.Marshal([7]string{"record.assert/v1", r.DataSubjectID, r.Subject, r.Predicate, r.Object, r.Statement, r.ValidFrom.Format(time.RFC3339Nano)})
		if err != nil {
			return out, err
		}
		entries[i] = prepared{request: r, key: sha256.Sum256(append([]byte("record.assert/v1:"), key[:]...)), fingerprint: sha256.Sum256(payload), order: key.String()}
	}
	order := make([]int, len(entries))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(i, j int) bool { return entries[order[i]].order < entries[order[j]].order })
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		for _, i := range order {
			lock := fmt.Sprintf("taisce:assert-retry:%q:%q:%x", s.schema.String(), scope, entries[i].key)
			if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, lock); err != nil {
				return err
			}
		}
		created := 0
		for _, entry := range entries {
			var fingerprint []byte
			var source *string
			err := tx.QueryRow(ctx, s.schema.SQL(`SELECT request_digest,observation_id::text FROM {schema}.observation_retry WHERE scope=$1 AND key_digest=$2 FOR UPDATE`), scope, entry.key[:]).Scan(&fingerprint, &source)
			if err == nil {
				if source == nil || !bytes.Equal(fingerprint, entry.fingerprint[:]) {
					return ErrIdempotencyConflict
				}
				if err := lockClaimSource(ctx, tx, s.schema, scope, *source); err != nil {
					return err
				}
				var id string
				if err := tx.QueryRow(ctx, s.schema.SQL(`SELECT fact_id::text FROM {schema}.curated_claim WHERE scope=$1 AND source_observation_id=$2::uuid`), scope, *source).Scan(&id); err != nil {
					return err
				}
				if _, err := restoreFactReceiptTx(ctx, tx, s.schema, scope, id); err != nil {
					return err
				}
				out.Records = append(out.Records, AssertedRecord{ID: id, SourceObservationID: *source, Replayed: true})
				continue
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			r := entry.request
			var cardinality, objectKind string
			err = tx.QueryRow(ctx, s.schema.SQL(`SELECT cardinality,object_kind FROM {schema}.predicate WHERE predicate=$1 AND (NOT EXISTS(SELECT 1 FROM {schema}.unresolvable_term WHERE term=$2) OR EXISTS(SELECT 1 FROM {schema}.speaker_term WHERE term=$2))`), r.Predicate, domain.NormalizeName(r.Subject)).Scan(&cardinality, &objectKind)
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrInvalidRecordMutation
			}
			if err != nil {
				return err
			}
			claim := domain.Claim{Subject: r.Subject, Predicate: r.Predicate, Object: r.Object, Statement: r.Statement, Quote: r.Statement, ByteEnd: len(r.Statement), Cardinality: domain.Cardinality(cardinality), ObjectType: objectKind, ValidFrom: r.ValidFrom}
			id, sourceID, err := authorCuratedTx(ctx, tx, s.schema, scope, principal, r.DataSubjectID, claim, time.Time{})
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, s.schema.SQL(`INSERT INTO {schema}.observation_retry(scope,key_digest,request_digest,observation_id) VALUES($1,$2,$3,$4::uuid)`), scope, entry.key[:], entry.fingerprint[:], sourceID); err != nil {
				return err
			}
			out.Records = append(out.Records, AssertedRecord{ID: id, SourceObservationID: sourceID})
			created++
		}
		// Later entries can supersede earlier entries in this same batch. Return their final
		// versions so a caller does not immediately edit with an already-stale creation token.
		for i := range out.Records {
			if err := tx.QueryRow(ctx, s.schema.SQL(`SELECT version::text FROM {schema}.fact WHERE scope=$1 AND fact_id=$2::uuid`), scope, out.Records[i].ID).Scan(&out.Records[i].Version); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, s.schema.SQL(advanceFormedWatermarkSQL), scope); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, s.schema.SQL(`INSERT INTO {schema}.audit_entry(operation,principal,principal_kind,project,magnitude,outcome) VALUES('record.assert',$1,'credential',$2,$3,'allowed')`), principal, scope, created)
		return err
	})
	if err != nil {
		var databaseError *pgconn.PgError
		if errors.As(err, &databaseError) && databaseError.Code == "23P01" {
			return AssertionBatch{}, ErrRecordConflict
		}
		return AssertionBatch{}, fmt.Errorf("assert records: %w", err)
	}
	return out, nil
}

// Corrections pass their exact withdrawal boundary. Independent assertions let the shared fact
// writer choose ordered knowledge time; the authored source records that server-assigned event.
func authorCuratedTx(ctx context.Context, tx pgx.Tx, schema Schema, scope, principal, owner string, claim domain.Claim, knownAt time.Time) (string, string, error) {
	eventAt := knownAt
	if eventAt.IsZero() {
		if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&eventAt); err != nil {
			return "", "", err
		}
	}
	if claim.ValidFrom.IsZero() {
		claim.ValidFrom = eventAt
	}
	turn := domain.Turn{Scope: scope, DataSubjectID: owner, OccurredAt: eventAt, Messages: []domain.Message{{Role: domain.RoleUser, Content: claim.Statement, OccurredAt: eventAt}}}
	if err := turn.Validate(); err != nil {
		return "", "", ErrInvalidRecordMutation
	}
	source, err := appendTurnTx(ctx, tx, schema, turn)
	if err != nil {
		return "", "", err
	}
	id, err := assertFactTx(ctx, tx, schema, scope, source.ID, domain.RoleUser, owner, claim, CuratedExtractorVersion, knownAt)
	if err != nil {
		return "", "", err
	}
	payload, err := json.Marshal(claim)
	if err != nil {
		return "", "", err
	}
	if _, err := tx.Exec(ctx, schema.SQL(`INSERT INTO {schema}.curated_claim(scope,source_observation_id,fact_id,principal_id,claim) VALUES($1,$2::uuid,$3::uuid,$4::uuid,$5::jsonb)`), scope, source.ID, id, principal, payload); err != nil {
		return "", "", err
	}
	if err := tx.QueryRow(ctx, schema.SQL(`SELECT recorded_at FROM {schema}.fact WHERE scope=$1 AND fact_id=$2::uuid`), scope, id).Scan(&eventAt); err != nil {
		return "", "", err
	}
	if _, err := tx.Exec(ctx, schema.SQL(`UPDATE {schema}.observation SET kind='curated',formed_at=clock_timestamp(),occurred_at=$3 WHERE scope=$1 AND observation_id=$2::uuid`), scope, source.ID, eventAt); err != nil {
		return "", "", err
	}
	if knownAt.IsZero() {
		if _, err := tx.Exec(ctx, schema.SQL(`UPDATE {schema}.turn_message SET occurred_at=$2 WHERE observation_id=$1::uuid`), source.ID, eventAt); err != nil {
			return "", "", err
		}
		if _, err := tx.Exec(ctx, schema.SQL(`UPDATE {schema}.chunk SET occurred_at=$3 WHERE scope=$1 AND source_observation_id=$2::uuid`), scope, source.ID, eventAt); err != nil {
			return "", "", err
		}
	}
	return id, source.ID, nil
}
