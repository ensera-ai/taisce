// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"context"
	"errors"
	"regexp"

	"github.com/jackc/pgx/v5"
)

var ErrExtractionChanged = errors.New("source extraction configuration changed; use its original configuration or an explicit rebuild")
var ErrInvalidExtractionVersion = errors.New("invalid extraction identity")
var extractionVersionPattern = regexp.MustCompile(`^extract/v2:[a-f0-9]{64}$`)

// PinExtraction prevents a restart with another model from silently mixing partial outcomes. It
// commits before inference so even a crash before the first fact retains the accepted identity.
func (s *FactStore) PinExtraction(ctx context.Context, schema Schema, scope, source, version string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error { return pinExtractionTx(ctx, tx, schema, scope, source, version) })
}

func pinExtractionTx(ctx context.Context, tx pgx.Tx, schema Schema, scope, source, version string) error {
	if err := lockClaimSource(ctx, tx, schema, scope, source); err != nil {
		return err
	}
	return pinLockedExtractionTx(ctx, tx, schema, scope, source, version)
}

// Fact assertion already holds the source lock while validating attribution. Share its lock so the
// established role refusal stays ahead of provenance writes without another advisory-lock query.
func pinLockedExtractionTx(ctx context.Context, tx pgx.Tx, schema Schema, scope, source, version string) error {
	if version != ExtractorVersion && version != CuratedExtractorVersion && !extractionVersionPattern.MatchString(version) {
		return ErrInvalidExtractionVersion
	}
	var existing string
	var generated bool
	err := tx.QueryRow(ctx, schema.SQL(`SELECT extractor_version,generation_id IS NOT NULL FROM {schema}.source_extraction WHERE scope=$1 AND source_observation_id=$2::uuid`), scope, source).Scan(&existing, &generated)
	if err == nil {
		if existing != version || generated {
			return ErrExtractionChanged
		}
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	// Surviving evidence also protects a source whose control row was lost. Never relabel it.
	var changed bool
	err = tx.QueryRow(ctx, schema.SQL(`SELECT EXISTS (
        SELECT 1 FROM {schema}.fact_evidence WHERE scope=$1 AND source_observation_id=$2::uuid AND extractor_version<>$3
        UNION ALL SELECT 1 FROM {schema}.rejected_claim WHERE scope=$1 AND source_observation_id=$2::uuid AND extractor_version<>$3
        UNION ALL SELECT 1 FROM {schema}.fact_receipt WHERE scope=$1 AND source_observation_id=$2::uuid AND evidence->>'extractor_version'<>$3
    )`), scope, source, version).Scan(&changed)
	if err != nil {
		return err
	}
	if changed {
		return ErrExtractionChanged
	}
	_, err = tx.Exec(ctx, schema.SQL(`INSERT INTO {schema}.source_extraction(scope,source_observation_id,extractor_version) VALUES($1,$2::uuid,$3)`), scope, source, version)
	return err
}
