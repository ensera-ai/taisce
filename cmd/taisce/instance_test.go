// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// isolatedInstance gives a test an empty database of its own, initialised the way the shipped
// PostgreSQL image initialises one, and returns a pool connected to it.
//
// Bootstrap establishes roles, and roles are cluster-wide rather than database-local, so two
// suites bootstrapping at once would race on the same names. The advisory lock is taken on the
// parent test database to serialise that; the child database is dropped when the test ends.
func isolatedInstance(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	env := complete(t)
	owner, err := pgxpool.New(ctx, env[envMemoryDSN])
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.Close)
	locked, err := owner.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := locked.Exec(ctx, `SELECT pg_advisory_lock(hashtext('taisce:establish-planes')::bigint)`); err != nil {
		t.Fatal(err)
	}
	name := "taisce_instance_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err := owner.Exec(ctx, "CREATE DATABASE "+quoted); err != nil {
		t.Fatal(err)
	}
	// Cleanups run in reverse, so this one is registered before the two that give the lock up and
	// therefore runs after them: the unlock has to reach the connection holding the lock while it
	// still holds it, and the database cannot be dropped while anything is connected to it.
	t.Cleanup(func() {
		if _, err := owner.Exec(context.WithoutCancel(ctx), "DROP DATABASE "+quoted); err != nil {
			t.Errorf("remove isolated database: %v", err)
		}
	})
	t.Cleanup(locked.Release)
	t.Cleanup(func() {
		locked.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock(hashtext('taisce:establish-planes')::bigint)`)
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
	// Closed before the database is dropped, because a dropped database cannot have connections.
	t.Cleanup(pool.Close)
	initSQL, err := os.ReadFile("../../deploy/postgres/initdb/00-extensions.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(initSQL)); err != nil {
		t.Fatal(err)
	}
	return ctx, pool
}

func TestDefaultInstanceBootstrapRestartAndProjectCreationUseOneMemoryNamespace(t *testing.T) {
	ctx, pool := isolatedInstance(t)
	for i := 0; i < 2; i++ {
		if err := bootstrap(ctx, quiet(), []string{"--project", "first"}, io.Discard); err != nil {
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

// The two tokens bootstrap prints are the only copies of themselves, and in a cluster the resource
// holding that output does not survive the next upgrade. Nothing is lost that cannot be minted
// again — but only if the operator is told so beside the tokens, with the command, at the moment
// they are looking at them. Printing a secret and saying nothing about replacing it is what turns
// an ordinary upgrade into a dead end.
//
// This test holds that property for the run that mints; the one below holds it for the run that
// does not, which is the run an operator reaches when they go back for a token already gone.
func TestBootstrapSaysHowToReplaceTheTokensItShowsOnce(t *testing.T) {
	ctx, _ := isolatedInstance(t)
	var shown bytes.Buffer
	if err := bootstrap(ctx, quiet(), []string{"--project", "first"}, &shown); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"operator token: ",
		"only digests are stored",
		"Copy it now",
		"taisce operator issue <name>",
		"taisce credential issue <name> -project first",
		"taisce credential revoke <id>",
	} {
		if !strings.Contains(shown.String(), want) {
			t.Errorf("the output that shows the tokens does not say %q:\n%s", want, shown.String())
		}
	}
}

// A second bootstrap mints nothing, so it shows no token — and must not print advice about tokens
// it did not show, which would read as if something had just been issued. The way out belongs in
// the line that says nothing happened, because that line is the whole of what an operator sees in
// the log of every bootstrap after the first.
func TestABootstrapThatMintsNothingShowsNoTokenAndStillNamesTheWayToMintOne(t *testing.T) {
	ctx, _ := isolatedInstance(t)
	if err := bootstrap(ctx, quiet(), []string{"--project", "first"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	var shown, logged bytes.Buffer
	if err := bootstrap(ctx, slog.New(slog.NewTextHandler(&logged, nil)), []string{"--project", "first"}, &shown); err != nil {
		t.Fatal(err)
	}
	if shown.Len() != 0 {
		t.Errorf("a bootstrap that minted nothing printed something:\n%s", shown.String())
	}
	if !strings.Contains(logged.String(), "no credential minted") {
		t.Errorf("the log does not say that nothing was minted:\n%s", logged.String())
	}
	if !strings.Contains(logged.String(), "taisce credential issue <name> -project first") {
		t.Errorf("the log does not name the command that mints one:\n%s", logged.String())
	}
}
