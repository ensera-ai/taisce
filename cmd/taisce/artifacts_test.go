// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestArtifactLimitCommandRefusesIncompletePoliciesBeforeConnecting(t *testing.T) {
	for _, args := range [][]string{nil, {"bad"}, {"limits"}, {"limits", "--bad"}, {"limits", "--project", "p1", "extra"}, {"limits", "--project", "p1", "--max-bytes", "1"}, {"limits", "--project", "p1", "--max-object-bytes", "0", "--max-bytes", "1", "--max-objects", "1", "--max-age-hours", "1"}} {
		if err := artifactCommand(context.Background(), args, io.Discard); err == nil {
			t.Fatalf("invalid limits accepted: %v", args)
		}
	}
	t.Setenv(envAdminDSN, "")
	t.Setenv(envMemoryDSN, "")
	if err := artifactCommand(context.Background(), []string{"limits", "--project", "p1"}, io.Discard); err == nil {
		t.Fatal("missing connection accepted")
	}
}
func TestArtifactOperatorCanInspectAndAtomicallyReplaceStorageAllowances(t *testing.T) {
	ctx := context.Background()
	env := complete(t)
	env[envSchema] = "cmd_artifacts"
	env[envAdminDSN] = env[envMemoryDSN]
	withEnv(t, env)
	pool, err := pgxpool.New(ctx, env[envAdminDSN])
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	dropSchema(t, env[envSchema])
	defer pool.Exec(ctx, "DROP SCHEMA cmd_artifacts CASCADE")
	if err := migrate.ProvisionMemorySchema(ctx, pool, env[envSchema]); err != nil {
		t.Fatal(err)
	}
	if err := migrate.ProvisionScope(ctx, pool, env[envSchema], "p1"); err != nil {
		t.Fatal(err)
	}
	read := []string{"limits", "--project", "p1"}
	var out bytes.Buffer
	if err := artifactCommand(ctx, read, &out); err != nil {
		t.Fatal(err)
	}
	var before pg.ArtifactLimits
	if err := json.Unmarshal(out.Bytes(), &before); err != nil || before.MaxBytes != 16<<20 || before.UsedBytes != 0 {
		t.Fatal("incorrect default allowance")
	}
	write := append(append([]string{}, read...), "--max-object-bytes", "8", "--max-bytes", "32", "--max-objects", "4", "--max-age-hours", "2")
	out.Reset()
	if err := artifactCommand(ctx, write, &out); err != nil {
		t.Fatal(err)
	}
	var after struct {
		pg.ArtifactLimits
		Principal string `json:"audit_principal"`
	}
	if err := json.Unmarshal(out.Bytes(), &after); err != nil || after.MaxBytes != 32 || after.Principal == "" {
		t.Fatal("operator change not reported")
	}
	if err := artifactCommand(ctx, read, failedRecoveryOutput{}); err == nil {
		t.Fatal("output failure swallowed")
	}
	if err := artifactCommand(ctx, []string{"limits", "--project", "missing"}, io.Discard); err == nil {
		t.Fatal("missing project accepted")
	}
	if err := dispatch(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), append([]string{"artifact"}, read...)); err != nil {
		t.Fatal(err)
	}
	schema, _ := pg.NewSchema(env[envSchema])
	if _, err := pool.Exec(ctx, schema.SQL(`ALTER TABLE {schema}.audit_entry ADD CONSTRAINT no_config CHECK(operation<>'artifact.configure') NOT VALID`)); err != nil {
		t.Fatal(err)
	}
	if err := artifactCommand(ctx, write, io.Discard); err == nil {
		t.Fatal("unaudited operator configuration accepted")
	}
	if _, err := pool.Exec(ctx, schema.SQL(`DROP TABLE {schema}.agent_storage_policy CASCADE`)); err != nil {
		t.Fatal(err)
	}
	if err := artifactCommand(ctx, read, io.Discard); err == nil {
		t.Fatal("missing policy table hidden")
	}
}
