// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"errors"
	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
	"strings"
	"testing"
)

func catchupVectors(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{1, 0, 0}
	}
	return out, nil
}

func TestCatchUpEmbeddingsExtendsFiniteCoverageWithoutRepeatingOldInputs(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "embedding_catchup_pages")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	observations := pg.NewObservationStore(pool)
	store := pg.NewMessageEmbeddingStore(pool, schema)
	model := pg.EmbeddingModel{Name: "test", Revision: "v1", EndpointHash: strings.Repeat("a", 64), Dimensions: 3}
	actor := uuid.NewString()
	first, err := observations.Append(ctx, schema, turnOf(domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "already embedded"}))
	if err != nil {
		t.Fatal(err)
	}
	generation, err := store.Start(ctx, "p1", uuid.NewString(), actor, model)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.BuildPage(ctx, "p1", generation.ID, actor, model, 64, catchupVectors); err != nil {
		t.Fatal(err)
	}
	if err := store.Activate(ctx, "p1", generation.ID, actor); err != nil {
		t.Fatal(err)
	}
	second, err := observations.Append(ctx, schema, turnOf(domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "new one"}, domain.Message{Ordinal: 1, Role: domain.RoleAssistant, Content: "new two"}))
	if err != nil {
		t.Fatal(err)
	}
	received := []string{}
	embed := func(ctx context.Context, texts []string) ([][]float32, error) {
		received = append(received, texts...)
		return catchupVectors(ctx, texts)
	}
	page, err := store.CatchUpPage(ctx, "p1", generation.ID, actor, model, 1, embed)
	if err != nil || page.Complete || page.Stored != 1 {
		t.Fatalf("first page %+v %v", page, err)
	}
	partial, err := store.Active(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if partial.CoveredThroughOffset != first.LogOffset || partial.ThroughOffset != second.LogOffset || partial.InitialThroughOffset != first.LogOffset {
		t.Fatalf("partial coverage %+v", partial)
	}
	third, err := observations.Append(ctx, schema, turnOf(domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "later window"}))
	if err != nil {
		t.Fatal(err)
	}
	page, err = store.CatchUpPage(ctx, "p1", generation.ID, actor, model, 1, embed)
	if err != nil || !page.Complete || page.Stored != 1 {
		t.Fatalf("captured boundary %+v %v", page, err)
	}
	covered, _ := store.Active(ctx, "p1")
	if covered.CoveredThroughOffset != second.LogOffset {
		t.Fatalf("overstated coverage %+v", covered)
	}
	page, err = store.CatchUpPage(ctx, "p1", generation.ID, actor, model, 1, embed)
	if err != nil || !page.Complete || page.Stored != 1 {
		t.Fatalf("next window %+v %v", page, err)
	}
	covered, _ = store.Active(ctx, "p1")
	if covered.CoveredThroughOffset != third.LogOffset || covered.ID != generation.ID {
		t.Fatalf("coverage %+v", covered)
	}
	if strings.Join(received, ",") != "new one,new two,later window" {
		t.Fatalf("repeated or skipped source: %q", received)
	}
	page, err = store.CatchUpPage(ctx, "p1", generation.ID, actor, model, 1, func(context.Context, []string) ([][]float32, error) {
		t.Fatal("idle poll called provider")
		return nil, nil
	})
	if err != nil || !page.Complete || page.Stored != 0 {
		t.Fatalf("idle page %+v %v", page, err)
	}
	if err := store.Repair(ctx, "p1", generation.ID, actor); err != nil {
		t.Fatal(err)
	}
	repairing, _ := store.Active(ctx, "p1")
	if repairing.CoveredThroughOffset != -1 || repairing.ThroughOffset != third.LogOffset {
		t.Fatalf("repair claimed coverage %+v", repairing)
	}
	if page, err := store.CatchUpPage(ctx, "p1", generation.ID, actor, model, 64, catchupVectors); err != nil || !page.Complete || page.Stored != 0 {
		t.Fatalf("repair existing range %+v %v", page, err)
	}
}

func TestCatchUpEmbeddingsFencesProviderFailuresErasureAndActivation(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "embedding_catchup_fencing")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	store := pg.NewMessageEmbeddingStore(pool, schema)
	observations := pg.NewObservationStore(pool)
	actor := uuid.NewString()
	model := pg.EmbeddingModel{Name: "test", Revision: "v1", EndpointHash: strings.Repeat("b", 64), Dimensions: 3}
	generation, err := store.Start(ctx, "p1", uuid.NewString(), actor, model)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.BuildPage(ctx, "p1", generation.ID, actor, model, 64, catchupVectors); err != nil {
		t.Fatal(err)
	}
	if err := store.Activate(ctx, "p1", generation.ID, actor); err != nil {
		t.Fatal(err)
	}
	if _, err := observations.Append(ctx, schema, turnOf(domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "new"})); err != nil {
		t.Fatal(err)
	}
	providerErr := errors.New("provider offline")
	if _, err := store.CatchUpPage(ctx, "p1", generation.ID, actor, model, 64, func(context.Context, []string) ([][]float32, error) { return nil, providerErr }); !errors.Is(err, providerErr) {
		t.Fatal(err)
	}
	failed, _ := store.Active(ctx, "p1")
	if failed.CoveredThroughOffset != -1 || failed.After.Offset != -1 {
		t.Fatalf("failure advanced %+v", failed)
	}
	page, err := store.CatchUpPage(ctx, "p1", generation.ID, actor, model, 64, func(ctx context.Context, texts []string) ([][]float32, error) {
		if _, err := store.CatchUpPage(ctx, "p1", generation.ID, actor, model, 64, catchupVectors); !errors.Is(err, pg.ErrEmbeddingBusy) {
			t.Fatalf("concurrent worker %v", err)
		}
		if _, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", "subject-1", "concurrent catch-up erasure"); err != nil {
			return nil, err
		}
		return catchupVectors(ctx, texts)
	})
	if err != nil || !page.Complete || page.Stored != 0 {
		t.Fatalf("erasure resurrected %+v %v", page, err)
	}
	if _, err := observations.Append(ctx, schema, turnOf(domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "after erasure"})); err != nil {
		t.Fatal(err)
	}
	candidate, err := store.Start(ctx, "p1", uuid.NewString(), actor, model)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.BuildPage(ctx, "p1", candidate.ID, actor, model, 64, catchupVectors); err != nil {
		t.Fatal(err)
	}
	_, err = store.CatchUpPage(ctx, "p1", generation.ID, actor, model, 64, func(ctx context.Context, texts []string) ([][]float32, error) {
		if err := store.Activate(ctx, "p1", candidate.ID, actor); err != nil {
			return nil, err
		}
		return catchupVectors(ctx, texts)
	})
	if !errors.Is(err, pg.ErrEmbeddingConflict) {
		t.Fatalf("old active worker published %v", err)
	}
	old, _ := store.Generation(ctx, "p1", generation.ID)
	if old.State != "building" || old.CoveredThroughOffset == old.ThroughOffset {
		t.Fatalf("cutover advanced old generation %+v", old)
	}
	if _, err := store.CatchUpPage(ctx, "p1", generation.ID, actor, model, 64, catchupVectors); !errors.Is(err, pg.ErrEmbeddingConflict) {
		t.Fatal("inactive catch-up accepted")
	}
}

func TestCatchUpEmbeddingsRefusesInvalidAndUnverifiedCoverage(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "embedding_catchup_refusals")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	store := pg.NewMessageEmbeddingStore(pool, schema)
	actor := uuid.NewString()
	model := pg.EmbeddingModel{Name: "test", Revision: "v1", EndpointHash: strings.Repeat("c", 64), Dimensions: 3}
	generation, err := store.Start(ctx, "p1", uuid.NewString(), actor, model)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.BuildPage(ctx, "p1", generation.ID, actor, model, 64, catchupVectors); err != nil {
		t.Fatal(err)
	}
	if err := store.Activate(ctx, "p1", generation.ID, actor); err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int{0, 65} {
		if _, err := store.CatchUpPage(ctx, "p1", generation.ID, actor, model, limit, catchupVectors); !errors.Is(err, pg.ErrInvalidEmbedding) {
			t.Fatal("invalid page accepted")
		}
	}
	if _, err := store.CatchUpPage(ctx, "p1", generation.ID, actor, model, 1, nil); !errors.Is(err, pg.ErrInvalidEmbedding) {
		t.Fatal("nil provider accepted")
	}
	if _, err := store.CatchUpPage(ctx, "p1", "bad", actor, model, 1, catchupVectors); !errors.Is(err, pg.ErrInvalidEmbedding) {
		t.Fatal("invalid id accepted")
	}
	invalid := model
	invalid.Dimensions = 0
	if _, err := store.CatchUpPage(ctx, "p1", generation.ID, actor, invalid, 1, catchupVectors); !errors.Is(err, pg.ErrInvalidEmbedding) {
		t.Fatal("invalid model accepted")
	}
	invalid = model
	invalid.Revision = "v2"
	if _, err := store.CatchUpPage(ctx, "p1", generation.ID, actor, invalid, 1, catchupVectors); !errors.Is(err, pg.ErrEmbeddingConflict) {
		t.Fatal("changed model accepted")
	}
	if _, err := store.CatchUpPage(ctx, "p2", generation.ID, actor, model, 1, catchupVectors); !errors.Is(err, pg.ErrEmbeddingNotFound) {
		t.Fatal("foreign generation exposed")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := store.CatchUpPage(cancelled, "p1", generation.ID, actor, model, 1, catchupVectors); err == nil {
		t.Fatal("cancelled work accepted")
	}
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.embedding_build SET through_offset=0 WHERE generation_id=$1::uuid`), generation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CatchUpPage(ctx, "p1", generation.ID, actor, model, 1, catchupVectors); !errors.Is(err, pg.ErrEmbeddingIncomplete) {
		t.Fatal("unverified ready range skipped")
	}
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.project SET suspended_at=clock_timestamp() WHERE scope='p1'`)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CatchUpPage(ctx, "p1", generation.ID, actor, model, 1, catchupVectors); !errors.Is(err, pg.ErrNoSuchProject) {
		t.Fatal("suspended project followed")
	}
	if _, err := pool.Exec(ctx, schema.SQL(`ALTER TABLE {schema}.embedding_build RENAME TO unavailable_build`)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CatchUpPage(ctx, "p1", generation.ID, actor, model, 1, catchupVectors); err == nil {
		t.Fatal("missing progress store hidden")
	}
}

func TestCatchUpEmbeddingsAdvancesPastExpiredAndNonMessageSources(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "embedding_catchup_expiry")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	store := pg.NewMessageEmbeddingStore(pool, schema)
	actor := uuid.NewString()
	model := pg.EmbeddingModel{Name: "test", Revision: "v1", EndpointHash: strings.Repeat("d", 64), Dimensions: 3}
	generation, err := store.Start(ctx, "p1", uuid.NewString(), actor, model)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.BuildPage(ctx, "p1", generation.ID, actor, model, 64, catchupVectors); err != nil {
		t.Fatal(err)
	}
	if err := store.Activate(ctx, "p1", generation.ID, actor); err != nil {
		t.Fatal(err)
	}
	source, err := pg.NewObservationStore(pool).Append(ctx, schema, turnOf(domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "expires during inference"}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pg.NewArtifactStore(pool, schema).Put(ctx, "p1", actor, pg.ArtifactPut{ID: uuid.NewString(), DataSubjectID: "artifact-owner", Kind: "state", Content: []byte("opaque state")}); err != nil {
		t.Fatal(err)
	}
	page, err := store.CatchUpPage(ctx, "p1", generation.ID, actor, model, 64, func(ctx context.Context, texts []string) ([][]float32, error) {
		if len(texts) != 1 || texts[0] != "expires during inference" {
			t.Fatalf("non-message source embedded %q", texts)
		}
		if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.observation SET retention_until=clock_timestamp()-interval '1 second' WHERE observation_id=$1::uuid`), source.ID); err != nil {
			return nil, err
		}
		if sweep, err := pg.NewRetentionStore(pool, schema).Sweep(ctx, "p1", 100); err != nil || sweep.Observations != 1 {
			t.Fatalf("expiry %+v %v", sweep, err)
		}
		return catchupVectors(ctx, texts)
	})
	if err != nil || !page.Complete || page.Stored != 0 {
		t.Fatalf("expiry publication %+v %v", page, err)
	}
	progress, _ := store.Active(ctx, "p1")
	if progress.CoveredThroughOffset <= source.LogOffset {
		t.Fatalf("non-message range not covered %+v", progress)
	}
	if _, err := store.CatchUpPage(ctx, "p1", generation.ID, actor, model, 64, func(context.Context, []string) ([][]float32, error) {
		t.Fatal("completed gap repeated inference")
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
}
