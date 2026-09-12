// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ensera-ai/taisce/internal/migrate"
)

// ── A rebuild says what it left behind, at the command ────────────────────────────────────────
//
// The staleness section is added beside the rebuild's own result, never around it: an operator
// scripts against this output, and moving a field somebody already reads to make room for a new one
// is the wrong way round. And a project with nothing stale says nothing, which is what decides
// whether this is information or noise.
func TestARebuildsOwnFieldsStayWhereTheyAreAndStalenessIsAddedBesideThem(t *testing.T) {
	ctx := context.Background()
	env := complete(t)
	env[envSchema] = "cmd_rebuild_stale"
	env[envAdminDSN] = env[envMemoryDSN]
	withEnv(t, env)
	t.Setenv("TAISCE_INFERENCE_ENDPOINT", "http://127.0.0.1:1/v1")
	t.Setenv("TAISCE_INFERENCE_EXTRACTOR_MODEL", "rebuild-staleness")
	t.Setenv("TAISCE_INFERENCE_ALLOWLIST", "127.0.0.1:1")

	pool, err := pgxpool.New(ctx, env[envAdminDSN])
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	dropSchema(t, env[envSchema])
	defer pool.Exec(ctx, "DROP SCHEMA "+env[envSchema]+" CASCADE")
	if err := migrate.ProvisionMemorySchema(ctx, pool, env[envSchema]); err != nil {
		t.Fatal(err)
	}
	if err := migrate.ProvisionScope(ctx, pool, env[envSchema], "p1"); err != nil {
		t.Fatal(err)
	}

	// An empty project: the rebuild's own fields, and not a word about staleness.
	var quiet bytes.Buffer
	if err := rebuildCommand(ctx, []string{"project", "--project", "p1", "--key", uuid.NewString()}, &quiet); err != nil {
		t.Fatal(err)
	}
	var clean map[string]any
	if err := json.Unmarshal(quiet.Bytes(), &clean); err != nil {
		t.Fatalf("the output did not parse: %v", err)
	}
	if clean["status"] != "completed" {
		t.Fatalf("the rebuild's own fields did not survive: %v", clean)
	}
	if _, told := clean["stale"]; told {
		t.Fatalf("a project with nothing stale was told about staleness: %v", clean)
	}
	if _, told := clean["what_to_do"]; told {
		t.Fatal("a project with nothing to do was given advice")
	}

	// A subject waiting for a report is the state a rebuild leaves, and now it is said.
	if _, err := pool.Exec(ctx,
		`INSERT INTO `+env[envSchema]+`.community (scope, community_id, level) VALUES ('p1', gen_random_uuid(), 0)`); err != nil {
		t.Fatal(err)
	}
	var told bytes.Buffer
	if err := rebuildCommand(ctx, []string{"project", "--project", "p1", "--key", uuid.NewString()}, &told); err != nil {
		t.Fatal(err)
	}
	var reported map[string]any
	if err := json.Unmarshal(told.Bytes(), &reported); err != nil {
		t.Fatal(err)
	}
	if reported["status"] != "completed" {
		t.Fatalf("adding staleness moved the rebuild's own fields: %v", reported)
	}
	stale, ok := reported["stale"].(map[string]any)
	if !ok || stale["communities_without_a_report"] != float64(1) {
		t.Fatalf("the subject waiting for a report was not reported: %v", reported["stale"])
	}
	advice, ok := reported["what_to_do"].([]any)
	if !ok || len(advice) != 1 {
		t.Fatalf("one thing was stale and the advice was %v", reported["what_to_do"])
	}
	if !bytes.Contains([]byte(advice[0].(string)), []byte("subject pass")) {
		t.Fatalf("the advice does not say what will fix it: %v", advice[0])
	}

	// A generation one turn behind says "one turn", not "1 turns". An operator reading this at two
	// in the morning is owed a sentence rather than a template with a number in it.
	if _, err := pool.Exec(ctx, `INSERT INTO `+env[envSchema]+`.observation
	       (observation_id, scope, log_offset, kind, occurred_at, ingested_at, source_role)
	       VALUES (gen_random_uuid(), 'p1', 1, 'turn', now(), now(), 'user')`); err != nil {
		t.Fatal(err)
	}
	var generation string
	if err := pool.QueryRow(ctx, `INSERT INTO `+env[envSchema]+`.embedding_generation
	       (generation_id, scope, operation_key, model_name, model_revision, endpoint_hash,
	        identity_hash, dimensions, through_offset)
	       VALUES (gen_random_uuid(), 'p1', gen_random_uuid(), 'advice-fixture', 'v1',
	               repeat('a',64), repeat('b',64), 3, 0)
	       RETURNING generation_id::text`).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO `+env[envSchema]+`.embedding_active (scope, generation_id)
	       VALUES ('p1', $1::uuid) ON CONFLICT (scope) DO UPDATE SET generation_id = EXCLUDED.generation_id`,
		generation); err != nil {
		t.Fatal(err)
	}

	var behind bytes.Buffer
	if err := rebuildCommand(ctx, []string{"project", "--project", "p1", "--key", uuid.NewString()}, &behind); err != nil {
		t.Fatal(err)
	}
	var lagging map[string]any
	if err := json.Unmarshal(behind.Bytes(), &lagging); err != nil {
		t.Fatal(err)
	}
	said := false
	for _, line := range lagging["what_to_do"].([]any) {
		if bytes.Contains([]byte(line.(string)), []byte("one turn behind")) {
			said = true
		}
	}
	if !said {
		t.Fatalf("a generation one turn behind was not described as one turn: %v", lagging["what_to_do"])
	}

	// And more than one is counted, not pluralised wrongly.
	if _, err := pool.Exec(ctx, `INSERT INTO `+env[envSchema]+`.observation
	       (observation_id, scope, log_offset, kind, occurred_at, ingested_at, source_role)
	       VALUES (gen_random_uuid(), 'p1', 2, 'turn', now(), now(), 'user')`); err != nil {
		t.Fatal(err)
	}
	var further bytes.Buffer
	if err := rebuildCommand(ctx, []string{"project", "--project", "p1", "--key", uuid.NewString()}, &further); err != nil {
		t.Fatal(err)
	}
	var behindTwo map[string]any
	if err := json.Unmarshal(further.Bytes(), &behindTwo); err != nil {
		t.Fatal(err)
	}
	counted := false
	for _, line := range behindTwo["what_to_do"].([]any) {
		if bytes.Contains([]byte(line.(string)), []byte("2 turns behind")) {
			counted = true
		}
	}
	if !counted {
		t.Fatalf("a generation two turns behind was not counted: %v", behindTwo["what_to_do"])
	}

	// A surface that keeps no offset is described by the database's own verdict rather than by a
	// number that does not exist for it.
	var entityGeneration string
	if err := pool.QueryRow(ctx, `INSERT INTO `+env[envSchema]+`.entity_embedding_generation
	       (generation_id, scope, operation_key, model_name, model_revision, endpoint_hash,
	        identity_hash, dimensions)
	       VALUES (gen_random_uuid(), 'p1', gen_random_uuid(), 'advice-fixture', 'v1',
	               repeat('c',64), repeat('d',64), 3)
	       RETURNING generation_id::text`).Scan(&entityGeneration); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO `+env[envSchema]+`.entity_embedding_build
	       (scope, generation_id, state) VALUES ('p1', $1::uuid, 'stale')`, entityGeneration); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO `+env[envSchema]+`.entity_embedding_active (scope, generation_id)
	       VALUES ('p1', $1::uuid) ON CONFLICT (scope) DO UPDATE SET generation_id = EXCLUDED.generation_id`,
		entityGeneration); err != nil {
		t.Fatal(err)
	}

	var verdict bytes.Buffer
	if err := rebuildCommand(ctx, []string{"project", "--project", "p1", "--key", uuid.NewString()}, &verdict); err != nil {
		t.Fatal(err)
	}
	var stated map[string]any
	if err := json.Unmarshal(verdict.Bytes(), &stated); err != nil {
		t.Fatal(err)
	}
	described := false
	for _, line := range stated["what_to_do"].([]any) {
		if bytes.Contains([]byte(line.(string)), []byte("set of things that has since changed")) {
			described = true
		}
	}
	if !described {
		t.Fatalf("a stale surface with no offset was not described: %v", stated["what_to_do"])
	}
}
