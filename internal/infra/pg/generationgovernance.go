// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"context"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/jackc/pgx/v5"
)

type GenerationLink struct {
	ID               string    `json:"generation_id"`
	ExtractorVersion string    `json:"extractor_version"`
	AppliedAt        time.Time `json:"applied_at"`
}

// At most two associations per record are enforced by the disposition index. Citation inspection
// can explain both how this version arose and why it stopped appearing in current recall.
func generationLinksTx(ctx context.Context, tx pgx.Tx, schema Schema, scope, id string) (*GenerationLink, *GenerationLink, error) {
	rows, err := tx.Query(ctx, schema.SQL(`SELECT r.disposition,g.generation_id::text,g.extractor_version,g.applied_at
        FROM {schema}.fact_generation_record r JOIN {schema}.fact_generation g ON g.scope=r.scope AND g.generation_id=r.generation_id
        WHERE r.scope=$1 AND r.fact_id=$2::uuid ORDER BY r.disposition`), scope, id)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var admitted, retired *GenerationLink
	for rows.Next() {
		var disposition string
		var link GenerationLink
		if err := rows.Scan(&disposition, &link.ID, &link.ExtractorVersion, &link.AppliedAt); err != nil {
			return nil, nil, err
		}
		if disposition == "admitted" {
			admitted = &link
		} else {
			retired = &link
		}
	}
	return admitted, retired, rows.Err()
}

// Delete and count before fact receipts cascade associations. The source pointer's deferred FK
// remains satisfied when erasure removes the source's extraction pin later in the transaction.
func eraseGenerationMetadataTx(ctx context.Context, tx pgx.Tx, schema Schema, scope, owned string, owner any, out *domain.Erasure) error {
	for _, table := range []string{"fact_generation_record", "fact_generation"} {
		predicate := `r.scope=$1 AND r.source_observation_id IN (` + owned + `)`
		tag, err := tx.Exec(ctx, schema.SQL("DELETE FROM {schema}."+table+" r WHERE "+predicate), scope, owner)
		if err != nil {
			return err
		}
		out.Deleted[table] = int(tag.RowsAffected())
		var residual int
		if err := tx.QueryRow(ctx, schema.SQL("SELECT count(*) FROM {schema}."+table+" r WHERE "+predicate), scope, owner).Scan(&residual); err != nil {
			return err
		}
		out.Residual[table] = residual
	}
	return nil
}

// A human correction governs its source-message subject/relation slot during reinterpretation.
// Merely changing the proposed object must not step around a human's corrected value. Ordinary
// withdrawals keep their structural-claim scope; independent later sources remain independent.
func refuseCorrectedGenerationSlotTx(ctx context.Context, tx pgx.Tx, schema Schema, scope, source, owner string, claim domain.Claim, leftSpeaker bool) error {
	kind, key := claimEndpoint(claim.Subject, owner, leftSpeaker)
	var corrected bool
	err := tx.QueryRow(ctx, schema.SQL(`SELECT EXISTS(SELECT 1 FROM {schema}.record_retraction w
        JOIN {schema}.fact_receipt r ON r.scope=w.scope AND r.fact_id=w.target_fact_id
        WHERE w.scope=$1 AND w.source_observation_id=$2::uuid AND w.source_ordinal=$3
        AND w.replacement_fact_id IS NOT NULL AND r.state->>'predicate'=$4
        AND coalesce(r.subject_identity->>'identity_kind','none')=$5
        AND coalesce(CASE WHEN $5='speaker' THEN r.subject_identity->>'speaker_subject_id' ELSE r.subject_identity->>'normalized_name' END,'')=$6)`), scope, source, claim.SourceOrdinal, claim.Predicate, kind, key).Scan(&corrected)
	if err != nil {
		return err
	}
	if corrected {
		return ErrRetractedClaim
	}
	return nil
}
