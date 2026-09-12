// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
)

func TestExtractionPinsSerializeConflictingVersionsAndCascadeWithSources(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "extraction_pin_race")
	obs, facts := pg.NewObservationStore(pool), pg.NewFactStore(pool)
	source, err := obs.Append(ctx, schema, domain.Turn{Scope: "p1", DataSubjectID: "owner", Messages: []domain.Message{{Role: domain.RoleUser, Content: "text"}}})
	if err != nil {
		t.Fatal(err)
	}
	versions := []string{"extract/v2:" + strings.Repeat("a", 64), "extract/v2:" + strings.Repeat("b", 64)}
	var wg sync.WaitGroup
	result := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			result <- facts.PinExtraction(ctx, schema, "p1", source.ID, versions[i%2])
		}(i)
	}
	wg.Wait()
	close(result)
	accepted, refused := 0, 0
	for err := range result {
		if err == nil {
			accepted++
		} else if errors.Is(err, pg.ErrExtractionChanged) {
			refused++
		} else {
			t.Fatal(err)
		}
	}
	if accepted != 8 || refused != 8 {
		t.Fatalf("conflicting winners: %d %d", accepted, refused)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.source_extraction SET extractor_version='extract/v1'`)); err == nil {
		t.Fatal("pin can be rewritten")
	}
	if err := facts.PinExtraction(ctx, schema, "foreign", source.ID, versions[0]); err == nil {
		t.Fatal("foreign project accepted")
	}
	if err := facts.PinExtraction(ctx, schema, "p1", uuid.NewString(), versions[0]); err == nil {
		t.Fatal("missing source accepted")
	}
	if err := facts.PinExtraction(ctx, schema, "p1", "invalid", versions[0]); err == nil {
		t.Fatal("malformed source accepted")
	}
	for _, version := range []string{"", "extract/v2:short", "extract/v2:" + strings.Repeat("A", 64), "model-secret"} {
		if err := facts.PinExtraction(ctx, schema, "p1", source.ID, version); !errors.Is(err, pg.ErrInvalidExtractionVersion) {
			t.Fatal("invalid identity accepted", err)
		}
	}
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.observation SET retention_until=clock_timestamp()-interval '1 hour' WHERE observation_id=$1`), source.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.NewRetentionStore(pool, schema).Sweep(ctx, "p1", 10); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.source_extraction`)).Scan(&count); err != nil || count != 0 {
		t.Fatal("expiry retained pin", err)
	}
}

func TestLostExtractionPinCannotRelabelSurvivingEvidenceOrReceipts(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "extraction_pin_loss")
	obs, facts := pg.NewObservationStore(pool), pg.NewFactStore(pool)
	id := statedAt(t, ctx, obs, facts, schema, time.Now(), "Atlas works at Ensera", domain.Claim{Subject: "Atlas", Object: "Ensera", Predicate: "works_at", Statement: "Atlas works at Ensera", Cardinality: domain.CardinalityMany})
	var source string
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT source_observation_id::text FROM {schema}.fact_receipt WHERE fact_id=$1`), id).Scan(&source); err != nil {
		t.Fatal(err)
	}
	version := "extract/v2:" + strings.Repeat("a", 64)
	for _, statement := range []string{`DELETE FROM {schema}.source_extraction`, `DELETE FROM {schema}.fact_evidence`, `DELETE FROM {schema}.fact`} {
		if _, err := pool.Exec(ctx, schema.SQL(statement)); err != nil {
			t.Fatal(err)
		}
		if err := facts.PinExtraction(ctx, schema, "p1", source, version); !errors.Is(err, pg.ErrExtractionChanged) {
			t.Fatal("lost projection let changed pipeline relabel source", err)
		}
	}
	if err := facts.PinExtraction(ctx, schema, "p1", source, pg.ExtractorVersion); err != nil {
		t.Fatal("original identity cannot restore pin", err)
	}
}
