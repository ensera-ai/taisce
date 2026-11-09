// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/ensera-ai/taisce/internal/api"
	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/inference"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/ensera-ai/taisce/internal/recall"
)

// oneSpaceSurfaces stands in for the three retrievers a deployment configures from one embedding
// model. Each method embeds the question it was given, exactly as the retriever it stands for does,
// and all three share one embedder because one model is what is configured. What is under test is
// the request scope and the embedder, not the retrievers, which this change does not touch.
type oneSpaceSurfaces struct{ embedder *inference.Embedder }

func (s oneSpaceSurfaces) embed(ctx context.Context, question string) error {
	_, err := s.embedder.Embed(ctx, []string{question})
	return err
}

func (s oneSpaceSurfaces) EntityAnchors(ctx context.Context, _, question string, _ int) ([]domain.Anchor, error) {
	return nil, s.embed(ctx, question)
}

func (s oneSpaceSurfaces) Reports(ctx context.Context, _, question string, _, _ int) ([]domain.ReportHit, error) {
	return nil, s.embed(ctx, question)
}

func (s oneSpaceSurfaces) Passages(ctx context.Context, _, question string, _ int, _ []string, _ string) ([]domain.Passage, error) {
	return nil, s.embed(ctx, question)
}

// One recall over the authenticated route embeds its question once, however many surfaces run.
//
// The scope is taken where every protected request is admitted rather than in the recall handler, so
// this also says that the door does it: a route added later inherits the scope instead of having to
// remember it.
func TestOneRecallOverTheRouteEmbedsItsQuestionOnce(t *testing.T) {
	var calls atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var request struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		data := make([]map[string]any, len(request.Input))
		for i := range request.Input {
			data[i] = map[string]any{"index": i, "embedding": []float32{1, 0, 0}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	t.Cleanup(provider.Close)

	h := newHarness(t, "questionscope")
	surfaces := oneSpaceSurfaces{embedder: inference.NewEmbedder(
		inference.Config{Endpoint: provider.URL, EmbeddingModel: "one-model"})}
	h.server.Close()
	h.server = httptest.NewServer(api.NewServer(credential.NewStore(h.pool, string(migrate.ControlSchema)), api.Stores{
		Notifications: h.notifyStore, Notifier: h.notifier,
		Audit: pg.NewAuditStore(h.pool, h.schema), Exporter: pg.NewExporter(h.pool, h.schema),
		Projects: pg.NewProjectStore(h.pool, h.schema), Observations: pg.NewObservationStore(h.pool),
		Eraser: pg.NewEraser(h.pool),
		Recaller: recall.NewWithBudget(pg.NewRecallStore(h.pool, h.schema),
			recall.Budget{Characters: 100000, MaxRows: 50}).WithSemantic(surfaces),
		Citations: pg.NewCitationStore(h.pool, h.schema), Records: pg.NewRecordStore(h.pool, h.schema),
		Subjects: pg.NewSubjectStore(h.pool, h.schema), Contexts: pg.NewSegmentStore(h.pool, h.schema),
	}, h.schema, nil).Handler())
	t.Cleanup(h.server.Close)

	// A question that anchors nothing exactly, so the semantic anchor step, reports and passages all
	// run: three consultations of three surfaces that share one model.
	h.do(t, http.MethodPost, "/v1/recalls",
		map[string]any{"question": "what is known about an unheard-of thing"}, 200, nil)

	if got := calls.Load(); got != 1 {
		t.Fatalf("one recall called the embedding provider %d times, want 1", got)
	}
}
