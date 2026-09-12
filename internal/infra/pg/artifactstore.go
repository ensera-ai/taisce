// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const MaxArtifactBytes = 512 << 10

var (
	ErrInvalidArtifact  = errors.New("invalid artifact request")
	ErrArtifactNotFound = errors.New("artifact not found")
	ErrArtifactConflict = errors.New("artifact version or identity conflict")
	ErrArtifactCapacity = errors.New("artifact storage capacity exceeded")
)

type ArtifactStore struct {
	pool   *pgxpool.Pool
	schema Schema
}

func NewArtifactStore(pool *pgxpool.Pool, schema Schema) *ArtifactStore {
	return &ArtifactStore{pool: pool, schema: schema}
}

type ArtifactPut struct {
	ID              string          `json:"id"`
	ExpectedVersion string          `json:"expected_version,omitempty"`
	DataSubjectID   string          `json:"data_subject_id"`
	Kind            string          `json:"kind"`
	Name            string          `json:"name"`
	Content         ArtifactContent `json:"content"`
}
type ArtifactMetadata struct {
	ID                  string    `json:"id"`
	Version             string    `json:"version"`
	DataSubjectID       string    `json:"data_subject_id"`
	Kind                string    `json:"kind"`
	Name                string    `json:"name"`
	SourceObservationID string    `json:"source_observation_id"`
	Bytes               int       `json:"bytes"`
	AuthoredBy          string    `json:"authored_by"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
	ExpiresAt           time.Time `json:"expires_at"`
}
type ArtifactWrite struct {
	ArtifactMetadata
	Replayed bool `json:"replayed"`
}
type Artifact struct {
	ArtifactMetadata
	Content []byte `json:"content"`
}
type ArtifactPage struct {
	Artifacts []ArtifactMetadata `json:"artifacts"`
	Next      *RecordCursor      `json:"next,omitempty"`
}

func artifactUUID(value string) (string, error) {
	id, err := uuid.Parse(value)
	if err != nil || len(value) != 36 {
		return "", ErrInvalidArtifact
	}
	return id.String(), nil
}
func artifactDigest(r ArtifactPut) []byte {
	h := sha256.New()
	for _, v := range [][]byte{[]byte(r.DataSubjectID), []byte(r.Kind), []byte(r.Name), r.Content} {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(v)))
		h.Write(size[:])
		h.Write(v)
	}
	return h.Sum(nil)
}
func artifactKey(id string) [32]byte { return sha256.Sum256([]byte("artifact/v1:" + id)) }
func lockArtifact(ctx context.Context, tx pgx.Tx, schema Schema, scope, id string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, fmt.Sprintf("taisce:artifact:%q:%q:%s", schema.String(), scope, id))
	return err
}

const artifactColumns = `a.artifact_id::text,a.version::text,a.data_subject_id,a.kind,a.name,a.source_observation_id::text,
    octet_length(a.content),a.authored_by::text,a.created_at,a.updated_at,o.retention_until`
const artifactFrom = ` FROM {schema}.agent_artifact a JOIN {schema}.observation o ON o.scope=a.scope AND o.observation_id=a.source_observation_id `

func artifactDest(m *ArtifactMetadata) []any {
	return []any{&m.ID, &m.Version, &m.DataSubjectID, &m.Kind, &m.Name, &m.SourceObservationID, &m.Bytes, &m.AuthoredBy, &m.CreatedAt, &m.UpdatedAt, &m.ExpiresAt}
}
func artifactFailure(err error) error {
	var db *pgconn.PgError
	if errors.As(err, &db) && db.ConstraintName == "agent_storage_capacity" {
		return ErrArtifactCapacity
	}
	return err
}

// Put stores opaque bytes under one owner. Creation identities have erasure-safe tombstones;
// overwrites use versions and retain only the latest bytes, never an implicit content archive.
func (s *ArtifactStore) Put(ctx context.Context, scope, principal string, r ArtifactPut) (ArtifactWrite, error) {
	var err error
	r.ID, err = artifactUUID(r.ID)
	if err != nil || !validAssertionText(r.DataSubjectID, 1024, false) || !validAssertionText(r.Name, 256, true) || (r.Kind != "state" && r.Kind != "file") || r.Content == nil || len(r.Content) > MaxArtifactBytes {
		return ArtifactWrite{}, ErrInvalidArtifact
	}
	if r.ExpectedVersion != "" {
		r.ExpectedVersion, err = artifactUUID(r.ExpectedVersion)
		if err != nil {
			return ArtifactWrite{}, err
		}
	}
	digest := artifactDigest(r)
	key := artifactKey(r.ID)
	var out ArtifactWrite
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockArtifact(ctx, tx, s.schema, scope, r.ID); err != nil {
			return err
		}
		var previous *string
		var last []byte
		dest := append(artifactDest(&out.ArtifactMetadata), &previous, &last)
		err := tx.QueryRow(ctx, s.schema.SQL(`SELECT `+artifactColumns+`,a.previous_version::text,a.last_digest`+artifactFrom+` WHERE a.scope=$1 AND a.artifact_id=$2::uuid AND o.retention_until>now() FOR UPDATE OF a FOR KEY SHARE OF o`), scope, r.ID).Scan(dest...)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			if r.ExpectedVersion != "" {
				return ErrArtifactNotFound
			}
			var used bool
			if err := tx.QueryRow(ctx, s.schema.SQL(`SELECT EXISTS(SELECT 1 FROM {schema}.observation_retry WHERE scope=$1 AND key_digest=$2)`), scope, key[:]).Scan(&used); err != nil {
				return err
			}
			if used {
				return ErrArtifactConflict
			}
			var offset int64
			now := time.Now().UTC()
			if err := tx.QueryRow(ctx, s.schema.SQL(claimOffsetSQL), scope, now).Scan(&offset); err != nil {
				return err
			}
			source := uuid.NewString()
			tag, err := tx.Exec(ctx, s.schema.SQL(`INSERT INTO {schema}.observation(observation_id,scope,log_offset,kind,occurred_at,ingested_at,source_role,data_subject_id,formed_at,retention_until)
                SELECT $1::uuid,p.scope,$3,'artifact',clock_timestamp(),clock_timestamp(),'assistant',$4,clock_timestamp(),
                    least(now()+make_interval(hours=>b.max_age_hours),now()+p.retention)
                FROM {schema}.project p JOIN {schema}.agent_storage_policy b USING(scope) WHERE p.scope=$2 AND p.suspended_at IS NULL`), source, scope, offset, r.DataSubjectID)
			if err != nil {
				return err
			}
			if tag.RowsAffected() != 1 {
				return ErrNoSuchProject
			}
			_, err = tx.Exec(ctx, s.schema.SQL(`INSERT INTO {schema}.agent_artifact(scope,artifact_id,source_observation_id,data_subject_id,kind,name,content,version,last_digest,authored_by)
                VALUES($1,$2::uuid,$3::uuid,$4,$5,$6,$7,$8::uuid,$9,$10::uuid)`), scope, r.ID, source, r.DataSubjectID, r.Kind, r.Name, r.Content, uuid.NewString(), digest, principal)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, s.schema.SQL(registerProjectionSQL), source, scope, "agent_artifact", r.ID, r.DataSubjectID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, s.schema.SQL(`INSERT INTO {schema}.observation_retry(scope,key_digest,request_digest,observation_id) VALUES($1,$2,$3,$4::uuid)`), scope, key[:], digest, source); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, s.schema.SQL(advanceFormedWatermarkSQL), scope); err != nil {
				return err
			}
		case err != nil:
			return err
		default:
			if out.DataSubjectID != r.DataSubjectID || out.Kind != r.Kind {
				return ErrArtifactConflict
			}
			if r.ExpectedVersion == "" {
				var original []byte
				if err := tx.QueryRow(ctx, s.schema.SQL(`SELECT request_digest FROM {schema}.observation_retry WHERE scope=$1 AND key_digest=$2 AND observation_id=$3::uuid`), scope, key[:], out.SourceObservationID).Scan(&original); err != nil {
					return err
				}
				if !bytes.Equal(original, digest) {
					return ErrArtifactConflict
				}
				out.Replayed = true
			} else if out.Version == r.ExpectedVersion {
				_, err := tx.Exec(ctx, s.schema.SQL(`UPDATE {schema}.agent_artifact SET name=$3,content=$4,previous_version=version,version=$5::uuid,last_digest=$6,authored_by=$7::uuid,updated_at=clock_timestamp() WHERE scope=$1 AND artifact_id=$2::uuid`), scope, r.ID, r.Name, r.Content, uuid.NewString(), digest, principal)
				if err != nil {
					return err
				}
			} else if previous != nil && *previous == r.ExpectedVersion && bytes.Equal(last, digest) {
				out.Replayed = true
			} else {
				return ErrArtifactConflict
			}
		}
		if err := tx.QueryRow(ctx, s.schema.SQL(`SELECT `+artifactColumns+artifactFrom+` WHERE a.scope=$1 AND a.artifact_id=$2::uuid`), scope, r.ID).Scan(artifactDest(&out.ArtifactMetadata)...); err != nil {
			return err
		}
		magnitude := 1
		if out.Replayed {
			magnitude = 0
		}
		_, err = tx.Exec(ctx, s.schema.SQL(`INSERT INTO {schema}.audit_entry(operation,principal,principal_kind,project,magnitude,outcome) VALUES('artifact.put',$1,'credential',$2,$3,'allowed')`), principal, scope, magnitude)
		return err
	})
	if err != nil {
		return ArtifactWrite{}, artifactFailure(err)
	}
	return out, nil
}

// Get reads one artifact, optionally requiring it to belong to a named data subject.
//
// # Why the subject is a predicate and not a check afterwards
//
// A credential opens a project, and a project holds every end user's objects. An application that
// serves many people through one credential therefore has nothing stopping it handing user A the
// object of user B — the credential permits it, so no amount of care in the application is a
// boundary, only a habit. Naming the subject puts the requirement in the statement that reads the
// row: a mismatch selects nothing, and the caller is told what it would be told about an object
// that does not exist, because to that caller it does not.
//
// Absent, the behaviour is what it always was, which is what the frozen contract requires.
func (s *ArtifactStore) Get(ctx context.Context, scope, id, dataSubjectID string) (Artifact, error) {
	id, err := artifactUUID(id)
	if err != nil {
		return Artifact{}, err
	}
	var out Artifact
	dest := append(artifactDest(&out.ArtifactMetadata), &out.Content)
	err = s.pool.QueryRow(ctx, s.schema.SQL(`SELECT `+artifactColumns+`,a.content`+artifactFrom+
		` WHERE a.scope=$1 AND a.artifact_id=$2::uuid AND o.retention_until>now()
		    AND ($3::text IS NULL OR a.data_subject_id=$3)`), scope, id, nullIfEmpty(dataSubjectID)).Scan(dest...)
	if errors.Is(err, pgx.ErrNoRows) {
		return Artifact{}, ErrArtifactNotFound
	}
	return out, err
}

type ArtifactFilter struct {
	Kind       string
	NamePrefix string
}

func (s *ArtifactStore) List(ctx context.Context, scope, subject, after string, limit int, filters ...ArtifactFilter) (ArtifactPage, error) {
	out := ArtifactPage{Artifacts: []ArtifactMetadata{}}
	if len(filters) > 1 {
		return out, ErrInvalidArtifact
	}
	filter := ArtifactFilter{}
	if len(filters) == 1 {
		filter = filters[0]
	}
	if (filter.Kind != "" && filter.Kind != "state" && filter.Kind != "file") || !validAssertionText(filter.NamePrefix, 256, true) {
		return out, ErrInvalidArtifact
	}

	if !validAssertionText(subject, 1024, true) || limit < 1 || limit > 100 {
		return out, ErrInvalidArtifact
	}
	var cursor any
	if after != "" {
		v, err := artifactUUID(after)
		if err != nil {
			return out, err
		}
		cursor = v
	}
	query := `SELECT ` + artifactColumns + artifactFrom + ` WHERE a.scope=$1 AND ($2::uuid IS NULL OR a.artifact_id>$2::uuid) AND o.retention_until>now()`
	args := []any{scope, cursor, limit + 1}
	if subject != "" {
		query += ` AND a.data_subject_id=$4`
		args = append(args, subject)
	}
	if filter.Kind != "" {
		args = append(args, filter.Kind)
		query += fmt.Sprintf(" AND a.kind=$%d", len(args))
	}
	if filter.NamePrefix != "" {
		prefix := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(filter.NamePrefix) + "%"
		args = append(args, prefix)
		query += fmt.Sprintf(` AND a.name LIKE $%d ESCAPE E'\\'`, len(args))
	}
	query += ` ORDER BY a.artifact_id LIMIT $3`
	rows, err := s.pool.Query(ctx, s.schema.SQL(query), args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var m ArtifactMetadata
		if err := rows.Scan(artifactDest(&m)...); err != nil {
			return out, err
		}
		out.Artifacts = append(out.Artifacts, m)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	if len(out.Artifacts) > limit {
		out.Artifacts = out.Artifacts[:limit]
		out.Next = &RecordCursor{ID: out.Artifacts[limit-1].ID}
	}
	return out, nil
}

// Delete removes one artifact, optionally requiring it to belong to a named data subject. The
// subject is a predicate for the reason it is one on Get: deleting another person's object is the
// same mistake, already spent.
func (s *ArtifactStore) Delete(ctx context.Context, scope, principal, id, version, dataSubjectID string) error {
	id, err := artifactUUID(id)
	if err != nil {
		return err
	}
	version, err = artifactUUID(version)
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockArtifact(ctx, tx, s.schema, scope, id); err != nil {
			return err
		}
		var stored, source string
		err := tx.QueryRow(ctx, s.schema.SQL(`SELECT a.version::text,a.source_observation_id::text`+artifactFrom+
			` WHERE a.scope=$1 AND a.artifact_id=$2::uuid AND o.retention_until>now()
			    AND ($3::text IS NULL OR a.data_subject_id=$3) FOR UPDATE OF a FOR KEY SHARE OF o`),
			scope, id, nullIfEmpty(dataSubjectID)).Scan(&stored, &source)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrArtifactNotFound
		}
		if err != nil {
			return err
		}
		if version != stored {
			return ErrArtifactConflict
		}
		if _, err := tx.Exec(ctx, s.schema.SQL(`DELETE FROM {schema}.agent_artifact WHERE scope=$1 AND artifact_id=$2::uuid`), scope, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, s.schema.SQL(`DELETE FROM {schema}.observation WHERE scope=$1 AND observation_id=$2::uuid`), scope, source); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, s.schema.SQL(`INSERT INTO {schema}.audit_entry(operation,principal,principal_kind,project,magnitude,outcome) VALUES('artifact.delete',$1,'credential',$2,1,'allowed')`), principal, scope)
		return err
	})
}
