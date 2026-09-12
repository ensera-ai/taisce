// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestProjectRebuildCommandsValidateBeforeConnecting(t *testing.T) {
	t.Setenv(envAdminDSN, "")
	t.Setenv(envMemoryDSN, "")
	for _, args := range [][]string{
		{"project"}, {"status", "--bad"}, {"cancel", "--project", "bad name", "--key", uuid.NewString()},
		{"status", "--project", "p1", "--key", "bad"}, {"project", "--project", "p1", "--key", uuid.NewString(), "--limit", "0"},
		{"project", "--project", "p1", "--key", uuid.NewString(), "--limit", "101"}, {"status", "--project", "p1", "--key", uuid.NewString(), "extra"},
		{"status", "--project", "p1", "--key", uuid.NewString()},
	} {
		if err := rebuildCommand(context.Background(), args, io.Discard); err == nil {
			t.Fatalf("invalid project rebuild request accepted: %v", args)
		}
	}
}

func TestProjectRebuildCommandsProvideProgressAndCancellationWithoutAProvider(t *testing.T) {
	ctx := context.Background()
	env := complete(t)
	env[envSchema] = "cmd_project_rebuild"
	env[envAdminDSN] = env[envMemoryDSN]
	withEnv(t, env)
	t.Setenv("TAISCE_INFERENCE_ENDPOINT", "http://127.0.0.1:1/v1")
	t.Setenv("TAISCE_INFERENCE_EXTRACTOR_MODEL", "project-rebuild-command")
	t.Setenv("TAISCE_INFERENCE_ALLOWLIST", "127.0.0.1:1")
	p, err := pgxpool.New(ctx, env[envAdminDSN])
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	dropSchema(t, env[envSchema])
	defer p.Exec(ctx, "DROP SCHEMA cmd_project_rebuild CASCADE")
	if err := migrate.ProvisionMemorySchema(ctx, p, env[envSchema]); err != nil {
		t.Fatal(err)
	}
	if err := migrate.ProvisionScope(ctx, p, env[envSchema], "p1"); err != nil {
		t.Fatal(err)
	}
	schema, _ := pg.NewSchema(env[envSchema])
	key := uuid.NewString()
	var output bytes.Buffer
	if err := rebuildCommand(ctx, []string{"project", "--project", "p1", "--key", key}, &output); err != nil {
		t.Fatal(err)
	}
	var first pg.RebuildJob
	if err := json.Unmarshal(output.Bytes(), &first); err != nil || first.Status != "completed" || first.Through != -1 {
		t.Fatalf("empty project command failed: %+v %v", first, err)
	}
	if bytes.Contains(output.Bytes(), []byte("lease")) {
		t.Fatal("progress exposed the runner fence")
	}

	t.Run("status without inference configuration", func(t *testing.T) {
		t.Setenv("TAISCE_INFERENCE_ENDPOINT", "")
		var output bytes.Buffer
		if err := rebuildCommand(ctx, []string{"status", "--project", "p1", "--key", key}, &output); err != nil {
			t.Fatal(err)
		}
		var state pg.RebuildJob
		if err := json.Unmarshal(output.Bytes(), &state); err != nil || state.ID != key || state.Status != "completed" {
			t.Fatalf("status failed: %+v %v", state, err)
		}
	})
	t.Run("cancel without inference configuration", func(t *testing.T) {
		t.Setenv("TAISCE_INFERENCE_ENDPOINT", "")
		cancelKey := uuid.NewString()
		session, err := pg.NewRebuildJobStore(p).Acquire(ctx, schema, "p1", cancelKey, first.Version, uuid.NewString())
		if err != nil {
			t.Fatal(err)
		}
		session.Close()
		var output bytes.Buffer
		if err := rebuildCommand(ctx, []string{"cancel", "--project", "p1", "--key", cancelKey}, &output); err != nil {
			t.Fatal(err)
		}
		var state pg.RebuildJob
		if err := json.Unmarshal(output.Bytes(), &state); err != nil || state.Status != "cancelled" {
			t.Fatalf("cancel failed: %+v %v", state, err)
		}
	})
	t.Run("missing project", func(t *testing.T) {
		if err := rebuildCommand(ctx, []string{"status", "--project", "missing_project", "--key", key}, io.Discard); err == nil {
			t.Fatal("missing project accepted")
		}
	})
	t.Run("missing job", func(t *testing.T) {
		if err := rebuildCommand(ctx, []string{"status", "--project", "p1", "--key", uuid.NewString()}, io.Discard); err == nil {
			t.Fatal("missing job accepted")
		}
	})
	t.Run("missing inference configuration", func(t *testing.T) {
		t.Setenv("TAISCE_INFERENCE_ENDPOINT", "")
		if err := rebuildCommand(ctx, []string{"project", "--project", "p1", "--key", uuid.NewString()}, io.Discard); err == nil {
			t.Fatal("missing inference configuration accepted")
		}
	})
	t.Run("missing vocabulary", func(t *testing.T) {
		if _, err := p.Exec(ctx, schema.SQL(`ALTER TABLE {schema}.predicate RENAME TO predicate_unavailable`)); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if _, err := p.Exec(ctx, schema.SQL(`ALTER TABLE {schema}.predicate_unavailable RENAME TO predicate`)); err != nil {
				t.Error(err)
			}
		})
		if err := rebuildCommand(ctx, []string{"project", "--project", "p1", "--key", uuid.NewString()}, io.Discard); err == nil {
			t.Fatal("missing vocabulary accepted")
		}
	})
}
