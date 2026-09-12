// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"time"

	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
)

func chunkRecoveryCommand(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("recover chunks", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	scope := flags.String("project", "", "project")
	source := flags.String("source", "", "optional source observation")
	offset := flags.Int64("after-offset", -1, "last examined source offset")
	ordinal := flags.Int("after-ordinal", -1, "last examined message ordinal")
	limit := flags.Int("limit", 100, "messages per page")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if _, err := pg.NewSchema(*scope); err != nil {
		return pg.ErrInvalidRecovery
	}
	if flags.NArg() != 0 || *limit < 1 || *limit > pg.MaxRecoveryPage || *offset < -1 || *ordinal < -1 || (*offset == -1) != (*ordinal == -1) {
		return pg.ErrInvalidRecovery
	}
	if *source != "" {
		if id, err := uuid.Parse(*source); err != nil || id == uuid.Nil {
			return pg.ErrInvalidRecovery
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
	actor := uuid.NewString()
	page, err := pg.NewObservationStore(pool).RecoverChunks(ctx, schema, *scope, *source, actor, pg.ChunkRecoveryCursor{Offset: *offset, Ordinal: *ordinal}, *limit)
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(struct {
		pg.ChunkRecoveryPage
		Actor string `json:"audit_principal"`
	}{page, actor})
}
