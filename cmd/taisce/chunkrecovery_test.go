// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestChunkRecoveryCommandValidatesAndReportsContentFreeProgress(t *testing.T) {
	ctx := context.Background()
	for _, args := range [][]string{{}, {"--bad"}, {"--project", "p1", "extra"}, {"--project", "p1", "--source", "bad"}, {"--project", "p1", "--limit", "0"}, {"--project", "p1", "--limit", "101"}, {"--project", "p1", "--after-offset", "0"}, {"--project", "p1", "--after-ordinal", "0"}, {"--project", "p1", "--after-offset", "-2"}} {
		if err := chunkRecoveryCommand(ctx, args, io.Discard); err == nil {
			t.Fatalf("invalid arguments accepted %v", args)
		}
	}
	env := complete(t)
	env[envSchema] = "cmd_chunk_recovery"
	env[envAdminDSN] = env[envMemoryDSN]
	withEnv(t, env)
	pool, err := pgxpool.New(ctx, env[envAdminDSN])
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	dropSchema(t, env[envSchema])
	defer pool.Exec(ctx, "DROP SCHEMA cmd_chunk_recovery CASCADE")
	if err := migrate.ProvisionMemorySchema(ctx, pool, env[envSchema]); err != nil {
		t.Fatal(err)
	}
	if err := migrate.ProvisionScope(ctx, pool, env[envSchema], "p1"); err != nil {
		t.Fatal(err)
	}
	schema, _ := pg.NewSchema(env[envSchema])
	source, err := pg.NewObservationStore(pool).Append(ctx, schema, domain.Turn{Scope: "p1", DataSubjectID: "private-owner", OccurredAt: time.Now(), Messages: []domain.Message{{Role: domain.RoleUser, Content: "private-text"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`DELETE FROM {schema}.chunk`)); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	args := []string{"chunks", "--project", "p1", "--source", source.ID, "--limit", "1"}
	if err := recoveryCommand(ctx, args, &out); err != nil {
		t.Fatal(err)
	}
	var page pg.ChunkRecoveryPage
	if err := json.Unmarshal(out.Bytes(), &page); err != nil || page.Restored != 1 || page.Examined != 1 || bytes.Contains(out.Bytes(), []byte("private")) {
		t.Fatalf("progress %s %v", out.String(), err)
	}
	if err := recoveryCommand(ctx, args, failedRecoveryOutput{}); err == nil {
		t.Fatal("output error ignored")
	}
	if err := chunkRecoveryCommand(ctx, []string{"--project", "missing"}, io.Discard); err == nil {
		t.Fatal("unknown project accepted")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := recoveryCommand(canceled, args, io.Discard); err == nil {
		t.Fatal("cancellation ignored")
	}
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.chunk SET text='conflict'`)); err != nil {
		t.Fatal(err)
	}
	if err := recoveryCommand(ctx, args, io.Discard); err == nil {
		t.Fatal("storage conflict ignored")
	}
	t.Setenv(envAdminDSN, "")
	t.Setenv(envMemoryDSN, "")
	if err := recoveryCommand(ctx, args, io.Discard); err == nil {
		t.Fatal("missing connection accepted")
	}
}
