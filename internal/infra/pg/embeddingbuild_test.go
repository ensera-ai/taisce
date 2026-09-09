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
)

func TestEmbeddingBuildResumesRepairsAndDoesNotResurrectErasedSources(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "embedding_build_pages")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	observations := pg.NewObservationStore(pool)
	source, err := observations.Append(ctx, schema, turnOf(domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "first"}, domain.Message{Ordinal: 1, Role: domain.RoleAssistant, Content: "second"}))
	if err != nil {
		t.Fatal(err)
	}
	store := pg.NewMessageEmbeddingStore(pool, schema)
	actor := uuid.NewString()
	model := pg.EmbeddingModel{Name: "test", Revision: "v1", EndpointHash: strings.Repeat("a", 64), Dimensions: 2560}
	gen, err := store.Start(ctx, "p1", uuid.NewString(), actor, model)
	if err != nil {
		t.Fatal(err)
	}
	embed := func(_ context.Context, texts []string) ([][]float32, error) {
		vectors := make([][]float32, len(texts))
		for i := range texts {
			vectors[i] = make([]float32, model.Dimensions)
			vectors[i][i%model.Dimensions] = 1
		}
		return vectors, nil
	}
	page, err := store.BuildPage(ctx, "p1", gen.ID, actor, model, 1, embed)
	if err != nil || page.Complete || page.Stored != 1 {
		t.Fatalf("first page %+v %v", page, err)
	}
	if err := store.Activate(ctx, "p1", gen.ID, actor); !errors.Is(err, pg.ErrEmbeddingIncomplete) {
		t.Fatalf("partial activation %v", err)
	}
	page, err = store.BuildPage(ctx, "p1", gen.ID, actor, model, 1, embed)
	if err != nil || !page.Complete || page.Stored != 1 {
		t.Fatalf("last page %+v %v", page, err)
	}
	if err := store.Activate(ctx, "p1", gen.ID, actor); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`DELETE FROM {schema}.message_embedding WHERE chunk_id=(SELECT chunk_id FROM {schema}.message_embedding LIMIT 1)`)); err != nil {
		t.Fatal(err)
	}
	if err := store.Repair(ctx, "p1", gen.ID, actor); err != nil {
		t.Fatal(err)
	}
	page, err = store.BuildPage(ctx, "p1", gen.ID, actor, model, 64, embed)
	if err != nil || !page.Complete || page.Stored != 1 {
		t.Fatalf("repair %+v %v", page, err)
	}
	second, err := store.Start(ctx, "p1", uuid.NewString(), actor, model)
	if err != nil {
		t.Fatal(err)
	}
	eraseDuringModel := func(ctx context.Context, texts []string) ([][]float32, error) {
		if _, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", "subject-1", "embedding test"); err != nil {
			return nil, err
		}
		return embed(ctx, texts)
	}
	page, err = store.BuildPage(ctx, "p1", second.ID, actor, model, 64, eraseDuringModel)
	if err != nil || !page.Complete || page.Stored != 0 || page.Examined != 2 {
		t.Fatalf("erasure race %+v %v", page, err)
	}
	var count int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.message_embedding WHERE source_observation_id=$1::uuid`), source.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("erased vectors remain %d %v", count, err)
	}
}

func TestEmbeddingBuildCancellationFencesInFlightResults(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "embedding_build_cancel")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	if _, err := pg.NewObservationStore(pool).Append(ctx, schema, turnOf(domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "message"})); err != nil {
		t.Fatal(err)
	}
	store := pg.NewMessageEmbeddingStore(pool, schema)
	actor := uuid.NewString()
	model := pg.EmbeddingModel{Name: "test", Revision: "v1", EndpointHash: strings.Repeat("b", 64), Dimensions: 3}
	gen, err := store.Start(ctx, "p1", uuid.NewString(), actor, model)
	if err != nil {
		t.Fatal(err)
	}
	embed := func(ctx context.Context, texts []string) ([][]float32, error) {
		if _, err := store.BuildPage(ctx, "p1", gen.ID, actor, model, 1, func(context.Context, []string) ([][]float32, error) {
			t.Fatal("second worker reached provider")
			return nil, nil
		}); !errors.Is(err, pg.ErrEmbeddingBusy) {
			t.Fatalf("concurrent worker: %v", err)
		}
		if err := store.Cancel(ctx, "p1", gen.ID, actor); err != nil {
			return nil, err
		}
		return [][]float32{{1, 0, 0}}, nil
	}
	if _, err := store.BuildPage(ctx, "p1", gen.ID, actor, model, 1, embed); !errors.Is(err, pg.ErrEmbeddingConflict) {
		t.Fatalf("cancelled result committed: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.message_embedding`)).Scan(&count); err != nil || count != 0 {
		t.Fatalf("cancelled vectors remain %d %v", count, err)
	}
}
