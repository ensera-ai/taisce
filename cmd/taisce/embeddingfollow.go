// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/ensera-ai/taisce/internal/infra/inference"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/google/uuid"
)

// Following is explicit read-path maintenance, under the unprivileged memory identity. It cannot
// allocate or activate a model generation, and it never follows an operator's model switch silently.
func embeddingFollowCommand(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("embeddings follow", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	project := flags.String("project", "", "project")
	generation := flags.String("generation", "", "active generation UUID")
	revision := flags.String("revision", "", "configured immutable model revision")
	limit := flags.Int("limit", 32, "messages per bounded page (1..64)")
	watch := flags.Bool("watch", false, "continue bounded pages until cancelled")
	interval := flags.Duration("interval", 2*time.Second, "wait between pages (100ms..1m); failures back off to 1m")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if _, err := pg.NewSchema(*project); err != nil || flags.NArg() != 0 || *limit < 1 || *limit > pg.MaxEmbeddingPage || *interval < 100*time.Millisecond || *interval > time.Minute {
		return pg.ErrInvalidEmbedding
	}
	id, err := uuid.Parse(*generation)
	if err != nil || id == uuid.Nil {
		return pg.ErrInvalidEmbedding
	}
	*generation = id.String()
	config, err := inference.EmbeddingConfigFromEnv()
	if err != nil {
		return err
	}
	endpoint, _ := config.EmbeddingsAt()
	digest := sha256.Sum256([]byte(endpoint))
	model := pg.EmbeddingModel{Name: config.EmbeddingModel, Revision: *revision, EndpointHash: hex.EncodeToString(digest[:]), Dimensions: 1}
	if err := model.Validate(); err != nil {
		return err
	}
	dsn := strings.TrimSpace(os.Getenv(envMemoryDSN))
	if dsn == "" {
		return fmt.Errorf("%s is required for the embedding worker", envMemoryDSN)
	}
	schema, err := configuredMemorySchema()
	if err != nil {
		return err
	}
	startup, cancelStartup := context.WithTimeout(ctx, 10*time.Second)
	defer cancelStartup()
	pool, err := migrate.NewRuntimePool(startup, dsn, schema, migrate.MemoryPlane)
	if err != nil {
		return err
	}
	defer pool.Close()
	store := pg.NewMessageEmbeddingStore(pool, schema)
	current, err := store.Generation(startup, *project, *generation)
	if err != nil {
		return err
	}
	model.Dimensions = current.Model.Dimensions
	if model.Identity() != current.Model.Identity() || !current.Active {
		return pg.ErrEmbeddingConflict
	}
	cancelStartup()
	embedder := inference.NewEmbedder(config)
	actor := uuid.NewString()
	encoder := json.NewEncoder(out)
	delay := *interval
	previous := ""
	for {
		pageCtx, cancelPage := context.WithTimeout(ctx, 2*time.Minute)
		page, pageErr := store.CatchUpPage(pageCtx, *project, *generation, actor, model, *limit, embedder.Embed)
		progress := pg.EmbeddingGeneration{ThroughOffset: -1, CoveredThroughOffset: -1}
		if pageErr == nil {
			var observed pg.EmbeddingGeneration
			observed, pageErr = store.Generation(pageCtx, *project, *generation)
			if pageErr == nil {
				progress = observed
			}
		}
		cancelPage()
		if ctx.Err() != nil {
			return nil
		}
		state := "ready"
		if pageErr != nil {
			if !*watch || errors.Is(pageErr, pg.ErrEmbeddingConflict) || errors.Is(pageErr, pg.ErrInvalidEmbedding) || errors.Is(pageErr, pg.ErrEmbeddingNotFound) || errors.Is(pageErr, pg.ErrEmbeddingIncomplete) || errors.Is(pageErr, pg.ErrNoSuchProject) {
				return pageErr
			}
			state = "retrying"
		} else if !page.Complete {
			state = "building"
		}
		signature := fmt.Sprintf("%s/%d/%d", state, progress.ThroughOffset, progress.CoveredThroughOffset)
		if !*watch || signature != previous || page.Examined > 0 {
			// Error payloads can contain provider input. A retry signal exposes no error text or vectors.
			if err := encoder.Encode(struct {
				Operation  string                `json:"operation"`
				Generation string                `json:"generation_id"`
				Principal  string                `json:"audit_principal"`
				State      string                `json:"state"`
				Target     int64                 `json:"through_offset"`
				Covered    int64                 `json:"covered_through_offset"`
				Page       pg.EmbeddingBuildPage `json:"page"`
			}{"follow", *generation, actor, state, progress.ThroughOffset, progress.CoveredThroughOffset, page}); err != nil {
				return err
			}
			previous = signature
		}
		if !*watch {
			return nil
		}
		if pageErr == nil {
			delay = *interval
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		if pageErr != nil {
			delay = min(delay*2, time.Minute)
		}
	}
}
