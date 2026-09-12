// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"github.com/ensera-ai/taisce/internal/entitycandidate"
	"github.com/ensera-ai/taisce/internal/infra/inference"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/passage"
	"github.com/ensera-ai/taisce/internal/reportcandidate"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Revision is explicit opt-in to read-path inference. Once set, an invalid endpoint/model fails
// startup rather than quietly disabling a capability the operator requested.
func configuredPassages(pool *pgxpool.Pool, schema pg.Schema) (*passage.Retriever, error) {
	revision := strings.TrimSpace(os.Getenv(inference.EnvEmbeddingRevision))
	if revision == "" {
		return nil, nil
	}
	config, err := inference.EmbeddingConfigFromEnv()
	if err != nil {
		return nil, fmt.Errorf("configure passage search: %w", err)
	}
	endpoint, _ := config.EmbeddingsAt()
	digest := sha256.Sum256([]byte(endpoint))
	retriever, err := passage.New(pg.NewMessageEmbeddingStore(pool, schema), inference.NewEmbedder(config), passage.Binding{Name: config.EmbeddingModel, Revision: revision, EndpointHash: hex.EncodeToString(digest[:])})
	if err != nil {
		return nil, fmt.Errorf("configure passage search: %w", err)
	}
	return retriever, nil
}

func configuredEntityCandidates(pool *pgxpool.Pool, schema pg.Schema) (*entitycandidate.Retriever, error) {
	revision := strings.TrimSpace(os.Getenv(inference.EnvEmbeddingRevision))
	if revision == "" {
		return nil, nil
	}
	config, err := inference.EmbeddingConfigFromEnv()
	if err != nil {
		return nil, fmt.Errorf("configure entity candidate search: %w", err)
	}
	endpoint, _ := config.EmbeddingsAt()
	digest := sha256.Sum256([]byte(endpoint))
	retriever, err := entitycandidate.New(pg.NewEntityEmbeddingStore(pool, schema), inference.NewEmbedder(config),
		entitycandidate.Binding{Name: config.EmbeddingModel, Revision: revision, EndpointHash: hex.EncodeToString(digest[:])})
	if err != nil {
		return nil, fmt.Errorf("configure entity candidate search: %w", err)
	}
	return retriever, nil
}

func configuredReportCandidates(pool *pgxpool.Pool, schema pg.Schema) (*reportcandidate.Retriever, error) {
	revision := strings.TrimSpace(os.Getenv(inference.EnvEmbeddingRevision))
	if revision == "" {
		return nil, nil
	}
	config, err := inference.EmbeddingConfigFromEnv()
	if err != nil {
		return nil, fmt.Errorf("configure report candidate search: %w", err)
	}
	endpoint, _ := config.EmbeddingsAt()
	digest := sha256.Sum256([]byte(endpoint))
	retriever, err := reportcandidate.New(pg.NewReportEmbeddingStore(pool, schema), inference.NewEmbedder(config),
		reportcandidate.Binding{Name: config.EmbeddingModel, Revision: revision, EndpointHash: hex.EncodeToString(digest[:])})
	if err != nil {
		return nil, fmt.Errorf("configure report candidate search: %w", err)
	}
	return retriever, nil
}
