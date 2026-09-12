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
