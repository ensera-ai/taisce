// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/inference"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestEmbeddingCommandsBuildAndActivateExplicitGenerations(t *testing.T) {
	ctx := context.Background()
	for _, args := range [][]string{{}, {"unknown"}, {"status", "--bad"}, {"status"}, {"status", "--project", "p1", "extra"}, {"status", "--project", "p1", "--limit", "0"}, {"status", "--project", "p1", "--generation", "bad"}, {"start", "--project", "p1"}, {"build", "--project", "p1"}} {
		if err := embeddingsCommand(ctx, args, io.Discard); err == nil {
			t.Fatalf("accepted invalid arguments: %v", args)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		data := make([]map[string]any, len(body.Input))
		for i := range data {
			data[i] = map[string]any{"index": i, "embedding": []float32{1, 0, 0}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer server.Close()
	env := complete(t)
	env[envSchema] = "cmd_embedding_generations"
	env[envAdminDSN] = env[envMemoryDSN]
	withEnv(t, env)
	t.Setenv(inference.EnvEndpoint, server.URL)
	t.Setenv(inference.EnvModel, "extractor")
	t.Setenv(inference.EnvEmbeddingModel, "embedder")
	t.Setenv(inference.EnvEmbeddingEndpoint, "")
	t.Setenv(inference.EnvAPIKey, "")
	t.Setenv(inference.EnvEmbeddingAPIKey, "")
	t.Setenv(inference.EnvAllowlist, strings.TrimPrefix(server.URL, "http://"))
	pool, err := pgxpool.New(ctx, env[envAdminDSN])
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	dropSchema(t, env[envSchema])
	defer pool.Exec(ctx, "DROP SCHEMA cmd_embedding_generations CASCADE")
	if err := migrate.ProvisionMemorySchema(ctx, pool, env[envSchema]); err != nil {
		t.Fatal(err)
	}
	if err := migrate.ProvisionScope(ctx, pool, env[envSchema], "p1"); err != nil {
		t.Fatal(err)
	}
	schema, _ := pg.NewSchema(env[envSchema])
	if _, err := pg.NewObservationStore(pool).Append(ctx, schema, domain.Turn{Scope: "p1", DataSubjectID: "owner", OccurredAt: time.Now(), Messages: []domain.Message{{Role: domain.RoleUser, Content: "private source text"}}}); err != nil {
		t.Fatal(err)
	}
	key := uuid.NewString()
	start := []string{"start", "--project", "p1", "--key", key, "--revision", "v1", "--dimensions", "3"}
	if err := embeddingsCommand(ctx, start, failedRecoveryOutput{}); err == nil {
		t.Fatal("lost output ignored")
	}
	var buffer bytes.Buffer
	if err := embeddingsCommand(ctx, start, &buffer); err != nil {
		t.Fatal(err)
	}
	var started struct {
		Result pg.EmbeddingGeneration `json:"result"`
	}
	if err := json.Unmarshal(buffer.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	id := started.Result.ID
	for _, operation := range []string{"status", "build", "activate", "status", "repair", "build"} {
		buffer.Reset()
		args := []string{operation, "--project", "p1", "--generation", id, "--revision", "v1"}
		if err := embeddingsCommand(ctx, args, &buffer); err != nil {
			t.Fatalf("%s: %v", operation, err)
		}
		if bytes.Contains(buffer.Bytes(), []byte("private source")) {
			t.Fatalf("progress contains source text: %s", buffer.String())
		}
	}
	for _, extra := range [][]string{{}, {"--candidates", "32"}} {
		buffer.Reset()
		args := append([]string{"search", "--project", "p1", "--revision", "v1", "--query", "question"}, extra...)
		if err := embeddingsCommand(ctx, args, &buffer); err != nil || !bytes.Contains(buffer.Bytes(), []byte("private source text")) {
			t.Fatalf("search %s %v", buffer.String(), err)
		}
	}
	if err := embeddingsCommand(ctx, []string{"status", "--project", "p1"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := embeddingsCommand(ctx, []string{"build", "--project", "p1", "--generation", id, "--revision", "wrong"}, io.Discard); err == nil {
		t.Fatal("model mismatch ignored")
	}
	buffer.Reset()
	start[4] = uuid.NewString()
	if err := embeddingsCommand(ctx, start, &buffer); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(buffer.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"cancel", "prune"} {
		if err := embeddingsCommand(ctx, []string{operation, "--project", "p1", "--generation", started.Result.ID}, io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	if err := embeddingsCommand(ctx, []string{"status", "--project", "missing"}, io.Discard); err == nil {
		t.Fatal("missing project accepted")
	}
	t.Setenv(envAdminDSN, "")
	t.Setenv(envMemoryDSN, "")
	if err := embeddingsCommand(ctx, []string{"status", "--project", "p1"}, io.Discard); err == nil {
		t.Fatal("missing connection accepted")
	}
}
