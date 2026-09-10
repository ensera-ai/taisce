// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// GenerationResult records what committed, not the current number of surviving rows after erasure.
// It contains no statements or model diagnostics and is safe for operator progress output.
type GenerationResult struct {
	ID          string    `json:"generation_id"`
	SourceID    string    `json:"source_observation_id"`
	Version     string    `json:"extractor_version"`
	AppliedAt   time.Time `json:"applied_at"`
	Messages    int       `json:"messages_read"`
	Asserted    int       `json:"facts_asserted"`
	Retired     int       `json:"facts_retired"`
	Rejected    int       `json:"claims_rejected"`
	Retracted   int       `json:"claims_retracted"`
	RefusedRole int       `json:"claims_refused_by_role"`
	Replayed    bool      `json:"replayed"`
}

type GenerationPlan struct {
	Claims   []domain.Claim
	Rejected []domain.RejectedClaim
	Job      *GenerationJobGuard `json:"-"`
}

func (s *FactStore) FindGeneration(ctx context.Context, schema Schema, scope, source, key, version string) (GenerationResult, bool, error) {
	var out GenerationResult
	var found bool
	if err := validGenerationRequest(scope, source, key, version); err != nil {
		return out, false, err
	}
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		out, found, err = findGenerationTx(ctx, tx, schema, scope, source, key, version)
		return err
	})
	return out, found, err
}

func validGenerationRequest(scope, source, key, version string) error {
	if _, err := NewSchema(scope); err != nil {
		return ErrInvalidGeneration
	}
	for _, v := range []string{source, key} {
		id, err := uuid.Parse(v)
		if err != nil || id == uuid.Nil {
			return ErrInvalidGeneration
		}
	}
	if !extractionVersionPattern.MatchString(version) {
		return ErrInvalidExtractionVersion
	}
	return nil
}

func findGenerationTx(ctx context.Context, tx pgx.Tx, schema Schema, scope, source, key, version string) (GenerationResult, bool, error) {
	out := GenerationResult{Replayed: true}
	err := tx.QueryRow(ctx, schema.SQL(`SELECT generation_id::text,source_observation_id::text,extractor_version,applied_at,messages_read,facts_asserted,facts_retired,claims_rejected,claims_retracted,claims_refused_by_role
        FROM {schema}.fact_generation WHERE scope=$1 AND source_observation_id=$2::uuid AND operation_key=$3::uuid`), scope, source, key).Scan(&out.ID, &out.SourceID, &out.Version, &out.AppliedAt, &out.Messages, &out.Asserted, &out.Retired, &out.Rejected, &out.Retracted, &out.RefusedRole)
	if errors.Is(err, pgx.ErrNoRows) {
		return GenerationResult{}, false, nil
	}
	if err != nil {
		return GenerationResult{}, false, err
	}
	if out.Version != version {
		return GenerationResult{}, false, ErrGenerationConflict
	}
	return out, true, nil
}

// ApplyGeneration is one source's atomic cutover. The caller has already spent the model budget;
// source/revision comparison, policy checks, retirement, new facts, provenance and audit all commit
// together. A failed cutover leaves the previously readable generation untouched.
func (s *FactStore) ApplyGeneration(ctx context.Context, schema Schema, snapshot GenerationSnapshot, key, version, principal string, plan GenerationPlan) (GenerationResult, error) {
	var out GenerationResult
	if err := validGenerationRequest(snapshot.Scope, snapshot.SourceID, key, version); err != nil {
		return out, err
	}
	actor, err := uuid.Parse(principal)
	if err != nil || actor == uuid.Nil {
		return out, ErrInvalidGeneration
	}
	if len(plan.Claims) > MaxGenerationClaims || len(plan.Rejected) > MaxGenerationClaims {
		return out, ErrGenerationLimit
	}
	plan.Rejected = append([]domain.RejectedClaim{}, plan.Rejected...)
	encoded, err := json.Marshal(plan)
	if err != nil {
		return out, ErrInvalidGeneration
	}
	if len(encoded) > MaxGenerationBytes {
		return out, ErrGenerationLimit
	}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if plan.Job != nil {
			if err := validateGenerationJobTx(ctx, tx, schema, snapshot, key, version, plan.Job); err != nil {
				return err
			}
		}
		if err := lockClaimSource(ctx, tx, schema, snapshot.Scope, snapshot.SourceID); err != nil {
			return err
		}
		prior, found, err := findGenerationTx(ctx, tx, schema, snapshot.Scope, snapshot.SourceID, key, version)
		if err != nil {
			return err
		}
		if found {
			out = prior
			return nil
		}
		current, err := readGenerationSourceTx(ctx, tx, schema, snapshot.Scope, snapshot.SourceID, true)
		if err != nil {
			return err
		}
		if current.Digest != snapshot.Digest {
			return ErrGenerationConflict
		}
		messages := map[int]domain.Message{}
		for _, m := range current.Messages {
			messages[m.Ordinal] = m
		}
		claims := append([]domain.Claim{}, plan.Claims...)
		for i, c := range claims {
			m, ok := messages[c.SourceOrdinal]
			if !ok || c.ByteStart < 0 || c.ByteEnd <= c.ByteStart || c.ByteEnd > len(m.Content) || m.Content[c.ByteStart:c.ByteEnd] != c.Quote {
				return ErrInvalidReceipt
			}
			claims[i].ValidFrom = current.MessageTime(m)
		}
		for _, r := range plan.Rejected {
			if _, ok := messages[r.SourceOrdinal]; !ok {
				return ErrInvalidReceipt
			}
		}
		sort.SliceStable(claims, func(i, j int) bool { return claims[i].ValidFrom.Before(claims[j].ValidFrom) })
		out = GenerationResult{ID: uuid.NewString(), SourceID: current.SourceID, Version: version, Messages: len(current.Messages)}
		retired, err := retireGenerationTx(ctx, tx, schema, current.Scope, current.SourceID, &out)
		if err != nil {
			return err
		}
		gen := uuid.MustParse(out.ID)
		seen := map[string]bool{}
		for _, claim := range claims {
			m := messages[claim.SourceOrdinal]
			// One claim per savepoint: a refused insert aborts a PostgreSQL transaction, and a
			// generation must be able to refuse one claim and keep the rest.
			savepoint, err := tx.Begin(ctx)
			if err != nil {
				return err
			}
			id, err := assertFactVersionTx(ctx, savepoint, schema, current.Scope, current.SourceID, m.Role, current.Owner, claim, version, out.AppliedAt, gen)
			if err == nil {
				err = savepoint.Commit(ctx)
			} else {
				_ = savepoint.Rollback(ctx)
			}
			switch {
			case err == nil:
				if seen[id] {
					continue
				}
				seen[id] = true
				if err := recordGenerationFactTx(ctx, tx, schema, current.Scope, current.SourceID, out.ID, id, "admitted"); err != nil {
					return err
				}
				out.Asserted++
			case errors.Is(err, ErrRetractedClaim):
				out.Retracted++
			case errors.Is(err, ErrNotSpokenByPrincipal):
				out.RefusedRole++
				plan.Rejected = append(plan.Rejected, domain.RejectedClaim{Statement: claim.Statement, Predicate: claim.Predicate, Quote: claim.Quote, SourceOrdinal: claim.SourceOrdinal, Reason: domain.ReasonNotSpokenByPrincipal})
			case errors.Is(err, ErrUnboundSpeaker):
				plan.Rejected = append(plan.Rejected, domain.RejectedClaim{Statement: claim.Statement, Predicate: claim.Predicate, Quote: claim.Quote, SourceOrdinal: claim.SourceOrdinal, Reason: domain.ReasonUnresolvableSubject})
			case errors.Is(err, ErrConflictingValue):
				plan.Rejected = append(plan.Rejected, domain.RejectedClaim{Statement: claim.Statement, Predicate: claim.Predicate, Quote: claim.Quote, SourceOrdinal: claim.SourceOrdinal, Reason: domain.ReasonConflictingValue})
			default:
				return err
			}
		}
		if len(plan.Rejected) > MaxGenerationClaims {
			return ErrGenerationLimit
		}
		for _, rejected := range plan.Rejected {
			if err := rejectGenerationTx(ctx, tx, schema, current.Scope, current.SourceID, current.Owner, version, rejected); err != nil {
				return err
			}
			out.Rejected++
		}
		for _, id := range retired {
			if err := recordGenerationFactTx(ctx, tx, schema, current.Scope, current.SourceID, out.ID, id, "retired"); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, schema.SQL(`INSERT INTO {schema}.fact_generation(scope,generation_id,source_observation_id,operation_key,extractor_version,previous_extractor_version,previous_generation_id,
            applied_at,messages_read,facts_asserted,facts_retired,claims_rejected,claims_retracted,claims_refused_by_role,principal_id)
            VALUES($1,$2::uuid,$3::uuid,$4::uuid,$5,$6,$7::uuid,$8,$9,$10,$11,$12,$13,$14,$15::uuid)`), current.Scope, out.ID, current.SourceID, key, version, nullIfEmptyString(current.PreviousVersion), nullIfEmptyString(current.PreviousGeneration), out.AppliedAt, out.Messages, out.Asserted, out.Retired, out.Rejected, out.Retracted, out.RefusedRole, actor.String())
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, schema.SQL(`DELETE FROM {schema}.source_extraction WHERE scope=$1 AND source_observation_id=$2::uuid`), current.Scope, current.SourceID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, schema.SQL(`INSERT INTO {schema}.source_extraction(scope,source_observation_id,extractor_version,generation_id) VALUES($1,$2::uuid,$3,$4::uuid)`), current.Scope, current.SourceID, version, out.ID); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, schema.SQL(`INSERT INTO {schema}.audit_entry(operation,principal,principal_kind,project,magnitude,outcome) VALUES('formation.rebuild',$1::uuid,'operator',$2,$3,'allowed')`), actor.String(), current.Scope, out.Asserted+out.Retired)
		return err
	})
	if err != nil {
		var p *pgconn.PgError
		if errors.As(err, &p) && p.Code == "23P01" {
			err = ErrGenerationConflict
		}
		return GenerationResult{}, fmt.Errorf("apply fact generation: %w", err)
	}
	return out, nil
}

func retireGenerationTx(ctx context.Context, tx pgx.Tx, schema Schema, scope, source string, out *GenerationResult) ([]string, error) {
	if err := restoreMissingGenerationFactsTx(ctx, tx, schema, scope, source); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, schema.SQL(`SELECT f.fact_id::text,lower(f.known) FROM {schema}.fact f JOIN {schema}.fact_receipt r ON r.scope=f.scope AND r.fact_id=f.fact_id
        WHERE f.scope=$1 AND r.source_observation_id=$2::uuid AND upper_inf(f.known) AND r.evidence->>'extractor_version'<>'curated/v1'
        ORDER BY f.fact_id LIMIT $3 FOR UPDATE OF f`), scope, source, MaxGenerationClaims+1)
	if err != nil {
		return nil, err
	}
	var ids []string
	var newest time.Time
	for rows.Next() {
		var id string
		var at time.Time
		if err := rows.Scan(&id, &at); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
		if at.After(newest) {
			newest = at
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) > MaxGenerationClaims {
		return nil, ErrGenerationLimit
	}
	if err := tx.QueryRow(ctx, schema.SQL(`SELECT greatest(clock_timestamp(),$1::timestamptz+interval '1 microsecond',
        (SELECT max(applied_at)+interval '1 microsecond' FROM {schema}.fact_generation WHERE scope=$2 AND source_observation_id=$3::uuid),
        (SELECT max(coalesce(upper(f.known),lower(f.known)))+interval '1 microsecond' FROM {schema}.fact f JOIN {schema}.fact_receipt r ON r.scope=f.scope AND r.fact_id=f.fact_id WHERE r.scope=$2 AND r.source_observation_id=$3::uuid))`), newest, scope, source).Scan(&out.AppliedAt); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, schema.SQL(`UPDATE {schema}.fact SET known=tstzrange(lower(known),$3) WHERE scope=$1 AND fact_id=ANY($2::uuid[])`), scope, ids, out.AppliedAt); err != nil {
		return nil, err
	}
	out.Retired = len(ids)
	return ids, nil
}

func recordGenerationFactTx(ctx context.Context, tx pgx.Tx, schema Schema, scope, source, generation, id, disposition string) error {
	_, err := tx.Exec(ctx, schema.SQL(`INSERT INTO {schema}.fact_generation_record(scope,source_observation_id,generation_id,fact_id,disposition) VALUES($1,$2::uuid,$3::uuid,$4::uuid,$5) ON CONFLICT DO NOTHING`), scope, source, generation, id, disposition)
	return err
}

func rejectGenerationTx(ctx context.Context, tx pgx.Tx, schema Schema, scope, source, owner, version string, r domain.RejectedClaim) error {
	id, err := rejectedIdentity(scope, source, r, version)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, schema.SQL(insertRejectedClaimSQL), id, scope, source, r.SourceOrdinal, r.Predicate, r.Statement, r.Quote, r.Reason, version, nullIfEmpty(owner)); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, schema.SQL(registerProjectionSQL), source, scope, ProjectionRejectedClaim, id.String(), nullIfEmpty(owner))
	return err
}
