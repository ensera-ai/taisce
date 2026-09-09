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
	"github.com/jackc/pgx/v5/pgxpool"
)

func reportVectors(_ context.Context, texts []string) ([][]float32, error) {
	vectors := make([][]float32, len(texts))
	for i, text := range texts {
		if strings.HasPrefix(text, "Growth") {
			vectors[i] = []float32{1, 0, 0}
		} else {
			vectors[i] = []float32{0, 1, 0}
		}
	}
	return vectors, nil
}

func storedReport(t *testing.T, pool *pgxpool.Pool, schema pg.Schema, title, summary string, owners ...string) string {
	t.Helper()
	ctx := context.Background()
	var sources []string
	for _, owner := range owners {
		turn := turnOf(domain.Message{Role: domain.RoleUser, Content: title})
		turn.DataSubjectID = owner
		stored, err := pg.NewObservationStore(pool).Append(ctx, schema, turn)
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, stored.ID)
	}
	communityID, reportID := uuid.NewString(), uuid.NewString()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, schema.SQL(`INSERT INTO {schema}.community(scope,community_id,level) VALUES('p1',$1,0)`), communityID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, schema.SQL(`INSERT INTO {schema}.community_report
 (scope,report_id,community_id,title,summary,importance,importance_reason,findings)
 VALUES('p1',$1,$2,$3,$4,8,'Material operational effect','[{"summary":"Hiring","explanation":"The team is expanding."}]')`),
		reportID, communityID, title, summary); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, schema.SQL(`INSERT INTO {schema}.projection_dependency
 (source_observation_id,scope,projection_kind,projection_id,data_subject_id,report_source_revision)
 SELECT observation_id,scope,'community_report',$2,data_subject_id,fact_revision
 FROM {schema}.observation WHERE scope='p1' AND observation_id=ANY($1::uuid[])`), sources, reportID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return reportID
}

func buildReportGeneration(t *testing.T, store *pg.ReportEmbeddingStore, generation pg.ReportEmbeddingGeneration,
	actor string, model pg.EmbeddingModel) {
	t.Helper()
	for {
		page, err := store.BuildPage(context.Background(), generation.Project, generation.ID, actor, model, 1, reportVectors)
		if err != nil {
			t.Fatal(err)
		}
		if page.Complete {
			return
		}
	}
}

func TestReportEmbeddingRetrievalHasSourceOwnedLifecycle(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "report_embedding_lifecycle")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	reportID := storedReport(t, pool, schema, "Growth plan", "Hiring, facilities and demand form one expansion theme.", "base", "departing")
	store := pg.NewReportEmbeddingStore(pool, schema)
	actor := uuid.NewString()
	model := pg.EmbeddingModel{Name: "theme-model", Revision: "revision-1", EndpointHash: strings.Repeat("a", 64), Dimensions: 3}
	generation, err := store.Start(ctx, "p1", uuid.NewString(), actor, model)
	if err != nil || generation.TargetCount != 1 {
		t.Fatalf("start %+v %v", generation, err)
	}
	buildReportGeneration(t, store, generation, actor, model)
	if err := store.Activate(ctx, "p1", generation.ID, actor); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`DELETE FROM {schema}.report_embedding WHERE generation_id=$1::uuid AND report_id=$2::uuid`), generation.ID, reportID); err != nil {
		t.Fatal(err)
	}
	if err := store.Activate(ctx, "p1", generation.ID, actor); !errors.Is(err, pg.ErrEmbeddingIncomplete) {
		t.Fatalf("incomplete generation activated: %v", err)
	}
	if err := store.Repair(ctx, "p1", generation.ID, actor); err != nil {
		t.Fatal(err)
	}
	buildReportGeneration(t, store, generation, actor, model)
	if err := store.Activate(ctx, "p1", generation.ID, actor); err != nil {
		t.Fatal(err)
	}
	for _, candidateCount := range []int{0, 16} {
		found, err := store.FindCandidates(ctx, "p1", model, []float32{1, 0, 0}, pg.ReportCandidateOptions{Limit: 1, Candidates: candidateCount})
		if err != nil || len(found.Candidates) != 1 || found.Candidates[0].ReportID != reportID ||
			found.Candidates[0].TitlePreview != "Growth plan" || !strings.Contains(found.Candidates[0].SummaryPreview, "expansion") ||
			found.Candidates[0].Similarity != 1 || found.Approximate != (candidateCount > 0) {
			t.Fatalf("candidates %+v %v", found, err)
		}
	}
	var sourceCount, registrations int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT source_count,(SELECT count(*) FROM {schema}.projection_dependency
 WHERE projection_kind='report_embedding' AND report_embedding_generation_ref=$1::uuid AND report_embedding_report_ref=$2::uuid)
 FROM {schema}.report_embedding WHERE generation_id=$1::uuid AND report_id=$2::uuid`), generation.ID, reportID).
		Scan(&sourceCount, &registrations); err != nil || sourceCount != 2 || registrations != 2 {
		t.Fatalf("ownership sources=%d registrations=%d err=%v", sourceCount, registrations, err)
	}
	exported, err := pg.NewExporter(pool, schema).Export(ctx, schema, "p1", "departing")
	if err != nil || len(exported.Sections["report_embedding"]) != 1 {
		t.Fatalf("export omitted report vector: %v %+v", err, exported.Sections)
	}
	erased, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", "departing", "requested")
	if err != nil || !erased.Clean() {
		t.Fatalf("erasure %+v %v", erased, err)
	}
	for _, table := range []string{"community_report", "report_embedding", "report_embedding_source"} {
		var count int
		if err := pool.QueryRow(ctx, schema.SQL("SELECT count(*) FROM {schema}."+table)).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s retained %d rows: %v", table, count, err)
		}
	}
	if _, err := store.FindCandidates(ctx, "p1", model, []float32{1, 0, 0}, pg.ReportCandidateOptions{Limit: 1}); !errors.Is(err, pg.ErrEmbeddingConflict) {
		t.Fatalf("stale active generation remained readable: %v", err)
	}
}

func TestReportEmbeddingExpiresWithItsSource(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "report_embedding_retention")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	storedReport(t, pool, schema, "Growth plan", "Expansion theme.", "expiring")
	store := pg.NewReportEmbeddingStore(pool, schema)
	actor := uuid.NewString()
	model := pg.EmbeddingModel{Name: "theme-model", Revision: "revision-1", EndpointHash: strings.Repeat("c", 64), Dimensions: 3}
	generation, err := store.Start(ctx, "p1", uuid.NewString(), actor, model)
	if err != nil {
		t.Fatal(err)
	}
	buildReportGeneration(t, store, generation, actor, model)
	if err := store.Activate(ctx, "p1", generation.ID, actor); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.observation SET retention_until=clock_timestamp()-interval '1 second' WHERE data_subject_id='expiring'`)); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.NewRetentionStore(pool, schema).Sweep(ctx, "p1", 10); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"community_report", "report_embedding", "report_embedding_source"} {
		var count int
		if err := pool.QueryRow(ctx, schema.SQL("SELECT count(*) FROM {schema}."+table)).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s retained %d rows after expiry: %v", table, count, err)
		}
	}
}

func TestReportEmbeddingFiniteSnapshotAndModelCutover(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "report_embedding_snapshot")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	storedReport(t, pool, schema, "Growth plan", "Expansion theme.", "one")
	store := pg.NewReportEmbeddingStore(pool, schema)
	actor := uuid.NewString()
	firstModel := pg.EmbeddingModel{Name: "theme-model", Revision: "revision-1", EndpointHash: strings.Repeat("b", 64), Dimensions: 3}
	first, err := store.Start(ctx, "p1", uuid.NewString(), actor, firstModel)
	if err != nil {
		t.Fatal(err)
	}
	buildReportGeneration(t, store, first, actor, firstModel)
	if err := store.Activate(ctx, "p1", first.ID, actor); err != nil {
		t.Fatal(err)
	}
	storedReport(t, pool, schema, "Security posture", "Controls form a separate theme.", "two")
	if _, err := store.FindCandidates(ctx, "p1", firstModel, []float32{1, 0, 0}, pg.ReportCandidateOptions{Limit: 1}); !errors.Is(err, pg.ErrEmbeddingConflict) {
		t.Fatalf("finite generation omitted a new report: %v", err)
	}
	secondModel := firstModel
	secondModel.Revision = "revision-2"
	second, err := store.Start(ctx, "p1", uuid.NewString(), actor, secondModel)
	if err != nil || second.TargetCount != 2 {
		t.Fatalf("second generation %+v %v", second, err)
	}
	buildReportGeneration(t, store, second, actor, secondModel)
	if err := store.Activate(ctx, "p1", second.ID, actor); err != nil {
		t.Fatal(err)
	}
	if err := store.Cancel(ctx, "p1", second.ID, actor); !errors.Is(err, pg.ErrEmbeddingConflict) {
		t.Fatalf("active generation cancelled: %v", err)
	}
	active, err := store.Active(ctx, "p1")
	if err != nil || active.ID != second.ID || active.Model.Revision != "revision-2" {
		t.Fatalf("wrong active generation: %+v %v", active, err)
	}
	if _, err := store.Search(ctx, "p1", actor, firstModel, []float32{1, 0, 0}, pg.ReportCandidateOptions{Limit: 1}); !errors.Is(err, pg.ErrEmbeddingConflict) {
		t.Fatalf("old model searched new generation: %v", err)
	}
	for {
		_, err := store.Prune(ctx, "p1", first.ID, actor, 1)
		if err != nil {
			t.Fatal(err)
		}
		status, err := store.Generation(ctx, "p1", first.ID)
		if err != nil {
			t.Fatal(err)
		}
		if status.State == "discarded" {
			break
		}
	}
	if err := store.Repair(ctx, "p1", first.ID, actor); !errors.Is(err, pg.ErrEmbeddingConflict) {
		t.Fatalf("discarded generation repaired: %v", err)
	}
	third, err := store.Start(ctx, "p1", uuid.NewString(), actor, secondModel)
	if err != nil {
		t.Fatalf("discarded generation retained capacity: %v", err)
	}
	if err := store.Cancel(ctx, "p1", third.ID, actor); err != nil {
		t.Fatal(err)
	}
	if err := store.Cancel(ctx, "p1", third.ID, actor); err != nil {
		t.Fatal(err)
	}
	if removed, err := store.Prune(ctx, "p1", third.ID, actor, 1); err != nil || removed != 0 {
		t.Fatalf("empty prune removed=%d err=%v", removed, err)
	}
	if removed, err := store.Prune(ctx, "p1", third.ID, actor, 1); err != nil || removed != 0 {
		t.Fatalf("discarded prune removed=%d err=%v", removed, err)
	}
}

func TestReportEmbeddingOperationsRefuseInvalidAndConflictingRequests(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "report_embedding_refusals")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	storedReport(t, pool, schema, "Growth plan", "Expansion theme.", "one")
	store := pg.NewReportEmbeddingStore(pool, schema)
	actor, key := uuid.NewString(), uuid.NewString()
	model := pg.EmbeddingModel{Name: "theme-model", Revision: "revision-1", EndpointHash: strings.Repeat("d", 64), Dimensions: 3}
	invalidModel := model
	invalidModel.Dimensions = 0
	if _, err := store.Start(ctx, "bad scope", key, actor, model); !errors.Is(err, pg.ErrInvalidEmbedding) {
		t.Fatalf("invalid start scope: %v", err)
	}
	if _, err := store.Start(ctx, "p1", key, actor, invalidModel); !errors.Is(err, pg.ErrInvalidEmbedding) {
		t.Fatalf("invalid start model: %v", err)
	}
	if _, err := store.Active(ctx, "bad scope"); !errors.Is(err, pg.ErrInvalidEmbedding) {
		t.Fatalf("invalid active scope: %v", err)
	}
	if err := store.Activate(ctx, "bad scope", uuid.NewString(), actor); !errors.Is(err, pg.ErrInvalidEmbedding) {
		t.Fatalf("invalid activate scope: %v", err)
	}
	if err := store.Repair(ctx, "bad scope", uuid.NewString(), actor); !errors.Is(err, pg.ErrInvalidEmbedding) {
		t.Fatalf("invalid repair scope: %v", err)
	}
	if err := store.Cancel(ctx, "bad scope", uuid.NewString(), actor); !errors.Is(err, pg.ErrInvalidEmbedding) {
		t.Fatalf("invalid cancel scope: %v", err)
	}
	if _, err := store.Prune(ctx, "p1", uuid.NewString(), actor, 0); !errors.Is(err, pg.ErrInvalidEmbedding) {
		t.Fatalf("invalid prune limit: %v", err)
	}
	if _, err := store.Search(ctx, "p1", actor, model, []float32{1, 0, 0}, pg.ReportCandidateOptions{}); !errors.Is(err, pg.ErrInvalidEmbedding) {
		t.Fatalf("invalid search controls: %v", err)
	}
	if _, err := store.Search(ctx, "p1", actor, invalidModel, []float32{1, 0, 0}, pg.ReportCandidateOptions{Limit: 1}); !errors.Is(err, pg.ErrInvalidEmbedding) {
		t.Fatalf("invalid search model: %v", err)
	}
	first, err := store.Start(ctx, "p1", key, actor, model)
	if err != nil {
		t.Fatal(err)
	}
	message, err := pg.NewMessageEmbeddingStore(pool, schema).Start(ctx, "p1", uuid.NewString(), actor, model)
	if err != nil {
		t.Fatal(err)
	}
	entity, err := pg.NewEntityEmbeddingStore(pool, schema).Start(ctx, "p1", uuid.NewString(), actor, model)
	if err != nil {
		t.Fatal(err)
	}
	var reportIdentity, messageIdentity, entityIdentity string
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT
  (SELECT identity_hash FROM {schema}.report_embedding_generation WHERE generation_id=$1::uuid),
  (SELECT identity_hash FROM {schema}.embedding_generation WHERE generation_id=$2::uuid),
  (SELECT identity_hash FROM {schema}.entity_embedding_generation WHERE generation_id=$3::uuid)`),
		first.ID, message.ID, entity.ID).Scan(&reportIdentity, &messageIdentity, &entityIdentity); err != nil {
		t.Fatal(err)
	}
	if reportIdentity == messageIdentity || reportIdentity == entityIdentity || messageIdentity == entityIdentity {
		t.Fatal("the three semantic inputs occupied the same model identity")
	}
	again, err := store.Start(ctx, "p1", key, actor, model)
	if err != nil || again.ID != first.ID {
		t.Fatalf("idempotent start %+v %v", again, err)
	}
	changed := model
	changed.Revision = "revision-2"
	if _, err := store.Start(ctx, "p1", key, actor, changed); !errors.Is(err, pg.ErrEmbeddingConflict) {
		t.Fatalf("changed operation contract: %v", err)
	}
	if _, err := store.BuildPage(ctx, "p1", first.ID, actor, changed, 1, reportVectors); !errors.Is(err, pg.ErrEmbeddingConflict) {
		t.Fatalf("changed build model: %v", err)
	}
	if _, err := store.BuildPage(ctx, "p1", first.ID, actor, model, 0, reportVectors); !errors.Is(err, pg.ErrInvalidEmbedding) {
		t.Fatalf("invalid build limit: %v", err)
	}
	if _, err := store.BuildPage(ctx, "p1", first.ID, actor, invalidModel, 1, reportVectors); !errors.Is(err, pg.ErrInvalidEmbedding) {
		t.Fatalf("invalid build model: %v", err)
	}
	providerFailure := errors.New("provider failed")
	if _, err := store.BuildPage(ctx, "p1", first.ID, actor, model, 1, func(context.Context, []string) ([][]float32, error) {
		return nil, providerFailure
	}); !errors.Is(err, providerFailure) {
		t.Fatalf("provider build failure: %v", err)
	}
	if _, err := store.BuildPage(ctx, "p1", first.ID, actor, model, 1, func(context.Context, []string) ([][]float32, error) {
		return nil, nil
	}); !errors.Is(err, pg.ErrInvalidEmbedding) {
		t.Fatalf("malformed provider output: %v", err)
	}
	if err := store.Activate(ctx, "p1", first.ID, actor); !errors.Is(err, pg.ErrEmbeddingIncomplete) {
		t.Fatalf("building generation activated: %v", err)
	}
	if _, err := store.Generation(ctx, "bad scope", first.ID); !errors.Is(err, pg.ErrInvalidEmbedding) {
		t.Fatalf("invalid scope: %v", err)
	}
	if _, err := store.Generation(ctx, "p1", uuid.NewString()); !errors.Is(err, pg.ErrEmbeddingNotFound) {
		t.Fatalf("missing generation: %v", err)
	}
	if _, err := store.Search(ctx, "p1", actor, model, []float32{1, 0}, pg.ReportCandidateOptions{Limit: 1}); !errors.Is(err, pg.ErrInvalidEmbedding) {
		t.Fatalf("wrong vector width: %v", err)
	}
	if err := store.Cancel(ctx, "p1", first.ID, actor); err != nil {
		t.Fatal(err)
	}
	if err := store.Repair(ctx, "p1", first.ID, actor); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Prune(ctx, "p1", first.ID, actor, 1); !errors.Is(err, pg.ErrEmbeddingConflict) {
		t.Fatalf("building generation pruned: %v", err)
	}
	buildReportGeneration(t, store, first, actor, model)
	if page, err := store.BuildPage(ctx, "p1", first.ID, actor, model, 1, reportVectors); err != nil || !page.Complete {
		t.Fatalf("ready generation retry: %+v %v", page, err)
	}
}

func TestReportEmbeddingRefusesProviderResultAfterReportChanges(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "report_embedding_changed_input")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	reportID := storedReport(t, pool, schema, "Growth plan", "Expansion theme.", "one")
	store := pg.NewReportEmbeddingStore(pool, schema)
	actor := uuid.NewString()
	model := pg.EmbeddingModel{Name: "theme-model", Revision: "revision-1", EndpointHash: strings.Repeat("e", 64), Dimensions: 3}
	generation, err := store.Start(ctx, "p1", uuid.NewString(), actor, model)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.BuildPage(ctx, "p1", generation.ID, actor, model, 1, func(_ context.Context, texts []string) ([][]float32, error) {
		if len(texts) != 1 || !strings.Contains(texts[0], "Expansion theme") {
			t.Fatalf("wrong provider input: %q", texts)
		}
		if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.community_report SET summary='Changed after provider input' WHERE report_id=$1::uuid`), reportID); err != nil {
			return nil, err
		}
		return [][]float32{{1, 0, 0}}, nil
	})
	if !errors.Is(err, pg.ErrEmbeddingConflict) {
		t.Fatalf("stale provider result committed: %v", err)
	}
	var vectors int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.report_embedding`)).Scan(&vectors); err != nil || vectors != 0 {
		t.Fatalf("stale vector count=%d err=%v", vectors, err)
	}
}
