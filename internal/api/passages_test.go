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
	"time"

	"github.com/ensera-ai/taisce/internal/api"
	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/inference"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/ensera-ai/taisce/internal/passage"
	"github.com/ensera-ai/taisce/internal/recall"
	"github.com/google/uuid"
)

type passageFixture struct {
	h         *harness
	model     pg.EmbeddingModel
	store     *pg.MessageEmbeddingStore
	retriever *passage.Retriever
	calls     atomic.Int32
	status    atomic.Int32
	after     func()
}

func passageVector(text string) []float32 {
	if strings.Contains(text, "PostgreSQL") {
		return []float32{1, 0, 0}
	}
	return []float32{0, 1, 0}
}

func attachPassages(t *testing.T, h *harness, after func()) *passageFixture {
	t.Helper()
	fixture := &passageFixture{h: h, store: pg.NewMessageEmbeddingStore(h.pool, h.schema), after: after}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture.calls.Add(1)
		if code := fixture.status.Load(); code != 0 {
			w.WriteHeader(int(code))
			return
		}
		var request struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if fixture.after != nil {
			fixture.after()
		}
		data := make([]map[string]any, len(request.Input))
		for i, text := range request.Input {
			data[i] = map[string]any{"index": i, "embedding": passageVector(text)}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	t.Cleanup(provider.Close)
	digest := sha256.Sum256([]byte(provider.URL))
	fixture.model = pg.EmbeddingModel{Name: "test-embedder", Revision: "v1", EndpointHash: hex.EncodeToString(digest[:]), Dimensions: 3}
	retriever, err := passage.New(fixture.store, inference.NewEmbedder(inference.Config{Endpoint: provider.URL, EmbeddingModel: fixture.model.Name}), passage.Binding{Name: fixture.model.Name, Revision: fixture.model.Revision, EndpointHash: fixture.model.EndpointHash})
	if err != nil {
		t.Fatal(err)
	}
	fixture.retriever = retriever
	h.server.Close()
	h.server = httptest.NewServer(api.NewServer(credential.NewStore(h.pool, string(migrate.ControlSchema)), api.Stores{Notifications: h.notifyStore, Notifier: h.notifier,
		Audit: pg.NewAuditStore(h.pool, h.schema), Exporter: pg.NewExporter(h.pool, h.schema), Projects: pg.NewProjectStore(h.pool, h.schema), Observations: pg.NewObservationStore(h.pool), Eraser: pg.NewEraser(h.pool), Recaller: recall.NewWithBudget(pg.NewRecallStore(h.pool, h.schema), recall.Budget{Characters: 100000, MaxRows: 50}), Citations: pg.NewCitationStore(h.pool, h.schema), Records: pg.NewRecordStore(h.pool, h.schema), Feedback: pg.NewFeedbackStore(h.pool, h.schema, pg.NewRecordStore(h.pool, h.schema)), Artifacts: pg.NewArtifactStore(h.pool, h.schema), Subjects: pg.NewSubjectStore(h.pool, h.schema), Contexts: pg.NewSegmentStore(h.pool, h.schema), Passages: retriever,
	}, h.schema, nil).Handler())
	t.Cleanup(h.server.Close)
	return fixture
}

func (f *passageFixture) activate(t *testing.T, scope, revision string) pg.EmbeddingGeneration {
	t.Helper()
	ctx := context.Background()
	model := f.model
	model.Revision = revision
	actor := uuid.NewString()
	generation, err := f.store.Start(ctx, scope, uuid.NewString(), actor, model)
	if err != nil {
		t.Fatal(err)
	}
	for {
		page, err := f.store.BuildPage(ctx, scope, generation.ID, actor, model, 64, func(_ context.Context, texts []string) ([][]float32, error) {
			vectors := make([][]float32, len(texts))
			for i, text := range texts {
				vectors[i] = passageVector(text)
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

func TestPassageSearchReturnsAuthoritativeMessageEvidenceWithoutFacts(t *testing.T) {
	h := newHarness(t, "api_passages_journey")
	ctx := context.Background()
	content := strings.Repeat("مرحبا ", 100) + "PostgreSQL"
	occurred := time.Date(2026, 9, 1, 12, 30, 0, 0, time.UTC)
	var observed struct {
		ID string `json:"id"`
	}
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": "subject-1", "messages": []map[string]any{{"role": "user", "content": "The migration uses SQLite."}, {"role": "assistant", "content": content, "occurred_at": occurred}}}, 201, &observed)
	f := attachPassages(t, h, nil)
	generation := f.activate(t, "p1", "v1")
	var page passage.Page
	h.do(t, http.MethodPost, "/v1/passages/search", map[string]any{"question": "PostgreSQL", "limit": 1}, 200, &page)
	if len(page.Passages) != 1 {
		t.Fatalf("passages %+v", page)
	}
	found := page.Passages[0]
	if found.SourceID != observed.ID || found.Ordinal != 1 || found.Role != "assistant" || !found.OccurredAt.Equal(occurred) || found.PreviewComplete || found.MessageBytes != len(content) || found.PreviewByteEnd != len(found.Preview) || !strings.HasPrefix(content, found.Preview) {
		t.Fatalf("wrong source metadata %+v", found)
	}
	if len([]rune(found.Preview)) != 512 || page.GenerationID != generation.ID || page.Approximate {
		t.Fatalf("wrong preview/generation %+v", page)
	}
	var facts int
	if err := h.pool.QueryRow(ctx, h.schema.SQL(`SELECT count(*) FROM {schema}.fact`)).Scan(&facts); err != nil || facts != 0 {
		t.Fatalf("passage search invented facts: %d %v", facts, err)
	}
	h.do(t, http.MethodPost, "/v1/passages/search", map[string]any{"question": "PostgreSQL", "source_role": "user", "data_subject_id": "subject-1", "candidates": 32}, 200, &page)
	if len(page.Passages) != 1 || page.Passages[0].Ordinal != 0 || !page.Passages[0].PreviewComplete || !page.Approximate {
		t.Fatalf("role filter %+v", page)
	}
	h.do(t, http.MethodPost, "/v1/passages/search", map[string]any{"question": "PostgreSQL", "data_subject_id": "another"}, 200, &page)
	if len(page.Passages) != 0 {
		t.Fatalf("subject filter %+v", page)
	}
	if err := migrate.ProvisionScope(ctx, h.pool, h.schema.String(), "p2"); err != nil {
		t.Fatal(err)
	}
	f.activate(t, "p2", "v1")
	token, _, err := credential.NewStore(h.pool, string(migrate.ControlSchema)).Issue(ctx, "passage-other", "p2")
	if err != nil {
		t.Fatal(err)
	}
	status, raw := h.raw(t, http.MethodPost, "/v1/passages/search", map[string]any{"question": "PostgreSQL"}, token)
	if status != 200 || strings.Contains(string(raw), observed.ID) || strings.Contains(string(raw), "مرحبا") {
		t.Fatalf("project leak: %d %s", status, raw)
	}
	entries, err := pg.NewAuditStore(h.pool, h.schema).Recent(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	matched := 0
	for _, entry := range entries {
		if entry.Operation == domain.AuditEmbeddingSearch {
			matched++
			if entry.PrincipalKind != domain.PrincipalCredential {
				t.Fatal("read audited as operator")
			}
		}
	}
	encoded, _ := json.Marshal(entries)
	if matched != 4 || strings.Contains(string(encoded), "PostgreSQL") || strings.Contains(string(encoded), "subject-1") || strings.Contains(string(encoded), "مرحبا") {
		t.Fatalf("audit content or attribution: %s", encoded)
	}
	h.do(t, http.MethodPost, "/v1/erasures", map[string]any{"data_subject_id": "subject-1", "reason": "test"}, 200, nil)
	h.do(t, http.MethodPost, "/v1/passages/search", map[string]any{"question": "PostgreSQL"}, 200, &page)
	if len(page.Passages) != 0 {
		t.Fatalf("erased evidence returned %+v", page)
	}
}

func TestPassageRequestsRefuseBeforeProviderWorkAndHideConfigurationDetails(t *testing.T) {
	h := newHarness(t, "api_passages_refusals")
	h.do(t, http.MethodPost, "/v1/passages/search", map[string]any{"question": "PostgreSQL"}, 503, nil)
	f := attachPassages(t, h, nil)
	h.do(t, http.MethodPost, "/v1/passages/search", map[string]any{"question": "PostgreSQL"}, 503, nil)
	if f.calls.Load() != 0 {
		t.Fatal("provider called without active generation")
	}
	f.activate(t, "p1", "v1")
	for _, body := range []map[string]any{{}, {"question": " "}, {"question": strings.Repeat("x", domain.MaxRecallQuestionBytes+1)}, {"question": "x", "limit": 0}, {"question": "x", "limit": 33}, {"question": "x", "candidates": -1}, {"question": "x", "candidates": 1}, {"question": "x", "candidates": 1001}, {"question": "x", "source_role": "invented"}, {"question": "x", "data_subject_id": strings.Repeat("x", 1025)}, {"question": "x", "scope": "p2"}, {"question": "x", "generation_id": uuid.NewString()}} {
		h.do(t, http.MethodPost, "/v1/passages/search", body, 400, nil)
	}
	if f.calls.Load() != 0 {
		t.Fatal("invalid request reached provider")
	}
	f.status.Store(429)
	h.do(t, http.MethodPost, "/v1/passages/search", map[string]any{"question": "PostgreSQL"}, 503, nil)
	f.status.Store(0)
	f.activate(t, "p1", "v2")
	before := f.calls.Load()
	status, body := h.raw(t, http.MethodPost, "/v1/passages/search", map[string]any{"question": "PostgreSQL"}, h.token)
	if status != 503 || f.calls.Load() != before || strings.Contains(string(body), f.model.EndpointHash) || strings.Contains(string(body), "v2") {
		t.Fatalf("configuration mismatch: %d %s", status, body)
	}
}

// The provider runs outside a database transaction. A concurrent erasure must remove its source
// before the final read, and activation must not combine a query with a different vector space.
func TestPassageSearchRechecksMemoryAfterProviderReturns(t *testing.T) {
	for _, change := range []string{"erase", "activate", "storage_failure"} {
		t.Run(change, func(t *testing.T) {
			h := newHarness(t, "api_passage_race_"+change)
			h.do(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": "subject-1", "messages": []map[string]any{{"role": "user", "content": "PostgreSQL is our database."}}}, 201, nil)
			entered, release := make(chan struct{}), make(chan struct{})
			defer func() {
				select {
				case <-release:
				default:
					close(release)
				}
			}()
			fixture := attachPassages(t, h, func() { close(entered); <-release })
			fixture.activate(t, "p1", "v1")
			type response struct {
				status int
				raw    []byte
			}
			done := make(chan response, 1)
			go func() {
				status, raw := h.raw(t, http.MethodPost, "/v1/passages/search", map[string]any{"question": "PostgreSQL"}, h.token)
				done <- response{status, raw}
			}()
			select {
			case <-entered:
			case <-time.After(10 * time.Second):
				close(release)
				t.Fatal("provider was not reached")
			}
			want := 200
			switch change {
			case "erase":
				// A second replica can erase while this replica spends its project slot on inference.
				if receipt, err := pg.NewEraser(h.pool).Erase(context.Background(), h.schema, "p1", "subject-1", "concurrent erase"); err != nil || !receipt.Clean() {
					t.Fatalf("concurrent erasure: %v", err)
				}
			case "activate":
				fixture.activate(t, "p1", "v2")
				want = 503
			case "storage_failure":
				if _, err := h.pool.Exec(context.Background(), h.schema.SQL(`DROP TABLE {schema}.message_embedding CASCADE`)); err != nil {
					close(release)
					t.Fatal(err)
				}
				want = 500
			}
			close(release)
			select {
			case result := <-done:
				if result.status != want {
					t.Fatalf("status %d want %d: %s", result.status, want, result.raw)
				}
				if strings.Contains(string(result.raw), "PostgreSQL is our database") {
					t.Fatal("stale source content returned")
				}
				if change == "erase" {
					var page passage.Page
					if err := json.Unmarshal(result.raw, &page); err != nil || len(page.Passages) != 0 {
						t.Fatalf("erased source retained: %s", result.raw)
					}
				}
			case <-time.After(10 * time.Second):
				t.Fatal("search did not finish")
			}
		})
	}
}

func TestCatchUpPassagesExposeNewEvidenceAndConservativeCoverage(t *testing.T) {
	h := newHarness(t, "api_passage_catchup")
	fixture := attachPassages(t, h, nil)
	generation := fixture.activate(t, "p1", "v1")
	var source struct {
		LogOffset int64 `json:"log_offset"`
	}
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": "subject-1", "messages": []map[string]any{{"role": "user", "content": "PostgreSQL is our database."}, {"role": "assistant", "content": "It supports the project boundary."}}}, 201, &source)
	embed := func(_ context.Context, texts []string) ([][]float32, error) {
		vectors := make([][]float32, len(texts))
		for i, text := range texts {
			vectors[i] = passageVector(text)
		}
		return vectors, nil
	}
	if page, err := fixture.store.CatchUpPage(context.Background(), "p1", generation.ID, uuid.NewString(), fixture.model, 1, embed); err != nil || page.Complete {
		t.Fatalf("partial catch-up %+v %v", page, err)
	}
	var result passage.Page
	h.do(t, http.MethodPost, "/v1/passages/search", map[string]any{"question": "PostgreSQL", "limit": 1}, 200, &result)
	if len(result.Passages) != 1 || result.Passages[0].Preview != "PostgreSQL is our database." || result.CoveredThroughOffset != -1 || result.BuildState != "building" {
		t.Fatalf("partial evidence %+v", result)
	}
	if page, err := fixture.store.CatchUpPage(context.Background(), "p1", generation.ID, uuid.NewString(), fixture.model, 1, embed); err != nil || !page.Complete {
		t.Fatalf("complete catch-up %+v %v", page, err)
	}
	h.do(t, http.MethodPost, "/v1/passages/search", map[string]any{"question": "PostgreSQL"}, 200, &result)
	if len(result.Passages) != 2 || result.CoveredThroughOffset != result.ThroughOffset || result.CoveredThroughOffset < 0 || result.GenerationID != generation.ID {
		t.Fatalf("completed evidence %+v", result)
	}
}
