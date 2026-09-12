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

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/inference"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestReportEmbeddingCommandsManageThemesWithoutPrintingBuildInput(t *testing.T) {
	ctx := context.Background()
	for _, args := range [][]string{{}, {"unknown"}, {"status"}, {"status", "--project", "bad scope"},
		{"status", "--project", "p1", "--generation", "not-a-uuid"}, {"status", "--project", "p1", "--unknown"},
		{"start", "--project", "p1"}, {"start", "--project", "p1", "--key", uuid.NewString(), "--dimensions", "4001"},
		{"build", "--project", "p1"}, {"search", "--project", "p1", "--query", ""}} {
		if err := reportEmbeddingsCommand(ctx, args, io.Discard); err == nil {
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
	env[envSchema] = "cmd_report_embeddings"
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
	defer pool.Exec(ctx, "DROP SCHEMA cmd_report_embeddings CASCADE")
	if err := migrate.ProvisionMemorySchema(ctx, pool, env[envSchema]); err != nil {
		t.Fatal(err)
	}
	if err := migrate.ProvisionScope(ctx, pool, env[envSchema], "p1"); err != nil {
		t.Fatal(err)
	}
	schema, _ := pg.NewSchema(env[envSchema])
	turn, err := pg.NewObservationStore(pool).Append(ctx, schema, domain.Turn{
		Scope: "p1", DataSubjectID: "owner", Messages: []domain.Message{{Role: domain.RoleUser, Content: "Hiring and demand are increasing"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	communityID, reportID := uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.community(scope,community_id,level) VALUES('p1',$1,0)`), communityID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.community_report
 (scope,report_id,community_id,title,summary,importance,importance_reason,findings)
 VALUES('p1',$1,$2,'Growth plan','Hiring and demand form one expansion theme',8,'material','[]')`), reportID, communityID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.projection_dependency
 (source_observation_id,scope,projection_kind,projection_id,data_subject_id,report_source_revision)
 SELECT observation_id,scope,'community_report',$2,data_subject_id,fact_revision FROM {schema}.observation WHERE observation_id=$1::uuid`), turn.ID, reportID); err != nil {
		t.Fatal(err)
	}
	key := uuid.NewString()
	var output bytes.Buffer
	if err := reportEmbeddingsCommand(ctx, []string{"start", "--project", "p1", "--key", key, "--revision", "v1", "--dimensions", "3"}, &output); err != nil {
		t.Fatal(err)
	}
	var started struct {
		Result pg.ReportEmbeddingGeneration `json:"result"`
	}
	if err := json.Unmarshal(output.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"build", "activate", "status", "repair", "build", "activate"} {
		output.Reset()
		args := []string{operation, "--project", "p1", "--generation", started.Result.ID, "--revision", "v1"}
		if err := reportEmbeddingsCommand(ctx, args, &output); err != nil {
			t.Fatalf("%s: %v", operation, err)
		}
		if bytes.Contains(output.Bytes(), []byte("Hiring and demand")) {
			t.Fatalf("%s printed provider input: %s", operation, output.String())
		}
	}
	output.Reset()
	if err := reportEmbeddingsCommand(ctx, []string{"search", "--project", "p1", "--revision", "v1",
		"--query", "growth", "--limit", "1", "--candidates", "16"}, &output); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(output.Bytes(), []byte(`"title_preview":"Growth plan"`)) {
		t.Fatalf("candidate missing: %s", output.String())
	}
	if err := reportEmbeddingsCommand(ctx, []string{"build", "--project", "p1", "--generation", started.Result.ID,
		"--revision", "wrong"}, io.Discard); !errors.Is(err, pg.ErrEmbeddingConflict) {
		t.Fatalf("model mismatch: %v", err)
	}
	output.Reset()
	if err := reportEmbeddingsCommand(ctx, []string{"start", "--project", "p1", "--key", uuid.NewString(),
		"--revision", "v1", "--dimensions", "3"}, &output); err != nil {
		t.Fatal(err)
	}
	var second struct {
		Result pg.ReportEmbeddingGeneration `json:"result"`
	}
	if err := json.Unmarshal(output.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"cancel", "prune"} {
		if err := reportEmbeddingsCommand(ctx, []string{operation, "--project", "p1", "--generation", second.Result.ID}, io.Discard); err != nil {
			t.Fatalf("%s: %v", operation, err)
		}
	}
}
