// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

func TestEntityEmbeddingCommandsManageCandidatesWithoutPrintingSourceText(t *testing.T) {
	ctx := context.Background()
	for _, args := range [][]string{{}, {"unknown"}, {"status"}, {"start", "--project", "p1"},
		{"build", "--project", "p1"}, {"search", "--project", "p1", "--query", ""}} {
		if err := entityEmbeddingsCommand(ctx, args, io.Discard); err == nil {
			t.Fatalf("accepted invalid arguments: %v", args)
		}
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		data := make([]map[string]any, len(request.Input))
		for i := range data {
			data[i] = map[string]any{"index": i, "embedding": []float32{1, 0, 0}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer provider.Close()
	env := complete(t)
	env[envSchema] = "cmd_entity_embeddings"
	env[envAdminDSN] = env[envMemoryDSN]
	withEnv(t, env)
	t.Setenv(inference.EnvEndpoint, provider.URL)
	t.Setenv(inference.EnvModel, "extractor")
	t.Setenv(inference.EnvEmbeddingModel, "embedder")
	t.Setenv(inference.EnvEmbeddingEndpoint, "")
	t.Setenv(inference.EnvAPIKey, "")
	t.Setenv(inference.EnvEmbeddingAPIKey, "")
	t.Setenv(inference.EnvAllowlist, strings.TrimPrefix(provider.URL, "http://"))
	pool, err := pgxpool.New(ctx, env[envAdminDSN])
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	dropSchema(t, env[envSchema])
	defer pool.Exec(ctx, "DROP SCHEMA cmd_entity_embeddings CASCADE")
	if err := migrate.ProvisionMemorySchema(ctx, pool, env[envSchema]); err != nil {
		t.Fatal(err)
	}
	if err := migrate.ProvisionScope(ctx, pool, env[envSchema], "p1"); err != nil {
		t.Fatal(err)
	}
	schema, _ := pg.NewSchema(env[envSchema])
	turn, err := pg.NewObservationStore(pool).Append(ctx, schema, domain.Turn{Scope: "p1", DataSubjectID: "owner",
		OccurredAt: time.Now(), Messages: []domain.Message{{Role: domain.RoleUser, Content: "Atlas works at Ensera"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pg.NewFactStore(pool).Assert(ctx, schema, "p1", turn.ID, domain.RoleUser, "owner", domain.Claim{
		Subject: "Atlas", Predicate: "works_at", Object: "Ensera", Statement: "Atlas works at Ensera",
		Quote: "Atlas works at Ensera", ByteEnd: len("Atlas works at Ensera"), Cardinality: domain.CardinalityMany,
	}); err != nil {
		t.Fatal(err)
	}
	key := uuid.NewString()
	start := []string{"start", "--project", "p1", "--key", key, "--revision", "v1", "--dimensions", "3"}
	var output bytes.Buffer
	if err := entityEmbeddingsCommand(ctx, start, &output); err != nil {
		t.Fatal(err)
	}
	var started struct {
		Result pg.EntityEmbeddingGeneration `json:"result"`
	}
	if err := json.Unmarshal(output.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"build", "activate", "status", "repair", "build", "activate"} {
		output.Reset()
		args := []string{operation, "--project", "p1", "--generation", started.Result.ID, "--revision", "v1"}
		if err := entityEmbeddingsCommand(ctx, args, &output); err != nil {
			t.Fatalf("%s: %v", operation, err)
		}
		if bytes.Contains(output.Bytes(), []byte("Atlas works")) {
			t.Fatalf("%s printed source input: %s", operation, output.String())
		}
	}
	output.Reset()
	if err := entityEmbeddingsCommand(ctx, []string{"search", "--project", "p1", "--revision", "v1",
		"--query", "Ensera", "--limit", "2", "--candidates", "16"}, &output); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(output.Bytes(), []byte(`"name_preview":"Ensera"`)) {
		t.Fatalf("candidate missing: %s", output.String())
	}
	if err := entityEmbeddingsCommand(ctx, []string{"build", "--project", "p1", "--generation", started.Result.ID,
		"--revision", "wrong"}, io.Discard); !errors.Is(err, pg.ErrEmbeddingConflict) {
		t.Fatalf("model mismatch: %v", err)
	}
	output.Reset()
	if err := entityEmbeddingsCommand(ctx, []string{"status", "--project", "p1"}, &output); err != nil {
		t.Fatalf("active status: %v", err)
	}
	secondKey := uuid.NewString()
	output.Reset()
	if err := entityEmbeddingsCommand(ctx, []string{"start", "--project", "p1", "--key", secondKey,
		"--revision", "v1", "--dimensions", "3"}, &output); err != nil {
		t.Fatal(err)
	}
	var second struct {
		Result pg.EntityEmbeddingGeneration `json:"result"`
	}
	if err := json.Unmarshal(output.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"cancel", "prune"} {
		if err := entityEmbeddingsCommand(ctx, []string{operation, "--project", "p1", "--generation", second.Result.ID}, io.Discard); err != nil {
			t.Fatalf("%s: %v", operation, err)
		}
	}
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.entity_embedding_build SET state='stale' WHERE generation_id=$1::uuid`), started.Result.ID); err != nil {
		t.Fatal(err)
	}
	if err := entityEmbeddingsCommand(ctx, []string{"search", "--project", "p1", "--revision", "v1",
		"--query", "Ensera"}, io.Discard); !errors.Is(err, pg.ErrEmbeddingConflict) {
		t.Fatalf("stale search: %v", err)
	}
}
