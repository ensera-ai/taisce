// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package migrate

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRuntimePoolRefusesPrivilegeDriftAndAcceptsRemediation(t *testing.T) {
	ctx := context.Background()
	owner := testPool(t)
	schema, _ := NewSchema("privilege_" + strings.ReplaceAll(uuid.NewString(), "-", ""))
	establish(t, owner, schema.String())
	// This fixture deliberately changes control's ACL. Coordinate with concurrent package
	// bootstrap tests using the existing bootstrap lock, so their ACL reconciliation cannot
	// race the injected grant/remediation or make a refusal vacuously pass.
	locked, err := owner.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer locked.Release()
	if _, err := locked.Exec(ctx, `SELECT pg_advisory_lock(hashtext('taisce:establish-planes')::bigint)`); err != nil {
		t.Fatal(err)
	}
	defer locked.Exec(ctx, `SELECT pg_advisory_unlock(hashtext('taisce:establish-planes')::bigint)`)
	t.Cleanup(func() { owner.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE") })
	var ownerName string
	if err := owner.QueryRow(ctx, `SELECT current_user`).Scan(&ownerName); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, mutate, restore string
		plane                 RuntimePlane
	}{
		{"direct credential namespace", `GRANT USAGE ON SCHEMA control TO {role}`, `REVOKE USAGE ON SCHEMA control FROM {role}`, MemoryPlane},
		{"column-only credential grant", `GRANT SELECT(token_digest) ON control.credential TO {role}`, `REVOKE SELECT(token_digest) ON control.credential FROM {role}`, MemoryPlane},
		{"inherited credential grant", `GRANT USAGE ON SCHEMA control TO {escape}; GRANT {escape} TO {role}`, `REVOKE {escape} FROM {role}; REVOKE USAGE ON SCHEMA control FROM {escape}`, MemoryPlane},
		{"set-role escape without inheritance", `ALTER ROLE {role} NOINHERIT; GRANT USAGE ON SCHEMA control TO {escape}; GRANT {escape} TO {role}`, `REVOKE {escape} FROM {role}; REVOKE USAGE ON SCHEMA control FROM {escape}; ALTER ROLE {role} INHERIT`, MemoryPlane},
		{"table owner", `ALTER TABLE SCHEMA.observation OWNER TO {role}`, `ALTER TABLE SCHEMA.observation OWNER TO {owner}`, MemoryPlane},
		{"namespace owner", `ALTER SCHEMA SCHEMA OWNER TO {role}`, `ALTER SCHEMA SCHEMA OWNER TO {owner}`, MemoryPlane},
		{"create database", `ALTER ROLE {role} CREATEDB`, `ALTER ROLE {role} NOCREATEDB`, MemoryPlane},
		{"schema create", `GRANT CREATE ON SCHEMA SCHEMA TO {role}`, `REVOKE CREATE ON SCHEMA SCHEMA FROM {role}`, MemoryPlane},
		{"artifact quota policy write", `GRANT UPDATE ON SCHEMA.agent_storage_policy TO {role}`, `REVOKE UPDATE ON SCHEMA.agent_storage_policy FROM {role}`, MemoryPlane},
		{"artifact quota column write", `GRANT UPDATE(used_bytes) ON SCHEMA.agent_storage_policy TO {role}`, `REVOKE UPDATE(used_bytes) ON SCHEMA.agent_storage_policy FROM {role}`, MemoryPlane},
		{"policy catalog write", `GRANT UPDATE ON SCHEMA.projection_kind TO {role}`, `REVOKE UPDATE ON SCHEMA.projection_kind FROM {role}`, MemoryPlane},
		{"embedding identity write", `GRANT UPDATE(model_revision) ON SCHEMA.embedding_generation TO {role}`, `REVOKE UPDATE(model_revision) ON SCHEMA.embedding_generation FROM {role}`, MemoryPlane},
		{"embedding activation write", `GRANT INSERT ON SCHEMA.embedding_active TO {role}`, `REVOKE INSERT ON SCHEMA.embedding_active FROM {role}`, MemoryPlane},
		{"entity embedding identity write", `GRANT UPDATE(model_revision) ON SCHEMA.entity_embedding_generation TO {role}`, `REVOKE UPDATE(model_revision) ON SCHEMA.entity_embedding_generation FROM {role}`, MemoryPlane},
		{"entity embedding activation write", `GRANT INSERT ON SCHEMA.entity_embedding_active TO {role}`, `REVOKE INSERT ON SCHEMA.entity_embedding_active FROM {role}`, MemoryPlane},
		{"report embedding identity write", `GRANT UPDATE(model_revision) ON SCHEMA.report_embedding_generation TO {role}`, `REVOKE UPDATE(model_revision) ON SCHEMA.report_embedding_generation FROM {role}`, MemoryPlane},
		{"report embedding activation write", `GRANT INSERT ON SCHEMA.report_embedding_active TO {role}`, `REVOKE INSERT ON SCHEMA.report_embedding_active FROM {role}`, MemoryPlane},
		{"ledger rewrite", `GRANT UPDATE ON SCHEMA.audit_entry TO {role}`, `REVOKE UPDATE ON SCHEMA.audit_entry FROM {role}`, MemoryPlane},
		{"project settings write", `GRANT UPDATE ON SCHEMA.project TO {role}`, `REVOKE UPDATE ON SCHEMA.project FROM {role}`, MemoryPlane},
		{"project suspension column write", `GRANT UPDATE(suspended_at) ON SCHEMA.project TO {role}`, `REVOKE UPDATE(suspended_at) ON SCHEMA.project FROM {role}`, MemoryPlane},
		{"write to a table nobody listed", `CREATE TABLE SCHEMA.unlisted_drift (x int); GRANT INSERT ON SCHEMA.unlisted_drift TO {role}`, `DROP TABLE IF EXISTS SCHEMA.unlisted_drift`, MemoryPlane},
		{"function owner", `CREATE FUNCTION SCHEMA.drift_owned() RETURNS int LANGUAGE sql AS 'SELECT 1'; ALTER FUNCTION SCHEMA.drift_owned() OWNER TO {role}`, `DROP FUNCTION IF EXISTS SCHEMA.drift_owned()`, MemoryPlane},
		{"definer without a pinned search path", `CREATE FUNCTION SCHEMA.drift_definer() RETURNS int LANGUAGE sql SECURITY DEFINER AS 'SELECT 1'`, `DROP FUNCTION IF EXISTS SCHEMA.drift_definer()`, MemoryPlane},
		{"definer with pg_temp first", `CREATE FUNCTION SCHEMA.drift_temp_first() RETURNS int LANGUAGE sql SECURITY DEFINER SET search_path=pg_temp,pg_catalog AS 'SELECT 1'`, `DROP FUNCTION IF EXISTS SCHEMA.drift_temp_first()`, MemoryPlane},
		{"definer running dynamic SQL", `CREATE FUNCTION SCHEMA.drift_dynamic() RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$BEGIN EXECUTE 'SELECT 1'; END$$`, `DROP FUNCTION IF EXISTS SCHEMA.drift_dynamic()`, MemoryPlane},
		{"registry-side definer without a pinned search path", `CREATE FUNCTION SCHEMA.drift_registry_definer() RETURNS int LANGUAGE sql SECURITY DEFINER AS 'SELECT 1'`, `DROP FUNCTION IF EXISTS SCHEMA.drift_registry_definer()`, RegistryPlane},
		{"registry credential write", `GRANT UPDATE ON control.credential TO {role}`, `REVOKE UPDATE ON control.credential FROM {role}`, RegistryPlane},
		{"registry memory read", `GRANT SELECT ON SCHEMA.observation TO {role}`, `REVOKE SELECT ON SCHEMA.observation FROM {role}`, RegistryPlane},
	} {
		t.Run(tc.name, func(t *testing.T) {
			role := "priv_role_" + strings.ReplaceAll(uuid.NewString(), "-", "")
			escape := "priv_escape_" + strings.ReplaceAll(uuid.NewString(), "-", "")
			for _, name := range []string{role, escape} {
				if err := ensureRole(ctx, owner, name, "isolated-privilege-test"); err != nil {
					t.Fatal(err)
				}
			}
			replacement := strings.NewReplacer("SCHEMA.", schema.String()+".", "SCHEMA SCHEMA", "SCHEMA "+schema.String(), "{role}", pgx.Identifier{role}.Sanitize(), "{escape}", pgx.Identifier{escape}.Sanitize(), "{owner}", pgx.Identifier{ownerName}.Sanitize())
			t.Cleanup(func() {
				owner.Exec(ctx, replacement.Replace(tc.restore))
				owner.Exec(ctx, "DROP OWNED BY "+pgx.Identifier{role}.Sanitize()+","+pgx.Identifier{escape}.Sanitize())
				owner.Exec(ctx, "DROP ROLE "+pgx.Identifier{role}.Sanitize()+","+pgx.Identifier{escape}.Sanitize())
			})
			base := DataRole
			if tc.plane == RegistryPlane {
				base = ControlRole
			}
			if _, err := owner.Exec(ctx, "GRANT "+pgx.Identifier{base}.Sanitize()+" TO "+pgx.Identifier{role}.Sanitize()); err != nil {
				t.Fatal(err)
			}
			parsed, err := url.Parse(os.Getenv("TAISCE_TEST_DSN"))
			if err != nil {
				t.Fatal(err)
			}
			parsed.User = url.UserPassword(role, "isolated-privilege-test")
			pool, err := NewRuntimePool(ctx, parsed.String(), schema, tc.plane)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(pool.Close)
			if err := pool.Ping(ctx); err != nil {
				t.Fatalf("baseline rejected: %v", err)
			}
			if _, err := owner.Exec(ctx, replacement.Replace(tc.mutate)); err != nil {
				t.Fatal(err)
			}
			pool.Reset()
			bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			if err := pool.Ping(bounded); !errors.Is(err, ErrUnsafeRuntimePrivileges) {
				t.Fatalf("unsafe physical connection admitted: %v", err)
			}
			if _, err := owner.Exec(ctx, replacement.Replace(tc.restore)); err != nil {
				t.Fatal(err)
			}
			pool.Reset()
			if err := pool.Ping(ctx); err != nil {
				t.Fatalf("remediation did not restore service: %v", err)
			}
		})
	}
}

func TestSetRoleCannotHideAPrivilegedLoginFromRuntimeValidation(t *testing.T) {
	ctx := context.Background()
	owner := testPool(t)
	schema, _ := NewSchema("priv_session")
	establish(t, owner, schema.String())
	conn, err := owner.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SET ROLE taisce_data`); err != nil {
		t.Fatal(err)
	}
	defer conn.Exec(ctx, `RESET ROLE`)
	if err := ValidateRuntimeConnection(ctx, conn.Conn(), schema, MemoryPlane); !errors.Is(err, ErrUnsafeRuntimePrivileges) {
		t.Fatalf("privileged session concealed by SET ROLE: %v", err)
	}
}

func TestPrivilegeInspectionRefusesMissingConfigurationAndCancelledQueries(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema, _ := NewSchema("priv_missing")
	if err := ValidateRolePrivileges(ctx, pool, schema, DataRole, "typo"); err == nil {
		t.Fatal("unknown plane accepted")
	}
	if err := ValidateRolePrivileges(ctx, pool, schema, "priv_role_does_not_exist", MemoryPlane); !errors.Is(err, ErrUnsafeRuntimePrivileges) {
		t.Fatalf("missing login accepted: %v", err)
	}
	if err := ValidateRolePrivileges(ctx, pool, schema, DataRole, MemoryPlane); !errors.Is(err, ErrUnsafeRuntimePrivileges) {
		t.Fatalf("missing schema accepted: %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := ValidateRolePrivileges(cancelled, pool, schema, DataRole, MemoryPlane); err == nil {
		t.Fatal("cancelled query passed")
	}
	if _, err := NewRuntimePool(ctx, "postgres://%", schema, MemoryPlane); err == nil {
		t.Fatal("invalid DSN accepted")
	}
	// The interface is usable by a real pool; no owner credential reaches a runtime connection.
	var _ privilegeReader = (*pgxpool.Pool)(nil)
}
