// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
)

func TestAllEmbeddingGenerationsAreExportedAndRemovedBySourceRetention(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "embedding_ownership_expiry")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	observations := pg.NewObservationStore(pool)
	source, err := observations.Append(ctx, schema, turnOf(domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "retained source"}))
	if err != nil {
		t.Fatal(err)
	}
	other := turnOf(domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "different subject"})
	other.DataSubjectID = "other"
	if _, err := observations.Append(ctx, schema, other); err != nil {
		t.Fatal(err)
	}
	store := pg.NewMessageEmbeddingStore(pool, schema)
	actor := uuid.NewString()
	model := pg.EmbeddingModel{Name: "test", Revision: "v1", EndpointHash: strings.Repeat("a", 64), Dimensions: 3}
	for i := 0; i < 2; i++ {
		generation, err := store.Start(ctx, "p1", uuid.NewString(), actor, model)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.BuildPage(ctx, "p1", generation.ID, actor, model, 64, func(context.Context, []string) ([][]float32, error) { return [][]float32{{1, 0, 0}, {0, 1, 0}}, nil }); err != nil {
			t.Fatal(err)
		}
		if err := store.Activate(ctx, "p1", generation.ID, actor); err != nil {
			t.Fatal(err)
		}
		model.Revision = "v2"
	}
	exported, err := pg.NewExporter(pool, schema).Export(ctx, schema, "p1", "subject-1")
	if err != nil || len(exported.Sections["message_embedding"]) != 2 {
		t.Fatalf("generation export: %+v %v", exported.Rows(), err)
	}
	// A forged registration cannot attach another subject's source to an existing vector.
	if _, err := pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.projection_dependency(source_observation_id,scope,projection_kind,projection_id,data_subject_id,pipeline_version)
 SELECT o.observation_id,'p1','message_embedding',v.embedding_key,o.data_subject_id,'v1' FROM {schema}.observation o CROSS JOIN {schema}.message_embedding v
 WHERE o.data_subject_id='other' AND v.source_observation_id=$1::uuid LIMIT 1`), source.ID); err == nil {
		t.Fatal("foreign source registered against vector")
	}
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.observation SET retention_until=clock_timestamp()-interval '1 second' WHERE observation_id=$1::uuid`), source.ID); err != nil {
		t.Fatal(err)
	}
	sweep, err := pg.NewRetentionStore(pool, schema).Sweep(ctx, "p1", 100)
	if err != nil || sweep.Observations != 1 {
		t.Fatalf("source expiry: %+v %v", sweep, err)
	}
	exported, err = pg.NewExporter(pool, schema).Export(ctx, schema, "p1", "subject-1")
	if err != nil || len(exported.Sections["message_embedding"]) != 0 {
		t.Fatalf("expired export: %+v %v", exported.Rows(), err)
	}
	erased, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", "other", "test")
	if err != nil || !erased.Clean() || erased.Deleted["message_embedding"] != 2 {
		t.Fatalf("counted generation erasure: %+v %v", erased, err)
	}
}
