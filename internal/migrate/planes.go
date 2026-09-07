// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package migrate

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The two database identities.
//
// One deployment, one database, two logins. The memory service connects as one of these and the
// registry as the other, and what separates them is a grant rather than a promise.
const (
	// ControlRole owns the registry and creates tenant schemas. It is the only identity that can
	// answer a question about more than one customer.
	ControlRole = "taisce_control"
	// DataRole serves memory. It reads and writes inside tenant schemas and has no privilege at all
	// in the registry, so a component that serves one tenant cannot enumerate the rest.
	DataRole = "taisce_data"
)

// EstablishPlanes creates the two identities, the registry's schema, and the grants between them.
//
// Run once per deployment, by whoever has the rights to create a role. It is idempotent, because
// this is the kind of step an operator repeats when unsure whether it ran.
//
// # Why a grant and not a convention
//
// The memory service must never be able to enumerate customers: it serves one tenant per statement
// and its entire boundary is that another tenant's name is not in the statement it is running. A
// component that can list every customer, read every credential and read every subscription is one
// bad predicate away from crossing that. Expressed in code, "the memory service does not read the
// registry" is a rule every future query has to remember. Expressed as a grant it is a rule no query
// can break, including one written by hand during an incident.
//
// # What this boundary does and does not buy
//
// It stops the memory service reading the registry. It does NOT stop it reading another tenant's
// memory: one process serves every tenant, so it necessarily holds privilege on every tenant schema,
// and the separation between them remains the schema name in the statement.
//
// That is the right division rather than a shortfall. The memory service already holds every
// customer's memory, so hiding the list of tenant names from it protects nothing worth protecting.
// What is worth protecting is a different category — credentials, billing, the people behind the
// accounts — and that is what is on the other side of this grant.
func EstablishPlanes(ctx context.Context, pool *pgxpool.Pool, controlPassword, dataPassword string) error {
	if controlPassword == "" || dataPassword == "" {
		// A login role with no password is one anything on the network can assume, which would make
		// the grant below decorative.
		return fmt.Errorf("both plane identities need a password")
	}
	// Serialised across every process that might run this at once.
	//
	// Bootstrap is documented as safe to run on every start, and the chart starts several replicas
	// together. Two of them altering the same role concurrently is not a race the database resolves
	// quietly — it is `tuple concurrently updated`, and the loser exits non-zero, which for a
	// bootstrap step means a deployment that fails to come up for a reason that is not wrong with it.
	//
	// A session-level advisory lock over the whole of this, rather than a transaction: role and
	// database DDL below cannot all run inside one transaction, so the lock has to outlive the
	// statements it protects. Taken on its own connection so returning it to the pool cannot leave
	// the lock held by whoever borrows that connection next.
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire a connection to establish the planes: %w", err)
	}
	defer conn.Release()

	const lockKey = "taisce:establish-planes"
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(hashtext($1)::bigint)`, lockKey); err != nil {
		return fmt.Errorf("take the bootstrap lock: %w", err)
	}
	defer func() {
		// WithoutCancel so a cancelled context still releases it. A held session lock outlives the
		// caller and would block the next start rather than the current one.
		_, _ = conn.Exec(context.WithoutCancel(ctx),
			`SELECT pg_advisory_unlock(hashtext($1)::bigint)`, lockKey)
	}()

	for _, role := range []struct{ name, password string }{
		{ControlRole, controlPassword},
		{DataRole, dataPassword},
	} {
		if err := ensureRole(ctx, pool, role.name, role.password); err != nil {
			return err
		}
	}

	if _, err := ApplyControl(ctx, pool); err != nil {
		return fmt.Errorf("migrate the registry: %w", err)
	}

	// The database name is needed as an IDENTIFIER in the REVOKE below, and an identifier can be
	// neither a bind parameter nor a function call — so it is read from the connection and
	// sanitised, rather than being configured somewhere it could disagree with where we are
	// actually connected.
	var database string
	if err := pool.QueryRow(ctx, `SELECT current_database()`).Scan(&database); err != nil {
		return fmt.Errorf("read the database name: %w", err)
	}
	database = pgx.Identifier{database}.Sanitize()

	for _, statement := range []string{
		// The registry belongs to the control identity and to nothing else. Revoked from PUBLIC
		// explicitly rather than relying on the default, because a default is a version-dependent
		// fact about PostgreSQL and this is a security boundary.
		fmt.Sprintf(`REVOKE ALL ON SCHEMA %s FROM PUBLIC`, ControlSchema),
		fmt.Sprintf(`REVOKE ALL ON ALL TABLES IN SCHEMA %s FROM PUBLIC`, ControlSchema),
		fmt.Sprintf(`GRANT ALL ON SCHEMA %s TO %s`, ControlSchema, ControlRole),
		fmt.Sprintf(`GRANT ALL ON ALL TABLES IN SCHEMA %s TO %s`, ControlSchema, ControlRole),
		// Nothing is granted to the data identity here. Its absence from this list IS the boundary,
		// so there is deliberately no statement to read.
		//
		// And it cannot create a schema of its own: provisioning a tenant is the control plane's
		// operation, and an identity that can create schemas can create one it then has full rights
		// inside.
		fmt.Sprintf(`REVOKE CREATE ON DATABASE %s FROM PUBLIC`, database),
		fmt.Sprintf(`REVOKE CREATE ON DATABASE %s FROM %s`, database, DataRole),
	} {
		if _, err := pool.Exec(ctx, statement); err != nil {
			return fmt.Errorf("establish planes: %w", err)
		}
	}
	return nil
}

func ensureRole(ctx context.Context, pool *pgxpool.Pool, name, password string) error {
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, name).
		Scan(&exists); err != nil {
		return fmt.Errorf("look for role %q: %w", name, err)
	}
	verb := "CREATE"
	if exists {
		verb = "ALTER"
	}
	// The password is interpolated as a quoted literal rather than bound, because role statements do
	// not take parameters. It comes from a deployment's configuration and never from a request.
	statement := fmt.Sprintf(`%s ROLE %s LOGIN PASSWORD %s`,
		verb, pgx.Identifier{name}.Sanitize(), quoteLiteral(password))
	if _, err := pool.Exec(ctx, statement); err != nil {
		return fmt.Errorf("%s role %q: %w", verb, name, err)
	}
	return nil
}

// quoteLiteral renders a string as a SQL literal, doubling any quote it contains.
func quoteLiteral(s string) string {
	out := make([]byte, 0, len(s)+2)
	out = append(out, '\'')
	for i := 0; i < len(s); i++ {
		if s[i] == '\'' {
			out = append(out, '\'')
		}
		out = append(out, s[i])
	}
	return string(append(out, '\''))
}

// grantTenantToDataPlane gives the memory service what it needs inside one tenant, and nothing more.
//
// Run after a tenant's migrations, so that tables created by a migration added later are covered by
// the grant rather than being unreachable until somebody notices.
//
// # Why this is skipped when the role is absent
//
// The identities are a deployment step and creating one needs the right to create a role, which a
// developer running against a local database may not have. Refusing to provision a tenant without
// them would make the substrate unusable for exactly the people who most need to run it.
//
// A deployment that has not established the planes runs everything as one identity — which is the
// state this decision replaces, and it is honest to say so rather than to fail in a way that reads
// as a bug.
func grantTenantToDataPlane(ctx context.Context, pool *pgxpool.Pool, schema Schema) error {
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, DataRole).
		Scan(&exists); err != nil {
		return fmt.Errorf("look for the data role: %w", err)
	}
	if !exists {
		return nil
	}
	// The database name is needed as an IDENTIFIER in the REVOKE below, and an identifier can be
	// neither a bind parameter nor a function call — so it is read from the connection and
	// sanitised, rather than being configured somewhere it could disagree with where we are
	// actually connected.
	var database string
	if err := pool.QueryRow(ctx, `SELECT current_database()`).Scan(&database); err != nil {
		return fmt.Errorf("read the database name: %w", err)
	}
	database = pgx.Identifier{database}.Sanitize()

	for _, statement := range []string{
		// USAGE lets it name the schema. Without it every statement fails, with it none of them
		// reaches another schema it was not also granted.
		fmt.Sprintf(`GRANT USAGE ON SCHEMA %s TO %s`, schema, DataRole),
		// Rows, not structure. No CREATE, no ALTER, no DROP: a migration is the control plane's
		// operation and the memory service has no reason to change a table's shape.
		fmt.Sprintf(`GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA %s TO %s`, schema, DataRole),
		fmt.Sprintf(`GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA %s TO %s`, schema, DataRole),
	} {
		if _, err := pool.Exec(ctx, statement); err != nil {
			return fmt.Errorf("grant tenant %q to the data plane: %w", schema, err)
		}
	}
	return nil
}
