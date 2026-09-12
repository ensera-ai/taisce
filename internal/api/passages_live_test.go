//go:build inference

// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"github.com/ensera-ai/taisce/internal/api"
	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/inference"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/ensera-ai/taisce/internal/passage"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Real provider vectors traverse the authenticated HTTP route, including query embedding. This
// proves compatibility with the configured model's dimensions, not retrieval quality or capacity.
func TestConfiguredPassageProviderServesAuthenticatedEvidence(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	config, err := inference.EmbeddingConfigFromEnv()
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
	model := pg.EmbeddingModel{Name: config.EmbeddingModel, Revision: "http-fixture-v1", EndpointHash: hex.EncodeToString(digest[:]), Dimensions: len(vectors[0])}
	h := newHarness(t, "api_passage_live")
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": "subject-1", "messages": []map[string]any{{"role": "user", "content": content}}}, 201, nil)
	store := pg.NewMessageEmbeddingStore(h.pool, h.schema)
	actor := uuid.NewString()
	generation, err := store.Start(ctx, "p1", uuid.NewString(), actor, model)
	if err != nil {
		t.Fatal(err)
	}
	page, err := store.BuildPage(ctx, "p1", generation.ID, actor, model, 64, embedder.Embed)
	if err != nil || !page.Complete {
		t.Fatalf("build: %+v %v", page, err)
	}
	if err := store.Activate(ctx, "p1", generation.ID, actor); err != nil {
		t.Fatal(err)
	}
	retriever, err := passage.New(store, embedder, passage.Binding{Name: model.Name, Revision: model.Revision, EndpointHash: model.EndpointHash})
	if err != nil {
		t.Fatal(err)
	}
	h.server.Close()
	h.server = httptest.NewServer(api.NewServer(credential.NewStore(h.pool, string(migrate.ControlSchema)), api.Stores{Notifications: h.notifyStore, Notifier: h.notifier, Audit: pg.NewAuditStore(h.pool, h.schema), Projects: pg.NewProjectStore(h.pool, h.schema), Passages: retriever}, h.schema, nil).Handler())
	t.Cleanup(h.server.Close)
	for _, candidates := range []int{0, 32} {
		var result passage.Page
		h.do(t, http.MethodPost, "/v1/passages/search", map[string]any{"question": content, "limit": 1, "candidates": candidates}, 200, &result)
		if result.GenerationID != generation.ID || len(result.Passages) != 1 || result.Passages[0].Preview != content || result.Passages[0].Similarity < 0.999 {
			t.Fatalf("HTTP evidence: %+v", result)
		}
	}
	const laterText = "Session messages arriving after activation must enter the active index incrementally."
	later, err := pg.NewObservationStore(h.pool).Append(ctx, h.schema, domain.Turn{Scope: "p1", DataSubjectID: "subject-1", OccurredAt: time.Now(), Messages: []domain.Message{{Role: domain.RoleUser, Content: laterText}}})
	if err != nil {
		t.Fatal(err)
	}
	if page, err := store.CatchUpPage(ctx, "p1", generation.ID, actor, model, 64, embedder.Embed); err != nil || !page.Complete || page.Stored != 1 {
		t.Fatalf("actual provider catch-up %+v %v", page, err)
	}
	var result passage.Page
	h.do(t, http.MethodPost, "/v1/passages/search", map[string]any{"question": laterText, "limit": 1}, 200, &result)
	if len(result.Passages) != 1 || result.Passages[0].SourceID != later.ID || result.CoveredThroughOffset != later.LogOffset || result.GenerationID != generation.ID {
		t.Fatalf("actual provider incremental evidence %+v", result)
	}
	t.Logf("authenticated exact/ANN and incremental source retrieval with %d-dimensional actual provider vectors", model.Dimensions)
}
