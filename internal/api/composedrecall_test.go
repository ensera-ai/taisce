// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package api_test

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ensera-ai/taisce/internal/api"
	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/ensera-ai/taisce/internal/recall"
)

// A question that anchors nothing is answered from the report hierarchy and from passages, over the
// same authenticated route, with every surface labelled for what it is: a report carries its
// sources, a passage carries its role, and neither is ever presented as a fact.
func TestComposedRecallAnswersFromThemesAndPassagesAndNeverInventsFacts(t *testing.T) {
	childID, childReportID := uuid.NewString(), uuid.NewString()
	h := newHarness(t, "composed")
	passages := attachPassages(t, h, nil)
	entities := attachEntityCandidatesWithPassages(t, h, passages.retriever)
	reports := attachReportCandidates(t, h, passages.retriever, entities.retriever)
	semantic := api.NewSemanticSurfaces(entities.retriever, reports.retriever, passages.retriever, pg.NewCommunityStore(h.pool, h.schema))
	if semantic == nil {
		t.Fatal("three configured retrievers must compose")
	}
	h.server.Close()
	h.server = httptest.NewServer(api.NewServer(credential.NewStore(h.pool, string(migrate.ControlSchema)), api.Stores{Notifications: h.notifyStore, Notifier: h.notifier,
		Audit: pg.NewAuditStore(h.pool, h.schema), Exporter: pg.NewExporter(h.pool, h.schema),
		Projects: pg.NewProjectStore(h.pool, h.schema), Observations: pg.NewObservationStore(h.pool),
		Eraser:    pg.NewEraser(h.pool),
		Recaller:  recall.NewWithBudget(pg.NewRecallStore(h.pool, h.schema), recall.Budget{Characters: 100000, MaxRows: 50}).WithSemantic(semantic),
		Citations: pg.NewCitationStore(h.pool, h.schema), Records: pg.NewRecordStore(h.pool, h.schema), Feedback: pg.NewFeedbackStore(h.pool, h.schema, pg.NewRecordStore(h.pool, h.schema)),
		Artifacts: pg.NewArtifactStore(h.pool, h.schema), Subjects: pg.NewSubjectStore(h.pool, h.schema), Contexts: pg.NewSegmentStore(h.pool, h.schema),
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
	// Narrowed to the person who said it: their words still come back as a passage, and reports —
	// which span people and name nobody as their source — are withheld, and the answer says so.
	narrowed := out
	h.do(t, http.MethodPost, "/v1/recalls", map[string]any{"data_subject_id": "subject-1", "question": "PostgreSQL growth", "source_roles": []string{"tool"}}, 200, &narrowed)
	if len(narrowed.Reports) != 0 || !slices.Contains(narrowed.Degraded, "reports_withheld_for_subject") {
		t.Fatalf("a recall narrowed to one person was answered from a report, or its absence went unsaid: %+v %v", narrowed.Reports, narrowed.Degraded)
	}
	if len(narrowed.Passages) == 0 || narrowed.Passages[0].Role != "tool" || narrowed.Passages[0].Quote == "" {
		t.Fatalf("the person's own words were withheld from their narrowed recall: %+v", narrowed.Passages)
	}
	// Not narrowed, the same question is answered by theme as well.
	h.do(t, http.MethodPost, "/v1/recalls", map[string]any{"question": "PostgreSQL growth", "source_roles": []string{"tool"}}, 200, &out)
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

	// An entity named by meaning rather than by its stored name starts a walk: the user's own turn
	// forms an entity, the candidate generation is built over it, and a project-wide question whose
	// tokens are not stored names still anchors on it and returns the facts around it. A subject's
	// own recall always anchors on the speaker, so the meaning path is a project-wide one.
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": "subject-1", "messages": []map[string]any{
		{"role": "user", "content": "I work at Ensera and I live in Dublin."}}}, 201, nil)
	h.form(t)
	entities.activate(t, "p1")
	// A child of the matched community, with a report of its own, is walked deterministically.
	var parent string
	if err := h.pool.QueryRow(t.Context(), h.schema.SQL(`SELECT community_id::text FROM {schema}.community WHERE scope='p1' AND level=0 LIMIT 1`)).Scan(&parent); err != nil {
		t.Fatalf("read the seeded community: %v", err)
	}
	if _, err := h.pool.Exec(t.Context(), h.schema.SQL(`INSERT INTO {schema}.community(scope,community_id,level,parent_id) VALUES('p1',$1,1,$2)`), childID, parent); err != nil {
		t.Fatalf("seed child community: %v", err)
	}
	if _, err := h.pool.Exec(t.Context(), h.schema.SQL(`INSERT INTO {schema}.community_report
 (scope,report_id,community_id,title,summary,importance,importance_reason,findings)
 VALUES('p1',$1,$2,'Hiring','Two teams are hiring',5,'material','[]')`), childReportID, childID); err != nil {
		t.Fatalf("seed child report: %v", err)
	}
	// A report is embedded only with its source registrations, so the child names the turn
	// that formed the entity as its source at that turn's current revision.
	if _, err := h.pool.Exec(t.Context(), h.schema.SQL(`INSERT INTO {schema}.projection_dependency
 (source_observation_id,scope,projection_kind,projection_id,data_subject_id,report_source_revision)
 SELECT observation_id,scope,'community_report',$1,data_subject_id,fact_revision
 FROM {schema}.observation WHERE scope='p1' ORDER BY log_offset DESC LIMIT 1`), childReportID); err != nil {
		t.Fatalf("register the child report's source: %v", err)
	}
	// Forming moved the fact revision the report generation was built against, so the report
	// surface refuses it until a generation is built over the current state.
	reports.activate(t, "p1")
	var walked struct {
		Anchors []struct {
			Matched string `json:"matched"`
		} `json:"anchors"`
		Facts   []map[string]any `json:"facts"`
		Reports []struct {
			Title  string `json:"title"`
			Parent string `json:"parent"`
		} `json:"reports"`
		Degraded []string `json:"degraded"`
	}
	h.do(t, http.MethodPost, "/v1/recalls", map[string]any{"question": "ensera-corp growth", "themes": true}, 200, &walked)
	if len(walked.Anchors) == 0 || !strings.HasPrefix(walked.Anchors[0].Matched, "semantic:") {
		t.Fatalf("expected a semantic anchor, got %+v", walked.Anchors)
	}
	if len(walked.Facts) == 0 {
		t.Fatalf("a semantic anchor must start a walk that reaches the facts around it, got none")
	}
	var child int
	for _, r := range walked.Reports {
		if r.Title == "Hiring" && r.Parent == parent {
			child++
		}
	}
	if child != 1 || len(walked.Reports) != 2 || walked.Reports[0].Title != "Growth plan" || len(walked.Degraded) != 0 {
		t.Fatalf("expected the matched community first, its child once with its lineage and nothing degraded, got %+v %v", walked.Reports, walked.Degraded)
	}
}

// The adapter composes what is configured and refuses, by name, what is not; a passage query over
// several roles filters in memory because the retriever takes one role.
func TestSemanticSurfacesRefuseWhatIsNotConfiguredAndFilterRolesInMemory(t *testing.T) {
	if api.NewSemanticSurfaces(nil, nil, nil, nil) != nil {
		t.Fatal("nothing configured must compose to nothing")
	}
	h := newHarness(t, "composed_partial")
	entities := attachEntityCandidates(t, h)
	passages := attachPassages(t, h, nil)
	only := api.NewSemanticSurfaces(nil, nil, passages.retriever, nil)
	if _, err := only.EntityAnchors(t.Context(), "p1", "anything", 4); err == nil {
		t.Fatal("an unconfigured surface must refuse")
	}
	if _, err := only.Reports(t.Context(), "p1", "anything", 3, 5); err == nil {
		t.Fatal("an unconfigured surface must refuse")
	}
	if _, err := api.NewSemanticSurfaces(entities.retriever, nil, nil, nil).Passages(t.Context(), "p1", "anything", 8, []string{"user"}, ""); err == nil {
		t.Fatal("an unconfigured surface must refuse")
	}
	// A question with no term in it is answered empty rather than refused: there is nothing to
	// look up and nothing to say about it, and the bundle still renders every field a reader parses.
	var empty struct {
		Anchors []map[string]any `json:"anchors"`
		Reach   map[string]any   `json:"reach"`
	}
	h.do(t, http.MethodPost, "/v1/recalls", map[string]any{"data_subject_id": "subject-1", "question": "?!"}, 200, &empty)
	if len(empty.Anchors) != 0 || empty.Reach == nil {
		t.Fatalf("a termless question answers empty with its reach, got %+v", empty)
	}
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": "subject-1", "messages": []map[string]any{
		{"role": "user", "content": "PostgreSQL is the substrate."},
		{"role": "assistant", "content": "PostgreSQL it is, then."}}}, 201, nil)
	passages.activate(t, "p1", "v1")
	both, err := only.Passages(t.Context(), "p1", "PostgreSQL", 8, []string{"user", "assistant"}, "")
	if err != nil || len(both) != 2 {
		t.Fatalf("expected both roles' passages, got %d %v", len(both), err)
	}
	users, err := only.Passages(t.Context(), "p1", "PostgreSQL", 8, []string{"user", "system"}, "")
	if err != nil || len(users) != 1 || users[0].Role != "user" {
		t.Fatalf("expected only the user's passage after the in-memory filter, got %+v %v", users, err)
	}
	// Narrowed to a person who said none of it, the same search quotes nobody: the subject reaches
	// the retriever itself, not only the recall that calls it.
	nobody, err := only.Passages(t.Context(), "p1", "PostgreSQL", 8, []string{"user", "assistant"}, "a-person-who-said-nothing")
	if err != nil || len(nobody) != 0 {
		t.Fatalf("a passage search narrowed to one person quoted somebody else: %d %v", len(nobody), err)
	}
}
