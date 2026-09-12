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
//
// It inspects functions too. A security-definer function runs as its owner, which is whoever
// ran the migrations, so each one must pin its search path with pg_temp last, or a temporary object
// could stand in for a name it resolves; and must not run dynamic SQL, the one way its text could
// be made to do something it does not say. A runtime role must own no function in an instance
// namespace: an owner can reset a function's search path without any other privilege.
// Findings are fixed diagnostic categories, never credential values or database object contents.
func ValidateRolePrivileges(ctx context.Context, db privilegeReader, schema Schema, role string, plane RuntimePlane) error {
	if plane != MemoryPlane && plane != RegistryPlane {
		return fmt.Errorf("unknown runtime plane %q", plane)
	}
	var reason string
	if err := db.QueryRow(ctx, privilegeCheckSQL, role, schema.String(), string(plane), memoryWriteGrants(), memoryColumnGrants()).Scan(&reason); err != nil {
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
	for name, value := range runtimeConnectionDefaults {
		if _, set := config.ConnConfig.RuntimeParams[name]; !set {
			config.ConnConfig.RuntimeParams[name] = value
		}
	}
	return pgxpool.NewWithConfig(ctx, config)
}

// runtimeConnectionDefaults are set on every runtime connection unless its DSN sets them.
//
// A process that holds a session-level advisory lock, as the formation worker does for a project
// while it drains it, keeps that lock until the server ends its session. When the process's host
// is cut off, the server learns that from TCP keepalives, and the kernel's default waits about two
// hours before the first probe: a project's formation stalls that long. These make the server probe
// an idle connection after 30 seconds and give up after three unanswered probes ten seconds apart,
// so the session and its locks go in about a minute. The client side needs nothing: pgx dials
// with Go's default keepalive.
//
// client_connection_check_interval makes the server check, during a long query, whether the client
// is still there, and cancel the query when it is not, instead of finishing work nobody will read.
//
// There is deliberately no statement_timeout: rebuilds, embedding builds and migrations run long
// statements on these identities, and a bound short enough to matter on the request path would cut
// them. Requests are bounded by their own contexts.
var runtimeConnectionDefaults = map[string]string{
	"tcp_keepalives_idle":              "30",
	"tcp_keepalives_interval":          "10",
	"tcp_keepalives_count":             "3",
	"client_connection_check_interval": "10s",
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
 WHEN EXISTS(SELECT 1 FROM reachable r JOIN pg_proc p ON p.proowner=r.oid
   JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname IN ($2,'control'))
   THEN 'runtime owns a function in an instance namespace'
 WHEN EXISTS(SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
   WHERE n.nspname IN ($2,'control') AND p.prosecdef
   AND NOT EXISTS(SELECT 1 FROM unnest(p.proconfig) c WHERE c ~ '^search_path=.*\mpg_temp\s*$'))
   THEN 'a security-definer function does not pin its search path with pg_temp last'
 WHEN EXISTS(SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
   WHERE n.nspname IN ($2,'control') AND p.prosecdef AND p.prosrc ~* '\mexecute\M')
   THEN 'a security-definer function runs dynamic SQL'
 WHEN $3='memory' AND EXISTS(SELECT 1 FROM reachable r WHERE has_schema_privilege(r.oid,'control','USAGE'))
   THEN 'memory role can access the credential namespace'
 WHEN $3='memory' AND EXISTS(SELECT 1 FROM reachable r CROSS JOIN objects o WHERE o.nspname='control' AND o.relkind<>'S'
   AND (has_table_privilege(r.oid,o.oid,'SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
     OR has_any_column_privilege(r.oid,o.oid,'SELECT,INSERT,UPDATE,REFERENCES')))
   THEN 'memory role has credential object privileges'
 WHEN $3='memory' AND EXISTS(SELECT 1 FROM reachable r CROSS JOIN objects o
   CROSS JOIN unnest(ARRAY['INSERT','UPDATE','DELETE','TRUNCATE','REFERENCES','TRIGGER']) AS p(privilege)
   WHERE o.nspname=$2 AND o.relkind IN ('r','p')
   AND NOT (o.relname || ':' || p.privilege = ANY($4::text[]))
   AND (has_table_privilege(r.oid,o.oid,p.privilege)
     OR (p.privilege IN ('INSERT','UPDATE','REFERENCES') AND EXISTS(SELECT 1 FROM pg_attribute a
       WHERE a.attrelid=o.oid AND a.attnum>0 AND NOT a.attisdropped
         AND has_column_privilege(r.oid,o.oid,a.attnum,p.privilege)
         AND NOT (o.relname || ':' || p.privilege || ':' || a.attname = ANY($5::text[]))))))
   THEN 'memory role can change a table the memory service does not write'
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
