package migrate

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// A database is required. These tests assert behaviour of real DDL — partition pruning, constraint
// enforcement — and a fake would assert only that the fake was written to match the assertion.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TAISCE_TEST_DSN")
	if dsn == "" {
		t.Skip("TAISCE_TEST_DSN is not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// freshSchema drops and recreates, so every test starts from the state a new customer starts from.
func freshSchema(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS memory CASCADE`); err != nil {
		t.Fatalf("drop schema: %v", err)
	}
	if _, err := Apply(context.Background(), pool); err != nil {
		t.Fatalf("apply: %v", err)
	}
}

func TestTheMigrationApplies(t *testing.T) {
	pool := testPool(t)
	freshSchema(t, pool)

	var version int
	if err := pool.QueryRow(context.Background(),
		`SELECT max(version) FROM memory.schema_migration`).Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if version != 1 {
		t.Fatalf("expected schema version 1, got %d", version)
	}
}

// Applying twice must be a no-op, because a migrator runs on every deploy and a second run is the
// normal case rather than the exception.
func TestApplyingTwiceChangesNothing(t *testing.T) {
	pool := testPool(t)
	freshSchema(t, pool)

	applied, err := Apply(context.Background(), pool)
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if len(applied) != 0 {
		t.Fatalf("second apply ran %v; it should have run nothing", applied)
	}
}

// ISSUE #1's CLAIM. memra's migration 0021 dropped its HNSW index because every recall carries a
// scope predicate and the planner therefore never reached the index. This asserts the fix works:
// the planner prunes to one project's partition FIRST, and the index scan then runs inside a graph
// holding only that project's vectors.
func TestTheVectorSearchPrunesToOnePartitionAndUsesItsIndex(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	freshSchema(t, pool)

	for _, scope := range []string{"projecta", "projectb"} {
		if err := ProvisionScope(ctx, pool, scope); err != nil {
			t.Fatalf("provision %s: %v", scope, err)
		}
	}
	// Enough rows that a sequential scan is not simply the cheapest option regardless.
	if _, err := pool.Exec(ctx, `
		INSERT INTO memory.chunk (chunk_id, scope, source_observation_id, text, occurred_at, source_role, embedding)
		SELECT gen_random_uuid(),
		       CASE WHEN i % 2 = 0 THEN 'projecta' ELSE 'projectb' END,
		       gen_random_uuid(), 'chunk ' || i, now(), 'user',
		       (SELECT array_agg(random())::vector FROM generate_series(1, 1024))
		  FROM generate_series(1, 2000) i`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := pool.Exec(ctx, `ANALYZE memory.chunk`); err != nil {
		t.Fatalf("analyze: %v", err)
	}

	plan := explain(t, pool, `
		SELECT chunk_id FROM memory.chunk
		 WHERE scope = 'projecta'
		 ORDER BY embedding <=> (SELECT array_agg(0.5)::vector FROM generate_series(1, 1024))
		 LIMIT 10`)
	t.Logf("plan:\n%s", plan)

	// Pruning: the other project's partition must not appear at all.
	if strings.Contains(plan, "chunk_projectb") {
		t.Fatalf("the query touched another project's partition — pruning did not happen:\n%s", plan)
	}
	// And the surviving partition's ANN index must be what answers it.
	if !strings.Contains(plan, "chunk_projecta_ann_idx") {
		t.Fatalf("the partition's HNSW index was not used, which is the whole point of #1:\n%s", plan)
	}
}

// Provisioning is retried by operators who are unsure whether it ran.
// explain returns the WHOLE plan. EXPLAIN yields one row per line, so reading it with QueryRow
// silently returns only the first — which made this test pass its pruning assertion and fail its
// index assertion for the same reason: it was looking at one line of the plan.
func explain(t *testing.T, pool *pgxpool.Pool, query string) string {
	t.Helper()
	rows, err := pool.Query(context.Background(), "EXPLAIN "+query)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		lines = append(lines, line)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read plan: %v", err)
	}
	return strings.Join(lines, "\n")
}

func TestProvisioningTwiceIsANoOp(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	freshSchema(t, pool)

	if err := ProvisionScope(ctx, pool, "repeated"); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := ProvisionScope(ctx, pool, "repeated"); err != nil {
		t.Fatalf("second provision should be a no-op, got: %v", err)
	}
}

// The partition name is built by string interpolation because DDL cannot take a bind value, so the
// allowlist is the only thing between a scope and an injection.
func TestAScopeThatIsNotAnIdentifierIsRefused(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	freshSchema(t, pool)

	for _, bad := range []string{
		`a'; DROP SCHEMA memory CASCADE; --`,
		`Uppercase`,
		`has space`,
		``,
		strings.Repeat("x", 64),
	} {
		if err := ProvisionScope(ctx, pool, bad); err == nil {
			t.Fatalf("scope %q was accepted and must not be", bad)
		}
	}
	// And the schema is still there, which is the assertion that matters.
	var exists bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.schemata WHERE schema_name = 'memory')`).Scan(&exists); err != nil {
		t.Fatalf("check schema: %v", err)
	}
	if !exists {
		t.Fatal("the memory schema is gone")
	}
}

// A write for an unprovisioned scope must land somewhere visible rather than fail: losing a
// customer's memory because provisioning missed a step is the worse failure.
func TestAnUnprovisionedScopeStillAcceptsWrites(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	freshSchema(t, pool)

	if _, err := pool.Exec(ctx, `
		INSERT INTO memory.chunk (chunk_id, scope, source_observation_id, text, occurred_at, source_role)
		VALUES (gen_random_uuid(), 'neverprovisioned', gen_random_uuid(), 'orphan', now(), 'user')`); err != nil {
		t.Fatalf("write to unprovisioned scope must succeed: %v", err)
	}

	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM memory.chunk_unpartitioned`).Scan(&n); err != nil {
		t.Fatalf("count default partition: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected the row in the default partition, found %d", n)
	}
}
