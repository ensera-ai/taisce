// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"testing"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
)

func TestExtractionPinCannotBeInventedWhenSurvivingOutputCannotBeChecked(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "extraction_pin_read_failure")
	source, err := pg.NewObservationStore(pool).Append(ctx, schema, domain.Turn{Scope: "p1", Messages: []domain.Message{{Role: domain.RoleTool, Content: "text"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`ALTER TABLE {schema}.fact_evidence RENAME TO unavailable_evidence`)); err != nil {
		t.Fatal(err)
	}
	if err := pg.NewFactStore(pool).PinExtraction(ctx, schema, "p1", source.ID, pg.ExtractorVersion); err == nil {
		t.Fatal("unchecked source pinned")
	}
	var count int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.source_extraction`)).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed read changed source identity", err)
	}
}
