// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Cancelling at a database boundary exercises real transaction/connection cleanup. No fabricated
// rows or successful writes are supplied: PostgreSQL still owns every rollback and lock release.
type embeddingQueryFault struct {
	match string
	fired atomic.Bool
}

func (f *embeddingQueryFault) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.Contains(data.SQL, f.match) && f.fired.CompareAndSwap(false, true) {
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		return cancelled
	}
	return ctx
}
func (*embeddingQueryFault) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestEmbeddingPageDatabaseFailuresNeverPublishPartialStateOrStrandTheWorkerLock(t *testing.T) {
	ctx := context.Background()
	owner := testPool(t)
	schema := tenant(t, owner, "embedding_database_failure")
	defer owner.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	if _, err := pg.NewObservationStore(owner).Append(ctx, schema, turnOf(domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "source"})); err != nil {
		t.Fatal(err)
	}
	store := pg.NewMessageEmbeddingStore(owner, schema)
	actor := uuid.NewString()
	model := pg.EmbeddingModel{Name: "test", Revision: "v1", EndpointHash: strings.Repeat("a", 64), Dimensions: 3}
	generation, err := store.Start(ctx, "p1", uuid.NewString(), actor, model)
	if err != nil {
		t.Fatal(err)
	}
	good := func(context.Context, []string) ([][]float32, error) { return [][]float32{{1, 0, 0}}, nil }
	for _, point := range []string{
		"pg_try_advisory_lock", "FOR UPDATE OF b", "SET lease_id=$3", "octet_length(m.content)", "CASE WHEN octet_length", "coalesce(lease_id::text", "sha256(convert_to(m.content,'UTF8'))", "WHERE c.chunk_id IS NULL", "ORDER BY chunk_id FOR UPDATE", "c.source_observation_id,c.source_message_ordinal", "o.scope,'chunk'", "message_embedding(scope,generation_id,chunk_id", "v.source_observation_id,v.input_digest", "o.scope,'message_embedding'", "d.scope,d.data_subject_id,d.pipeline_version", "SET after_offset=$3", "audit_entry(operation,principal",
	} {
		t.Run(point, func(t *testing.T) {
			if err := store.Repair(ctx, "p1", generation.ID, actor); err != nil {
				t.Fatal(err)
			}
			if _, err := owner.Exec(ctx, schema.SQL(`DELETE FROM {schema}.message_embedding`)); err != nil {
				t.Fatal(err)
			}
			fault := &embeddingQueryFault{match: point}
			config, err := pgxpool.ParseConfig(os.Getenv("TAISCE_TEST_DSN"))
			if err != nil {
				t.Fatal(err)
			}
			config.ConnConfig.Tracer = fault
			pool, err := pgxpool.NewWithConfig(ctx, config)
			if err != nil {
				t.Fatal(err)
			}
			_, err = pg.NewMessageEmbeddingStore(pool, schema).BuildPage(ctx, "p1", generation.ID, actor, model, 64, good)
			pool.Close()
			if err == nil || !fault.fired.Load() {
				t.Fatalf("failure boundary was not exercised: %v", err)
			}
			current, err := store.Generation(ctx, "p1", generation.ID)
			if err != nil {
				t.Fatal(err)
			}
			var count int
			if err := owner.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.message_embedding`)).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if current.After.Offset != -1 || current.Stored != 0 || count != 0 {
				t.Fatalf("partial page after failure: %+v rows=%d", current, count)
			}
			if page, err := store.BuildPage(ctx, "p1", generation.ID, actor, model, 64, good); err != nil || page.Stored != 1 {
				t.Fatalf("worker lock or retry broken: %+v %v", page, err)
			}
		})
	}
}

func TestEmbeddingAllocationFailureIsAtomicAtEveryStorageBoundary(t *testing.T) {
	ctx := context.Background()
	owner := testPool(t)
	schema := tenant(t, owner, "embedding_allocation_failure")
	defer owner.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	store := pg.NewMessageEmbeddingStore(owner, schema)
	actor := uuid.NewString()
	model := pg.EmbeddingModel{Name: "test", Revision: "v1", EndpointHash: strings.Repeat("a", 64), Dimensions: 3}
	for _, point := range []string{"pg_advisory_xact_lock", "g.operation_key=$2", "suspended_at IS NULL", "state<>'discarded'", "coalesce(max(log_offset)", "embedding_generation\n", "embedding_build(scope,generation_id,", "CREATE TABLE", "CREATE INDEX", "g.generation_id=$2"} {
		t.Run(point, func(t *testing.T) {
			key := uuid.NewString()
			fault := &embeddingQueryFault{match: point}
			config, err := pgxpool.ParseConfig(os.Getenv("TAISCE_TEST_DSN"))
			if err != nil {
				t.Fatal(err)
			}
			config.ConnConfig.Tracer = fault
			pool, err := pgxpool.NewWithConfig(ctx, config)
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			if _, err := pg.NewMessageEmbeddingStore(pool, schema).Start(ctx, "p1", key, actor, model); err == nil || !fault.fired.Load() {
				t.Fatalf("failure not exercised: %v", err)
			}
			var count int
			if err := owner.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.embedding_generation WHERE operation_key=$1::uuid`), key).Scan(&count); err != nil || count != 0 {
				t.Fatalf("partial allocation %d %v", count, err)
			}
			generation, err := store.Start(ctx, "p1", key, actor, model)
			if err != nil {
				t.Fatalf("allocation retry: %v", err)
			}
			if err := store.Cancel(ctx, "p1", generation.ID, actor); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Prune(ctx, "p1", generation.ID, actor, 64); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestEmbeddingPolicyFailuresPreserveTheActiveGeneration(t *testing.T) {
	ctx := context.Background()
	owner := testPool(t)
	schema := tenant(t, owner, "embedding_policy_failure")
	defer owner.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	store := pg.NewMessageEmbeddingStore(owner, schema)
	actor := uuid.NewString()
	model := pg.EmbeddingModel{Name: "test", Revision: "v1", EndpointHash: strings.Repeat("a", 64), Dimensions: 3}
	good := func(context.Context, []string) ([][]float32, error) { return nil, nil }
	active, err := store.Start(ctx, "p1", uuid.NewString(), actor, model)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.BuildPage(ctx, "p1", active.ID, actor, model, 64, good); err != nil {
		t.Fatal(err)
	}
	if err := store.Activate(ctx, "p1", active.ID, actor); err != nil {
		t.Fatal(err)
	}
	candidate, err := store.Start(ctx, "p1", uuid.NewString(), actor, model)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.BuildPage(ctx, "p1", candidate.ID, actor, model, 64, good); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ operation, point string }{
		{"activate", "pg_advisory_xact_lock"}, {"activate", "FOR UPDATE OF b"}, {"activate", "v.input_digest<>sha256"}, {"activate", "embedding_active(scope,generation_id)"}, {"activate", "audit_entry(operation,principal"},
		{"cancel", "pg_advisory_xact_lock"}, {"cancel", "FOR UPDATE OF b"}, {"cancel", "SET state='cancelled'"}, {"cancel", "audit_entry(operation,principal"},
		{"repair", "pg_advisory_xact_lock"}, {"repair", "FOR UPDATE OF b"}, {"repair", "SET state='building'"}, {"repair", "audit_entry(operation,principal"},
		{"prune", "pg_advisory_xact_lock"}, {"prune", "FOR UPDATE OF b"}, {"prune", "DELETE FROM"}, {"prune", "SELECT EXISTS(SELECT 1 FROM"}, {"prune", "SELECT EXISTS(SELECT 1 FROM pg_inherits"}, {"prune", "DETACH PARTITION"}, {"prune", "DROP TABLE"}, {"prune", "SET state='discarded'"}, {"prune", "audit_entry(operation,principal"},
		{"search", "FOR SHARE OF b"}, {"search", "WITH candidates"}, {"search", "audit_entry(operation,principal"},
	} {
		t.Run(tc.operation+"/"+tc.point, func(t *testing.T) {
			fault := &embeddingQueryFault{match: tc.point}
			config, err := pgxpool.ParseConfig(os.Getenv("TAISCE_TEST_DSN"))
			if err != nil {
				t.Fatal(err)
			}
			config.ConnConfig.Tracer = fault
			pool, err := pgxpool.NewWithConfig(ctx, config)
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			failing := pg.NewMessageEmbeddingStore(pool, schema)
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
				_, err = failing.Search(ctx, "p1", actor, model, []float32{1, 0, 0}, pg.MessageSearchOptions{Limit: 1})
			}
			if err == nil || !fault.fired.Load() {
				t.Fatalf("failure not exercised: %v", err)
			}
			current, err := store.Active(ctx, "p1")
			if err != nil || current.ID != active.ID {
				t.Fatalf("active changed %+v %v", current, err)
			}
			unchanged, err := store.Generation(ctx, "p1", candidate.ID)
			if err != nil || unchanged.State != "ready" {
				t.Fatalf("candidate changed %+v %v", unchanged, err)
			}
		})
	}
	if err := store.Activate(ctx, "p1", candidate.ID, actor); err != nil {
		t.Fatalf("cutover retry: %v", err)
	}
	if _, err := store.Prune(ctx, "p1", active.ID, actor, 64); err != nil {
		t.Fatalf("retired cleanup: %v", err)
	}
}
