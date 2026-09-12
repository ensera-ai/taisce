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
	t.Cleanup(owner.Close)
	// Advisory locks are database-local, but these test roles are cluster-wide. Coordinate the
	// child database's bootstrap with suites using the parent test database's bootstrap lock.
	locked, err := owner.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer locked.Release()
	if _, err := locked.Exec(ctx, `SELECT pg_advisory_lock(hashtext('taisce:establish-planes')::bigint)`); err != nil {
		t.Fatal(err)
	}
	defer locked.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock(hashtext('taisce:establish-planes')::bigint)`)
	name := "taisce_instance_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err := owner.Exec(ctx, "CREATE DATABASE "+quoted); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, err := owner.Exec(ctx, "DROP DATABASE "+quoted)
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
	var after, projects, credentials, operators int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM pg_namespace),(SELECT count(*) FROM memory.project),
		(SELECT count(*) FROM control.credential WHERE kind='project'),(SELECT count(*) FROM control.credential WHERE kind='operator')`).Scan(&after, &projects, &credentials, &operators); err != nil {
		t.Fatal(err)
	}
	// Two bootstraps mint one project credential and one operator credential, not two of either.
	if before != after || projects != 2 || credentials != 1 || operators != 1 {
		t.Fatalf("restart or project creation changed namespaces/credentials: %d -> %d, projects=%d credentials=%d operators=%d", before, after, projects, credentials, operators)
	}
	var registry bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema='control' AND table_name LIKE '%tenant%')`).Scan(&registry); err != nil || registry {
		t.Fatalf("unexpected tenant registry: %v %v", registry, err)
	}
}
