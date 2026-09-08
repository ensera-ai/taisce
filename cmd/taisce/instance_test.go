// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDefaultInstanceBootstrapRestartAndProjectCreationUseOneMemoryNamespace(t *testing.T) {
	ctx := context.Background()
	env := complete(t)
	owner, err := pgxpool.New(ctx, env[envMemoryDSN])
	if err != nil {
		t.Fatal(err)
	}
	name := "taisce_instance_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err := owner.Exec(ctx, "CREATE DATABASE "+quoted); err != nil {
		owner.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, err := owner.Exec(ctx, "DROP DATABASE "+quoted)
		owner.Close()
		if err != nil {
			t.Errorf("remove isolated database: %v", err)
		}
	})
	parsed, err := url.Parse(env[envMemoryDSN])
	if err != nil {
		t.Fatal(err)
	}
	parsed.Path = "/" + name
	for _, key := range []string{envMemoryDSN, envRegistryDSN, envAdminDSN} {
		env[key] = parsed.String()
	}
	env[envSchema] = ""
	env["TAISCE_CONTROL_PASSWORD"] = "taisce-test-control"
	env["TAISCE_DATA_PASSWORD"] = "taisce-test-data"
	withEnv(t, env)
	pool, err := pgxpool.New(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	// Match the shipped PostgreSQL image's initialization before application bootstrap.
	initSQL, err := os.ReadFile("../../deploy/postgres/initdb/00-extensions.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(initSQL)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := bootstrap(ctx, quiet(), []string{"--project", "first"}); err != nil {
			t.Fatal(err)
		}
	}
	var before int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_namespace`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := dispatch(ctx, quiet(), []string{"project", "create", "second"}); err != nil {
		t.Fatal(err)
	}
	var after, projects, credentials int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM pg_namespace),(SELECT count(*) FROM memory.project),(SELECT count(*) FROM control.credential)`).Scan(&after, &projects, &credentials); err != nil {
		t.Fatal(err)
	}
	if before != after || projects != 2 || credentials != 1 {
		t.Fatalf("restart or project creation changed namespaces/credentials: %d -> %d, projects=%d credentials=%d", before, after, projects, credentials)
	}
	var registry bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema='control' AND table_name LIKE '%tenant%')`).Scan(&registry); err != nil || registry {
		t.Fatalf("unexpected tenant registry: %v %v", registry, err)
	}
}
