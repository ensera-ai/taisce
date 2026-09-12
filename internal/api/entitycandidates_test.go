// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ensera-ai/taisce/internal/api"
	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/entitycandidate"
	"github.com/ensera-ai/taisce/internal/infra/inference"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/ensera-ai/taisce/internal/passage"
	"github.com/ensera-ai/taisce/internal/recall"
	"github.com/google/uuid"
)

type entityCandidateFixture struct {
	h         *harness
	store     *pg.EntityEmbeddingStore
	model     pg.EmbeddingModel
	retriever *entitycandidate.Retriever
	status    atomic.Int32
}

func candidateVector(text string) []float32 {
	if strings.Contains(strings.ToLower(text), "ensera") {
		return []float32{1, 0, 0}
	}
	return []float32{0, 1, 0}
}

func attachEntityCandidates(t *testing.T, h *harness) *entityCandidateFixture {
	return attachEntityCandidatesWithPassages(t, h, nil)
}

func attachEntityCandidatesWithPassages(t *testing.T, h *harness, passages *passage.Retriever) *entityCandidateFixture {
	t.Helper()
	fixture := &entityCandidateFixture{h: h, store: pg.NewEntityEmbeddingStore(h.pool, h.schema)}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status := fixture.status.Load(); status != 0 {
			w.WriteHeader(int(status))
			return
		}
		var request struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		data := make([]map[string]any, len(request.Input))
		for i, text := range request.Input {
			data[i] = map[string]any{"index": i, "embedding": candidateVector(text)}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	t.Cleanup(provider.Close)
	digest := sha256.Sum256([]byte(provider.URL))
	fixture.model = pg.EmbeddingModel{Name: "candidate-test", Revision: "v1", EndpointHash: hex.EncodeToString(digest[:]), Dimensions: 3}
	retriever, err := entitycandidate.New(fixture.store,
		inference.NewEmbedder(inference.Config{Endpoint: provider.URL, EmbeddingModel: fixture.model.Name}),
		entitycandidate.Binding{Name: fixture.model.Name, Revision: fixture.model.Revision, EndpointHash: fixture.model.EndpointHash})
	if err != nil {
		t.Fatal(err)
	}
	fixture.retriever = retriever
	h.server.Close()
	h.server = httptest.NewServer(api.NewServer(credential.NewStore(h.pool, string(migrate.ControlSchema)), api.Stores{Notifications: h.notifyStore, Notifier: h.notifier,
		Audit: pg.NewAuditStore(h.pool, h.schema), Exporter: pg.NewExporter(h.pool, h.schema),
		Projects: pg.NewProjectStore(h.pool, h.schema), Observations: pg.NewObservationStore(h.pool),
		Eraser: pg.NewEraser(h.pool), Recaller: recall.NewWithBudget(pg.NewRecallStore(h.pool, h.schema), recall.Budget{Characters: 100000, MaxRows: 50}),
		Citations: pg.NewCitationStore(h.pool, h.schema), Records: pg.NewRecordStore(h.pool, h.schema), Feedback: pg.NewFeedbackStore(h.pool, h.schema, pg.NewRecordStore(h.pool, h.schema)),
		Artifacts: pg.NewArtifactStore(h.pool, h.schema), Subjects: pg.NewSubjectStore(h.pool, h.schema), Contexts: pg.NewSegmentStore(h.pool, h.schema),
		Passages: passages, EntityCandidates: retriever,
	}, h.schema, nil).Handler())
	t.Cleanup(h.server.Close)
	return fixture
}

func (f *entityCandidateFixture) activate(t *testing.T, scope string) pg.EntityEmbeddingGeneration {
	t.Helper()
	ctx := context.Background()
	actor := uuid.NewString()
	generation, err := f.store.Start(ctx, scope, uuid.NewString(), actor, f.model)
	if err != nil {
		t.Fatal(err)
	}
	for {
		page, err := f.store.BuildPage(ctx, scope, generation.ID, actor, f.model, 64,
			func(_ context.Context, texts []string) ([][]float32, error) {
				vectors := make([][]float32, len(texts))
				for i, text := range texts {
					vectors[i] = candidateVector(text)
				}
				return vectors, nil
			})
		if err != nil {
			t.Fatal(err)
		}
		if page.Complete {
			break
		}
	}
	if err := f.store.Activate(ctx, scope, generation.ID, actor); err != nil {
		t.Fatal(err)
	}
	return generation
}

func TestEntityCandidateSearchIsAuthenticatedBoundedAndProjectScoped(t *testing.T) {
	h := newHarness(t, "api_entity_candidates")
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{
		"data_subject_id": "subject-1",
		"messages":        []map[string]any{{"role": "user", "content": "I work at Ensera and I live in Dublin."}},
	}, http.StatusCreated, nil)
	h.form(t)
	fixture := attachEntityCandidates(t, h)
	generation := fixture.activate(t, "p1")
	for _, candidates := range []int{0, 16} {
		var page entitycandidate.Page
		h.do(t, http.MethodPost, "/v1/entities/candidates", map[string]any{
			"question": "Ensera", "limit": 1, "candidates": candidates,
		}, http.StatusOK, &page)
		if page.GenerationID != generation.ID || page.Approximate != (candidates > 0) ||
			len(page.Candidates) != 1 || page.Candidates[0].NamePreview != "Ensera" {
			t.Fatalf("candidate page %+v", page)
		}
	}
	for _, body := range []map[string]any{{"question": ""}, {"question": "Ensera", "limit": 0},
		{"question": "Ensera", "limit": 2, "candidates": 1}} {
		h.do(t, http.MethodPost, "/v1/entities/candidates", body, http.StatusBadRequest, nil)
	}
	fixture.status.Store(http.StatusTooManyRequests)
	h.do(t, http.MethodPost, "/v1/entities/candidates", map[string]any{"question": "Ensera"}, http.StatusServiceUnavailable, nil)
	if err := migrate.ProvisionScope(context.Background(), h.pool, h.schema.String(), "p2"); err != nil {
		t.Fatal(err)
	}
	token, _, err := credential.NewStore(h.pool, string(migrate.ControlSchema)).Issue(context.Background(), "candidate-other", "p2")
	if err != nil {
		t.Fatal(err)
	}
	status, _ := h.raw(t, http.MethodPost, "/v1/entities/candidates", map[string]any{"question": "Ensera"}, token)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("foreign project status %d", status)
	}
}
