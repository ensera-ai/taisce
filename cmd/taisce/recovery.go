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

// One page per invocation bounds transaction lifetime. The operator can persist the returned cursor;
// if the process dies or stdout fails after commit, replaying the same page is safe.
func recoveryCommand(ctx context.Context, args []string, out io.Writer) error {
	if len(args) > 0 && args[0] == "chunks" {
		return chunkRecoveryCommand(ctx, args[1:], out)
	}
	if len(args) == 0 || args[0] != "facts" {
		return fmt.Errorf("usage: taisce recover facts --project <name> [--source <observation-uuid>] [--after <fact-uuid>] [--limit <1..100>]")
	}
	flags := flag.NewFlagSet("recover facts", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	scope := flags.String("project", "", "project to recover")
	source := flags.String("source", "", "optional source observation")
	after := flags.String("after", "", "exclusive fact cursor")
	limit := flags.Int("limit", 100, "page size, 1..100")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if _, err := pg.NewSchema(*scope); err != nil {
		return fmt.Errorf("--project must be a valid project name")
	}
	if flags.NArg() != 0 || *limit < 1 || *limit > pg.MaxRecoveryPage {
		return pg.ErrInvalidRecovery
	}
	for _, v := range []string{*source, *after} {
		if v != "" {
			if _, err := uuid.Parse(v); err != nil {
				return pg.ErrInvalidRecovery
			}
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
	principal := uuid.NewString()
	stopActivity := startCLIActivity(ctx, "recovering retained facts")
	defer stopActivity()
	page, err := pg.NewFactStore(pool).RecoverFacts(ctx, schema, *scope, *source, *after, principal, *limit)
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(struct {
		pg.RecoveryPage
		AuditPrincipal string `json:"audit_principal"`
	}{page, principal})
}
