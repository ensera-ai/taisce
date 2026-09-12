// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestOperatorCanIssueAnExplicitReadOnlyCredential(t *testing.T) {
	env := complete(t)
	env[envSchema] = "cmd_readonly"
	env[envAdminDSN] = env[envMemoryDSN]
	withEnv(t, env)
	t.Setenv("TAISCE_CONTROL_PASSWORD", "taisce-test-control")
	t.Setenv("TAISCE_DATA_PASSWORD", "taisce-test-data")
	ctx := context.Background()
	project := "readonly_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	dropSchema(t, "cmd_readonly")
	if err := dispatch(ctx, quiet(), []string{"bootstrap", "-project", project}); err != nil {
		t.Fatal(err)
	}
	if err := dispatch(ctx, quiet(), []string{"credential", "issue", "reader", "--project", project, "--read-only"}); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, os.Getenv("TAISCE_TEST_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM control.credential WHERE project=$1 AND name='reader' AND access='read_only' AND left(token_prefix,4)='tsk_' AND revoked_at IS NULL`, project).Scan(&count); err != nil || count != 1 {
		t.Fatalf("read-only issuance: %d %v", count, err)
	}
}

func TestMisplacedReadOnlyFlagCannotSilentlyCreateAWriter(t *testing.T) {
	env := complete(t)
	env[envSchema] = "cmd_readonly_args"
	env[envAdminDSN] = env[envMemoryDSN]
	withEnv(t, env)
	ctx := context.Background()
	// Rejected before project validation or issuance, including a flag after an unexpected positional.
	if err := credentialCommand(ctx, []string{"issue", "restricted", "unexpected", "--read-only"}); err == nil || !strings.Contains(err.Error(), "unexpected credential arguments") {
		t.Fatal("misplaced read-only flag silently issued default authority")
	}
}
