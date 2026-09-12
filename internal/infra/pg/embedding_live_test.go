//go:build inference

// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/inference"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
)

// The actual configured provider establishes the dimension; PostgreSQL must retain every coordinate
// and find the exact source under both retrieval modes. This is a compatibility check, not a corpus
// quality or latency claim.
func TestConfiguredEmbeddingProviderPersistsAndRetrievesItsActualDimensions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	config, err := inference.ConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	embedder := inference.NewEmbedder(config)
	const content = "The database is PostgreSQL and projects are logical boundaries."
	vectors, err := embedder.Embed(ctx, []string{content})
	if err != nil {
		t.Fatal(err)
	}
	endpoint, _ := config.EmbeddingsAt()
	digest := sha256.Sum256([]byte(endpoint))
	model := pg.EmbeddingModel{Name: config.EmbeddingModel, Revision: "integration-fixture-v1", EndpointHash: hex.EncodeToString(digest[:]), Dimensions: len(vectors[0])}
	if err := model.Validate(); err != nil {
		t.Fatal(err)
	}
	pool := testPool(t)
	schema := tenant(t, pool, "embedding_live_provider")
	defer pool.Exec(context.Background(), "DROP SCHEMA "+schema.String()+" CASCADE")
	if _, err := pg.NewObservationStore(pool).Append(ctx, schema, turnOf(domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: content})); err != nil {
		t.Fatal(err)
	}
	store := pg.NewMessageEmbeddingStore(pool, schema)
	actor := uuid.NewString()
	generation, err := store.Start(ctx, "p1", uuid.NewString(), actor, model)
	if err != nil {
		t.Fatal(err)
	}
	page, err := store.BuildPage(ctx, "p1", generation.ID, actor, model, 64, embedder.Embed)
	if err != nil || !page.Complete || page.Stored != 1 {
		t.Fatalf("actual provider persistence: %+v %v", page, err)
	}
	if err := store.Activate(ctx, "p1", generation.ID, actor); err != nil {
		t.Fatal(err)
	}
	for _, candidates := range []int{0, 32} {
		result, err := store.Search(ctx, "p1", actor, model, vectors[0], pg.MessageSearchOptions{Limit: 1, Candidates: candidates})
		if err != nil || len(result.Matches) != 1 || result.Matches[0].Preview != content || result.Matches[0].Similarity < 0.999 {
			t.Fatalf("actual provider retrieval %+v %v", result, err)
		}
	}
	t.Logf("persisted and retrieved %d-dimensional provider vectors", model.Dimensions)
}

// Entity inputs occupy a distinct generation identity and still use the configured provider's real
// vector width. Candidate retrieval remains a navigation result; this test does not set merge policy.
func TestConfiguredEmbeddingProviderPersistsEntityCandidatesAtActualDimensions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	config, err := inference.ConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	embedder := inference.NewEmbedder(config)
	query, err := embedder.Embed(ctx, []string{"Ensera"})
	if err != nil || len(query) != 1 {
		t.Fatalf("query embedding count=%d err=%v", len(query), err)
	}
	endpoint, _ := config.EmbeddingsAt()
	digest := sha256.Sum256([]byte(endpoint))
	model := pg.EmbeddingModel{Name: config.EmbeddingModel, Revision: "entity-integration-fixture-v1",
		EndpointHash: hex.EncodeToString(digest[:]), Dimensions: len(query[0])}
	if err := model.Validate(); err != nil {
		t.Fatal(err)
	}
	pool := testPool(t)
	schema := tenant(t, pool, "entity_embedding_live_provider")
	defer pool.Exec(context.Background(), "DROP SCHEMA "+schema.String()+" CASCADE")
	if _, err := storeEntityName(ctx, pool, schema, "owner", "Ensera"); err != nil {
		t.Fatal(err)
	}
	store := pg.NewEntityEmbeddingStore(pool, schema)
	actor := uuid.NewString()
	generation, err := store.Start(ctx, "p1", uuid.NewString(), actor, model)
	if err != nil {
		t.Fatal(err)
	}
	page, err := store.BuildPage(ctx, "p1", generation.ID, actor, model, 64, embedder.Embed)
	if err != nil || !page.Complete || page.Stored == 0 {
		t.Fatalf("actual provider entity persistence: %+v %v", page, err)
	}
	if err := store.Activate(ctx, "p1", generation.ID, actor); err != nil {
		t.Fatal(err)
	}
	for _, candidates := range []int{0, 32} {
		result, err := store.Search(ctx, "p1", actor, model, query[0], pg.EntityCandidateOptions{Limit: 1, Candidates: candidates})
		if err != nil || len(result.Candidates) != 1 {
			t.Fatalf("actual provider entity retrieval %+v %v", result, err)
		}
	}
	t.Logf("persisted and retrieved entity candidates with %d-dimensional provider vectors", model.Dimensions)
}

// Community reports are the third model space. The configured provider must preserve its actual
// width while report provenance and retrieval remain independent from messages and entities.
func TestConfiguredEmbeddingProviderPersistsReportThemesAtActualDimensions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	config, err := inference.ConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	embedder := inference.NewEmbedder(config)
	query, err := embedder.Embed(ctx, []string{"What themes indicate organizational growth?"})
	if err != nil || len(query) != 1 {
		t.Fatalf("query embedding count=%d err=%v", len(query), err)
	}
	endpoint, _ := config.EmbeddingsAt()
	digest := sha256.Sum256([]byte(endpoint))
	model := pg.EmbeddingModel{Name: config.EmbeddingModel, Revision: "report-integration-fixture-v1",
		EndpointHash: hex.EncodeToString(digest[:]), Dimensions: len(query[0])}
	pool := testPool(t)
	schema := tenant(t, pool, "report_embedding_live_provider")
	defer pool.Exec(context.Background(), "DROP SCHEMA "+schema.String()+" CASCADE")
	storedReport(t, pool, schema, "Growth plan", "Hiring, facilities and demand form one expansion theme.", "owner")
	store := pg.NewReportEmbeddingStore(pool, schema)
	actor := uuid.NewString()
	generation, err := store.Start(ctx, "p1", uuid.NewString(), actor, model)
	if err != nil {
		t.Fatal(err)
	}
	page, err := store.BuildPage(ctx, "p1", generation.ID, actor, model, 64, embedder.Embed)
	if err != nil || !page.Complete || page.Stored != 1 {
		t.Fatalf("actual provider report persistence: %+v %v", page, err)
	}
	if err := store.Activate(ctx, "p1", generation.ID, actor); err != nil {
		t.Fatal(err)
	}
	for _, candidates := range []int{0, 32} {
		result, err := store.Search(ctx, "p1", actor, model, query[0], pg.ReportCandidateOptions{Limit: 1, Candidates: candidates})
		if err != nil || len(result.Candidates) != 1 || result.Candidates[0].TitlePreview != "Growth plan" {
			t.Fatalf("actual provider report retrieval %+v %v", result, err)
		}
	}
	t.Logf("persisted and retrieved report themes with %d-dimensional provider vectors", model.Dimensions)
}
