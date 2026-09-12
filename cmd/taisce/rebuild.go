// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/ensera-ai/taisce/internal/extract"
	"github.com/ensera-ai/taisce/internal/formation"
	"github.com/ensera-ai/taisce/internal/infra/inference"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
)

// The explicit operation key survives a broken output stream. Repeating it for this source and
// configuration reads the committed outcome and finishes freshness without spending model calls.
func rebuildCommand(ctx context.Context, args []string, out io.Writer) error {
	if len(args) > 0 && (args[0] == "project" || args[0] == "status" || args[0] == "cancel") {
		return projectRebuildCommand(ctx, args, out)
	}
	if len(args) > 0 && args[0] == "reports" {
		return reportRebuildCommand(ctx, args[1:], out)
	}
	if len(args) == 0 || args[0] != "facts" {
		return fmt.Errorf("usage: taisce rebuild facts --project <name> --source <observation-uuid> --key <operation-uuid>")
	}
	flags := flag.NewFlagSet("rebuild facts", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	scope := flags.String("project", "", "project")
	source := flags.String("source", "", "source observation")
	key := flags.String("key", "", "stable operation key")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if _, err := pg.NewSchema(*scope); err != nil || flags.NArg() != 0 {
		return pg.ErrInvalidGeneration
	}
	for _, v := range []string{*source, *key} {
		id, err := uuid.Parse(v)
		if err != nil || id == uuid.Nil {
			return pg.ErrInvalidGeneration
		}
	}
	// The same bound as a formation attempt, and from the same setting: a rebuild is one attempt
	// under the current identity, and a document that needed a longer budget to form needs it here.
	budget, err := configuredTurnBudget(formation.DefaultPolicy().TurnBudget)
	if err != nil {
		return err
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
	config, err := inference.ConfigFromEnv()
	if err != nil {
		return err
	}
	config.Timeout = budget
	vocabulary, err := pg.LoadVocabulary(ctx, pool, schema)
	if err != nil {
		return err
	}
	r := formation.NewRebuilder(pg.NewFactStore(pool), extract.NewWith(inference.NewModel(config), vocabulary))
	result, err := r.Rebuild(ctx, schema, *scope, *source, *key, uuid.NewString())
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(withStaleness(ctx, pool, schema, *scope, result))
}
