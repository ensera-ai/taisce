// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestFormationCommandsValidateBeforeConnecting(t *testing.T) {
	t.Setenv(envAdminDSN, "")
	t.Setenv(envMemoryDSN, "")
	for _, args := range [][]string{nil, {"unknown"}, {"parked"}, {"parked", "--project", "Bad"}, {"parked", "--project", "p1", "--after", "-2"}, {"parked", "--project", "p1", "--limit", "201"}, {"parked", "--project", "p1", "extra"}, {"parked", "--bogus"}, {"unpark", "--project", "p1"}, {"unpark", "--project", "p1", "bad"}, {"unpark", "--project", "p1", uuid.NewString(), "extra"}} {
		err := formationCommand(context.Background(), args, io.Discard)
		if err == nil {
			t.Fatalf("accepted invalid arguments %v", args)
		}
	}
	if err := formationCommand(context.Background(), []string{"parked", "--project", "p1"}, io.Discard); err == nil {
		t.Fatal("missing connection accepted")
	}
	if err := dispatch(context.Background(), quiet(), []string{"formation"}); err == nil {
		t.Fatal("bare formation accepted")
	}
}

type failedRecoveryOutput struct{}

func (failedRecoveryOutput) Write([]byte) (int, error) { return 0, errors.New("output unavailable") }

func TestFormationCommandsInspectAndRecoverWithoutReturningMemory(t *testing.T) {
	ctx := context.Background()
	env := complete(t)
	env[envSchema] = "cmd_recovery"
	env[envAdminDSN] = env[envMemoryDSN]
	withEnv(t, env)
	pool, err := pgxpool.New(ctx, env[envAdminDSN])
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	dropSchema(t, env[envSchema])
	defer pool.Exec(ctx, "DROP SCHEMA cmd_recovery CASCADE")
	if err := migrate.ProvisionMemorySchema(ctx, pool, env[envSchema]); err != nil {
		t.Fatal(err)
	}
	if err := migrate.ProvisionScope(ctx, pool, env[envSchema], "p1"); err != nil {
		t.Fatal(err)
	}
	schema, _ := pg.NewSchema(env[envSchema])
	store := pg.NewObservationStore(pool)
	obs, err := store.Append(ctx, schema, domain.Turn{Scope: "p1", DataSubjectID: "private-subject", OccurredAt: time.Now(), Messages: []domain.Message{{Role: domain.RoleUser, Content: "private-content"}}})
	if err != nil {
		t.Fatal(err)
	}
	// The attempt a worker's claim counts: a turn that was never attempted cannot be parked.
	if _, err := pool.Exec(ctx, schema.SQL(
		`UPDATE {schema}.observation SET formation_attempts = formation_attempts + 1 WHERE observation_id = $1`), obs.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordFormationFailure(ctx, schema, obs.ID, "private-provider-echo"); err != nil {
		t.Fatal(err)
	}
	if err := store.Park(ctx, schema, obs.ID); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := formationCommand(ctx, []string{"parked", "--project", "p1", "--limit", "1"}, &out); err != nil {
		t.Fatal(err)
	}
	var page pg.ParkedPage
	if err := json.Unmarshal(out.Bytes(), &page); err != nil || len(page.Items) != 1 || page.Items[0].ObservationID != obs.ID || bytes.Contains(out.Bytes(), []byte("private")) {
		t.Fatalf("metadata output: %s err=%v", out.String(), err)
	}
	if err := formationCommand(ctx, []string{"parked", "--project", "p1"}, failedRecoveryOutput{}); err == nil {
		t.Fatal("output failure swallowed")
	}
	if err := formationCommand(ctx, []string{"parked", "--project", "missing"}, io.Discard); err == nil {
		t.Fatal("missing project accepted")
	}
	if err := formationCommand(ctx, []string{"unpark", "--project", "p1", uuid.NewString()}, io.Discard); !errors.Is(err, pg.ErrFormationTurnNotFound) {
		t.Fatalf("missing observation: %v", err)
	}
	// Output can fail after commit. Repeating recovery is safe and reports no additional change.
	if err := formationCommand(ctx, []string{"unpark", "--project", "p1", obs.ID}, failedRecoveryOutput{}); err == nil {
		t.Fatal("output failure swallowed")
	}
	out.Reset()
	if err := formationCommand(ctx, []string{"unpark", "--project", "p1", obs.ID}, &out); err != nil {
		t.Fatal(err)
	}
	var result unparkResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.Unparked || result.ObservationID != obs.ID {
		t.Fatalf("repeat recovery: %s %v", out.String(), err)
	}
	if _, err := uuid.Parse(result.AuditPrincipal); err != nil {
		t.Fatal(err)
	}
	fresh, err := store.Freshness(ctx, schema, "p1")
	if err != nil || fresh.HasFormed || fresh.Parked != 0 {
		t.Fatalf("watermark: %+v %v", fresh, err)
	}
}
