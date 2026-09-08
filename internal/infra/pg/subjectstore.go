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
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrInvalidSubject  = errors.New("invalid subject request")
	ErrSubjectNotFound = errors.New("subject not found")
	ErrSubjectConflict = errors.New("subject reference, retry or version conflict")
)

// SubjectStore holds optional identity bookkeeping. It never changes a source's stored attribution
// and its external references never participate in inference, displayed claims or recall.
type SubjectStore struct {
	pool   *pgxpool.Pool
	schema Schema
}

func NewSubjectStore(pool *pgxpool.Pool, schema Schema) *SubjectStore {
	return &SubjectStore{pool: pool, schema: schema}
}

type SubjectFields struct {
	ExternalReference string `json:"external_reference,omitempty"`
	Label             string `json:"label"`
}
type SubjectRegistration struct {
	IdempotencyKey string `json:"idempotency_key"`
	SubjectFields
}
type SubjectUpdate struct {
	ID              string `json:"id"`
	ExpectedVersion string `json:"expected_version"`
	SubjectFields
}
type ManagedSubject struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	SubjectFields
	UpdatedBy     string     `json:"updated_by"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	InactiveAfter *time.Time `json:"inactive_after"`
}
type SubjectWrite struct {
	ManagedSubject
	Replayed bool `json:"replayed"`
}
type SubjectPage struct {
	Subjects []ManagedSubject `json:"subjects"`
	Next     *RecordCursor    `json:"next,omitempty"`
}

func subjectUUID(value string) (string, error) {
	id, err := uuid.Parse(value)
	if err != nil || len(value) != 36 {
		return "", ErrInvalidSubject
	}
	return id.String(), nil
}
func subjectFieldsValid(f SubjectFields) bool {
	return validAssertionText(f.ExternalReference, 1024, true) && validAssertionText(f.Label, 256, true)
}
func subjectDigest(f SubjectFields) []byte {
	// A fixed, versioned array preserves exact field boundaries, including absent references.
	body, _ := json.Marshal([3]string{"subject/v1", f.ExternalReference, f.Label})
	hash := sha256.Sum256(body)
	return hash[:]
}
func subjectFailure(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrSubjectNotFound
	}
	var db *pgconn.PgError
	if errors.As(err, &db) && db.ConstraintName == "subject_external_reference_unique" {
		return ErrSubjectConflict
	}
	return err
}

const subjectColumns = `s.subject_id,s.version::text,coalesce(s.external_reference,''),s.label,s.updated_by::text,s.created_at,s.updated_at,s.inactive_after`
const subjectLive = `(s.inactive_after IS NULL OR s.inactive_after>now() OR EXISTS(SELECT 1 FROM {schema}.observation o WHERE o.scope=s.scope AND o.data_subject_id=s.subject_id))`

func subjectDest(s *ManagedSubject) []any {
	return []any{&s.ID, &s.Version, &s.ExternalReference, &s.Label, &s.UpdatedBy, &s.CreatedAt, &s.UpdatedAt, &s.InactiveAfter}
}

// Register uses a random ID rather than embedding or hashing a personal reference into attribution.
// A request's original fingerprint supports lost acknowledgements; erasure leaves only a retry-key
// tombstone so a delayed registration cannot recreate an erased mapping.
func (s *SubjectStore) Register(ctx context.Context, scope, principal string, r SubjectRegistration) (SubjectWrite, error) {
	key, err := subjectUUID(r.IdempotencyKey)
	if err != nil || !subjectFieldsValid(r.SubjectFields) {
		return SubjectWrite{}, ErrInvalidSubject
	}
	keyHash := sha256.Sum256([]byte("subject-registration/v1:" + key))
	digest := subjectDigest(r.SubjectFields)
	var out SubjectWrite
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, fmt.Sprintf("taisce:subject-retry:%q:%q:%s", s.schema.String(), scope, key)); err != nil {
			return err
		}
		var id *string
		var original []byte
		err := tx.QueryRow(ctx, s.schema.SQL(`SELECT subject_id,request_digest FROM {schema}.subject_retry WHERE scope=$1 AND key_digest=$2`), scope, keyHash[:]).Scan(&id, &original)
		switch {
		case err == nil:
			if id == nil || !bytes.Equal(original, digest) {
				return ErrSubjectConflict
			}
			err = tx.QueryRow(ctx, s.schema.SQL(`SELECT `+subjectColumns+` FROM {schema}.data_subject s WHERE s.scope=$1 AND s.subject_id=$2 AND `+subjectLive+` FOR SHARE OF s`), scope, *id).Scan(subjectDest(&out.ManagedSubject)...)
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrSubjectConflict
			}
			if err != nil {
				return err
			}
			out.Replayed = true
		case errors.Is(err, pgx.ErrNoRows):
			out.ID = uuid.NewString()
			tag, err := tx.Exec(ctx, s.schema.SQL(`INSERT INTO {schema}.data_subject(scope,subject_id,external_reference,label,version,last_digest,updated_by,inactive_after)
                SELECT scope,$2,nullif($3,''),$4,$5::uuid,$6,$7::uuid,now()+retention FROM {schema}.project WHERE scope=$1 AND suspended_at IS NULL`), scope, out.ID, r.ExternalReference, r.Label, uuid.NewString(), digest, principal)
			if err != nil {
				return err
			}
			if tag.RowsAffected() != 1 {
				return ErrNoSuchProject
			}
			if _, err := tx.Exec(ctx, s.schema.SQL(`INSERT INTO {schema}.subject_retry(scope,key_digest,subject_id,request_digest) VALUES($1,$2,$3,$4)`), scope, keyHash[:], out.ID, digest); err != nil {
				return err
			}
			if err := tx.QueryRow(ctx, s.schema.SQL(`SELECT `+subjectColumns+` FROM {schema}.data_subject s WHERE scope=$1 AND subject_id=$2`), scope, out.ID).Scan(subjectDest(&out.ManagedSubject)...); err != nil {
				return err
			}
		default:
			return err
		}
		magnitude := 1
		if out.Replayed {
			magnitude = 0
		}
		return auditSubjectMutation(ctx, tx, s.schema, scope, principal, domain.AuditSubjectRegister, magnitude)
	})
	if err != nil {
		return SubjectWrite{}, subjectFailure(err)
	}
	return out, nil
}

// Get resolves exactly one project-scoped ID or external reference. No normalization guesses at
// an application's identifier semantics, and an expired/foreign/absent entry is the same refusal.
func (s *SubjectStore) Get(ctx context.Context, scope, id, reference string) (ManagedSubject, error) {
	if (id == "") == (reference == "") || !validAssertionText(reference, 1024, true) {
		return ManagedSubject{}, ErrInvalidSubject
	}
	var err error
	predicate := `s.external_reference=$2`
	value := reference
	if id != "" {
		value, err = subjectUUID(id)
		if err != nil {
			return ManagedSubject{}, err
		}
		predicate = `s.subject_id=$2`
	}
	var out ManagedSubject
	err = s.pool.QueryRow(ctx, s.schema.SQL(`SELECT `+subjectColumns+` FROM {schema}.data_subject s WHERE s.scope=$1 AND `+predicate+` AND `+subjectLive), scope, value).Scan(subjectDest(&out)...)
	return out, subjectFailure(err)
}

// Update rotates a reference without rewriting attribution. The immediate previous version can
// retry an identical update; a different stale request cannot overwrite a concurrent accepted one.
func (s *SubjectStore) Update(ctx context.Context, scope, principal string, r SubjectUpdate) (SubjectWrite, error) {
	id, err := subjectUUID(r.ID)
	version, verErr := subjectUUID(r.ExpectedVersion)
	if err != nil || verErr != nil || !subjectFieldsValid(r.SubjectFields) {
		return SubjectWrite{}, ErrInvalidSubject
	}
	digest := subjectDigest(r.SubjectFields)
	var out SubjectWrite
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var previous *string
		var last []byte
		dest := append(subjectDest(&out.ManagedSubject), &previous, &last)
		if err := tx.QueryRow(ctx, s.schema.SQL(`SELECT `+subjectColumns+`,s.previous_version::text,s.last_digest FROM {schema}.data_subject s WHERE s.scope=$1 AND s.subject_id=$2 AND `+subjectLive+` FOR UPDATE OF s`), scope, id).Scan(dest...); err != nil {
			return err
		}
		if version != out.Version {
			if previous == nil || version != *previous || !bytes.Equal(last, digest) {
				return ErrSubjectConflict
			}
			out.Replayed = true
		} else {
			if _, err := tx.Exec(ctx, s.schema.SQL(`UPDATE {schema}.data_subject SET external_reference=nullif($3,''),label=$4,previous_version=version,version=$5::uuid,last_digest=$6,updated_at=clock_timestamp(),updated_by=$7::uuid WHERE scope=$1 AND subject_id=$2`), scope, id, r.ExternalReference, r.Label, uuid.NewString(), digest, principal); err != nil {
				return err
			}
			if err := tx.QueryRow(ctx, s.schema.SQL(`SELECT `+subjectColumns+` FROM {schema}.data_subject s WHERE scope=$1 AND subject_id=$2`), scope, id).Scan(subjectDest(&out.ManagedSubject)...); err != nil {
				return err
			}
		}
		magnitude := 1
		if out.Replayed {
			magnitude = 0
		}
		return auditSubjectMutation(ctx, tx, s.schema, scope, principal, domain.AuditSubjectUpdate, magnitude)
	})
	if err != nil {
		return SubjectWrite{}, subjectFailure(err)
	}
	return out, nil
}

func (s *SubjectStore) List(ctx context.Context, scope, after string, limit int) (SubjectPage, error) {
	if limit < 1 || limit > 100 {
		return SubjectPage{}, ErrInvalidSubject
	}
	if after != "" {
		var err error
		after, err = subjectUUID(after)
		if err != nil {
			return SubjectPage{}, err
		}
	}
	rows, err := s.pool.Query(ctx, s.schema.SQL(`SELECT `+subjectColumns+` FROM {schema}.data_subject s WHERE s.scope=$1 AND s.subject_id>$2 AND `+subjectLive+` ORDER BY s.subject_id LIMIT $3`), scope, after, limit+1)
	if err != nil {
		return SubjectPage{}, err
	}
	defer rows.Close()
	out := SubjectPage{Subjects: []ManagedSubject{}}
	for rows.Next() {
		var item ManagedSubject
		if err := rows.Scan(subjectDest(&item)...); err != nil {
			return SubjectPage{}, err
		}
		out.Subjects = append(out.Subjects, item)
	}
	if err := rows.Err(); err != nil {
		return SubjectPage{}, err
	}
	if len(out.Subjects) > limit {
		out.Subjects = out.Subjects[:limit]
		out.Next = &RecordCursor{ID: out.Subjects[limit-1].ID}
	}
	return out, nil
}

func auditSubjectMutation(ctx context.Context, tx pgx.Tx, schema Schema, scope, principal, operation string, magnitude int) error {
	_, err := tx.Exec(ctx, schema.SQL(`INSERT INTO {schema}.audit_entry(operation,principal_kind,principal,project,outcome,magnitude) VALUES($1,'credential',$2,$3,'allowed',$4)`), operation, principal, scope, magnitude)
	return err
}
