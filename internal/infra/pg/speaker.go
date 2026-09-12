// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"context"
	"errors"
	"fmt"
	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The model supplies reference words, never the key. This read binds relative endpoints to the
// stored source, refusing forged attribution and preventing a caller's role argument from granting
// an assistant the authority of a user. Named endpoints retain their ordinary shared identity.
func bindSpeaker(ctx context.Context, tx pgx.Tx, schema Schema, scope, observationID string,
	role domain.Role, subject string, claim domain.Claim) (bool, bool, error) {
	var left, right bool
	err := tx.QueryRow(ctx, schema.SQL(`SELECT
        EXISTS(SELECT 1 FROM {schema}.speaker_term WHERE term=$1),
        EXISTS(SELECT 1 FROM {schema}.speaker_term WHERE term=$2)`),
		domain.NormalizeName(claim.Subject), domain.NormalizeName(claim.Object)).Scan(&left, &right)
	if err != nil {
		return false, false, fmt.Errorf("classify speaker references: %w", err)
	}
	if !left && !right {
		return false, false, nil
	}
	if !role.SpeaksForPrincipal() {
		// The words speak for the principal and the message was not the principal speaking. The
		// caller's role argument cannot lend that authority; only the stored user message has it.
		return false, false, fmt.Errorf("%w: message %d has role %q", ErrNotSpokenByPrincipal, claim.SourceOrdinal, role)
	}
	if subject == "" {
		return false, false, ErrUnboundSpeaker
	}
	var storedSubject *string
	var storedRole string
	err = tx.QueryRow(ctx, schema.SQL(`SELECT o.data_subject_id, m.role
        FROM {schema}.observation o JOIN {schema}.turn_message m USING (observation_id)
        WHERE o.observation_id=$1::uuid AND o.scope=$2 AND m.ordinal=$3
        FOR KEY SHARE OF o, m`), observationID, scope, claim.SourceOrdinal).Scan(&storedSubject, &storedRole)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, false, ErrUnboundSpeaker
	}
	if err != nil {
		return false, false, fmt.Errorf("bind speaker to source: %w", err)
	}
	if storedSubject == nil || *storedSubject != subject || storedRole != string(domain.RoleUser) {
		return false, false, ErrUnboundSpeaker
	}
	return left, right, nil
}

// Speaker names are intentionally generic. The protected attribution key is separate metadata;
// a named entity literally called "speaker" cannot collide with this identity namespace.
func resolveSpeaker(ctx context.Context, tx pgx.Tx, schema Schema, scope, observationID, subject string) (string, error) {
	var id string
	err := tx.QueryRow(ctx, schema.SQL(`INSERT INTO {schema}.entity
        (entity_id,scope,canonical_name,normalized_name,entity_type,identity_kind,speaker_subject_id,resolution_version)
        VALUES ($1,$2,'speaker','speaker','person','speaker',$3,'entity/speaker-v1')
        ON CONFLICT (scope,speaker_subject_id) WHERE identity_kind='speaker'
        DO UPDATE SET resolution_version={schema}.entity.resolution_version
        RETURNING entity_id::text`), uuid.New(), scope, subject).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("resolve speaker: %w", err)
	}
	if _, err = tx.Exec(ctx, schema.SQL(registerProjectionSQL), observationID, scope, ProjectionEntity, id, subject); err != nil {
		return "", fmt.Errorf("register speaker: %w", err)
	}
	return id, nil
}
