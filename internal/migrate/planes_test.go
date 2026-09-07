package migrate

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// asDataPlane connects with the memory service's own identity, which is the whole point: a boundary
// asserted through a superuser connection asserts nothing.
func asDataPlane(t *testing.T, password string) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TAISCE_TEST_DSN")
	if dsn == "" {
		t.Skip("TAISCE_TEST_DSN is not set")
	}
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	parsed.User = url.UserPassword(DataRole, password)
	pool, err := pgxpool.New(context.Background(), parsed.String())
	if err != nil {
		t.Fatalf("connect as %s: %v", DataRole, err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// establish creates the identities and returns a tenant the memory service is entitled to serve.
func establish(t *testing.T, pool *pgxpool.Pool, tenant string) string {
	t.Helper()
	ctx := context.Background()
	const password = "taisce-test-data"
	if err := EstablishPlanes(ctx, pool, "taisce-test-control", password); err != nil {
		t.Fatalf("establish planes: %v", err)
	}
	if _, err := pool.Exec(ctx, `DROP SCHEMA IF EXISTS `+tenant+` CASCADE`); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if err := ProvisionTenant(ctx, pool, tenant); err != nil {
		t.Fatalf("provision: %v", err)
	}
	return password
}

// ── #50's claim ───────────────────────────────────────────────────────────────────────────────
//
// The memory service cannot read the registry.
//
// Asserted through the memory service's own database login, because a rule expressed in Go is one
// every future query has to remember, and the query that forgets is the one written during an
// incident. Expressed as a grant it is a rule no statement can break.
func TestTheMemoryServiceCannotReadTheRegistry(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	password := establish(t, pool, "pl_registry")
	data := asDataPlane(t, password)

	var version int
	err := data.QueryRow(ctx, `SELECT max(version) FROM control.schema_migration`).Scan(&version)
	if err == nil {
		t.Fatal("the memory service read the registry; the boundary is a convention")
	}
	if !strings.Contains(err.Error(), "permission denied") {
		// A different error would mean the table is missing rather than forbidden, and a boundary
		// that holds because the thing behind it does not exist yet is not a boundary.
		t.Fatalf("refused for the wrong reason: %v", err)
	}

	// And it cannot reach the schema at all, not merely the tables in it — so a table added to the
	// registry tomorrow is behind the same boundary without anybody granting anything.
	if _, err := data.Exec(ctx, `CREATE TABLE control.smuggled (x int)`); err == nil {
		t.Fatal("the memory service created a table inside the registry")
	}
}

// The other half: it can serve the tenant it exists to serve. Without this the test above would pass
// on a login that cannot do anything at all.
func TestTheMemoryServiceCanServeTheTenantItIsGranted(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	password := establish(t, pool, "pl_serve")
	data := asDataPlane(t, password)

	var predicates int
	if err := data.QueryRow(ctx, `SELECT count(*) FROM pl_serve.predicate`).Scan(&predicates); err != nil {
		t.Fatalf("the memory service must be able to read its tenant: %v", err)
	}
	if predicates == 0 {
		t.Fatal("the tenant is empty, so this test asserts nothing")
	}
	if _, err := data.Exec(ctx, `
		INSERT INTO pl_serve.watermark (scope, log_offset, watermark_at)
		VALUES ('p9', 0, now())`); err != nil {
		t.Fatalf("the memory service must be able to write to its tenant: %v", err)
	}
}

// It cannot change the shape of what it serves, and it cannot make itself a new place to work in.
//
// A migration is the control plane's operation. An identity that can create a schema can create one
// it holds every right inside, which is a way around every grant below it.
func TestTheMemoryServiceCannotChangeTheSchemaOrMakeANewOne(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	password := establish(t, pool, "pl_ddl")
	data := asDataPlane(t, password)

	for _, tc := range []struct{ name, statement string }{
		{"create a schema", `CREATE SCHEMA pl_ddl_smuggled`},
		{"drop a table it serves", `DROP TABLE pl_ddl.fact`},
		{"add a column to a table it serves", `ALTER TABLE pl_ddl.fact ADD COLUMN smuggled int`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := data.Exec(ctx, tc.statement); err == nil {
				t.Fatalf("the memory service was able to: %s", tc.statement)
			}
		})
	}
}

// ── The limit of this boundary, asserted so nobody claims more than it gives ──────────────────
//
// The memory service CAN see that other tenants exist. Every role reads the system catalogue and
// that cannot be revoked without breaking the tools everybody uses.
//
// It is not a shortfall, and this test exists so the claim stays accurate. One process serves every
// tenant, so it necessarily holds privilege on every tenant schema — hiding the list of names from a
// component that already holds all of their memory protects nothing. What the grant protects is a
// different category: credentials, subscriptions, the people behind the accounts. Separation BETWEEN
// tenants is, and remains, the schema name in the statement.
func TestTheMemoryServiceCanStillSeeThatOtherTenantsExist(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	password := establish(t, pool, "pl_limit")
	data := asDataPlane(t, password)

	var schemas int
	if err := data.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.schemata WHERE schema_name LIKE 'pl\_%'`).Scan(&schemas); err != nil {
		t.Fatalf("read catalogue: %v", err)
	}
	if schemas == 0 {
		t.Fatal("no tenant schemas were visible, so this test asserts nothing")
	}
}

// Establishing the planes twice changes nothing, because it is the kind of step an operator repeats
// when unsure whether it ran.
func TestEstablishingThePlanesTwiceIsANoOp(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	establish(t, pool, "pl_twice")
	if err := EstablishPlanes(ctx, pool, "taisce-test-control", "taisce-test-data"); err != nil {
		t.Fatalf("second establish: %v", err)
	}
}

// A login role with no password is one anything on the network can assume, which would make every
// grant above decorative.
func TestAPlaneIdentityWithoutAPasswordIsRefused(t *testing.T) {
	pool := testPool(t)
	if err := EstablishPlanes(context.Background(), pool, "taisce-test-control", ""); err == nil {
		t.Fatal("an identity with no password was established")
	}
}
