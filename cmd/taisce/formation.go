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

// formationArgs is a formation command, validated before anything is connected to.
type formationArgs struct {
	op            string
	project       string
	after         int64
	limit         int
	observationID string
}

// unparkResult is what both paths print for an unpark. AuditPrincipal is the principal the direct
// path records in the ledger; over the surface the principal is the operator credential, which the
// operator already holds, so it is omitted there.
type unparkResult struct {
	ObservationID  string `json:"observation_id"`
	Unparked       bool   `json:"unparked"`
	AuditPrincipal string `json:"audit_principal,omitempty"`
}

func parseFormation(args []string) (formationArgs, error) {
	if len(args) == 0 || (args[0] != "parked" && args[0] != "unpark") {
		return formationArgs{}, fmt.Errorf("usage: taisce formation parked --project <name> [--after <offset>] [--limit <1..200>] | taisce formation unpark --project <name> <observation-uuid>")
	}
	f := formationArgs{op: args[0], after: -1, limit: 100}
	flags := flag.NewFlagSet("formation "+args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&f.project, "project", "", "project to inspect or recover")
	if f.op == "parked" {
		flags.Int64Var(&f.after, "after", -1, "exclusive log offset")
		flags.IntVar(&f.limit, "limit", 100, "page size, 1..200")
	}
	if err := flags.Parse(args[1:]); err != nil {
		return formationArgs{}, err
	}
	if _, err := pg.NewSchema(f.project); err != nil {
		return formationArgs{}, fmt.Errorf("--project must be a valid project name")
	}
	if f.after < -1 || f.limit < 1 || f.limit > 200 {
		return formationArgs{}, fmt.Errorf("--after must be >= -1 and --limit must be 1..200")
	}
	if f.op == "parked" && flags.NArg() != 0 {
		return formationArgs{}, fmt.Errorf("parked accepts no positional arguments")
	}
	if f.op == "unpark" {
		if flags.NArg() != 1 {
			return formationArgs{}, fmt.Errorf("unpark requires one observation UUID after the flags")
		}
		if _, err := uuid.Parse(flags.Arg(0)); err != nil {
			return formationArgs{}, fmt.Errorf("invalid observation UUID")
		}
		f.observationID = flags.Arg(0)
	}
	return f, nil
}

func formationCommand(ctx context.Context, args []string, out io.Writer) error {
	f, err := parseFormation(args)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	admin, schema, err := adminPool(ctx)
	if err != nil {
		return err
	}
	defer admin.Close()
	if err := requireProject(ctx, admin, f.project); err != nil {
		return err
	}
	store := pg.NewObservationStore(admin)
	if f.op == "parked" {
		page, err := store.ParkedPage(ctx, schema, f.project, f.after, f.limit)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(page)
	}
	principal := uuid.NewString()
	changed, err := store.UnparkAudited(ctx, schema, f.project, f.observationID, principal)
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(unparkResult{ObservationID: f.observationID, Unparked: changed, AuditPrincipal: principal})
}
