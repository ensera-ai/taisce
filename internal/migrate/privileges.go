// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package migrate

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type RuntimePlane string

const (
	MemoryPlane   RuntimePlane = "memory"
	RegistryPlane RuntimePlane = "registry"
)

var ErrUnsafeRuntimePrivileges = errors.New("unsafe runtime database privileges")

type privilegeReader interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// ValidateRolePrivileges inspects effective privileges, including roles whose privileges are
// immediately inherited or reachable with SET ROLE. Bootstrap uses it through its admin connection.
// Findings are fixed diagnostic categories, never credential values or database object contents.
func ValidateRolePrivileges(ctx context.Context, db privilegeReader, schema Schema, role string, plane RuntimePlane) error {
	if plane != MemoryPlane && plane != RegistryPlane {
		return fmt.Errorf("unknown runtime plane %q", plane)
	}
	var reason string
	if err := db.QueryRow(ctx, privilegeCheckSQL, role, schema.String(), string(plane)).Scan(&reason); err != nil {
		return fmt.Errorf("inspect %s privileges: %w", plane, err)
	}
	if reason != "" {
		return fmt.Errorf("%w: %s: %s", ErrUnsafeRuntimePrivileges, plane, reason)
	}
	return nil
}

// ValidateRuntimeConnection checks both identities. A privileged session cannot hide behind
// SET ROLE before validation and recover its original privilege afterwards with RESET ROLE.
func ValidateRuntimeConnection(ctx context.Context, conn *pgx.Conn, schema Schema, plane RuntimePlane) error {
	var login, current string
	if err := conn.QueryRow(ctx, `SELECT session_user,current_user`).Scan(&login, &current); err != nil {
		return err
	}
	if err := ValidateRolePrivileges(ctx, conn, schema, login, plane); err != nil {
		return err
	}
	if current != login {
		return ValidateRolePrivileges(ctx, conn, schema, current, plane)
	}
	return nil
}

// NewRuntimePool validates every newly opened physical connection before it enters the pool.
// Startup must Ping the pool before opening listeners or launching workers. This is a connection
// admission check, not continuous protection against an administrator changing grants afterwards.
func NewRuntimePool(ctx context.Context, dsn string, schema Schema, plane RuntimePlane) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		return ValidateRuntimeConnection(ctx, conn, schema, plane)
	}
	return pgxpool.NewWithConfig(ctx, config)
}

const privilegeCheckSQL = `
WITH reachable AS (
 SELECT r.* FROM pg_roles r WHERE r.rolname=$1
    OR pg_has_role($1,r.oid,'USAGE') OR pg_has_role($1,r.oid,'SET')
), objects AS (
 SELECT c.oid,c.relowner,c.relkind,n.nspname,c.relname FROM pg_class c
 JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname IN ($2,'control') AND c.relkind IN ('r','p','v','m','f','S')
)
SELECT CASE
 WHEN NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1) THEN 'login does not exist'
 WHEN to_regnamespace($2) IS NULL OR to_regnamespace('control') IS NULL THEN 'instance namespaces are missing'
 WHEN EXISTS(SELECT 1 FROM reachable WHERE rolsuper OR rolcreatedb OR rolcreaterole OR rolreplication OR rolbypassrls)
   THEN 'elevated role attributes or reachable privileged role'
 WHEN EXISTS(SELECT 1 FROM reachable r WHERE has_database_privilege(r.oid,current_database(),'CREATE'))
   THEN 'database schema creation is permitted'
 WHEN EXISTS(SELECT 1 FROM reachable r CROSS JOIN pg_namespace n
   WHERE n.nspname IN ($2,'control','public') AND (n.nspowner=r.oid OR has_schema_privilege(r.oid,n.oid,'CREATE')))
   THEN 'namespace ownership or creation is permitted'
 WHEN EXISTS(SELECT 1 FROM reachable r JOIN objects o ON o.relowner=r.oid)
   THEN 'runtime owns application database objects'
 WHEN $3='memory' AND EXISTS(SELECT 1 FROM reachable r WHERE has_schema_privilege(r.oid,'control','USAGE'))
   THEN 'memory role can access the credential namespace'
 WHEN $3='memory' AND EXISTS(SELECT 1 FROM reachable r CROSS JOIN objects o WHERE o.nspname='control' AND o.relkind<>'S'
   AND (has_table_privilege(r.oid,o.oid,'SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
     OR has_any_column_privilege(r.oid,o.oid,'SELECT,INSERT,UPDATE,REFERENCES')))
   THEN 'memory role has credential object privileges'
 WHEN $3='memory' AND EXISTS(SELECT 1 FROM reachable r CROSS JOIN objects o WHERE o.nspname=$2
   AND o.relname IN ('projection_kind','predicate','speaker_term','unresolvable_term','schema_migration',
                    'ingestion_budget','ingestion_project_usage','ingestion_reservation','agent_storage_policy',
                    'embedding_generation','embedding_active','entity_embedding_generation','entity_embedding_active',
                    'report_embedding_generation','report_embedding_active')
   AND (has_table_privilege(r.oid,o.oid,'INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
     OR has_any_column_privilege(r.oid,o.oid,'INSERT,UPDATE,REFERENCES')))
   THEN 'memory policy catalogs are writable'
 WHEN $3='registry' AND EXISTS(SELECT 1 FROM reachable r CROSS JOIN objects o WHERE o.relkind<>'S' AND
   ((o.nspname=$2 AND (has_table_privilege(r.oid,o.oid,'SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
     OR has_any_column_privilege(r.oid,o.oid,'SELECT,INSERT,UPDATE,REFERENCES')))
    OR (o.nspname='control' AND (has_table_privilege(r.oid,o.oid,'INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
     OR has_any_column_privilege(r.oid,o.oid,'INSERT,UPDATE,REFERENCES')))))
   THEN 'registry runtime has memory access or credential write privileges'
 WHEN $3='memory' AND (NOT has_schema_privilege($1,$2,'USAGE')
   OR NOT COALESCE(has_table_privilege($1,to_regclass(format('%I.observation',$2)),'SELECT'),false))
   THEN 'required memory read privileges are missing'
 WHEN $3='registry' AND (NOT has_schema_privilege($1,'control','USAGE')
   OR NOT COALESCE(has_table_privilege($1,to_regclass('control.credential'),'SELECT'),false))
   THEN 'required credential read privileges are missing'
 ELSE '' END`
