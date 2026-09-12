// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func entityFailurePool(t *testing.T, fault *embeddingQueryFault) *pgxpool.Pool {
	t.Helper()
	config, err := pgxpool.ParseConfig(os.Getenv("TAISCE_TEST_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.Tracer = fault
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestEntityEmbeddingAllocationRollsBackAtEveryStorageBoundary(t *testing.T) {
	ctx := context.Background()
	owner := testPool(t)
	schema := tenant(t, owner, "entity_embedding_allocation_failure")
	defer owner.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	actor := uuid.NewString()
	model := pg.EmbeddingModel{Name: "test", Revision: "v1", EndpointHash: strings.Repeat("7", 64), Dimensions: 3}
	for _, point := range []string{"pg_advisory_xact_lock", "g.operation_key=$2", "suspended_at IS NULL", "state<>'discarded'",
		"identity_kind='named'", "entity_embedding_generation\n", "entity_embedding_target", "entity_embedding_build(scope",
		"CREATE TABLE", "CREATE INDEX", "audit_entry(operation,principal", "g.generation_id=$2"} {
		t.Run(point, func(t *testing.T) {
			key := uuid.NewString()
			fault := &embeddingQueryFault{match: point}
			pool := entityFailurePool(t, fault)
			if _, err := pg.NewEntityEmbeddingStore(pool, schema).Start(ctx, "p1", key, actor, model); err == nil || !fault.fired.Load() {
				t.Fatalf("failure boundary was not exercised: %v", err)
			}
			var count int
			if err := owner.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.entity_embedding_generation WHERE operation_key=$1::uuid`), key).Scan(&count); err != nil || count != 0 {
				t.Fatalf("partial allocation count=%d err=%v", count, err)
			}
		})
	}
}

func TestEntityEmbeddingPageFailuresRollbackAndReleaseTheWorkerLock(t *testing.T) {
	ctx := context.Background()
	owner := testPool(t)
	schema := tenant(t, owner, "entity_embedding_page_failure")
	defer owner.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	if _, err := storeEntityName(ctx, owner, schema, "base", "Ensera"); err != nil {
		t.Fatal(err)
	}
	store := pg.NewEntityEmbeddingStore(owner, schema)
	actor := uuid.NewString()
	model := pg.EmbeddingModel{Name: "test", Revision: "v1", EndpointHash: strings.Repeat("8", 64), Dimensions: 3}
	generation, err := store.Start(ctx, "p1", uuid.NewString(), actor, model)
	if err != nil {
		t.Fatal(err)
	}
	for _, point := range []string{"pg_try_advisory_lock", "FOR UPDATE OF b", "SET lease_id=$3", "SELECT entity_id::text",
		"WITH selected AS", "coalesce(lease_id::text", "ORDER BY observation_id FOR SHARE", "pg_advisory_xact_lock",
		"INSERT INTO " + schema.String() + ".entity_embedding\n", "AND (input_digest,source_digest,source_count)",
		"entity_embedding_source\n", "o.scope,'entity_embedding'", "SET after_entity_id=$3", "audit_entry(operation,principal"} {
		t.Run(point, func(t *testing.T) {
			if err := store.Repair(ctx, "p1", generation.ID, actor); err != nil {
				t.Fatal(err)
			}
			if _, err := owner.Exec(ctx, schema.SQL(`DELETE FROM {schema}.entity_embedding`)); err != nil {
				t.Fatal(err)
			}
			fault := &embeddingQueryFault{match: point}
			pool := entityFailurePool(t, fault)
			_, err := pg.NewEntityEmbeddingStore(pool, schema).BuildPage(ctx, "p1", generation.ID, actor, model, 64, entityVectors)
			if err == nil || !fault.fired.Load() {
				t.Fatalf("failure boundary was not exercised: %v", err)
			}
			current, err := store.Generation(ctx, "p1", generation.ID)
			if err != nil || current.Examined != 0 || current.Stored != 0 {
				t.Fatalf("partial progress: %+v %v", current, err)
			}
			if page, err := store.BuildPage(ctx, "p1", generation.ID, actor, model, 64, entityVectors); err != nil || page.Stored == 0 {
				t.Fatalf("worker lock or retry broken: %+v %v", page, err)
			}
		})
	}
}

func TestEntityEmbeddingPolicyFailuresPreserveActiveAndCandidateGenerations(t *testing.T) {
	ctx := context.Background()
	owner := testPool(t)
	schema := tenant(t, owner, "entity_embedding_policy_failure")
	defer owner.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	if _, err := storeEntityName(ctx, owner, schema, "base", "Ensera"); err != nil {
		t.Fatal(err)
	}
	store := pg.NewEntityEmbeddingStore(owner, schema)
	actor := uuid.NewString()
	model := pg.EmbeddingModel{Name: "test", Revision: "v1", EndpointHash: strings.Repeat("9", 64), Dimensions: 3}
	active, err := store.Start(ctx, "p1", uuid.NewString(), actor, model)
	if err != nil {
		t.Fatal(err)
	}
	buildEntityGeneration(t, store, active, actor, model)
	if err := store.Activate(ctx, "p1", active.ID, actor); err != nil {
		t.Fatal(err)
	}
	candidate, err := store.Start(ctx, "p1", uuid.NewString(), actor, model)
	if err != nil {
		t.Fatal(err)
	}
	buildEntityGeneration(t, store, candidate, actor, model)
	for _, tc := range []struct{ operation, point string }{
		{"activate", "pg_advisory_xact_lock"}, {"activate", "FOR UPDATE OF b"}, {"activate", "SELECT EXISTS(SELECT 1 FROM"}, {"activate", "entity_embedding_active(scope,generation_id)"}, {"activate", "audit_entry(operation,principal"},
		{"cancel", "pg_advisory_xact_lock"}, {"cancel", "FOR UPDATE OF b"}, {"cancel", "SET state='cancelled'"}, {"cancel", "audit_entry(operation,principal"},
		{"repair", "pg_advisory_xact_lock"}, {"repair", "FOR UPDATE OF b"}, {"repair", "SET state='building'"}, {"repair", "audit_entry(operation,principal"},
		{"prune", "pg_advisory_xact_lock"}, {"prune", "FOR UPDATE OF b"}, {"prune", "DELETE FROM"}, {"prune", "SELECT EXISTS(SELECT 1 FROM"}, {"prune", "DETACH PARTITION"}, {"prune", "DROP TABLE"}, {"prune", "SET state='discarded'"}, {"prune", "audit_entry(operation,principal"},
		{"search", "FOR SHARE OF b"}, {"search", "WITH candidates"}, {"search", "audit_entry(operation,principal"},
	} {
		t.Run(tc.operation+"/"+tc.point, func(t *testing.T) {
			fault := &embeddingQueryFault{match: tc.point}
			failing := pg.NewEntityEmbeddingStore(entityFailurePool(t, fault), schema)
			switch tc.operation {
			case "activate":
				err = failing.Activate(ctx, "p1", candidate.ID, actor)
			case "cancel":
				err = failing.Cancel(ctx, "p1", candidate.ID, actor)
			case "repair":
				err = failing.Repair(ctx, "p1", candidate.ID, actor)
			case "prune":
				_, err = failing.Prune(ctx, "p1", candidate.ID, actor, 64)
			case "search":
				_, err = failing.Search(ctx, "p1", actor, model, []float32{1, 0, 0}, pg.EntityCandidateOptions{Limit: 1})
			}
			if err == nil || !fault.fired.Load() {
				t.Fatalf("failure boundary was not exercised: %v", err)
			}
			current, currentErr := store.Active(ctx, "p1")
			if currentErr != nil || current.ID != active.ID {
				t.Fatalf("active changed: %+v %v", current, currentErr)
			}
			unchanged, currentErr := store.Generation(ctx, "p1", candidate.ID)
			if currentErr != nil || unchanged.State != "ready" {
				t.Fatalf("candidate changed: %+v %v", unchanged, currentErr)
			}
		})
	}
}
