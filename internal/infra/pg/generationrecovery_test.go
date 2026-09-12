// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
)

func TestGenerationRetiresMissingFactsBeforeReceiptRecoveryCanReviveThem(t *testing.T) {
	ctx := context.Background()
	f := generationSource(t, "generation_missing_prior")
	if _, err := f.pool.Exec(ctx, f.schema.SQL(`DELETE FROM {schema}.fact WHERE fact_id=$1`), f.original); err != nil {
		t.Fatal(err)
	}
	snapshot, err := f.facts.ReadGenerationSource(ctx, f.schema, "p1", f.snapshot.SourceID)
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.facts.ApplyGeneration(ctx, f.schema, snapshot, uuid.NewString(), f.version, uuid.NewString(), pg.GenerationPlan{Claims: []domain.Claim{f.claim}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.facts.RecoverFacts(ctx, f.schema, "p1", f.snapshot.SourceID, "", uuid.NewString(), 100); err != nil {
		t.Fatal(err)
	}
	old, err := pg.NewCitationStore(f.pool, f.schema).Resolve(ctx, "p1", f.original, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if old.Known.Until == nil || !old.Known.Until.Equal(result.AppliedAt) || old.ReinterpretedBy == nil || old.ReinterpretedBy.ID != result.ID || result.Retired != 1 {
		t.Fatalf("receipt recovery revived a superseded interpretation: retired=%d known=%+v lineage=%+v", result.Retired, old.Known, old.ReinterpretedBy)
	}
}

func TestGenerationRefusesDamagedRecoveryReceiptsWithoutPublishing(t *testing.T) {
	ctx := context.Background()
	f := generationSource(t, "generation_damaged_receipt")
	if _, err := f.pool.Exec(ctx, f.schema.SQL(`DELETE FROM {schema}.fact WHERE fact_id=$1`), f.original); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, f.schema.SQL(`UPDATE {schema}.fact_receipt SET evidence=jsonb_set(evidence,'{quote}','"not the original words"') WHERE fact_id=$1`), f.original); err != nil {
		t.Fatal(err)
	}
	snapshot, err := f.facts.ReadGenerationSource(ctx, f.schema, "p1", f.snapshot.SourceID)
	if err != nil {
		t.Fatal(err)
	}
	before := generationState(t, f, "fact_receipt")
	if _, err := f.facts.ApplyGeneration(ctx, f.schema, snapshot, uuid.NewString(), f.version, uuid.NewString(), pg.GenerationPlan{Claims: []domain.Claim{f.claim}}); !errors.Is(err, pg.ErrInvalidReceipt) {
		t.Fatalf("damaged receipt was ignored: %v", err)
	}
	if generationState(t, f, "fact_receipt") != before {
		t.Fatal("failed cutover changed the damaged receipt")
	}
	for _, table := range []string{"fact", "fact_generation", "fact_generation_record"} {
		if generationState(t, f, table) != "[]" {
			t.Fatalf("failed cutover published %s", table)
		}
	}
}
