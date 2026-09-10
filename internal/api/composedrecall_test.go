// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package api_test

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/ensera-ai/taisce/internal/api"
	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/ensera-ai/taisce/internal/recall"
)

// A question that anchors nothing is answered from the report hierarchy and from passages, over the
// same authenticated route, with every surface labelled for what it is: a report carries its
// sources, a passage carries its role, and neither is ever presented as a fact (D124).
func TestComposedRecallAnswersFromThemesAndPassagesAndNeverInventsFacts(t *testing.T) {
	h := newHarness(t, "composed")
	passages := attachPassages(t, h, nil)
	entities := attachEntityCandidatesWithPassages(t, h, passages.retriever)
	reports := attachReportCandidates(t, h, passages.retriever, entities.retriever)
	semantic := api.NewSemanticSurfaces(entities.retriever, reports.retriever, passages.retriever, pg.NewCommunityStore(h.pool, h.schema))
	if semantic == nil {
		t.Fatal("three configured retrievers must compose")
	}
	h.server.Close()
	h.server = httptest.NewServer(api.NewServer(credential.NewStore(h.pool, string(migrate.ControlSchema)), api.Stores{
		Audit: pg.NewAuditStore(h.pool, h.schema), Exporter: pg.NewExporter(h.pool, h.schema),
		Projects: pg.NewProjectStore(h.pool, h.schema), Observations: pg.NewObservationStore(h.pool),
		Eraser:    pg.NewEraser(h.pool),
		Recaller:  recall.NewWithBudget(pg.NewRecallStore(h.pool, h.schema), recall.Budget{Characters: 100000, MaxRows: 50}).WithSemantic(semantic),
		Citations: pg.NewCitationStore(h.pool, h.schema), Records: pg.NewRecordStore(h.pool, h.schema),
		Artifacts: pg.NewArtifactStore(h.pool, h.schema), Subjects: pg.NewSubjectStore(h.pool, h.schema),
		Passages: passages.retriever, EntityCandidates: entities.retriever, ReportCandidates: reports.retriever,
	}, h.schema, nil).Handler())
	t.Cleanup(h.server.Close)

	// A fetched document's words: they are a passage under the tool role, never a fact.
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": "subject-1", "messages": []map[string]any{
		{"group_ordinal": 0, "role": "tool", "content": "PostgreSQL growth is strong this quarter."}}}, 201, nil)
	insertAPIReport(t, h)
	reports.activate(t, "p1")
	passages.activate(t, "p1", "v1")

	var out struct {
		Facts   []map[string]any `json:"facts"`
		Reports []struct {
			Title   string   `json:"title"`
			Sources []string `json:"sources"`
		} `json:"reports"`
		Passages []struct {
			Role  string `json:"role"`
			Quote string `json:"quote"`
		} `json:"passages"`
		Degraded []string `json:"degraded"`
		Controls struct {
			Surfaces []string `json:"surfaces"`
		} `json:"controls"`
		Reach struct {
			Anchored int `json:"anchored"`
		} `json:"reach"`
	}
	h.do(t, http.MethodPost, "/v1/recalls", map[string]any{"data_subject_id": "subject-1", "question": "PostgreSQL growth", "source_roles": []string{"tool"}}, 200, &out)
	if len(out.Facts) != 0 || out.Reach.Anchored != 0 {
		t.Fatalf("nothing was anchored and no fact exists, got %+v", out)
	}
	if len(out.Reports) == 0 || out.Reports[0].Title != "Growth plan" || len(out.Reports[0].Sources) == 0 {
		t.Fatalf("expected the report by theme with its sources, got %+v", out.Reports)
	}
	if len(out.Passages) == 0 || out.Passages[0].Role != "tool" || out.Passages[0].Quote == "" {
		t.Fatalf("expected the document's words as a labelled passage, got %+v", out.Passages)
	}
	if !slices.Equal(out.Controls.Surfaces, recall.AllSurfaces) {
		t.Fatalf("controls must echo the surfaces that applied, got %v", out.Controls.Surfaces)
	}
	// The entity generation was never activated, so that surface refused: named, not fatal.
	if !slices.Contains(out.Degraded, "semantic_anchors") {
		t.Fatalf("a refusing surface must be named as degraded, got %v", out.Degraded)
	}

	// Leaving a surface out keeps it out, and naming an unknown one is refused.
	h.do(t, http.MethodPost, "/v1/recalls", map[string]any{"data_subject_id": "subject-1", "question": "PostgreSQL growth", "source_roles": []string{"tool"}, "surfaces": []string{"facts"}}, 200, &out)
	if len(out.Reports) != 0 || len(out.Passages) != 0 {
		t.Fatalf("surfaces left out must not answer, got %+v", out)
	}
	h.do(t, http.MethodPost, "/v1/recalls", map[string]any{"data_subject_id": "subject-1", "question": "PostgreSQL growth", "surfaces": []string{"themes"}}, 400, nil)
}
