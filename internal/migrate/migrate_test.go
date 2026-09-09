package migrate

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
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

// freshTenant drops and re-provisions, so every test starts where a new customer starts. The name is
// per-test so tests cannot see each other's data — which is also the property under test.
func freshTenant(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	tenant := "t_" + strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return '_'
	}, strings.ToLower(t.Name()))
	if len(tenant) > maxIdentifierLength {
		tenant = tenant[:maxIdentifierLength]
	}
	if _, err := pool.Exec(context.Background(),
		`DROP SCHEMA IF EXISTS `+pgx.Identifier{tenant}.Sanitize()+` CASCADE`); err != nil {
		t.Fatalf("drop schema: %v", err)
	}
	if err := ProvisionMemorySchema(context.Background(), pool, tenant); err != nil {
		t.Fatalf("provision tenant: %v", err)
	}
	return tenant
}

func TestTheMigrationApplies(t *testing.T) {
	pool := testPool(t)
	tenant := freshTenant(t, pool)
	schema, _ := NewSchema(tenant)

	// Asserted against what is embedded rather than against a literal: a literal makes every new
	// migration fail this test for the wrong reason, which trains people to update the number
	// without reading what broke.
	all, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want := all[len(all)-1].Version

	var got int
	if err := pool.QueryRow(context.Background(),
		schema.SQL(`SELECT max(version) FROM {schema}.schema_migration`)).Scan(&got); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if got != want {
		t.Fatalf("schema is at version %d; the embedded set goes to %d", got, want)
	}

	// Every embedded migration recorded itself. A migration that ran without recording would be
	// re-applied on the next deploy, and most of these are not idempotent.
	var recorded int
	if err := pool.QueryRow(context.Background(),
		schema.SQL(`SELECT count(*) FROM {schema}.schema_migration`)).Scan(&recorded); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if recorded != len(all) {
		t.Fatalf("%d migrations recorded, %d embedded", recorded, len(all))
	}
}

// Applying twice must be a no-op, because a migrator runs on every deploy and a second run is the
// normal case rather than the exception.
func TestApplyingTwiceChangesNothing(t *testing.T) {
	pool := testPool(t)
	tenant := freshTenant(t, pool)
	schema, _ := NewSchema(tenant)

	applied, err := Apply(context.Background(), pool, schema)
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if len(applied) != 0 {
		t.Fatalf("second apply ran %v; it should have run nothing", applied)
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
	tenant := freshTenant(t, pool)

	if err := ProvisionScope(ctx, pool, tenant, "repeated"); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := ProvisionScope(ctx, pool, tenant, "repeated"); err != nil {
		t.Fatalf("second provision should be a no-op, got: %v", err)
	}
}

// The partition name is built by string interpolation because DDL cannot take a bind value, so the
// allowlist is the only thing between a scope and an injection.
func TestAScopeThatIsNotAnIdentifierIsRefused(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	tenant := freshTenant(t, pool)

	for _, bad := range []string{
		`a'; DROP SCHEMA memory CASCADE; --`,
		`Uppercase`,
		`has space`,
		``,
		strings.Repeat("x", 64),
	} {
		if err := ProvisionScope(ctx, pool, tenant, bad); err == nil {
			t.Fatalf("scope %q was accepted and must not be", bad)
		}
	}
	// And the tenant is still there, which is the assertion that matters.
	var exists bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.schemata WHERE schema_name = $1)`,
		tenant).Scan(&exists); err != nil {
		t.Fatalf("check schema: %v", err)
	}
	if !exists {
		t.Fatal("the tenant schema is gone")
	}
}

// A write for an unprovisioned scope must land somewhere visible rather than fail: losing a
// customer's memory because provisioning missed a step is the worse failure.
func TestAnUnprovisionedScopeStillAcceptsWrites(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	tenant := freshTenant(t, pool)
	schema, _ := NewSchema(tenant)

	if _, err := pool.Exec(ctx, schema.SQL(`
		INSERT INTO {schema}.chunk (chunk_id, scope, source_observation_id, text, occurred_at, source_role)
		VALUES (gen_random_uuid(), 'neverprovisioned', $1::uuid, 'orphan', now(), 'user')`), seedObservation(t, pool, schema, "neverprovisioned")); err != nil {
		t.Fatalf("write to unprovisioned scope must succeed: %v", err)
	}

	var n int
	if err := pool.QueryRow(ctx,
		schema.SQL(`SELECT count(*) FROM {schema}.chunk_unpartitioned`)).Scan(&n); err != nil {
		t.Fatalf("count default partition: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected the row in the default partition, found %d", n)
	}
}

// THE HARD BOUNDARY. Two tenants, same project name, same data subject. The point is not that a
// predicate filters one out — it is that the other tenant's rows are NOT ADDRESSABLE from a
// statement about this one, because the schema name is the only thing that reaches them.
//
// This is the test that would fail if tenancy were ever demoted to a column.
func TestOneTenantCannotAddressAnother(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)

	for _, tenant := range []string{"acme_hard", "globex_hard"} {
		if _, err := pool.Exec(ctx,
			`DROP SCHEMA IF EXISTS `+pgx.Identifier{tenant}.Sanitize()+` CASCADE`); err != nil {
			t.Fatalf("drop %s: %v", tenant, err)
		}
		if err := ProvisionMemorySchema(ctx, pool, tenant); err != nil {
			t.Fatalf("provision %s: %v", tenant, err)
		}
		// The SAME project name in both tenants, which is legal and must stay isolated.
		if err := ProvisionScope(ctx, pool, tenant, "shared"); err != nil {
			t.Fatalf("provision scope in %s: %v", tenant, err)
		}
		schema, _ := NewSchema(tenant)
		if _, err := pool.Exec(ctx, schema.SQL(`
			INSERT INTO {schema}.chunk (chunk_id, scope, source_observation_id, text, occurred_at, source_role, data_subject_id)
			VALUES (gen_random_uuid(), 'shared', $2::uuid, $1, now(), 'user', 'subject-1')`),
			tenant+" secret", seedObservation(t, pool, schema, "shared")); err != nil {
			t.Fatalf("seed %s: %v", tenant, err)
		}
	}

	// A query written for one tenant sees exactly its own row, with no tenant predicate anywhere in
	// it — there is nothing to get wrong, which is the property.
	acme, _ := NewSchema("acme_hard")
	rows, err := pool.Query(ctx, acme.SQL(`SELECT text FROM {schema}.chunk`))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	defer rows.Close()
	var seen []string
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			t.Fatalf("scan: %v", err)
		}
		seen = append(seen, text)
	}
	if len(seen) != 1 || seen[0] != "acme_hard secret" {
		t.Fatalf("a tenant-scoped read returned %v; it must return exactly its own row", seen)
	}
}

// The soft boundary. Projects live in ONE tenant and a principal may hold several, so a recall
// spans the authorised set in one query — which is why scope is a column and not a schema.
func TestProjectsAreASoftBoundaryWithinATenant(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	tenant := freshTenant(t, pool)
	schema, _ := NewSchema(tenant)

	for _, scope := range []string{"alpha", "beta", "gamma"} {
		if err := ProvisionScope(ctx, pool, tenant, scope); err != nil {
			t.Fatalf("provision %s: %v", scope, err)
		}
		if _, err := pool.Exec(ctx, schema.SQL(`
			INSERT INTO {schema}.chunk (chunk_id, scope, source_observation_id, text, occurred_at, source_role)
			VALUES (gen_random_uuid(), $1, $3::uuid, $2, now(), 'user')`),
			scope, scope+" content", seedObservation(t, pool, schema, scope)); err != nil {
			t.Fatalf("seed %s: %v", scope, err)
		}
	}

	// Authorised for two of the three: one query, one bundle, the third excluded by permission.
	rows, err := pool.Query(ctx,
		schema.SQL(`SELECT text FROM {schema}.chunk WHERE scope = ANY($1) ORDER BY text`),
		[]string{"alpha", "gamma"})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	defer rows.Close()
	var seen []string
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			t.Fatalf("scan: %v", err)
		}
		seen = append(seen, text)
	}
	if len(seen) != 2 || seen[0] != "alpha content" || seen[1] != "gamma content" {
		t.Fatalf("a multi-project recall returned %v; expected alpha and gamma only", seen)
	}
}

// A truncated schema name would put two tenants in one schema, which is the worst failure this type
// can prevent, so the length check is asserted rather than assumed.
func TestASchemaNameThatWouldCollideIsRefused(t *testing.T) {
	for _, bad := range []string{
		"",
		"9leading_digit",
		"Uppercase",
		"has-hyphen",
		"has space",
		`quote"injection`,
		strings.Repeat("a", maxIdentifierLength+1),
	} {
		if _, err := NewSchema(bad); err == nil {
			t.Fatalf("schema name %q was accepted and must not be", bad)
		}
	}
	if _, err := NewSchema(strings.Repeat("a", maxIdentifierLength)); err != nil {
		t.Fatalf("a name at the limit must be accepted: %v", err)
	}
}

// Provisioning a tenant twice is a no-op, and a schema name that cannot be an identifier is refused
// before it reaches a statement.
//
// The second is not tidiness: a schema name is interpolated because DDL cannot take a bind value, so
// validation is the thing standing between a name and a statement.
func TestProvisioningATenantIsRepeatableAndRefusesABadName(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	tenant := freshTenant(t, pool)

	// Again, which is what a chart does on every start.
	if err := ProvisionMemorySchema(ctx, pool, tenant); err != nil {
		t.Fatalf("provisioning twice failed: %v", err)
	}

	for _, bad := range []string{"has spaces", "Uppercase", "1leading", "drop; --", ""} {
		if err := ProvisionMemorySchema(ctx, pool, bad); err == nil {
			t.Fatalf("provisioned a tenant named %q", bad)
		}
	}

	// And public is still there, which is the assertion the loop above is for.
	var alive bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.schemata WHERE schema_name = 'public')`).
		Scan(&alive); err != nil {
		t.Fatalf("check: %v", err)
	}
	if !alive {
		t.Fatal("a tenant name reached a statement")
	}
}

// A column that existed because it was expected rather than because something read it is gone.
//
// Salience was zero for every fact ever written. The distinction it was created for stands —
// confidence is reported and salience would be computed — and a schema carrying one column that is a
// promise while every other is a fact teaches a reader that this schema contains things which are not
// true.
func TestSalienceIsGoneAndConfidenceIsNot(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	tenant := freshTenant(t, pool)

	var salience, confidence bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM information_schema.columns
		                WHERE table_schema = $1 AND table_name = 'fact' AND column_name = 'salience'),
		       EXISTS (SELECT 1 FROM information_schema.columns
		                WHERE table_schema = $1 AND table_name = 'fact' AND column_name = 'confidence')`,
		tenant).Scan(&salience, &confidence); err != nil {
		t.Fatalf("read columns: %v", err)
	}
	if salience {
		t.Fatal("salience is still on the fact, with nothing computing or reading it")
	}
	if !confidence {
		t.Fatal("confidence went with it; the distinction was the point, not the deletion")
	}
}

// A source root for synthetic index fixtures. It adds no extra chunks to the measured corpus.
func seedObservation(t *testing.T, pool *pgxpool.Pool, schema Schema, scope string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(), schema.SQL(`INSERT INTO {schema}.observation
        (observation_id,scope,log_offset,kind,occurred_at,source_role)
        VALUES(gen_random_uuid(),$1,0,'turn',now(),'user') RETURNING observation_id::text`), scope).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
