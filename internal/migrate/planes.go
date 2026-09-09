// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package migrate

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// One instance has separate control and memory database identities.
const (
	// ControlRole accesses the credential registry. Operator DDL uses the admin connection.
	ControlRole = "taisce_control"
	// DataRole serves project memory and cannot read credential secrets in control.
	DataRole = "taisce_data"
)

// EstablishPlanes creates or reconciles the instance logins, control namespace and grants.
// It is an idempotent deployment operation requiring an administrative connection.
// DataRole serves all projects in the instance: authenticated application scope filters control
// reads, while foreign keys enforce project-consistent writes. Schema naming alone grants no
// confidentiality against a role with access to that schema. An owner/admin can change these rules.
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
		fmt.Sprintf(`REVOKE CREATE ON SCHEMA %s FROM %s`, ControlSchema, ControlRole),
		fmt.Sprintf(`GRANT USAGE ON SCHEMA %s TO %s`, ControlSchema, ControlRole),
		fmt.Sprintf(`REVOKE INSERT, UPDATE, DELETE, TRUNCATE, REFERENCES, TRIGGER ON ALL TABLES IN SCHEMA %s FROM %s`, ControlSchema, ControlRole),
		fmt.Sprintf(`GRANT SELECT ON ALL TABLES IN SCHEMA %s TO %s`, ControlSchema, ControlRole),
		// Nothing is granted to the data identity here. Its absence from this list IS the boundary,
		// so there is deliberately no statement to read.
		//
		// And it cannot create a schema of its own: provisioning instance storage is the operator's
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
	statement := fmt.Sprintf(`%s ROLE %s LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD %s`,
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

// grantMemoryToDataPlane applies runtime privileges after memory migrations. It grants one
// instance namespace. An absent role is
// allowed for isolated migration fixtures without deployment-role provisioning privileges.
func grantMemoryToDataPlane(ctx context.Context, pool *pgxpool.Pool, schema Schema) error {
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
		// Runtime DML must not redefine erasure targets, admitted relations, speaker terms or
		// migration state. These catalogs are changed by reviewed migrations through the owner.
		fmt.Sprintf(`REVOKE INSERT, UPDATE, DELETE ON %s.projection_kind, %s.predicate,
            %s.unresolvable_term, %s.speaker_term, %s.schema_migration FROM %s`,
			schema, schema, schema, schema, schema, DataRole),
		fmt.Sprintf(`REVOKE INSERT, UPDATE, DELETE, TRUNCATE, REFERENCES, TRIGGER ON
            %s.embedding_generation, %s.embedding_active FROM %s`, schema, schema, DataRole),
		fmt.Sprintf(`REVOKE INSERT, UPDATE, DELETE, TRUNCATE, REFERENCES, TRIGGER ON
            %s.ingestion_budget, %s.ingestion_project_usage, %s.ingestion_reservation, %s.agent_storage_policy FROM %s`,
			schema, schema, schema, schema, DataRole),
	} {
		if _, err := pool.Exec(ctx, statement); err != nil {
			return fmt.Errorf("grant memory namespace %q to the data plane: %w", schema, err)
		}
	}
	return nil
}
