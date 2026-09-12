// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/inference"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type followOutput func([]byte) (int, error)

func (f followOutput) Write(p []byte) (int, error) { return f(p) }

func TestEmbeddingFollowUsesRuntimePrivilegesAndRetriesWithoutLeakingProviderPayload(t *testing.T) {
	ctx := context.Background()
	var failures, calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if failures.Load() > 0 {
			failures.Add(-1)
			w.WriteHeader(503)
			_, _ = io.WriteString(w, "private provider payload")
			return
		}
		var req struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		data := make([]map[string]any, len(req.Input))
		for i := range data {
			data[i] = map[string]any{"index": i, "embedding": []float32{1, 0, 0}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer provider.Close()
	env := complete(t)
	env[envSchema] = "cmd_embedding_follow"
	ownerDSN := env[envMemoryDSN]
	owner, err := pgxpool.New(ctx, ownerDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if err := migrate.EstablishPlanes(ctx, owner, "taisce-test-control", "taisce-test-data"); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, "DROP SCHEMA IF EXISTS cmd_embedding_follow CASCADE"); err != nil {
		t.Fatal(err)
	}
	defer owner.Exec(ctx, "DROP SCHEMA cmd_embedding_follow CASCADE")
	if err := migrate.ProvisionMemorySchema(ctx, owner, env[envSchema]); err != nil {
		t.Fatal(err)
	}
	if err := migrate.ProvisionScope(ctx, owner, env[envSchema], "p1"); err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(ownerDSN)
	if err != nil {
		t.Fatal(err)
	}
	parsed.User = url.UserPassword(migrate.DataRole, "taisce-test-data")
	env[envMemoryDSN] = parsed.String()
	withEnv(t, env)
	t.Setenv(inference.EnvEndpoint, provider.URL)
	t.Setenv(inference.EnvEmbeddingEndpoint, "")
	t.Setenv(inference.EnvEmbeddingModel, "embedder")
	t.Setenv(inference.EnvModel, "")
	t.Setenv(inference.EnvAllowlist, strings.TrimPrefix(provider.URL, "http://"))
	schema, _ := pg.NewSchema(env[envSchema])
	store := pg.NewMessageEmbeddingStore(owner, schema)
	digest := sha256.Sum256([]byte(provider.URL))
	model := pg.EmbeddingModel{Name: "embedder", Revision: "v1", EndpointHash: hex.EncodeToString(digest[:]), Dimensions: 3}
	actor := uuid.NewString()
	generation, err := store.Start(ctx, "p1", uuid.NewString(), actor, model)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.BuildPage(ctx, "p1", generation.ID, actor, model, 64, inference.NewEmbedder(inference.Config{Endpoint: provider.URL, EmbeddingModel: "embedder"}).Embed); err != nil {
		t.Fatal(err)
	}
	if err := store.Activate(ctx, "p1", generation.ID, actor); err != nil {
		t.Fatal(err)
	}
	appendSource := func(content string) {
		t.Helper()
		if _, err := pg.NewObservationStore(owner).Append(ctx, schema, domain.Turn{Scope: "p1", DataSubjectID: "owner", OccurredAt: time.Now(), Messages: []domain.Message{{Role: domain.RoleUser, Content: content}}}); err != nil {
			t.Fatal(err)
		}
	}
	args := []string{"follow", "--project", "p1", "--generation", generation.ID, "--revision", "v1", "--interval", "100ms"}
	appendSource("first source")
	var output bytes.Buffer
	if err := embeddingsCommand(ctx, args, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"stored":1`) || strings.Contains(output.String(), "first source") {
		t.Fatalf("unsafe/missing status %s", output.String())
	}
	count := calls.Load()
	if err := embeddingsCommand(ctx, args, failedRecoveryOutput{}); err == nil {
		t.Fatal("lost output ignored")
	}
	if calls.Load() != count {
		t.Fatal("idle retry repeated inference")
	}
	appendSource("second source")
	failures.Store(1)
	watchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	lines := make(chan string, 10)
	done := make(chan error, 1)
	go func() {
		done <- embeddingsCommand(watchCtx, append(args, "--watch"), followOutput(func(p []byte) (int, error) { lines <- string(p); return len(p), nil }))
	}()
	sawRetry := false
	for {
		select {
		case line := <-lines:
			if strings.Contains(line, "private") || strings.Contains(line, "second source") {
				cancel()
				t.Fatal("source/provider payload in worker status")
			}
			if strings.Contains(line, `"state":"retrying"`) {
				sawRetry = true
			}
			if strings.Contains(line, `"state":"ready"`) {
				cancel()
				if !sawRetry {
					t.Fatal("provider failure was silent")
				}
				goto stopped
			}
		case err := <-done:
			t.Fatalf("worker stopped before recovery: %v", err)
		case <-time.After(10 * time.Second):
			cancel()
			t.Fatal("worker did not recover")
		}
	}
stopped:
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watch cancellation stalled")
	}
	// The same runtime identity that followed cannot rewrite identity or activation policy.
	runtime, err := migrate.NewRuntimePool(ctx, env[envMemoryDSN], schema, migrate.MemoryPlane)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	for _, statement := range []string{`UPDATE {schema}.embedding_generation SET model_revision='changed'`, `DELETE FROM {schema}.embedding_active`} {
		if _, err := runtime.Exec(ctx, schema.SQL(statement)); err == nil {
			t.Fatal("worker mutated operator policy")
		}
	}
	t.Setenv(envMemoryDSN, ownerDSN)
	if err := embeddingsCommand(ctx, args, io.Discard); err == nil {
		t.Fatal("privileged identity accepted for following")
	}
}

func TestEmbeddingFollowRefusesInvalidControlsBeforeConnecting(t *testing.T) {
	for _, args := range [][]string{{}, {"--bad"}, {"--project", "bad-name"}, {"--project", "p1", "--generation", "invalid"}, {"--project", "p1", "--limit", "0"}, {"--project", "p1", "--limit", "65"}, {"--project", "p1", "--interval", "0s"}, {"--project", "p1", "--interval", "2m"}, {"--project", "p1", "extra"}} {
		if err := embeddingFollowCommand(context.Background(), args, io.Discard); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestEmbeddingFollowRequiresCompleteExplicitConfiguration(t *testing.T) {
	args := []string{"--project", "p1", "--generation", uuid.NewString(), "--revision", "v1"}
	t.Setenv(inference.EnvEndpoint, "http://localhost:11434/v1")
	t.Setenv(inference.EnvEmbeddingEndpoint, "")
	t.Setenv(inference.EnvEmbeddingModel, "")
	t.Setenv(inference.EnvAllowlist, "")
	t.Setenv(envMemoryDSN, "")
	t.Setenv(envSchema, "follow_config")
	if err := embeddingFollowCommand(context.Background(), args, io.Discard); err == nil {
		t.Fatal("missing model accepted")
	}
	t.Setenv(inference.EnvEmbeddingModel, "embedder")
	if err := embeddingFollowCommand(context.Background(), args, io.Discard); err == nil {
		t.Fatal("unlisted provider accepted")
	}
	t.Setenv(inference.EnvAllowlist, "localhost:11434")
	if err := embeddingFollowCommand(context.Background(), args, io.Discard); err == nil {
		t.Fatal("missing runtime DSN accepted")
	}
	if err := embeddingFollowCommand(context.Background(), append(args, "--revision", strings.Repeat("r", 129)), io.Discard); err == nil {
		t.Fatal("oversized revision accepted")
	}
	t.Setenv(envMemoryDSN, "%")
	t.Setenv(envSchema, "invalid-name")
	if err := embeddingFollowCommand(context.Background(), args, io.Discard); err == nil {
		t.Fatal("invalid schema accepted")
	}
	t.Setenv(envSchema, "follow_config")
	if err := embeddingFollowCommand(context.Background(), args, io.Discard); err == nil {
		t.Fatal("malformed connection accepted")
	}
}
