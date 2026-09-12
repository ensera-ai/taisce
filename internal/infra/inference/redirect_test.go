// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package inference_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/ensera-ai/taisce/internal/compaction"
	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/inference"
)

// ── The allowlist names where content goes, not where it goes first ───────────────────────────
//
// A listed endpoint that answers with a redirect is asking for the request body — message content,
// a report's facts, a history to summarise — to be sent again to a host nobody listed. Every model
// client refuses. Proved against two real listeners: the listed one redirects, and the unlisted one
// counts what reached it. For each client and each redirect that re-sends a body, that count stays 0.
func TestAModelEndpointCannotRedirectContentToAHostNobodyListed(t *testing.T) {
	var reached atomic.Int64
	unlisted := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(unlisted.Close)

	vocabulary, err := domain.NewOntology([]domain.Predicate{
		{Name: "works_at", SemanticType: "identity", Cardinality: domain.CardinalityMany,
			ObjectKind: "organisation", Description: "An organisation the subject works for."},
	})
	if err != nil {
		t.Fatalf("vocabulary: %v", err)
	}
	calls := map[string]func(inference.Config) error{
		"extraction": func(c inference.Config) error {
			_, err := inference.NewModel(c).Propose(context.Background(),
				domain.Message{Content: "I work at Ensera."}, vocabulary)
			return err
		},
		"embedding": func(c inference.Config) error {
			c.EmbeddingModel = "test-embedder"
			_, err := inference.NewEmbedder(c).Embed(context.Background(), []string{"anything"})
			return err
		},
		"report": func(c inference.Config) error {
			_, err := inference.NewReporter(c).Write(context.Background(), material())
			return err
		},
		"summary": func(c inference.Config) error {
			_, err := inference.NewSummariser(c).Write(context.Background(), compaction.Material{})
			return err
		},
	}
	for _, status := range []int{http.StatusTemporaryRedirect, http.StatusPermanentRedirect, http.StatusFound} {
		for name, call := range calls {
			config := serving(t, func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, unlisted.URL+r.URL.Path, status)
			})
			if err := call(config); err == nil {
				t.Errorf("%s: a %d from the listed endpoint was reported as a success", name, status)
			}
		}
	}
	if n := reached.Load(); n != 0 {
		t.Fatalf("a host nobody listed received %d model request(s) by redirect", n)
	}
}
