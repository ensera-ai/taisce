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

func reportFailurePool(t *testing.T, fault *embeddingQueryFault) *pgxpool.Pool {
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

func TestReportEmbeddingAllocationRollsBackAtEveryStorageBoundary(t *testing.T) {
	ctx := context.Background()
	owner := testPool(t)
	schema := tenant(t, owner, "report_embedding_allocation_failure")
	defer owner.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	actor := uuid.NewString()
	model := pg.EmbeddingModel{Name: "test", Revision: "v1", EndpointHash: strings.Repeat("7", 64), Dimensions: 3}
	for _, point := range []string{"pg_advisory_xact_lock", "g.operation_key=$2", "suspended_at IS NULL", "state<>'discarded'",
		"community_report WHERE scope=$1", "report_embedding_generation\n", "report_embedding_target", "report_embedding_build(scope",
		"CREATE TABLE", "CREATE INDEX", "audit_entry(operation,principal", "g.generation_id=$2"} {
		t.Run(point, func(t *testing.T) {
			key := uuid.NewString()
			fault := &embeddingQueryFault{match: point}
			pool := reportFailurePool(t, fault)
			if _, err := pg.NewReportEmbeddingStore(pool, schema).Start(ctx, "p1", key, actor, model); err == nil || !fault.fired.Load() {
				t.Fatalf("failure boundary was not exercised: %v", err)
			}
			var count int
			if err := owner.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.report_embedding_generation WHERE operation_key=$1::uuid`), key).Scan(&count); err != nil || count != 0 {
				t.Fatalf("partial allocation count=%d err=%v", count, err)
			}
		})
	}
}

func TestReportEmbeddingPageFailuresRollbackAndReleaseTheWorkerLock(t *testing.T) {
	ctx := context.Background()
	owner := testPool(t)
	schema := tenant(t, owner, "report_embedding_page_failure")
	defer owner.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	storedReport(t, owner, schema, "Growth plan", "Expansion theme.", "base")
	store := pg.NewReportEmbeddingStore(owner, schema)
	actor := uuid.NewString()
	model := pg.EmbeddingModel{Name: "test", Revision: "v1", EndpointHash: strings.Repeat("8", 64), Dimensions: 3}
	generation, err := store.Start(ctx, "p1", uuid.NewString(), actor, model)
	if err != nil {
		t.Fatal(err)
	}
	for _, point := range []string{"pg_try_advisory_lock", "FOR UPDATE OF b", "SET lease_id=$3", "SELECT report_id::text",
		"SELECT r.title", "coalesce(lease_id::text", "ORDER BY observation_id FOR SHARE", "pg_advisory_xact_lock",
		"INSERT INTO " + schema.String() + ".report_embedding\n", "AND (input_digest,source_digest,source_count)",
		"report_embedding_source\n", "o.scope,'report_embedding'", "SET after_report_id=$3", "audit_entry(operation,principal"} {
		t.Run(point, func(t *testing.T) {
			if err := store.Repair(ctx, "p1", generation.ID, actor); err != nil {
				t.Fatal(err)
			}
			if _, err := owner.Exec(ctx, schema.SQL(`DELETE FROM {schema}.report_embedding`)); err != nil {
				t.Fatal(err)
			}
			fault := &embeddingQueryFault{match: point}
			pool := reportFailurePool(t, fault)
			_, err := pg.NewReportEmbeddingStore(pool, schema).BuildPage(ctx, "p1", generation.ID, actor, model, 64, reportVectors)
			if err == nil || !fault.fired.Load() {
				t.Fatalf("failure boundary was not exercised: %v", err)
			}
			current, err := store.Generation(ctx, "p1", generation.ID)
			if err != nil || current.Examined != 0 || current.Stored != 0 {
				t.Fatalf("partial progress: %+v %v", current, err)
			}
			if page, err := store.BuildPage(ctx, "p1", generation.ID, actor, model, 64, reportVectors); err != nil || page.Stored == 0 {
				t.Fatalf("worker lock or retry broken: %+v %v", page, err)
			}
		})
	}
}

func TestReportEmbeddingPolicyFailuresPreserveActiveAndCandidateGenerations(t *testing.T) {
	ctx := context.Background()
	owner := testPool(t)
	schema := tenant(t, owner, "report_embedding_policy_failure")
	defer owner.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	storedReport(t, owner, schema, "Growth plan", "Expansion theme.", "base")
	store := pg.NewReportEmbeddingStore(owner, schema)
	actor := uuid.NewString()
	model := pg.EmbeddingModel{Name: "test", Revision: "v1", EndpointHash: strings.Repeat("9", 64), Dimensions: 3}
	active, err := store.Start(ctx, "p1", uuid.NewString(), actor, model)
	if err != nil {
		t.Fatal(err)
	}
	buildReportGeneration(t, store, active, actor, model)
	if err := store.Activate(ctx, "p1", active.ID, actor); err != nil {
		t.Fatal(err)
	}
	candidate, err := store.Start(ctx, "p1", uuid.NewString(), actor, model)
	if err != nil {
		t.Fatal(err)
	}
	buildReportGeneration(t, store, candidate, actor, model)
	for _, tc := range []struct{ operation, point string }{
		{"activate", "pg_advisory_xact_lock"}, {"activate", "FOR UPDATE OF b"}, {"activate", "SELECT EXISTS(SELECT 1 FROM"}, {"activate", "report_embedding_active(scope,generation_id)"}, {"activate", "audit_entry(operation,principal"},
		{"cancel", "pg_advisory_xact_lock"}, {"cancel", "FOR UPDATE OF b"}, {"cancel", "SET state='cancelled'"}, {"cancel", "audit_entry(operation,principal"},
		{"repair", "pg_advisory_xact_lock"}, {"repair", "FOR UPDATE OF b"}, {"repair", "SET state='building'"}, {"repair", "audit_entry(operation,principal"},
		{"prune", "pg_advisory_xact_lock"}, {"prune", "FOR UPDATE OF b"}, {"prune", "DELETE FROM"}, {"prune", "SELECT EXISTS(SELECT 1 FROM"}, {"prune", "DETACH PARTITION"}, {"prune", "DROP TABLE"}, {"prune", "SET state='discarded'"}, {"prune", "audit_entry(operation,principal"},
		{"search", "FOR SHARE OF b"}, {"search", "WITH candidates"}, {"search", "audit_entry(operation,principal"},
	} {
		t.Run(tc.operation+"/"+tc.point, func(t *testing.T) {
			fault := &embeddingQueryFault{match: tc.point}
			failing := pg.NewReportEmbeddingStore(reportFailurePool(t, fault), schema)
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
				_, err = failing.Search(ctx, "p1", actor, model, []float32{1, 0, 0}, pg.ReportCandidateOptions{Limit: 1})
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
