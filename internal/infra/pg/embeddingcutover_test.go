// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestEmbeddingCutoverChecksCoverageAndPruningRefusesRenamedStorage(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "embedding_cutover_checks")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	if _, err := pg.NewObservationStore(pool).Append(ctx, schema, turnOf(domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "one"}, domain.Message{Ordinal: 1, Role: domain.RoleUser, Content: "two"})); err != nil {
		t.Fatal(err)
	}
	store := pg.NewMessageEmbeddingStore(pool, schema)
	actor := uuid.NewString()
	model := pg.EmbeddingModel{Name: "test", Revision: "v1", EndpointHash: strings.Repeat("a", 64), Dimensions: 3}
	generation, err := store.Start(ctx, "p1", uuid.NewString(), actor, model)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.embedding_build SET state='ready'`)); err != nil {
		t.Fatal(err)
	}
	if err := store.Activate(ctx, "p1", generation.ID, actor); !errors.Is(err, pg.ErrEmbeddingIncomplete) {
		t.Fatalf("forged progress activated missing vectors: %v", err)
	}
	if err := store.Repair(ctx, "p1", generation.ID, actor); err != nil {
		t.Fatal(err)
	}
	changed := model
	changed.Revision = "different"
	good := func(context.Context, []string) ([][]float32, error) { return [][]float32{{1, 0, 0}, {0, 1, 0}}, nil }
	if _, err := store.BuildPage(ctx, "p1", generation.ID, actor, changed, 64, good); !errors.Is(err, pg.ErrEmbeddingConflict) {
		t.Fatalf("model mismatch: %v", err)
	}
	if _, err := store.BuildPage(ctx, "p1", generation.ID, actor, pg.EmbeddingModel{}, 64, good); !errors.Is(err, pg.ErrInvalidEmbedding) {
		t.Fatalf("invalid model: %v", err)
	}
	if _, err := store.BuildPage(ctx, "p1", generation.ID, actor, model, 64, good); err != nil {
		t.Fatal(err)
	}
	if page, err := store.BuildPage(ctx, "p1", generation.ID, actor, model, 64, func(context.Context, []string) ([][]float32, error) {
		t.Fatal("completed build repeated provider call")
		return nil, nil
	}); err != nil || !page.Complete || page.Stored != 0 {
		t.Fatalf("completion retry: %+v %v", page, err)
	}
	if n, err := store.Prune(ctx, "p1", generation.ID, actor, 1); err != nil || n != 1 {
		t.Fatalf("bounded prune: %d %v", n, err)
	}
	current, err := store.Generation(ctx, "p1", generation.ID)
	if err != nil || current.State != "ready" {
		t.Fatalf("partial prune discarded remaining rows: %+v %v", current, err)
	}
	partition := "message_vector_" + strings.ReplaceAll(generation.ID, "-", "")
	qualified := pgx.Identifier{schema.String(), partition}.Sanitize()
	renamed := pgx.Identifier{schema.String(), "unexpected_generation"}.Sanitize()
	if _, err := pool.Exec(ctx, `ALTER TABLE `+qualified+` RENAME TO unexpected_generation`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Prune(ctx, "p1", generation.ID, actor, 64); !errors.Is(err, pg.ErrEmbeddingConflict) {
		t.Fatalf("unvalidated storage dropped: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM `+renamed).Scan(&count); err != nil || count != 1 {
		t.Fatalf("failed prune changed storage: %d %v", count, err)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE `+renamed+` RENAME TO `+pgx.Identifier{partition}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Prune(ctx, "p1", generation.ID, actor, 64); err != nil {
		t.Fatal(err)
	}
}

func TestEmbeddingPagesRespectTheCombinedUTF8ByteBudget(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "embedding_byte_pages")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	for i := 0; i < 17; i++ {
		if _, err := pg.NewObservationStore(pool).Append(ctx, schema, turnOf(domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: strings.Repeat("é", 32768)})); err != nil {
			t.Fatal(err)
		}
	}
	store := pg.NewMessageEmbeddingStore(pool, schema)
	actor := uuid.NewString()
	model := pg.EmbeddingModel{Name: "test", Revision: "v1", EndpointHash: strings.Repeat("a", 64), Dimensions: 3}
	generation, err := store.Start(ctx, "p1", uuid.NewString(), actor, model)
	if err != nil {
		t.Fatal(err)
	}
	embed := func(_ context.Context, texts []string) ([][]float32, error) {
		total := 0
		vectors := make([][]float32, len(texts))
		for i, text := range texts {
			total += len(text)
			vectors[i] = []float32{1, 0, 0}
		}
		if total > pg.MaxEmbeddingPageBytes {
			t.Fatalf("provider received %d bytes", total)
		}
		return vectors, nil
	}
	first, err := store.BuildPage(ctx, "p1", generation.ID, actor, model, 64, embed)
	if err != nil || first.Complete || first.Examined != 16 {
		t.Fatalf("byte page: %+v %v", first, err)
	}
	second, err := store.BuildPage(ctx, "p1", generation.ID, actor, model, 64, embed)
	if err != nil || !second.Complete || second.Examined != 1 {
		t.Fatalf("remaining page: %+v %v", second, err)
	}
}
