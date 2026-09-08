// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
)

// Read current usage or replace all four operator limits atomically. Requiring a complete policy
// avoids a read-modify-write race between two operators changing different fields.
func artifactCommand(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 || args[0] != "limits" {
		return fmt.Errorf("usage: taisce artifact limits --project <name> [--max-object-bytes N --max-bytes N --max-objects N --max-age-hours N]")
	}
	flags := flag.NewFlagSet("artifact limits", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	scope := flags.String("project", "", "project to inspect or configure")
	object := flags.Int64("max-object-bytes", 0, "maximum bytes per object")
	total := flags.Int64("max-bytes", 0, "maximum project artifact bytes")
	objects := flags.Int64("max-objects", 0, "maximum project artifact count")
	age := flags.Int("max-age-hours", 0, "maximum lifetime of new objects")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if _, err := pg.NewSchema(*scope); err != nil || flags.NArg() != 0 {
		return pg.ErrInvalidArtifact
	}
	updates := 0
	flags.Visit(func(f *flag.Flag) {
		if f.Name != "project" {
			updates++
		}
	})
	limits := pg.ArtifactLimits{MaxObjectBytes: *object, MaxBytes: *total, MaxObjects: *objects, MaxAgeHours: *age}
	if updates != 0 {
		if updates != 4 {
			return fmt.Errorf("all four storage limits must be supplied together")
		}
		if err := limits.Validate(); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	pool, schema, err := adminPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := requireProject(ctx, pool, *scope); err != nil {
		return err
	}
	store := pg.NewArtifactStore(pool, schema)
	principal := ""
	if updates != 0 {
		principal = uuid.NewString()
		if err := store.ConfigureLimits(ctx, *scope, principal, limits); err != nil {
			return err
		}
	}
	current, err := store.Limits(ctx, *scope)
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(struct {
		pg.ArtifactLimits
		AuditPrincipal string `json:"audit_principal,omitempty"`
	}{current, principal})
}
