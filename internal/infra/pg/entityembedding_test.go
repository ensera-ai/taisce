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

func entityVectors(_ context.Context, texts []string) ([][]float32, error) {
	vectors := make([][]float32, len(texts))
	for i, text := range texts {
		if strings.HasPrefix(strings.ToLower(text), "ensera") {
			vectors[i] = []float32{1, 0, 0}
		} else {
			vectors[i] = []float32{0, 1, 0}
		}
	}
	return vectors, nil
}

func buildEntityGeneration(t *testing.T, store *pg.EntityEmbeddingStore, generation pg.EntityEmbeddingGeneration,
	actor string, model pg.EmbeddingModel) {
	t.Helper()
	for {
		page, err := store.BuildPage(context.Background(), generation.Project, generation.ID, actor, model, 1, entityVectors)
		if err != nil {
			t.Fatal(err)
		}
		if page.Complete {
			return
		}
	}
}

func TestEntityEmbeddingCandidatesAreSourceOwnedAndRebuildAfterErasure(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "entity_embedding_ownership")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	for _, source := range []struct{ owner, name string }{{"base", "Ensera"}, {"departing", "ENSERA"}} {
		if _, err := storeEntityName(ctx, pool, schema, source.owner, source.name); err != nil {
			t.Fatal(err)
		}
	}
	store := pg.NewEntityEmbeddingStore(pool, schema)
	actor := uuid.NewString()
	model := pg.EmbeddingModel{Name: "candidate-model", Revision: "revision-1", EndpointHash: strings.Repeat("a", 64), Dimensions: 3}
	generation, err := store.Start(ctx, "p1", uuid.NewString(), actor, model)
	if err != nil || generation.TargetCount != 2 {
		t.Fatalf("start %+v %v", generation, err)
	}
	buildEntityGeneration(t, store, generation, actor, model)
	if err := store.Activate(ctx, "p1", generation.ID, actor); err != nil {
		t.Fatal(err)
	}
	for _, candidateCount := range []int{0, 16} {
		found, err := store.FindCandidates(ctx, "p1", model, []float32{1, 0, 0}, pg.EntityCandidateOptions{Limit: 2, Candidates: candidateCount})
		if err != nil || len(found.Candidates) != 2 || found.Candidates[0].NamePreview != "Ensera" ||
			found.Candidates[0].Similarity != 1 || found.Approximate != (candidateCount > 0) {
			t.Fatalf("candidates %+v %v", found, err)
		}
	}
	var enseraID string
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT entity_id::text FROM {schema}.entity WHERE scope='p1' AND normalized_name='ensera'`)).Scan(&enseraID); err != nil {
		t.Fatal(err)
	}
	var sourceCount, registrations int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT source_count,(SELECT count(*) FROM {schema}.projection_dependency
 WHERE projection_kind='entity_embedding' AND entity_embedding_generation_ref=$1::uuid AND entity_embedding_entity_ref=$2::uuid)
 FROM {schema}.entity_embedding WHERE generation_id=$1::uuid AND entity_id=$2::uuid`), generation.ID, enseraID).
		Scan(&sourceCount, &registrations); err != nil || sourceCount != 2 || registrations != 2 {
		t.Fatalf("ownership sources=%d registrations=%d err=%v", sourceCount, registrations, err)
	}

	erased, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", "departing", "requested")
	if err != nil || !erased.Clean() || erased.Deleted["entity_embedding"] != 2 {
		t.Fatalf("erasure %+v %v", erased, err)
	}
	identity, err := pg.NewRecordStore(pool, schema).InspectEntity(ctx, "p1", enseraID)
	if err != nil || identity.Name != "Ensera" || len(identity.Aliases) != 0 {
		t.Fatalf("shared entity %+v %v", identity, err)
	}
	if _, err := store.FindCandidates(ctx, "p1", model, []float32{1, 0, 0}, pg.EntityCandidateOptions{Limit: 1}); !errors.Is(err, pg.ErrEmbeddingConflict) {
		t.Fatalf("stale active generation remained readable: %v", err)
	}
	if err := store.Repair(ctx, "p1", generation.ID, actor); err != nil {
		t.Fatal(err)
	}
	buildEntityGeneration(t, store, generation, actor, model)
	if err := store.Activate(ctx, "p1", generation.ID, actor); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT source_count FROM {schema}.entity_embedding
 WHERE generation_id=$1::uuid AND entity_id=$2::uuid`), generation.ID, enseraID).Scan(&sourceCount); err != nil || sourceCount != 1 {
		t.Fatalf("surviving provenance %d %v", sourceCount, err)
	}
	exported, err := pg.NewExporter(pool, schema).Export(ctx, schema, "p1", "base")
	if err != nil || len(exported.Sections["entity_embedding"]) == 0 {
		t.Fatalf("entity vectors absent from export %+v %v", exported.Rows(), err)
	}
}

func TestEntityEmbeddingGenerationSeparatesModelSpaceAndRefusesChangedInput(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "entity_embedding_refusal")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	if _, err := storeEntityName(ctx, pool, schema, "base", "Ensera"); err != nil {
		t.Fatal(err)
	}
	store := pg.NewEntityEmbeddingStore(pool, schema)
	actor, key := uuid.NewString(), uuid.NewString()
	model := pg.EmbeddingModel{Name: "same-model", Revision: "revision-1", EndpointHash: strings.Repeat("b", 64), Dimensions: 3}
	generation, err := store.Start(ctx, "p1", key, actor, model)
	if err != nil {
		t.Fatal(err)
	}
	again, err := store.Start(ctx, "p1", key, actor, model)
	if err != nil || again.ID != generation.ID {
		t.Fatalf("idempotent start %+v %v", again, err)
	}
	changed := model
	changed.Revision = "revision-2"
	if _, err := store.Start(ctx, "p1", key, actor, changed); !errors.Is(err, pg.ErrEmbeddingConflict) {
		t.Fatalf("changed operation contract: %v", err)
	}
	var entityIdentity, messageIdentity string
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT identity_hash FROM {schema}.entity_embedding_generation WHERE generation_id=$1::uuid`), generation.ID).Scan(&entityIdentity); err != nil {
		t.Fatal(err)
	}
	message, err := pg.NewMessageEmbeddingStore(pool, schema).Start(ctx, "p1", uuid.NewString(), actor, model)
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT identity_hash FROM {schema}.embedding_generation WHERE generation_id=$1::uuid`), message.ID).Scan(&messageIdentity); err != nil {
		t.Fatal(err)
	}
	if entityIdentity == messageIdentity {
		t.Fatal("entity and message inputs occupied the same model identity")
	}
	if _, err := store.BuildPage(ctx, "p1", generation.ID, actor, model, 64, func(ctx context.Context, texts []string) ([][]float32, error) {
		if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.entity SET canonical_name='Changed' WHERE scope='p1' AND normalized_name='ensera'`)); err != nil {
			return nil, err
		}
		return entityVectors(ctx, texts)
	}); !errors.Is(err, pg.ErrEmbeddingConflict) {
		t.Fatalf("changed provider input was published: %v", err)
	}
	status, err := store.Generation(ctx, "p1", generation.ID)
	if err != nil || status.Examined != 0 || status.Stored != 0 || status.State != "building" {
		t.Fatalf("failed page advanced %+v %v", status, err)
	}
	if _, err := store.BuildPage(ctx, "p1", generation.ID, actor, model, 64,
		func(context.Context, []string) ([][]float32, error) { return [][]float32{{1, 0}}, nil }); !errors.Is(err, pg.ErrInvalidEmbedding) {
		t.Fatalf("invalid provider output accepted: %v", err)
	}
}

func TestEntityEmbeddingRetentionInvalidatesAggregateAndLifecycleRemainsBounded(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "entity_embedding_retention")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	if _, err := storeEntityName(ctx, pool, schema, "base", "Ensera"); err != nil {
		t.Fatal(err)
	}
	expired, err := storeEntityName(ctx, pool, schema, "expired", "ENSERA")
	if err != nil {
		t.Fatal(err)
	}
	store := pg.NewEntityEmbeddingStore(pool, schema)
	actor := uuid.NewString()
	model := pg.EmbeddingModel{Name: "candidate-model", Revision: "v1", EndpointHash: strings.Repeat("c", 64), Dimensions: 3}
	first, err := store.Start(ctx, "p1", uuid.NewString(), actor, model)
	if err != nil {
		t.Fatal(err)
	}
	buildEntityGeneration(t, store, first, actor, model)
	if err := store.Activate(ctx, "p1", first.ID, actor); err != nil {
		t.Fatal(err)
	}
	if err := store.Cancel(ctx, "p1", first.ID, actor); !errors.Is(err, pg.ErrEmbeddingConflict) {
		t.Fatalf("active generation cancelled: %v", err)
	}
	if _, err := store.Prune(ctx, "p1", first.ID, actor, 1); !errors.Is(err, pg.ErrEmbeddingConflict) {
		t.Fatalf("active generation pruned: %v", err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.observation SET retention_until=clock_timestamp()-interval '1 second'
 WHERE observation_id=$1::uuid`), expired); err != nil {
		t.Fatal(err)
	}
	sweep, err := pg.NewRetentionStore(pool, schema).Sweep(ctx, "p1", 100)
	if err != nil || sweep.Observations != 1 || sweep.Deleted["entity_embedding"] != 2 {
		t.Fatalf("retention %+v %v", sweep, err)
	}
	status, err := store.Generation(ctx, "p1", first.ID)
	if err != nil || status.State != "building" {
		t.Fatalf("invalidated generation %+v %v", status, err)
	}
	if err := store.Repair(ctx, "p1", first.ID, actor); err != nil {
		t.Fatal(err)
	}
	buildEntityGeneration(t, store, first, actor, model)
	if err := store.Activate(ctx, "p1", first.ID, actor); err != nil {
		t.Fatal(err)
	}
	second, err := store.Start(ctx, "p1", uuid.NewString(), actor, model)
	if err != nil {
		t.Fatal(err)
	}
	buildEntityGeneration(t, store, second, actor, model)
	if err := store.Cancel(ctx, "p1", second.ID, actor); err != nil {
		t.Fatal(err)
	}
	for {
		removed, err := store.Prune(ctx, "p1", second.ID, actor, 1)
		if err != nil {
			t.Fatal(err)
		}
		status, err := store.Generation(ctx, "p1", second.ID)
		if err != nil {
			t.Fatal(err)
		}
		if status.State == "discarded" {
			break
		}
		if removed == 0 {
			t.Fatal("prune made no progress")
		}
	}
	if _, err := store.Start(ctx, "p1", uuid.NewString(), actor, model); err != nil {
		t.Fatalf("discarded generation still consumed capacity: %v", err)
	}
}

func TestEntityEmbeddingGenerationBecomesStaleWhenNamedEntityIsCreated(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "entity_embedding_finite_snapshot")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	if _, err := storeEntityName(ctx, pool, schema, "base", "Ensera"); err != nil {
		t.Fatal(err)
	}
	store := pg.NewEntityEmbeddingStore(pool, schema)
	actor := uuid.NewString()
	model := pg.EmbeddingModel{Name: "candidate-model", Revision: "v1", EndpointHash: strings.Repeat("d", 64), Dimensions: 3}
	first, err := store.Start(ctx, "p1", uuid.NewString(), actor, model)
	if err != nil {
		t.Fatal(err)
	}
	buildEntityGeneration(t, store, first, actor, model)
	if err := store.Activate(ctx, "p1", first.ID, actor); err != nil {
		t.Fatal(err)
	}
	if _, err := storeEntityName(ctx, pool, schema, "new-source", "Taisce"); err != nil {
		t.Fatal(err)
	}
	status, err := store.Generation(ctx, "p1", first.ID)
	if err != nil || status.State != "stale" {
		t.Fatalf("finite generation did not become stale: %+v %v", status, err)
	}
	if _, err := store.FindCandidates(ctx, "p1", model, []float32{1, 0, 0}, pg.EntityCandidateOptions{Limit: 1}); !errors.Is(err, pg.ErrEmbeddingConflict) {
		t.Fatalf("stale generation remained searchable: %v", err)
	}
	if err := store.Repair(ctx, "p1", first.ID, actor); !errors.Is(err, pg.ErrEmbeddingConflict) {
		t.Fatalf("repair accepted an incomplete target snapshot: %v", err)
	}
	second, err := store.Start(ctx, "p1", uuid.NewString(), actor, model)
	if err != nil || second.TargetCount != 3 {
		t.Fatalf("replacement generation %+v %v", second, err)
	}
	buildEntityGeneration(t, store, second, actor, model)
	if err := store.Activate(ctx, "p1", second.ID, actor); err != nil {
		t.Fatal(err)
	}
	found, err := store.FindCandidates(ctx, "p1", model, []float32{0, 1, 0}, pg.EntityCandidateOptions{Limit: 2})
	if err != nil || len(found.Candidates) != 2 {
		t.Fatalf("replacement candidates %+v %v", found, err)
	}
}

func TestEntityEmbeddingLifecycleRefusesInvalidAndIncompatibleOperations(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "entity_embedding_refusals")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	if _, err := storeEntityName(ctx, pool, schema, "base", "Ensera"); err != nil {
		t.Fatal(err)
	}
	store := pg.NewEntityEmbeddingStore(pool, schema)
	actor := uuid.NewString()
	model := pg.EmbeddingModel{Name: "candidate-model", Revision: "v1", EndpointHash: strings.Repeat("e", 64), Dimensions: 3}
	for name, err := range map[string]error{
		"generation id": func() error { _, err := store.Generation(ctx, "p1", "bad"); return err }(),
		"active scope":  func() error { _, err := store.Active(ctx, "bad scope"); return err }(),
		"start actor":   func() error { _, err := store.Start(ctx, "p1", uuid.NewString(), "bad", model); return err }(),
		"missing project": func() error {
			_, err := store.Start(ctx, "missing", uuid.NewString(), actor, model)
			return err
		}(),
	} {
		if err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	invalidModel := model
	invalidModel.Dimensions = 0
	if _, err := store.Start(ctx, "p1", uuid.NewString(), actor, invalidModel); !errors.Is(err, pg.ErrInvalidEmbedding) {
		t.Fatalf("invalid model: %v", err)
	}
	generation, err := store.Start(ctx, "p1", uuid.NewString(), actor, model)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Activate(ctx, "p1", generation.ID, actor); !errors.Is(err, pg.ErrEmbeddingIncomplete) {
		t.Fatalf("incomplete generation activated: %v", err)
	}
	if _, err := store.BuildPage(ctx, "p1", generation.ID, actor, model, 0, entityVectors); !errors.Is(err, pg.ErrInvalidEmbedding) {
		t.Fatalf("invalid page accepted: %v", err)
	}
	wrong := model
	wrong.Revision = "v2"
	if _, err := store.BuildPage(ctx, "p1", generation.ID, actor, wrong, 1, entityVectors); !errors.Is(err, pg.ErrEmbeddingConflict) {
		t.Fatalf("wrong build model accepted: %v", err)
	}
	buildEntityGeneration(t, store, generation, actor, model)
	page, err := store.BuildPage(ctx, "p1", generation.ID, actor, model, 1, entityVectors)
	if err != nil || !page.Complete {
		t.Fatalf("ready generation did not return complete: %+v %v", page, err)
	}
	if err := store.Activate(ctx, "p1", generation.ID, actor); err != nil {
		t.Fatal(err)
	}
	if err := store.Activate(ctx, "p1", generation.ID, actor); err != nil {
		t.Fatalf("repeated activation: %v", err)
	}
	if _, err := store.Search(ctx, "p1", "bad", model, []float32{1, 0, 0}, pg.EntityCandidateOptions{Limit: 1}); !errors.Is(err, pg.ErrInvalidEmbedding) {
		t.Fatalf("invalid audit actor accepted: %v", err)
	}
	if _, err := store.FindCandidates(ctx, "p1", wrong, []float32{1, 0, 0}, pg.EntityCandidateOptions{Limit: 1}); !errors.Is(err, pg.ErrEmbeddingConflict) {
		t.Fatalf("wrong search model accepted: %v", err)
	}
	if _, err := store.FindCandidates(ctx, "p1", model, []float32{1, 0, 0}, pg.EntityCandidateOptions{Limit: 2, Candidates: 1}); !errors.Is(err, pg.ErrInvalidEmbedding) {
		t.Fatalf("invalid ANN bound accepted: %v", err)
	}
	second, err := store.Start(ctx, "p1", uuid.NewString(), actor, model)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Start(ctx, "p1", uuid.NewString(), actor, model); !errors.Is(err, pg.ErrEmbeddingCapacity) {
		t.Fatalf("retained generation capacity was not enforced: %v", err)
	}
	if err := store.Cancel(ctx, "p1", second.ID, actor); err != nil {
		t.Fatal(err)
	}
	if err := store.Cancel(ctx, "p1", second.ID, actor); err != nil {
		t.Fatalf("repeated cancellation: %v", err)
	}
	if _, err := store.BuildPage(ctx, "p1", second.ID, actor, model, 1, entityVectors); !errors.Is(err, pg.ErrEmbeddingConflict) {
		t.Fatalf("cancelled generation built: %v", err)
	}
	if err := store.Repair(ctx, "p1", second.ID, actor); err != nil {
		t.Fatalf("cancelled generation repair: %v", err)
	}
	if _, err := store.Prune(ctx, "p1", second.ID, actor, 1); !errors.Is(err, pg.ErrEmbeddingConflict) {
		t.Fatalf("building generation pruned: %v", err)
	}
}

func TestEntityEmbeddingPublicationRefusesLostSourcesAndIncompatibleStoredInput(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "entity_embedding_publication_refusal")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	source, err := storeEntityName(ctx, pool, schema, "base", "Ensera")
	if err != nil {
		t.Fatal(err)
	}
	store := pg.NewEntityEmbeddingStore(pool, schema)
	actor := uuid.NewString()
	model := pg.EmbeddingModel{Name: "candidate-model", Revision: "v1", EndpointHash: strings.Repeat("f", 64), Dimensions: 3}
	generation, err := store.Start(ctx, "p1", uuid.NewString(), actor, model)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.BuildPage(ctx, "p1", generation.ID, actor, model, 64,
		func(ctx context.Context, texts []string) ([][]float32, error) {
			if _, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", "base", "during inference"); err != nil {
				return nil, err
			}
			return entityVectors(ctx, texts)
		}); err != nil {
		t.Fatalf("source loss should discard calculated vectors safely: %v", err)
	}
	var stored int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.entity_embedding WHERE generation_id=$1::uuid`), generation.ID).Scan(&stored); err != nil || stored != 0 {
		t.Fatalf("lost-source vectors stored=%d err=%v source=%s", stored, err, source)
	}

	if _, err := storeEntityName(ctx, pool, schema, "replacement", "Taisce"); err != nil {
		t.Fatal(err)
	}
	replacement, err := store.Start(ctx, "p1", uuid.NewString(), actor, model)
	if err != nil {
		t.Fatal(err)
	}
	buildEntityGeneration(t, store, replacement, actor, model)
	if err := store.Repair(ctx, "p1", replacement.ID, actor); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.entity_embedding SET input_digest=decode(repeat('00',32),'hex')
 WHERE generation_id=$1::uuid`), replacement.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.BuildPage(ctx, "p1", replacement.ID, actor, model, 64, entityVectors); !errors.Is(err, pg.ErrEmbeddingConflict) {
		t.Fatalf("incompatible stored input accepted: %v", err)
	}
}
