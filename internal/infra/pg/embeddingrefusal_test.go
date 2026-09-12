// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
)

func TestEmbeddingBoundariesRefuseInvalidModelsIdentifiersAndSearchWork(t *testing.T) {
	ctx := context.Background()
	schema, _ := pg.NewSchema("memory")
	store := pg.NewMessageEmbeddingStore(nil, schema)
	actor, id := uuid.NewString(), uuid.NewString()
	valid := pg.EmbeddingModel{Name: "test", Revision: "v1", EndpointHash: strings.Repeat("a", 64), Dimensions: 3}
	for _, model := range []pg.EmbeddingModel{{}, {Name: strings.Repeat("x", 257)}, {Name: "\xff"}, {Name: "a\x00"}, {Name: "a", Revision: " ", EndpointHash: valid.EndpointHash, Dimensions: 3}, {Name: "a", Revision: "v1", EndpointHash: "bad", Dimensions: 3}, {Name: "a", Revision: "v1", EndpointHash: strings.Repeat("A", 64), Dimensions: 3}, {Name: "a", Revision: "v1", EndpointHash: valid.EndpointHash, Dimensions: 4001}} {
		if _, err := store.Start(ctx, "p1", id, actor, model); !errors.Is(err, pg.ErrInvalidEmbedding) {
			t.Fatalf("invalid model: %+v %v", model, err)
		}
	}
	for _, scope := range []string{"", "Uppercase"} {
		checks := []func() error{
			func() error { _, e := store.Start(ctx, scope, id, actor, valid); return e }, func() error { _, e := store.Generation(ctx, scope, id); return e }, func() error { _, e := store.Active(ctx, scope); return e },
			func() error { return store.Activate(ctx, scope, id, actor) }, func() error { return store.Cancel(ctx, scope, id, actor) }, func() error { return store.Repair(ctx, scope, id, actor) }, func() error { _, e := store.Prune(ctx, scope, id, actor, 1); return e },
			func() error { _, e := store.BuildPage(ctx, scope, id, actor, valid, 1, nil); return e }, func() error {
				_, e := store.Search(ctx, scope, actor, valid, []float32{1, 0, 0}, pg.MessageSearchOptions{Limit: 1})
				return e
			},
		}
		for _, check := range checks {
			if err := check(); !errors.Is(err, pg.ErrInvalidEmbedding) {
				t.Fatalf("invalid scope: %v", err)
			}
		}
	}
	if _, err := store.Generation(ctx, "p1", uuid.Nil.String()); !errors.Is(err, pg.ErrInvalidEmbedding) {
		t.Fatal(err)
	}
	if _, err := store.Generation(ctx, "p1", "invalid"); !errors.Is(err, pg.ErrInvalidEmbedding) {
		t.Fatal(err)
	}
	for _, n := range []int{0, 65} {
		if _, err := store.Prune(ctx, "p1", id, actor, n); !errors.Is(err, pg.ErrInvalidEmbedding) {
			t.Fatal(err)
		}
		if _, err := store.BuildPage(ctx, "p1", id, actor, valid, n, nil); !errors.Is(err, pg.ErrInvalidEmbedding) {
			t.Fatal(err)
		}
	}
	for _, vector := range [][]float32{{}, {1, 0}, {0, 0, 0}, {float32(math.NaN()), 0, 1}, {float32(math.Inf(1)), 0, 1}} {
		if _, err := store.Search(ctx, "p1", actor, valid, vector, pg.MessageSearchOptions{Limit: 1}); !errors.Is(err, pg.ErrInvalidEmbedding) {
			t.Fatal(err)
		}
	}
	for _, options := range []pg.MessageSearchOptions{{}, {Limit: 101}, {Limit: 1, Candidates: -1}, {Limit: 1, Candidates: 1001}, {Limit: 2, Candidates: 1}, {Limit: 1, Role: "invented"}, {Limit: 1, Subject: strings.Repeat("x", 1025)}, {Limit: 1, Subject: "\xff"}, {Limit: 1, Subject: "\x00"}} {
		if _, err := store.Search(ctx, "p1", actor, valid, []float32{1, 0, 0}, options); !errors.Is(err, pg.ErrInvalidEmbedding) {
			t.Fatalf("options %+v %v", options, err)
		}
	}
	if _, err := store.Search(ctx, "p1", actor, pg.EmbeddingModel{}, []float32{1}, pg.MessageSearchOptions{Limit: 1}); !errors.Is(err, pg.ErrInvalidEmbedding) {
		t.Fatal(err)
	}
}

func TestEmbeddingBuildRefusesProviderFailuresChangedSourcesAndConflictingChunks(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "embedding_build_refusals")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	if _, err := pg.NewObservationStore(pool).Append(ctx, schema, turnOf(domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "message"})); err != nil {
		t.Fatal(err)
	}
	store := pg.NewMessageEmbeddingStore(pool, schema)
	actor := uuid.NewString()
	model := pg.EmbeddingModel{Name: "test", Revision: "v1", EndpointHash: strings.Repeat("a", 64), Dimensions: 3}
	generation, err := store.Start(ctx, "p1", uuid.NewString(), actor, model)
	if err != nil {
		t.Fatal(err)
	}
	good := func(context.Context, []string) ([][]float32, error) { return [][]float32{{1, 0, 0}}, nil }
	for _, vectors := range [][][]float32{nil, {{1, 0}}, {{0, 0, 0}}, {{float32(math.NaN()), 0, 0}}, {{float32(math.Inf(-1)), 0, 0}}} {
		if _, err := store.BuildPage(ctx, "p1", generation.ID, actor, model, 1, func(context.Context, []string) ([][]float32, error) { return vectors, nil }); !errors.Is(err, pg.ErrInvalidEmbedding) {
			t.Fatalf("invalid provider result %v", err)
		}
	}
	refused := errors.New("provider refused")
	if _, err := store.BuildPage(ctx, "p1", generation.ID, actor, model, 1, func(context.Context, []string) ([][]float32, error) { return nil, refused }); !errors.Is(err, refused) {
		t.Fatal(err)
	}
	changed := func(ctx context.Context, texts []string) ([][]float32, error) {
		if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.turn_message SET content='edited'`)); err != nil {
			return nil, err
		}
		return good(ctx, texts)
	}
	if _, err := store.BuildPage(ctx, "p1", generation.ID, actor, model, 1, changed); !errors.Is(err, pg.ErrEmbeddingConflict) {
		t.Fatalf("source changed during inference: %v", err)
	}
	if _, err := store.BuildPage(ctx, "p1", generation.ID, actor, model, 1, good); !errors.Is(err, pg.ErrEmbeddingConflict) {
		t.Fatalf("conflicting chunk accepted: %v", err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`DELETE FROM {schema}.chunk; DELETE FROM {schema}.projection_dependency WHERE projection_kind='chunk'`)); err != nil {
		t.Fatal(err)
	}
	if page, err := store.BuildPage(ctx, "p1", generation.ID, actor, model, 1, good); err != nil || page.Stored != 1 {
		t.Fatalf("missing chunk repair: %+v %v", page, err)
	}
	if err := store.Cancel(ctx, "p1", generation.ID, actor); err != nil {
		t.Fatal(err)
	}
	if err := store.Cancel(ctx, "p1", generation.ID, actor); err != nil {
		t.Fatal(err)
	}
	if _, err := store.BuildPage(ctx, "p1", generation.ID, actor, model, 1, good); !errors.Is(err, pg.ErrEmbeddingConflict) {
		t.Fatalf("cancelled build %v", err)
	}
	if n, err := store.Prune(ctx, "p1", generation.ID, actor, 1); err != nil || n != 1 {
		t.Fatalf("prune nonempty: %d %v", n, err)
	}
	if n, err := store.Prune(ctx, "p1", generation.ID, actor, 1); err != nil || n != 0 {
		t.Fatalf("retry prune: %d %v", n, err)
	}
	if err := store.Repair(ctx, "p1", generation.ID, actor); !errors.Is(err, pg.ErrEmbeddingConflict) {
		t.Fatalf("discarded repair %v", err)
	}
}

func TestEmbeddingBuildDoesNotSendOversizedOrBlankSourcesToTheProvider(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "embedding_build_size")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	if _, err := pg.NewObservationStore(pool).Append(ctx, schema, turnOf(domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "message"})); err != nil {
		t.Fatal(err)
	}
	store := pg.NewMessageEmbeddingStore(pool, schema)
	actor := uuid.NewString()
	model := pg.EmbeddingModel{Name: "test", Revision: "v1", EndpointHash: strings.Repeat("a", 64), Dimensions: 3}
	generation, err := store.Start(ctx, "p1", uuid.NewString(), actor, model)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{strings.Repeat("x", 65537), "   "} {
		if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.turn_message SET content=$1`), text); err != nil {
			t.Fatal(err)
		}
		if _, err := store.BuildPage(ctx, "p1", generation.ID, actor, model, 64, func(context.Context, []string) ([][]float32, error) {
			t.Fatal("invalid source reached provider")
			return nil, nil
		}); err == nil {
			t.Fatal("invalid source accepted")
		}
	}
}
