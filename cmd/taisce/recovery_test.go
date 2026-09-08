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
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRecoveryCommandRejectsInvalidRequestsBeforeConnecting(t *testing.T) {
	for _, args := range [][]string{nil, {"unknown"}, {"facts"}, {"facts", "--bad"}, {"facts", "--project", "p1", "extra"}, {"facts", "--project", "p1", "--limit", "0"}, {"facts", "--project", "p1", "--limit", "101"}, {"facts", "--project", "p1", "--after", "bad"}, {"facts", "--project", "p1", "--source", "bad"}} {
		if err := recoveryCommand(context.Background(), args, io.Discard); err == nil {
			t.Fatalf("invalid recovery accepted: %v", args)
		}
	}
	t.Setenv(envAdminDSN, "")
	t.Setenv(envMemoryDSN, "")
	if err := recoveryCommand(context.Background(), []string{"facts", "--project", "p1"}, io.Discard); err == nil {
		t.Fatal("missing connection accepted")
	}
}

func TestRecoveryCommandRestoresOneSourceAndReturnsOnlyProgress(t *testing.T) {
	ctx := context.Background()
	env := complete(t)
	env[envSchema] = "cmd_fact_recovery"
	env[envAdminDSN] = env[envMemoryDSN]
	withEnv(t, env)
	pool, err := pgxpool.New(ctx, env[envAdminDSN])
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	dropSchema(t, env[envSchema])
	defer pool.Exec(ctx, "DROP SCHEMA cmd_fact_recovery CASCADE")
	if err := migrate.ProvisionMemorySchema(ctx, pool, env[envSchema]); err != nil {
		t.Fatal(err)
	}
	if err := migrate.ProvisionScope(ctx, pool, env[envSchema], "p1"); err != nil {
		t.Fatal(err)
	}
	schema, _ := pg.NewSchema(env[envSchema])
	source, err := pg.NewObservationStore(pool).Append(ctx, schema, domain.Turn{Scope: "p1", DataSubjectID: "private-owner", OccurredAt: time.Now(), Messages: []domain.Message{{Role: domain.RoleUser, Content: "I work at Ensera"}}})
	if err != nil {
		t.Fatal(err)
	}
	id, err := pg.NewFactStore(pool).Assert(ctx, schema, "p1", source.ID, domain.RoleUser, "private-owner", domain.Claim{Subject: "I", Predicate: "works_at", Object: "Ensera", Statement: "private-statement", Quote: "I work at Ensera", ByteEnd: 16, Cardinality: domain.CardinalityMany})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`DELETE FROM {schema}.fact WHERE fact_id=$1`), id); err != nil {
		t.Fatal(err)
	}
	args := []string{"facts", "--project", "p1", "--source", source.ID, "--limit", "1"}
	var out bytes.Buffer
	if err := recoveryCommand(ctx, args, &out); err != nil {
		t.Fatal(err)
	}
	var page struct {
		pg.RecoveryPage
		Principal string `json:"audit_principal"`
	}
	if err := json.Unmarshal(out.Bytes(), &page); err != nil || page.Restored != 1 || page.Examined != 1 || bytes.Contains(out.Bytes(), []byte("private")) {
		t.Fatalf("progress: %s %v", out.String(), err)
	}
	if _, err := uuid.Parse(page.Principal); err != nil {
		t.Fatal(err)
	}

	if _, err := pool.Exec(ctx, schema.SQL(`DELETE FROM {schema}.fact_evidence WHERE fact_id=$1::uuid`), id); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := recoveryCommand(ctx, args, &out); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(out.Bytes(), &page); err != nil || page.Restored != 0 || page.Repaired != 1 || page.Examined != 1 || bytes.Contains(out.Bytes(), []byte("private")) {
		t.Fatal("support repair was not reported as content-free progress")
	}
	if err := recoveryCommand(ctx, args, failedRecoveryOutput{}); err == nil {
		t.Fatal("output error swallowed")
	}
	if err := recoveryCommand(ctx, []string{"facts", "--project", "missing"}, io.Discard); err == nil {
		t.Fatal("missing project accepted")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := recoveryCommand(canceled, args, io.Discard); err == nil {
		t.Fatal("cancellation swallowed")
	}
	if err := dispatch(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), append([]string{"recover"}, args...)); err != nil {
		t.Fatal(err)
	}
}
