// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
)

func TestEmbeddingGenerationsPreserveIdentityAndRequireExplicitCutover(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "embedding_generation_lifecycle")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	store := pg.NewMessageEmbeddingStore(pool, schema)
	actor, key := uuid.NewString(), uuid.NewString()
	model := pg.EmbeddingModel{Name: "model", Revision: "revision-1", EndpointHash: strings.Repeat("a", 64), Dimensions: 1024}
	first, err := store.Start(ctx, "p1", key, actor, model)
	if err != nil {
		t.Fatal(err)
	}
	again, err := store.Start(ctx, "p1", key, actor, model)
	if err != nil || again.ID != first.ID {
		t.Fatalf("lost acknowledgement: %+v %v", again, err)
	}
	model.Revision = "revision-2"
	if _, err := store.Start(ctx, "p1", key, actor, model); !errors.Is(err, pg.ErrEmbeddingConflict) {
		t.Fatalf("changed contract: %v", err)
	}
	if err := store.Activate(ctx, "p1", first.ID, actor); !errors.Is(err, pg.ErrEmbeddingIncomplete) {
		t.Fatalf("premature activation: %v", err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.embedding_generation SET model_revision='changed' WHERE generation_id=$1::uuid`), first.ID); err == nil {
		t.Fatal("immutable identity changed")
	}
	// Empty projects are complete once the worker finishes its empty page.
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.embedding_build SET state='ready' WHERE generation_id=$1::uuid`), first.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.Activate(ctx, "p1", first.ID, actor); err != nil {
		t.Fatal(err)
	}
	model.Dimensions = 2560
	second, err := store.Start(ctx, "p1", uuid.NewString(), actor, model)
	if err != nil {
		t.Fatal(err)
	}
	active, err := store.Active(ctx, "p1")
	if err != nil || active.ID != first.ID {
		t.Fatalf("candidate altered active model: %+v %v", active, err)
	}
	if _, err := store.Start(ctx, "p1", uuid.NewString(), actor, model); !errors.Is(err, pg.ErrEmbeddingCapacity) {
		t.Fatalf("unbounded generations: %v", err)
	}
	if err := store.Cancel(ctx, "p1", first.ID, actor); !errors.Is(err, pg.ErrEmbeddingConflict) {
		t.Fatalf("cancel active: %v", err)
	}
	if _, err := store.Prune(ctx, "p1", first.ID, actor, 64); !errors.Is(err, pg.ErrEmbeddingConflict) {
		t.Fatalf("prune active: %v", err)
	}
	if _, err := store.Generation(ctx, "p2", first.ID); !errors.Is(err, pg.ErrEmbeddingNotFound) {
		t.Fatalf("foreign project: %v", err)
	}
	if err := store.Cancel(ctx, "p1", second.ID, actor); err != nil {
		t.Fatal(err)
	}
	if n, err := store.Prune(ctx, "p1", second.ID, actor, 64); err != nil || n != 0 {
		t.Fatalf("prune empty candidate: %d %v", n, err)
	}
	tombstone, err := store.Start(ctx, "p1", second.Key, actor, model)
	if err != nil || tombstone.State != "discarded" || tombstone.ID != second.ID {
		t.Fatalf("prune lost identity: %+v %v", tombstone, err)
	}
	if _, err := store.Start(ctx, "p1", uuid.NewString(), actor, model); err != nil {
		t.Fatalf("prune did not free capacity: %v", err)
	}
}
