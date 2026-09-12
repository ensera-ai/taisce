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
	"testing"

	"github.com/ensera-ai/taisce/internal/api"
	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/entitycandidate"
	"github.com/ensera-ai/taisce/internal/infra/inference"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/ensera-ai/taisce/internal/passage"
	"github.com/ensera-ai/taisce/internal/recall"
	"github.com/ensera-ai/taisce/internal/reportcandidate"
	"github.com/google/uuid"
)

type reportCandidateFixture struct {
	h         *harness
	store     *pg.ReportEmbeddingStore
	model     pg.EmbeddingModel
	retriever *reportcandidate.Retriever
	provider  *httptest.Server
}

func attachReportCandidates(t *testing.T, h *harness, passages *passage.Retriever, entities *entitycandidate.Retriever) *reportCandidateFixture {
	t.Helper()
	fixture := &reportCandidateFixture{h: h, store: pg.NewReportEmbeddingStore(h.pool, h.schema)}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		data := make([]map[string]any, len(request.Input))
		for i, text := range request.Input {
			vector := []float32{0, 1, 0}
			if strings.Contains(strings.ToLower(text), "growth") {
				vector = []float32{1, 0, 0}
			}
			data[i] = map[string]any{"index": i, "embedding": vector}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	fixture.provider = provider
	t.Cleanup(provider.Close)
	digest := sha256.Sum256([]byte(provider.URL))
	fixture.model = pg.EmbeddingModel{Name: "report-candidate-test", Revision: "v1", EndpointHash: hex.EncodeToString(digest[:]), Dimensions: 3}
	retriever, err := reportcandidate.New(fixture.store,
		inference.NewEmbedder(inference.Config{Endpoint: provider.URL, EmbeddingModel: fixture.model.Name}),
		reportcandidate.Binding{Name: fixture.model.Name, Revision: fixture.model.Revision, EndpointHash: fixture.model.EndpointHash})
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
		Passages: passages, EntityCandidates: entities, ReportCandidates: retriever,
	}, h.schema, nil).Handler())
	t.Cleanup(h.server.Close)
	return fixture
}

func insertAPIReport(t *testing.T, h *harness) {
	t.Helper()
	ctx := context.Background()
	communityID, reportID := uuid.NewString(), uuid.NewString()
	if _, err := h.pool.Exec(ctx, h.schema.SQL(`INSERT INTO {schema}.community(scope,community_id,level) VALUES('p1',$1,0)`), communityID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(ctx, h.schema.SQL(`INSERT INTO {schema}.community_report
 (scope,report_id,community_id,title,summary,importance,importance_reason,findings)
 VALUES('p1',$1,$2,'Growth plan','Hiring and demand form an expansion theme',8,'material','[]')`), reportID, communityID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(ctx, h.schema.SQL(`INSERT INTO {schema}.projection_dependency
 (source_observation_id,scope,projection_kind,projection_id,data_subject_id,report_source_revision)
 SELECT observation_id,scope,'community_report',$1,data_subject_id,fact_revision
 FROM {schema}.observation WHERE scope='p1' ORDER BY log_offset LIMIT 1`), reportID); err != nil {
		t.Fatal(err)
	}
}

func (f *reportCandidateFixture) activate(t *testing.T, scope string) pg.ReportEmbeddingGeneration {
	t.Helper()
	ctx := context.Background()
	actor := uuid.NewString()
	generation, err := f.store.Start(ctx, scope, uuid.NewString(), actor, f.model)
	if err != nil {
		t.Fatal(err)
	}
	for {
		page, err := f.store.BuildPage(ctx, scope, generation.ID, actor, f.model, 64, func(_ context.Context, texts []string) ([][]float32, error) {
			// The same rule as the provider above, so a report is near the questions its words are near.
			vectors := make([][]float32, len(texts))
			for i, text := range texts {
				vectors[i] = []float32{0, 1, 0}
				if strings.Contains(strings.ToLower(text), "growth") {
					vectors[i] = []float32{1, 0, 0}
				}
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

func TestReportCandidateSearchIsAuthenticatedBoundedAndThematic(t *testing.T) {
	h := newHarness(t, "api_report_candidates")
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{
		"data_subject_id": "subject-1", "messages": []map[string]any{{"role": "user", "content": "We are hiring while demand increases."}},
	}, http.StatusCreated, nil)
	h.form(t)
	insertAPIReport(t, h)
	fixture := attachReportCandidates(t, h, nil, nil)
	generation := fixture.activate(t, "p1")
	for _, candidates := range []int{0, 16} {
		var page reportcandidate.Page
		h.do(t, http.MethodPost, "/v1/reports/candidates", map[string]any{
			"question": "What is the growth theme?", "limit": 1, "candidates": candidates,
		}, http.StatusOK, &page)
		if page.GenerationID != generation.ID || page.Approximate != (candidates > 0) ||
			len(page.Candidates) != 1 || page.Candidates[0].TitlePreview != "Growth plan" {
			t.Fatalf("candidate page %+v", page)
		}
	}
	for _, body := range []map[string]any{{"question": ""}, {"question": "growth", "limit": 0},
		{"question": "growth", "limit": 2, "candidates": 1}} {
		h.do(t, http.MethodPost, "/v1/reports/candidates", body, http.StatusBadRequest, nil)
	}
	fixture.provider.Close()
	h.do(t, http.MethodPost, "/v1/reports/candidates", map[string]any{
		"question": "What is the growth theme?", "limit": 1,
	}, http.StatusServiceUnavailable, nil)
}
