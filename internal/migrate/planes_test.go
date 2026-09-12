package migrate

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
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
	if err := ProvisionMemorySchema(ctx, pool, tenant); err != nil {
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
// System catalogs expose namespace names. These fixtures create several instance namespaces in
// one test database; that is test isolation, not a multi-tenant service contract. Runtime privileges
// protect control credentials, and application project grants protect memory reads. A schema name
// itself does not stop a role granted access to it from issuing a query against it.
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

// Several processes establishing the planes at once all succeed.
//
// Bootstrap is documented as safe to run on every start, and a chart running several replicas starts
// them together. Two altering the same role concurrently is not a race PostgreSQL resolves quietly —
// it is `tuple concurrently updated`, and the loser exits non-zero, which for a bootstrap step means
// a deployment that fails to come up for a reason that is not wrong with it.
//
// Found by the test suite failing intermittently once three packages called this, which is the
// cheapest possible place to find it.
//
// The pool is smaller than the number of starters on purpose. Each starter holds one connection
// for the session lock; if the holder borrowed a second one for its work, every connection would
// be held by a waiter and the holder would wait forever. A pool sized by the machine's cores hid
// that on a laptop and showed it on a four-core runner; a fixed size shows it everywhere.
func TestEstablishingThePlanesConcurrentlyIsSafe(t *testing.T) {
	ctx := context.Background()
	const starters = 6
	config, err := pgxpool.ParseConfig(os.Getenv("TAISCE_TEST_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	config.MaxConns = starters - 2
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, starters)
	for i := 0; i < starters; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = EstablishPlanes(ctx, pool, "taisce-test-control", "taisce-test-data")
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("starter %d failed while another was establishing the planes: %v", i, err)
		}
	}
}

// ── #87: a project is a row, and its surfaces only ever grow ──────────────────────────────────

// Provisioning records the project, so a project that exists but has never been written to is still
// a project.
//
// It used to be inferred from the watermark table, which meant existence depended on somebody having
// used it — an operator who created a project and checked their work saw nothing.
func TestAProvisionedProjectExistsBeforeAnythingIsWrittenToIt(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	tenant := freshTenant(t, pool)
	if err := ProvisionScope(ctx, pool, tenant, "unused"); err != nil {
		t.Fatalf("provision: %v", err)
	}

	var label string
	var surfaces []string
	if err := pool.QueryRow(ctx,
		`SELECT label, surfaces FROM `+tenant+`.project WHERE scope = 'unused'`).
		Scan(&label, &surfaces); err != nil {
		t.Fatalf("a provisioned project has no row: %v", err)
	}
	if len(surfaces) != 0 {
		t.Fatalf("provisioning chose surfaces (%v); which kinds of memory a project keeps is a "+
			"separate decision with its own arguments", surfaces)
	}
}

// A memory surface can be turned on and never off.
//
// Enforced in the substrate rather than the service because it is the rule a write path forgets: an
// administrative endpoint that sets the whole array from a form would silently drop whatever the form
// did not send, and nothing would notice until a caller asked for memory that had stopped being
// served.
func TestAMemorySurfaceCanBeAddedAndNeverRemoved(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	tenant := freshTenant(t, pool)
	if err := ProvisionScope(ctx, pool, tenant, "growing"); err != nil {
		t.Fatalf("provision: %v", err)
	}

	// On.
	if _, err := pool.Exec(ctx,
		`UPDATE `+tenant+`.project SET surfaces = ARRAY['history','context'] WHERE scope = 'growing'`); err != nil {
		t.Fatalf("turning surfaces on was refused: %v", err)
	}
	// More on.
	if _, err := pool.Exec(ctx,
		`UPDATE `+tenant+`.project SET surfaces = ARRAY['history','context','search'] WHERE scope = 'growing'`); err != nil {
		t.Fatalf("adding a surface was refused: %v", err)
	}

	// Off — refused, and it says which one would have gone.
	_, err := pool.Exec(ctx,
		`UPDATE `+tenant+`.project SET surfaces = ARRAY['history'] WHERE scope = 'growing'`)
	if err == nil {
		t.Fatal("a memory surface was turned off, which either strands or destroys what it stored")
	}
	if !strings.Contains(err.Error(), "cannot be turned off") {
		t.Fatalf("refused for the wrong reason: %v", err)
	}
	if !strings.Contains(err.Error(), "context") && !strings.Contains(err.Error(), "search") {
		t.Fatalf("the refusal does not name what would have been removed: %v", err)
	}

	// And the whole array being replaced with an empty one — the form-submission failure — is the
	// same refusal.
	if _, err := pool.Exec(ctx,
		`UPDATE `+tenant+`.project SET surfaces = '{}' WHERE scope = 'growing'`); err == nil {
		t.Fatal("every surface was turned off at once")
	}
}

// A retention that would delete on write is refused. Whatever an operator meant by zero, it was not
// that.
func TestARetentionThatWouldDeleteOnWriteIsRefused(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	tenant := freshTenant(t, pool)
	if err := ProvisionScope(ctx, pool, tenant, "retained"); err != nil {
		t.Fatalf("provision: %v", err)
	}
	for _, bad := range []string{"0", "-1 day"} {
		if _, err := pool.Exec(ctx,
			`UPDATE `+tenant+`.project SET retention = $1::interval WHERE scope = 'retained'`, bad); err == nil {
			t.Fatalf("a retention of %q was accepted", bad)
		}
	}
	// NULL is a policy — keep indefinitely — rather than an absence of one.
	if _, err := pool.Exec(ctx,
		`UPDATE `+tenant+`.project SET retention = NULL WHERE scope = 'retained'`); err != nil {
		t.Fatalf("indefinite retention was refused: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE `+tenant+`.project SET retention = interval '90 days' WHERE scope = 'retained'`); err != nil {
		t.Fatalf("a real retention policy was refused: %v", err)
	}
}

// ── #86: the ledger cannot be rewritten ───────────────────────────────────────────────────────

// Append-only, enforced by the database rather than intended by the application.
//
// A ledger the application is trusted not to rewrite is a ledger whose integrity is a code review.
// This asserts it against a superuser connection: if the strongest identity available cannot change a
// row, nothing weaker can either — including a migration written in a hurry.
func TestTheAuditLedgerCannotBeRewrittenOrDeleted(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	tenant := freshTenant(t, pool)

	if _, err := pool.Exec(ctx, `INSERT INTO `+tenant+`.audit_entry
	    (operation, principal, principal_kind, outcome) VALUES ('recall','cred-1','credential','allowed')`); err != nil {
		t.Fatalf("append: %v", err)
	}

	_, err := pool.Exec(ctx, `UPDATE `+tenant+`.audit_entry SET outcome = 'refused'`)
	if err == nil {
		t.Fatal("an audit row was rewritten; a record that can be changed is not a record")
	}
	if !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("refused for the wrong reason: %v", err)
	}

	_, err = pool.Exec(ctx, `DELETE FROM `+tenant+`.audit_entry`)
	if err == nil {
		t.Fatal("an audit row was deleted")
	}
	if !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("refused for the wrong reason: %v", err)
	}

	// And the row is still there, which is the assertion the two above are for.
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM `+tenant+`.audit_entry`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("%d rows survived", n)
	}
}

// The ledger records no operation it does not know about, so a count of what happened is a count of
// everything that happened.
func TestTheLedgerRefusesAnOperationItDoesNotKnow(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	tenant := freshTenant(t, pool)

	for _, bad := range []struct{ column, value string }{
		{"operation", "exfiltrate"},
		{"principal_kind", "ghost"},
		{"outcome", "maybe"},
	} {
		sql := `INSERT INTO ` + tenant + `.audit_entry (operation, principal, principal_kind, outcome)
		        VALUES ('recall','cred-1','credential','allowed')`
		switch bad.column {
		case "operation":
			sql = `INSERT INTO ` + tenant + `.audit_entry (operation, principal, principal_kind, outcome)
			       VALUES ('` + bad.value + `','cred-1','credential','allowed')`
		case "principal_kind":
			sql = `INSERT INTO ` + tenant + `.audit_entry (operation, principal, principal_kind, outcome)
			       VALUES ('recall','cred-1','` + bad.value + `','allowed')`
		case "outcome":
			sql = `INSERT INTO ` + tenant + `.audit_entry (operation, principal, principal_kind, outcome)
			       VALUES ('recall','cred-1','credential','` + bad.value + `')`
		}
		if _, err := pool.Exec(ctx, sql); err == nil {
			t.Fatalf("%s=%q was accepted", bad.column, bad.value)
		}
	}

	// And an unattributed row, which is the one thing this table exists to make impossible.
	if _, err := pool.Exec(ctx, `INSERT INTO `+tenant+`.audit_entry
	    (operation, principal, principal_kind, outcome) VALUES ('recall','','credential','allowed')`); err == nil {
		t.Fatal("an operation was recorded with no principal behind it")
	}
}

// ── #53/#82: a rewritten ledger is detectable ─────────────────────────────────────────────────

// Altering a sealed entry breaks its seal, and the verifier says which one and how.
//
// This is what the append-only triggers cannot cover: somebody who can drop them. Rule 12 asks
// directly whether an operator with database access can do something invisible, and self-hosting
// makes that the realistic case rather than the exotic one.
func TestAlteringASealedEntryIsDetected(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	tenant := freshTenant(t, pool)
	schema, _ := NewSchema(tenant)
	audit := pg.NewAuditStore(pool, schema)

	for i := 0; i < 3; i++ {
		if err := audit.Append(ctx, domain.AuditEntry{
			Operation: domain.AuditRecall, Principal: "cred-1", Project: "p1",
			PrincipalKind: domain.PrincipalCredential, Outcome: domain.OutcomeAllowed, Magnitude: i,
		}); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	seal, err := audit.Seal(ctx)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if seal.Entries != 3 {
		t.Fatalf("sealed %d entries", seal.Entries)
	}

	v, err := audit.Verify(ctx)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !v.Valid {
		t.Fatalf("a freshly sealed ledger does not verify: %s", v.Failure)
	}

	// Now tamper, the way somebody with full access would: drop the trigger, change a row.
	if _, err := pool.Exec(ctx, `DROP TRIGGER audit_no_update ON `+tenant+`.audit_entry`); err != nil {
		t.Fatalf("drop trigger: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE `+tenant+`.audit_entry SET outcome = 'refused' WHERE entry_id = (
		     SELECT min(entry_id) FROM `+tenant+`.audit_entry)`); err != nil {
		t.Fatalf("tamper: %v", err)
	}

	v, err = audit.Verify(ctx)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if v.Valid {
		t.Fatal("an altered entry verified; the seal proves nothing")
	}
	if !strings.Contains(v.Failure, "altered or removed") {
		t.Fatalf("the failure does not say what happened: %s", v.Failure)
	}
}

// Removing a seal to hide a range breaks the chain, and the verifier distinguishes that from an
// altered entry — they are different accusations.
func TestRemovingASealBreaksTheChain(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	tenant := freshTenant(t, pool)
	schema, _ := NewSchema(tenant)
	audit := pg.NewAuditStore(pool, schema)

	for round := 0; round < 3; round++ {
		if err := audit.Append(ctx, domain.AuditEntry{
			Operation: domain.AuditObserve, Principal: "cred-1", Project: "p1",
			PrincipalKind: domain.PrincipalCredential, Outcome: domain.OutcomeAllowed,
		}); err != nil {
			t.Fatalf("append: %v", err)
		}
		if _, err := audit.Seal(ctx); err != nil {
			t.Fatalf("seal: %v", err)
		}
	}
	if v, err := audit.Verify(ctx); err != nil || !v.Valid {
		t.Fatalf("three seals do not verify: %v %+v", err, v)
	}

	if _, err := pool.Exec(ctx, `DROP TRIGGER audit_seal_no_delete ON `+tenant+`.audit_seal`); err != nil {
		t.Fatalf("drop trigger: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`DELETE FROM `+tenant+`.audit_seal WHERE seal_id = (
		     SELECT min(seal_id) FROM `+tenant+`.audit_seal)`); err != nil {
		t.Fatalf("remove seal: %v", err)
	}

	v, err := audit.Verify(ctx)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if v.Valid {
		t.Fatal("removing a seal left the chain verifying")
	}
	if !strings.Contains(v.Failure, "re-linked") {
		t.Fatalf("the failure does not distinguish a re-linked chain: %s", v.Failure)
	}
}

// A verification says what it does NOT cover. Entries written since the last seal are covered by
// nothing, and reporting only the covered ones would be a reassurance rather than a check.
func TestAVerificationReportsWhatItDoesNotCover(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	tenant := freshTenant(t, pool)
	schema, _ := NewSchema(tenant)
	audit := pg.NewAuditStore(pool, schema)

	if err := audit.Append(ctx, domain.AuditEntry{
		Operation: domain.AuditRecall, Principal: "c", PrincipalKind: domain.PrincipalCredential,
		Outcome: domain.OutcomeAllowed,
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if _, err := audit.Seal(ctx); err != nil {
		t.Fatalf("seal: %v", err)
	}
	// Two more, unsealed.
	for i := 0; i < 2; i++ {
		if err := audit.Append(ctx, domain.AuditEntry{
			Operation: domain.AuditRecall, Principal: "c", PrincipalKind: domain.PrincipalCredential,
			Outcome: domain.OutcomeAllowed,
		}); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	v, err := audit.Verify(ctx)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !v.Valid {
		t.Fatalf("valid ledger reported invalid: %s", v.Failure)
	}
	if v.Unsealed != 2 {
		t.Fatalf("reported %d unsealed entries, wanted 2 — the window is the thing to be honest about",
			v.Unsealed)
	}
	if len(v.Head) == 0 {
		t.Fatal("no head digest, so there is nothing to record outside the database")
	}
}

// Sealing twice with nothing in between does nothing rather than writing an empty seal.
func TestSealingWithNothingToSealDoesNothing(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	tenant := freshTenant(t, pool)
	schema, _ := NewSchema(tenant)
	audit := pg.NewAuditStore(pool, schema)

	if seal, err := audit.Seal(ctx); err != nil || seal.Entries != 0 {
		t.Fatalf("sealing an empty ledger produced %+v (%v)", seal, err)
	}
	if err := audit.Append(ctx, domain.AuditEntry{
		Operation: domain.AuditRecall, Principal: "c", PrincipalKind: domain.PrincipalCredential,
		Outcome: domain.OutcomeAllowed,
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if seal, err := audit.Seal(ctx); err != nil || seal.Entries != 1 {
		t.Fatalf("first seal: %+v (%v)", seal, err)
	}
	if seal, err := audit.Seal(ctx); err != nil || seal.Entries != 0 {
		t.Fatalf("sealing again wrote %+v", seal)
	}
}

// Sealing is serialised, so two sealers cannot both claim the same entries.
//
// A seal always starts where the last one ended, so two running concurrently could otherwise write
// overlapping ranges — and a chain with overlapping links is one that verifies against nothing.
func TestTwoSealersCannotOverlap(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	tenant := freshTenant(t, pool)
	schema, _ := NewSchema(tenant)
	audit := pg.NewAuditStore(pool, schema)

	for i := 0; i < 20; i++ {
		if err := audit.Append(ctx, domain.AuditEntry{
			Operation: domain.AuditRecall, Principal: "c", Project: "p1",
			PrincipalKind: domain.PrincipalCredential, Outcome: domain.OutcomeAllowed,
		}); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	const sealers = 4
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < sealers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _ = audit.Seal(ctx)
		}()
	}
	close(start)
	wg.Wait()

	// However the four raced, the chain must verify and cover every entry exactly once.
	v, err := audit.Verify(ctx)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !v.Valid {
		t.Fatalf("concurrent sealing produced a chain that does not verify: %s", v.Failure)
	}
	if v.Entries != 20 {
		t.Fatalf("the seals cover %d entries, wanted the 20 that exist", v.Entries)
	}
	if v.Unsealed != 0 {
		t.Fatalf("%d entries were left unsealed", v.Unsealed)
	}
}

// ── #192's claim ──────────────────────────────────────────────────────────────────────────────
//
// Bootstrap repeats under an administrative identity that is not a superuser.
//
// A cluster deployment hands bootstrap a CREATEROLE login and keeps the superuser closed, and it
// runs bootstrap again on every upgrade. PostgreSQL lets such a login create a role while naming
// every privilege it lacks, and refuses it an ALTER that names any of them — so the first run
// passing says nothing about the second, and the second is the one an operator meets on the day
// they upgrade. Asserted as that identity, because asserted as the superuser it asserts nothing.

// asCreateRoleAdmin makes a login with CREATEROLE and nothing more, and connects as it.
func asCreateRoleAdmin(t *testing.T, owner *pgxpool.Pool) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	name := "pl_admin_" + uniqueSuffix()
	if _, err := owner.Exec(ctx, "CREATE ROLE "+pgx.Identifier{name}.Sanitize()+" LOGIN CREATEROLE PASSWORD 'pl-admin'"); err != nil {
		t.Fatalf("create the admin: %v", err)
	}
	t.Cleanup(func() {
		_, _ = owner.Exec(context.Background(), "DROP ROLE IF EXISTS "+pgx.Identifier{name}.Sanitize())
	})
	parsed, err := url.Parse(os.Getenv("TAISCE_TEST_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	parsed.User = url.UserPassword(name, "pl-admin")
	pool, err := pgxpool.New(ctx, parsed.String())
	if err != nil {
		t.Fatalf("connect as the admin: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func uniqueSuffix() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func TestBootstrapRepeatsUnderAnAdministratorWhoIsNotASuperuser(t *testing.T) {
	ctx := context.Background()
	owner := testPool(t)
	admin := asCreateRoleAdmin(t, owner)
	role := "pl_ctl_" + uniqueSuffix()
	t.Cleanup(func() {
		_, _ = owner.Exec(context.Background(), "DROP ROLE IF EXISTS "+pgx.Identifier{role}.Sanitize())
	})

	if err := ensureRole(ctx, admin, role, "first"); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if err := ensureRole(ctx, admin, role, "second"); err != nil {
		t.Fatalf("second run, the one every upgrade performs: %v", err)
	}

	var login, super, createdb, createrole, replication, bypassrls bool
	if err := owner.QueryRow(ctx,
		`SELECT rolcanlogin, rolsuper, rolcreatedb, rolcreaterole, rolreplication, rolbypassrls FROM pg_roles WHERE rolname = $1`,
		role).Scan(&login, &super, &createdb, &createrole, &replication, &bypassrls); err != nil {
		t.Fatal(err)
	}
	if !login || super || createdb || createrole || replication || bypassrls {
		t.Fatalf("the role must be a plain login and nothing more: login=%t super=%t createdb=%t createrole=%t replication=%t bypassrls=%t",
			login, super, createdb, createrole, replication, bypassrls)
	}
	// The second password is the one that opens the door: the ALTER did run, rather than being
	// skipped as the way to avoid the refusal.
	parsed, err := url.Parse(os.Getenv("TAISCE_TEST_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	parsed.User = url.UserPassword(role, "second")
	as, err := pgxpool.New(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	defer as.Close()
	if err := as.Ping(ctx); err != nil {
		t.Fatalf("the second password must open the role: %v", err)
	}
}

// A role that gained a privilege this system never grants was not made here, and bootstrap must
// not hand it the registry password — under any identity, since the superuser could.
func TestBootstrapDoesNotAdoptARoleThatCarriesAPrivilegeItNeverGrants(t *testing.T) {
	ctx := context.Background()
	owner := testPool(t)
	role := "pl_priv_" + uniqueSuffix()
	if _, err := owner.Exec(ctx, "CREATE ROLE "+pgx.Identifier{role}.Sanitize()+" LOGIN CREATEDB PASSWORD 'before'"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = owner.Exec(context.Background(), "DROP ROLE IF EXISTS "+pgx.Identifier{role}.Sanitize())
	})
	err := ensureRole(ctx, owner, role, "after")
	if err == nil || !strings.Contains(err.Error(), "createdb=true") {
		t.Fatalf("a role with CREATEDB must be refused, naming the privilege: %v", err)
	}
	parsed, perr := url.Parse(os.Getenv("TAISCE_TEST_DSN"))
	if perr != nil {
		t.Fatal(perr)
	}
	parsed.User = url.UserPassword(role, "after")
	as, perr := pgxpool.New(ctx, parsed.String())
	if perr != nil {
		t.Fatal(perr)
	}
	defer as.Close()
	if err := as.Ping(ctx); err == nil {
		t.Fatal("the refused role must not have received the new password")
	}
}

// ── #242: the memory role writes only what it is listed for ─────────────────────────────────────
//
// A table a migration adds is readable by the memory service and not writable until somebody lists
// it. The project's suspension and retention are not the memory service's to change, and the ledger
// is append-only in the grant itself — so a rewrite is refused as insufficient privilege before any
// trigger runs. And the list is the whole truth: a privilege granted by hand is gone after the next
// migration run.
func TestTheMemoryServiceWritesOnlyTheTablesItIsListedFor(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	const tenant = "pl_writes"
	password := establish(t, pool, tenant)
	t.Cleanup(func() { pool.Exec(ctx, `DROP SCHEMA IF EXISTS `+tenant+` CASCADE`) })
	schema, err := NewSchema(tenant)
	if err != nil {
		t.Fatal(err)
	}
	data := asDataPlane(t, password)
	refused := func(statement string) {
		t.Helper()
		_, err := data.Exec(ctx, statement)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
			t.Fatalf("%s: want insufficient privilege, got %v", statement, err)
		}
	}

	if _, err := pool.Exec(ctx, `CREATE TABLE `+tenant+`.brand_new (x int)`); err != nil {
		t.Fatal(err)
	}
	if err := grantMemoryToDataPlane(ctx, pool, schema); err != nil {
		t.Fatal(err)
	}
	if _, err := data.Exec(ctx, `SELECT count(*) FROM `+tenant+`.brand_new`); err != nil {
		t.Fatalf("a new table is not readable by the memory service: %v", err)
	}
	refused(`INSERT INTO ` + tenant + `.brand_new VALUES (1)`)
	refused(`UPDATE ` + tenant + `.project SET suspended_at = now()`)
	refused(`UPDATE ` + tenant + `.project SET retention = NULL`)
	// The one column it holds is the one that lets it lock the row, which the entity-embedding
	// triggers do to serialise against a suspension.
	if _, err := data.Exec(ctx, `SELECT 1 FROM `+tenant+`.project FOR UPDATE`); err != nil {
		t.Fatalf("the memory service cannot lock a project row: %v", err)
	}

	if _, err := data.Exec(ctx, `INSERT INTO `+tenant+`.audit_entry (operation, principal, principal_kind, outcome)
		VALUES ('recall', 'c1', 'credential', 'allowed')`); err != nil {
		t.Fatalf("the memory service cannot append to the ledger: %v", err)
	}
	refused(`UPDATE ` + tenant + `.audit_entry SET magnitude = 0`)
	refused(`DELETE FROM ` + tenant + `.audit_entry`)
	refused(`UPDATE ` + tenant + `.audit_seal SET entry_count = entry_count`)
	// It seals through the substrate and cannot write a seal of its own.
	refused(`INSERT INTO ` + tenant + `.audit_seal (from_entry, to_entry, entry_count, entries_digest, previous_digest, digest)
		VALUES (0, 0, 1, '\x00', '\x00', '\x00')`)
	var sealed int
	if err := data.QueryRow(ctx, `SELECT count(*) FROM `+tenant+`.audit_seal_now()`).Scan(&sealed); err != nil || sealed != 1 {
		t.Fatalf("the memory service cannot seal through the substrate: %d, %v", sealed, err)
	}

	if _, err := pool.Exec(ctx, `GRANT UPDATE ON `+tenant+`.project TO `+pgx.Identifier{DataRole}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	if _, err := data.Exec(ctx, `UPDATE `+tenant+`.project SET suspended_at = NULL`); err != nil {
		t.Fatalf("the hand-granted privilege did not take, so the test proves nothing: %v", err)
	}
	if err := grantMemoryToDataPlane(ctx, pool, schema); err != nil {
		t.Fatal(err)
	}
	refused(`UPDATE ` + tenant + `.project SET suspended_at = NULL`)

	for _, table := range memoryWriteTables() {
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, tenant+"."+table).Scan(&exists); err != nil || !exists {
			t.Fatalf("the list names %q, which the namespace does not hold", table)
		}
	}
}
