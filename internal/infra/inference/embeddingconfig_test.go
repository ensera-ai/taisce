// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package inference_test

import (
	"github.com/ensera-ai/taisce/internal/infra/inference"
	"testing"
)

// A read-only service validates only the endpoint it uses, without borrowing a different
// provider's credential or weakening the generation process's independent validation.
func TestEmbeddingOnlyConfigurationBindsItsOwnAllowedEndpoint(t *testing.T) {
	hermetic(t)
	t.Setenv(inference.EnvEmbeddingModel, "embedder")
	if _, err := inference.EmbeddingConfigFromEnv(); err == nil {
		t.Fatal("missing endpoint accepted")
	}
	t.Setenv(inference.EnvEndpoint, "https://generation.example/v1")
	t.Setenv(inference.EnvAPIKey, "generation-secret")
	t.Setenv(inference.EnvEmbeddingEndpoint, "http://localhost:11434/v1/")
	if _, err := inference.EmbeddingConfigFromEnv(); err == nil {
		t.Fatal("empty allowlist accepted")
	}
	t.Setenv(inference.EnvAllowlist, "localhost:11434")
	cfg, err := inference.EmbeddingConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	endpoint, key := cfg.EmbeddingsAt()
	if endpoint != "http://localhost:11434/v1" || key != "" || cfg.Model != "" {
		t.Fatalf("wrong endpoint/key selection: %q", endpoint)
	}
	if _, err := inference.ConfigFromEnv(); err == nil {
		t.Fatal("generation accepted without model")
	}
	t.Setenv(inference.EnvModel, "extractor")
	if _, err := inference.ConfigFromEnv(); err == nil {
		t.Fatal("generation accepted unlisted endpoint")
	}
	t.Setenv(inference.EnvEmbeddingAPIKey, "embedding-secret")
	cfg, err = inference.EmbeddingConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	_, key = cfg.EmbeddingsAt()
	if key != "embedding-secret" {
		t.Fatal("embedding key not selected")
	}
	t.Setenv(inference.EnvEmbeddingModel, "")
	if _, err := inference.EmbeddingConfigFromEnv(); err == nil {
		t.Fatal("missing embedding model accepted")
	}
}
