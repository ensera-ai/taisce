// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/infra/inference"
	"github.com/ensera-ai/taisce/internal/infra/pg"
)

func TestPassageConfigurationRequiresExplicitValidRevision(t *testing.T) {
	for _, key := range []string{inference.EnvEmbeddingRevision, inference.EnvEndpoint, inference.EnvEmbeddingEndpoint, inference.EnvEmbeddingModel, inference.EnvModel, inference.EnvAllowlist} {
		t.Setenv(key, "")
	}
	schema, _ := pg.NewSchema("passage_config")
	service, err := configuredPassages(nil, schema)
	if err != nil || service != nil {
		t.Fatal("disabled passage search required inference")
	}
	t.Setenv(inference.EnvEmbeddingRevision, "v1")
	if _, err := configuredPassages(nil, schema); err == nil {
		t.Fatal("opt-in accepted missing provider")
	}
	t.Setenv(inference.EnvEmbeddingEndpoint, "http://localhost:11434/v1")
	t.Setenv(inference.EnvEmbeddingModel, "embedder")
	t.Setenv(inference.EnvAllowlist, "localhost:11434")
	if service, err := configuredPassages(nil, schema); err != nil || service == nil {
		t.Fatalf("embedding-only configuration: %v", err)
	}
	t.Setenv(inference.EnvEmbeddingRevision, strings.Repeat("x", 1025))
	if _, err := configuredPassages(nil, schema); err == nil {
		t.Fatal("invalid revision accepted")
	}
}

func TestWorkerDoesNotRequirePassageAPIConfiguration(t *testing.T) {
	env := runtimeConfiguration(t)
	env[envRole] = roleWorker
	port := freePort(t)
	env[envHealthAddr] = fmt.Sprintf("127.0.0.1:%d", port)
	withEnv(t, env)
	t.Setenv(inference.EnvEmbeddingRevision, "v1")
	t.Setenv(inference.EnvEmbeddingModel, "")
	t.Setenv(inference.EnvEndpoint, "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopped := make(chan error, 1)
	go func() { stopped <- run(ctx, quiet()) }()
	if !reachable(fmt.Sprintf("http://127.0.0.1:%d/health", port), 5*time.Second) {
		t.Fatal("worker failed to start independently of passage search")
	}
	cancel()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("worker did not stop")
	}
}

// TestCandidateSearchRequiresTheSameExplicitOptInAsPassageSearch holds entity and report candidate
// search to the rule passage search already follows.
//
// All three turn the embedding revision into read-path inference, and all three have the same two
// failure modes: running inference the operator did not ask for, and quietly disabling a capability
// the operator did ask for. So an unset revision is no search and no error, a revision without a
// usable provider refuses to start, and a revision with one builds the search. Only the passage half
// of that was ever exercised; a regression in either of these would have shipped unseen.
func TestCandidateSearchRequiresTheSameExplicitOptInAsPassageSearch(t *testing.T) {
	schema, _ := pg.NewSchema("candidate_config")
	reset := func() {
		for _, key := range []string{inference.EnvEmbeddingRevision, inference.EnvEndpoint, inference.EnvEmbeddingEndpoint, inference.EnvEmbeddingModel, inference.EnvModel, inference.EnvAllowlist} {
			t.Setenv(key, "")
		}
	}
	optIn := func() { t.Setenv(inference.EnvEmbeddingRevision, "v1") }
	provide := func() {
		t.Setenv(inference.EnvEmbeddingEndpoint, "http://localhost:11434/v1")
		t.Setenv(inference.EnvEmbeddingModel, "embedder")
		t.Setenv(inference.EnvAllowlist, "localhost:11434")
	}

	reset()
	if r, err := configuredEntityCandidates(nil, schema); err != nil || r != nil {
		t.Fatalf("entity candidate search with no revision must be off without error, got %v (%v)", r, err)
	}
	optIn()
	if _, err := configuredEntityCandidates(nil, schema); err == nil || !strings.Contains(err.Error(), "configure entity candidate search") {
		t.Fatalf("a revision with no provider must refuse to start entity candidate search, got %v", err)
	}
	provide()
	if r, err := configuredEntityCandidates(nil, schema); err != nil || r == nil {
		t.Fatalf("an embedding-only configuration must build entity candidate search, got %v (%v)", r, err)
	}

	reset()
	if r, err := configuredReportCandidates(nil, schema); err != nil || r != nil {
		t.Fatalf("report candidate search with no revision must be off without error, got %v (%v)", r, err)
	}
	optIn()
	if _, err := configuredReportCandidates(nil, schema); err == nil || !strings.Contains(err.Error(), "configure report candidate search") {
		t.Fatalf("a revision with no provider must refuse to start report candidate search, got %v", err)
	}
	provide()
	if r, err := configuredReportCandidates(nil, schema); err != nil || r == nil {
		t.Fatalf("an embedding-only configuration must build report candidate search, got %v (%v)", r, err)
	}
}
