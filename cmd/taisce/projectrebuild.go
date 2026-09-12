// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"time"

	"github.com/ensera-ai/taisce/internal/extract"
	"github.com/ensera-ai/taisce/internal/formation"
	"github.com/ensera-ai/taisce/internal/infra/inference"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
)

// Status and cancellation need operator database authority but no model configuration. They remain
// available when the provider is down or the deployed model has changed since the job was created.
func projectRebuildCommand(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("rebuild "+args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	scope := flags.String("project", "", "project")
	key := flags.String("key", "", "stable project operation key")
	limit := flags.Int("limit", 10, "sources per invocation (1..100)")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if _, err := pg.NewSchema(*scope); err != nil || flags.NArg() != 0 || *limit < 1 || *limit > formation.MaxRebuildPage {
		return pg.ErrInvalidGeneration
	}
	if id, err := uuid.Parse(*key); err != nil || id == uuid.Nil {
		return pg.ErrInvalidGeneration
	}
	budget := formation.DefaultPolicy().TurnBudget
	if args[0] == "project" {
		budget *= time.Duration(*limit)
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	pool, schema, err := adminPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := requireProject(ctx, pool, *scope); err != nil {
		return err
	}
	jobs := pg.NewRebuildJobStore(pool)
	var job pg.RebuildJob
	stopActivity := func() {}
	if args[0] == "project" {
		stopActivity = startCLIActivity(ctx, "rebuilding retained sources")
	}
	defer stopActivity()
	switch args[0] {
	case "status":
		job, err = jobs.Status(ctx, schema, *scope, *key)
	case "cancel":
		job, err = jobs.Cancel(ctx, schema, *scope, *key, uuid.NewString())
	default:
		var config inference.Config
		config, err = inference.ConfigFromEnv()
		if err != nil {
			return err
		}
		vocabulary, loadErr := pg.LoadVocabulary(ctx, pool, schema)
		if loadErr != nil {
			return loadErr
		}
		rebuilder := formation.NewRebuilder(pg.NewFactStore(pool), extract.NewWith(inference.NewModel(config), vocabulary))
		job, err = formation.NewProjectRebuilder(rebuilder, jobs).RunPage(ctx, schema, *scope, *key, uuid.NewString(), *limit)
	}
	if err != nil {
		return err
	}
	if fancyOutput(out) {
		_, err = io.WriteString(out, renderRebuild(currentTheme(), job))
		return err
	}
	return json.NewEncoder(out).Encode(withStaleness(ctx, pool, schema, *scope, job))
}
