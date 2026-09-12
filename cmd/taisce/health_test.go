// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestHealthConfigurationAndLocalProbesRefuseAmbiguity(t *testing.T) {
	for _, value := range []string{"", "false", "true"} {
		t.Setenv(envRequireFormation, value)
		got, err := formationRequired()
		if err != nil || got != (value == "true") {
			t.Fatalf("formation policy %q: %v %v", value, got, err)
		}
	}
	t.Setenv(envRequireFormation, "perhaps")
	if _, err := formationRequired(); err == nil {
		t.Fatal("invalid policy accepted")
	}
	for _, value := range []string{"not-an-address", "0.0.0.0:8081", "example.com:8081", "127.0.0.1:bad", "127.0.0.1:65536", "127.0.0.1:-1"} {
		t.Setenv(envHealthAddr, value)
		if _, err := workerHealthAddress(); err == nil {
			t.Fatalf("worker address accepted: %q", value)
		}
	}
	t.Setenv(envHealthAddr, "")
	if addr, err := workerHealthAddress(); err != nil || addr != "127.0.0.1:8082" {
		t.Fatal("default worker health address changed")
	}
	t.Setenv(envHealthAddr, "[::1]:8081")
	if _, err := workerHealthAddress(); err != nil {
		t.Fatal(err)
	}
	var health processFormationHealth
	if health.current() {
		t.Fatal("unstarted worker healthy")
	}
	health.responsive()
	if !health.current() {
		t.Fatal("responsive worker unhealthy")
	}
	health.last.Store(time.Now().Add(-10 * time.Minute).UnixNano())
	if health.current() {
		t.Fatal("stalled worker healthy")
	}
	for _, args := range [][]string{{"--bad"}, {"positional"}} {
		if err := probeCommand(context.Background(), args); err == nil {
			t.Fatal("invalid probe accepted")
		}
	}
	for _, addr := range []string{"bad-address", "example.com:80"} {
		t.Setenv(envAddr, addr)
		if err := probeCommand(context.Background(), nil); err == nil {
			t.Fatal("nonlocal probe accepted")
		}
	}
	t.Setenv(envHealthAddr, "0.0.0.0:8081")
	if err := probeCommand(context.Background(), []string{"--worker"}); err == nil {
		t.Fatal("nonlocal worker probe accepted")
	}
	t.Setenv(envAddr, fmt.Sprintf("127.0.0.1:%d", freePort(t)))
	if err := probeCommand(context.Background(), nil); err == nil {
		t.Fatal("absent listener healthy")
	}
	if err := healthCommand(context.Background(), []string{"extra"}, io.Discard); err == nil {
		t.Fatal("health positional args accepted")
	}
	t.Setenv(envAdminDSN, "")
	if err := healthCommand(context.Background(), nil, io.Discard); err == nil {
		t.Fatal("operator health without explicit admin connection")
	}
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/ready" {
			w.WriteHeader(503)
		}
	}))
	defer server.Close()
	addr := strings.TrimPrefix(server.URL, "http://")
	t.Setenv(envAddr, addr)
	t.Setenv(envHealthAddr, addr)
	if err := probeCommand(context.Background(), nil); err == nil {
		t.Fatal("unready listener healthy")
	}
	if err := probeCommand(context.Background(), []string{"--live"}); err != nil {
		t.Fatal(err)
	}
	if err := probeCommand(context.Background(), []string{"--worker", "--live"}); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 3 || paths[0] != "/ready" || paths[1] != "/health" || paths[2] != "/health" {
		t.Fatalf("probe routes %v", paths)
	}
}

func TestOperatorHealthCommandReturnsOnlyAggregates(t *testing.T) {
	ctx := context.Background()
	env := complete(t)
	env[envAdminDSN] = env[envMemoryDSN]
	env[envSchema] = "cmd_health"
	withEnv(t, env)
	pool, err := pgxpool.New(ctx, env[envAdminDSN])
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	dropSchema(t, env[envSchema])
	defer pool.Exec(ctx, "DROP SCHEMA cmd_health CASCADE")
	if err := migrate.ProvisionMemorySchema(ctx, pool, env[envSchema]); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := healthCommand(ctx, nil, &out); err != nil {
		t.Fatal(err)
	}
	var status pg.OperationalHealth
	if err := json.Unmarshal(out.Bytes(), &status); err != nil || status.Pending != 0 || status.WorkerResponsive || status.PendingLimit != 4096 {
		t.Fatalf("health output=%s err=%v", out.String(), err)
	}
	if err := healthCommand(ctx, nil, failedRecoveryOutput{}); err == nil {
		t.Fatal("health output failure swallowed")
	}
	t.Setenv(envSchema, "missing_health_namespace")
	if err := healthCommand(ctx, nil, io.Discard); err == nil {
		t.Fatal("missing health schema accepted")
	}
	t.Setenv(envAdminDSN, "postgres://invalid@127.0.0.1:1/missing?sslmode=disable")
	if err := healthCommand(ctx, nil, io.Discard); err == nil {
		t.Fatal("unreachable admin health accepted")
	}
}

func TestServingReadinessDistinguishesMissingRequiredWorkerAndRecovery(t *testing.T) {
	ctx := context.Background()
	env := runtimeConfiguration(t)
	env[envRole] = roleAPI
	env[envAddr] = fmt.Sprintf("127.0.0.1:%d", freePort(t))
	env[envRequireFormation] = "true"
	withEnv(t, env)
	owner, err := pgxpool.New(ctx, os.Getenv("TAISCE_TEST_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	schema, _ := pg.NewSchema(env[envSchema])
	if _, err := owner.Exec(ctx, schema.SQL(`UPDATE {schema}.formation_health SET heartbeat_at=NULL`)); err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	stopped := make(chan error, 1)
	go func() { stopped <- run(runCtx, quiet()) }()
	defer func() {
		cancel()
		select {
		case err := <-stopped:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(10 * time.Second):
			t.Error("server did not stop")
		}
	}()
	if !reachable("http://"+env[envAddr]+"/health", 5*time.Second) {
		t.Fatal("API did not start")
	}
	if err := probeCommand(ctx, nil); err == nil {
		t.Fatal("required missing worker was ready")
	}
	if err := probeCommand(ctx, []string{"--live"}); err != nil {
		t.Fatal("missing worker stopped liveness")
	}
	if err := pg.RecordFormationHealth(ctx, owner, schema, 0, 0); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1050 * time.Millisecond)
	if err := probeCommand(ctx, nil); err != nil {
		t.Fatalf("worker recovery not visible: %v", err)
	}
	if _, err := owner.Exec(ctx, schema.SQL(`UPDATE {schema}.formation_health SET heartbeat_at=now()-interval '10 minutes'`)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1050 * time.Millisecond)
	if err := probeCommand(ctx, nil); err == nil {
		t.Fatal("stale worker was ready")
	}
}

func TestWorkerLoopbackHealthExposesNoMemoryAndRequiresItsOwnDriver(t *testing.T) {
	ctx := context.Background()
	env := runtimeConfiguration(t)
	env[envRole] = roleWorker
	env[envHealthAddr] = fmt.Sprintf("127.0.0.1:%d", freePort(t))
	withEnv(t, env)
	t.Setenv("TAISCE_INFERENCE_ENDPOINT", "http://127.0.0.1:1/v1")
	t.Setenv("TAISCE_INFERENCE_EXTRACTOR_MODEL", "health-fixture")
	t.Setenv("TAISCE_INFERENCE_ALLOWLIST", "127.0.0.1:1")
	runCtx, cancel := context.WithCancel(ctx)
	stopped := make(chan error, 1)
	go func() { stopped <- run(runCtx, quiet()) }()
	defer func() {
		cancel()
		select {
		case err := <-stopped:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(10 * time.Second):
			t.Error("worker did not stop")
		}
	}()
	if !reachable("http://"+env[envHealthAddr]+"/health", 5*time.Second) {
		t.Fatal("worker did not become responsive")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := probeCommand(ctx, []string{"--worker"})
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	resp, err := http.Get("http://" + env[envHealthAddr] + "/v1/observations")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatal("worker health listener exposes memory routes")
	}
}

func TestWorkerReadinessCannotBorrowAnotherWorkersHeartbeat(t *testing.T) {
	ctx := context.Background()
	env := runtimeConfiguration(t)
	env[envRole] = roleWorker
	env[envHealthAddr] = fmt.Sprintf("127.0.0.1:%d", freePort(t))
	withEnv(t, env)
	t.Setenv("TAISCE_INFERENCE_ENDPOINT", "")
	owner, err := pgxpool.New(ctx, os.Getenv("TAISCE_TEST_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	schema, _ := pg.NewSchema(env[envSchema])
	if err := pg.RecordFormationHealth(ctx, owner, schema, 0, 0); err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	stopped := make(chan error, 1)
	go func() { stopped <- run(runCtx, quiet()) }()
	defer func() {
		cancel()
		select {
		case err := <-stopped:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(10 * time.Second):
			t.Error("worker did not stop")
		}
	}()
	if !reachable("http://"+env[envHealthAddr]+"/health", 5*time.Second) {
		t.Fatal("worker liveness missing")
	}
	if err := probeCommand(ctx, []string{"--worker"}); err == nil {
		t.Fatal("worker borrowed the fleet heartbeat despite no local driver")
	}
	if err := pg.CheckServingHealth(ctx, owner, nil, schema, true); err != nil {
		t.Fatalf("fixture fleet heartbeat missing: %v", err)
	}
}

func TestWorkerHealthPortMustBeDiscoverableByItsProbe(t *testing.T) {
	t.Setenv(envHealthAddr, "127.0.0.1:0")
	if _, err := workerHealthAddress(); err == nil {
		t.Fatal("ephemeral worker health port accepted")
	}
}
