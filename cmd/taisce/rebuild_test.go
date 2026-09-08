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
	"github.com/ensera-ai/taisce/internal/extract"
	"github.com/ensera-ai/taisce/internal/formation"
	"github.com/ensera-ai/taisce/internal/infra/inference"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type commandRebuildModel struct{ identity string }

func (m commandRebuildModel) ExtractionIdentity() string { return m.identity }

func (commandRebuildModel) Propose(_ context.Context, message domain.Message, _ domain.Ontology) ([]extract.Proposal, error) {
	quote := "I work at Ensera"
	if message.Content != quote {
		return nil, nil
	}
	return []extract.Proposal{{
		Subject: "I", Predicate: "works_at", Object: "Ensera", Statement: quote,
		Quote: quote, Polarity: domain.PolarityAsserted, Tense: domain.TensePresent,
	}}, nil
}

func TestRebuildCommandValidatesBeforeConnecting(t *testing.T) {
	t.Setenv(envAdminDSN, "")
	t.Setenv(envMemoryDSN, "")
	for _, args := range [][]string{
		nil,
		{"unknown"},
		{"facts"},
		{"facts", "--bad"},
		{"facts", "--project", "not valid", "--source", uuid.NewString(), "--key", uuid.NewString()},
		{"facts", "--project", "p1", "--source", "bad", "--key", uuid.NewString()},
		{"facts", "--project", "p1", "--source", uuid.NewString(), "--key", "bad"},
		{"facts", "--project", "p1", "--source", uuid.NewString(), "--key", uuid.NewString(), "extra"},
	} {
		if err := rebuildCommand(context.Background(), args, io.Discard); err == nil {
			t.Fatalf("invalid rebuild accepted: %v", args)
		}
	}
}

func TestRebuildCommandReplaysCommittedGeneration(t *testing.T) {
	ctx := context.Background()
	env := complete(t)
	env[envSchema] = "cmd_rebuild"
	env[envAdminDSN] = env[envMemoryDSN]
	withEnv(t, env)
	t.Setenv("TAISCE_INFERENCE_ENDPOINT", "http://127.0.0.1:1/v1")
	t.Setenv("TAISCE_INFERENCE_EXTRACTOR_MODEL", "rebuild-command-test")
	t.Setenv("TAISCE_INFERENCE_ALLOWLIST", "127.0.0.1:1")

	pool, err := pgxpool.New(ctx, env[envAdminDSN])
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	dropSchema(t, env[envSchema])
	defer pool.Exec(ctx, "DROP SCHEMA cmd_rebuild CASCADE")
	if err := migrate.ProvisionMemorySchema(ctx, pool, env[envSchema]); err != nil {
		t.Fatal(err)
	}
	if err := migrate.ProvisionScope(ctx, pool, env[envSchema], "p1"); err != nil {
		t.Fatal(err)
	}
	schema, _ := pg.NewSchema(env[envSchema])
	observations := pg.NewObservationStore(pool)
	stored, err := observations.Append(ctx, schema, domain.Turn{
		Scope:         "p1",
		DataSubjectID: "subject-1",
		OccurredAt:    time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC),
		Messages:      []domain.Message{{Role: domain.RoleUser, Content: "I work at Ensera"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	key, principal := uuid.NewString(), uuid.NewString()
	vocabulary, err := pg.LoadVocabulary(ctx, pool, schema)
	if err != nil {
		t.Fatal(err)
	}
	config := inference.Config{Endpoint: "http://127.0.0.1:1/v1", Model: "rebuild-command-test"}
	rebuilder := formation.NewRebuilder(pg.NewFactStore(pool), extract.NewWith(commandRebuildModel{identity: inference.NewModel(config).ExtractionIdentity()}, vocabulary))
	if _, err := rebuilder.Rebuild(ctx, schema, "p1", stored.ID, key, principal); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := rebuildCommand(ctx, []string{"facts", "--project", "p1", "--source", stored.ID, "--key", key}, &out); err != nil {
		t.Fatal(err)
	}
	var result pg.GenerationResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Replayed || result.SourceID != stored.ID || result.Asserted != 1 {
		t.Fatalf("unexpected replay result: %+v", result)
	}
	t.Run("missing database configuration", func(t *testing.T) {
		t.Setenv(envAdminDSN, "")
		t.Setenv(envMemoryDSN, "")
		if err := rebuildCommand(ctx, []string{"facts", "--project", "p1", "--source", stored.ID, "--key", key}, io.Discard); err == nil {
			t.Fatal("rebuild accepted missing database configuration")
		}
	})
	t.Run("missing project", func(t *testing.T) {
		if err := rebuildCommand(ctx, []string{"facts", "--project", "missing_project", "--source", stored.ID, "--key", key}, io.Discard); err == nil {
			t.Fatal("rebuild accepted a missing project")
		}
	})
	t.Run("missing inference configuration", func(t *testing.T) {
		t.Setenv("TAISCE_INFERENCE_ENDPOINT", "")
		if err := rebuildCommand(ctx, []string{"facts", "--project", "p1", "--source", stored.ID, "--key", key}, io.Discard); err == nil {
			t.Fatal("rebuild accepted missing inference configuration")
		}
	})
	t.Run("provider unavailable before publication", func(t *testing.T) {
		var output bytes.Buffer
		if err := rebuildCommand(ctx, []string{"facts", "--project", "p1", "--source", stored.ID, "--key", uuid.NewString()}, &output); !errors.Is(err, formation.ErrGenerationInference) || output.Len() != 0 {
			t.Fatalf("provider failure emitted a successful result: err=%v output=%s", err, output.String())
		}
	})
	t.Run("missing vocabulary", func(t *testing.T) {
		if _, err := pool.Exec(ctx, schema.SQL(`ALTER TABLE {schema}.predicate RENAME TO predicate_unavailable`)); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if _, err := pool.Exec(ctx, schema.SQL(`ALTER TABLE {schema}.predicate_unavailable RENAME TO predicate`)); err != nil {
				t.Error(err)
			}
		})
		if err := rebuildCommand(ctx, []string{"facts", "--project", "p1", "--source", stored.ID, "--key", key}, io.Discard); err == nil {
			t.Fatal("rebuild accepted missing vocabulary")
		}
	})
}
