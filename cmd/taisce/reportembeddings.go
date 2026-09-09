// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/ensera-ai/taisce/internal/infra/inference"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/reportcandidate"
	"github.com/google/uuid"
)

func reportEmbeddingsCommand(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: taisce report-embeddings <start|build|status|activate|cancel|prune|repair|search> --project <name>")
	}
	operation := args[0]
	switch operation {
	case "start", "build", "status", "activate", "cancel", "prune", "repair", "search":
	default:
		return fmt.Errorf("unknown report-embeddings operation %q", operation)
	}
	flags := flag.NewFlagSet("report-embeddings "+operation, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	scope := flags.String("project", "", "project")
	generation := flags.String("generation", "", "generation UUID; status defaults to the active generation")
	key := flags.String("key", "", "idempotent start operation UUID")
	revision := flags.String("revision", "", "operator-declared immutable embedding model revision")
	dimensions := flags.Int("dimensions", 0, "embedding model dimensions; required when starting")
	limit := flags.Int("limit", 32, "build/prune page size or search result limit")
	candidates := flags.Int("candidates", 0, "opt into approximate search with this candidate bound")
	question := flags.String("query", "", "text used to find thematic reports")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if _, err := pg.NewSchema(*scope); err != nil || flags.NArg() != 0 || *limit < 1 || *limit > 64 {
		return pg.ErrInvalidEmbedding
	}
	for _, value := range []string{*generation, *key} {
		if value != "" {
			if id, err := uuid.Parse(value); err != nil || id == uuid.Nil {
				return pg.ErrInvalidEmbedding
			}
		}
	}
	if operation == "start" && (*key == "" || *dimensions < 1 || *dimensions > pg.MaxStoredEmbeddingDimensions) {
		return fmt.Errorf("start requires --key and --dimensions (1..4000)")
	}
	if operation != "start" && operation != "status" && operation != "search" && *generation == "" {
		return fmt.Errorf("--generation is required")
	}
	if operation == "search" {
		if err := (reportcandidate.Query{Question: *question, Limit: *limit, Candidates: *candidates}).Validate(); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	pool, schema, err := adminPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := requireProject(ctx, pool, *scope); err != nil {
		return err
	}
	store := pg.NewReportEmbeddingStore(pool, schema)
	actor := uuid.NewString()
	var result any
	switch operation {
	case "status":
		if *generation == "" {
			result, err = store.Active(ctx, *scope)
		} else {
			result, err = store.Generation(ctx, *scope, *generation)
		}
	case "activate":
		err = store.Activate(ctx, *scope, *generation, actor)
	case "cancel":
		err = store.Cancel(ctx, *scope, *generation, actor)
	case "repair":
		err = store.Repair(ctx, *scope, *generation, actor)
	case "prune":
		result, err = store.Prune(ctx, *scope, *generation, actor, *limit)
	default:
		config, configErr := inference.EmbeddingConfigFromEnv()
		if configErr != nil {
			return configErr
		}
		endpoint, _ := config.EmbeddingsAt()
		digest := sha256.Sum256([]byte(endpoint))
		model := pg.EmbeddingModel{Name: config.EmbeddingModel, Revision: *revision,
			EndpointHash: hex.EncodeToString(digest[:]), Dimensions: *dimensions}
		embedder := inference.NewEmbedder(config)
		if operation == "start" {
			result, err = store.Start(ctx, *scope, *key, actor, model)
		} else {
			var current pg.ReportEmbeddingGeneration
			if operation == "search" {
				current, err = store.Active(ctx, *scope)
			} else {
				current, err = store.Generation(ctx, *scope, *generation)
			}
			if err != nil {
				return err
			}
			if operation == "search" && current.State != "ready" {
				return pg.ErrEmbeddingConflict
			}
			model.Dimensions = current.Model.Dimensions
			if operation == "build" {
				result, err = store.BuildPage(ctx, *scope, *generation, actor, model, *limit, embedder.Embed)
			} else {
				vectors, embedErr := embedder.Embed(ctx, []string{*question})
				if embedErr != nil {
					return embedErr
				}
				if len(vectors) != 1 {
					return pg.ErrInvalidEmbedding
				}
				result, err = store.Search(ctx, *scope, actor, model, vectors[0], pg.ReportCandidateOptions{
					Limit: *limit, Candidates: *candidates,
				})
			}
		}
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(struct {
		Operation      string `json:"operation"`
		AuditPrincipal string `json:"audit_principal"`
		Result         any    `json:"result,omitempty"`
	}{operation, actor, result})
}
