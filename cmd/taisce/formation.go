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

func formationCommand(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 || (args[0] != "parked" && args[0] != "unpark") {
		return fmt.Errorf("usage: taisce formation parked --project <name> [--after <offset>] [--limit <1..200>] | taisce formation unpark --project <name> <observation-uuid>")
	}
	flags := flag.NewFlagSet("formation "+args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	scope := flags.String("project", "", "project to inspect or recover")
	var after int64 = -1
	limit := 100
	if args[0] == "parked" {
		flags.Int64Var(&after, "after", -1, "exclusive log offset")
		flags.IntVar(&limit, "limit", 100, "page size, 1..200")
	}
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if _, err := pg.NewSchema(*scope); err != nil {
		return fmt.Errorf("--project must be a valid project name")
	}
	if after < -1 || limit < 1 || limit > 200 {
		return fmt.Errorf("--after must be >= -1 and --limit must be 1..200")
	}
	if args[0] == "parked" && flags.NArg() != 0 {
		return fmt.Errorf("parked accepts no positional arguments")
	}
	if args[0] == "unpark" {
		if flags.NArg() != 1 {
			return fmt.Errorf("unpark requires one observation UUID after the flags")
		}
		if _, err := uuid.Parse(flags.Arg(0)); err != nil {
			return fmt.Errorf("invalid observation UUID")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	admin, schema, err := adminPool(ctx)
	if err != nil {
		return err
	}
	defer admin.Close()
	if err := requireProject(ctx, admin, *scope); err != nil {
		return err
	}
	store := pg.NewObservationStore(admin)
	if args[0] == "parked" {
		page, err := store.ParkedPage(ctx, schema, *scope, after, limit)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(page)
	}
	principal := uuid.NewString()
	changed, err := store.UnparkAudited(ctx, schema, *scope, flags.Arg(0), principal)
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(struct {
		Changed        bool   `json:"changed"`
		AuditPrincipal string `json:"audit_principal"`
	}{changed, principal})
}
