// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package docsite

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ensera-ai/taisce/internal/migrate"
)

// The schema reference is what PostgreSQL built, read back from its catalog.
//
// A scratch database is created, bootstrapped the way a deployment is, given one project so that the
// per-project partitions exist, described, and dropped. Describing a database somebody already has
// would describe whatever state it happens to be in; a fresh one describes what the migrations make.

const (
	// The instance roles are cluster-wide, so bootstrapping sets their passwords on the whole
	// substrate. These are the passwords the test suite sets, and bootstrap runs under the suite's
	// own lock, so a build and a test run leave the substrate in the same state and cannot race.
	testControlPassword = "taisce-test-control"
	testDataPassword    = "taisce-test-data"
	// bootstrapLock is the key EstablishPlanes and the suites serialise role changes on.
	bootstrapLock = "taisce:establish-planes"
	// exampleProject is provisioned so the reference shows a project's partitions.
	exampleProject  = "example"
	memoryNamespace = "memory"
)

// withScratchDatabase creates an empty database on the substrate, gives it the extensions the
// substrate image gives its own database at first start, bootstraps it, hands a pool on it to fn,
// and drops it afterwards whatever fn returned.
//
// The extensions come from deploy/postgres/initdb, the files the image runs, rather than from a list
// here: one bootstrap for a deployment, the suite and the site, so none of them can describe a
// database the others would not build.
func withScratchDatabase(ctx context.Context, root, dsn string, fn func(*pgxpool.Pool) error) (err error) {
	initdb, err := filepath.Glob(filepath.Join(root, "deploy", "postgres", "initdb", "*.sql"))
	if err != nil || len(initdb) == 0 {
		return fmt.Errorf("docsite: the substrate's extension bootstrap in deploy/postgres/initdb is missing")
	}
	sort.Strings(initdb)
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Scheme == "" {
		return fmt.Errorf("docsite: the DSN must be a postgres:// URL")
	}
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("docsite: connect: %w", err)
	}
	defer admin.Close()
	suffix := make([]byte, 8)
	_, _ = rand.Read(suffix)
	name := "taisce_docsite_" + hex.EncodeToString(suffix)
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+quoted); err != nil {
		return fmt.Errorf("docsite: create the scratch database: %w", err)
	}
	defer func() {
		if _, dropErr := admin.Exec(context.WithoutCancel(ctx), "DROP DATABASE "+quoted+" WITH (FORCE)"); dropErr != nil && err == nil {
			err = fmt.Errorf("docsite: drop the scratch database %s: %w", name, dropErr)
		}
	}()
	parsed.Path = "/" + name
	scratch, err := pgxpool.New(ctx, parsed.String())
	if err != nil {
		return fmt.Errorf("docsite: connect to the scratch database: %w", err)
	}
	defer scratch.Close()
	for _, script := range initdb {
		body, err := os.ReadFile(script)
		if err != nil {
			return fmt.Errorf("docsite: %w", err)
		}
		if _, err := scratch.Exec(ctx, string(body)); err != nil {
			return fmt.Errorf("docsite: run %s: %w", filepath.Base(script), err)
		}
	}

	// Advisory locks are per database, and the suites take this one in the substrate's own database.
	lock, err := admin.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("docsite: %w", err)
	}
	defer lock.Release()
	if _, err := lock.Exec(ctx, `SELECT pg_advisory_lock(hashtext($1)::bigint)`, bootstrapLock); err != nil {
		return fmt.Errorf("docsite: take the bootstrap lock: %w", err)
	}
	err = bootstrap(ctx, scratch)
	_, _ = lock.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock(hashtext($1)::bigint)`, bootstrapLock)
	if err != nil {
		return err
	}
	return fn(scratch)
}

// bootstrap does what a deployment's bootstrap does, through the same functions.
func bootstrap(ctx context.Context, pool *pgxpool.Pool) error {
	if err := migrate.EstablishPlanes(ctx, pool, testControlPassword, testDataPassword); err != nil {
		return fmt.Errorf("docsite: establish the planes: %w", err)
	}
	if err := migrate.ProvisionMemorySchema(ctx, pool, memoryNamespace); err != nil {
		return fmt.Errorf("docsite: provision the memory namespace: %w", err)
	}
	if err := migrate.ProvisionScope(ctx, pool, memoryNamespace, exampleProject); err != nil {
		return fmt.Errorf("docsite: provision a project: %w", err)
	}
	return nil
}

func renderSchema(ctx context.Context, opts Options) (rendered, int, error) {
	var out rendered
	tables := 0
	err := withScratchDatabase(ctx, opts.Root, opts.DSN, func(pool *pgxpool.Pool) error {
		var err error
		out, tables, err = describe(ctx, pool)
		return err
	})
	return out, tables, err
}

// describe renders the reference from a bootstrapped database.
func describe(ctx context.Context, pool *pgxpool.Pool) (rendered, int, error) {
	q := &querier{ctx: ctx, pool: pool}
	var out rendered
	var version string
	q.one(`SELECT current_setting('server_version')`, &version)
	extensions := q.strings(`SELECT extname || ' ' || extversion FROM pg_extension ORDER BY extname`)

	tables := 0
	var summaries []string
	for _, ns := range []struct{ name, label, about string }{
		{migrate.ControlSchema.String(), "Control namespace",
			"The credential registry: projects' credentials and operator credentials. The memory role holds no privilege here; the registry role reads it."},
		{memoryNamespace, "Memory namespace",
			"Every project's memory. `memory` is the default name; an instance may choose another with `TAISCE_SCHEMA`. Projects are rows and partitions inside it, not namespaces of their own."},
	} {
		body, count := q.namespace(ns.name, ns.label, ns.about)
		tables += count
		stage := "reference/schema/" + ns.name + ".md"
		out.pages = append(out.pages, page{Stage: stage, Body: body})
		summaries = append(summaries, fmt.Sprintf("- [%s](%s): %d tables. %s", ns.label, ns.name+".md", count, ns.about))
	}
	out.pages = append(out.pages, page{Stage: "reference/schema/roles.md", Body: q.roles()})
	if q.err != nil {
		return rendered{}, 0, fmt.Errorf("docsite: describe the schema: %w", q.err)
	}

	var index strings.Builder
	index.WriteString("# Schema reference\n\n")
	index.WriteString("What PostgreSQL built, read back from its catalog. The build creates an empty database, " +
		"bootstraps it through the same functions a deployment's bootstrap calls, provisions one project " +
		"called `" + exampleProject + "` so that per-project partitions exist, reads the catalog, and drops the database. " +
		"Nothing here is parsed from SQL text, so what it shows is the end state after every migration, " +
		"including the ones that altered earlier ones.\n\n")
	fmt.Fprintf(&index, "Generated from PostgreSQL %s with %s.\n\n", version, strings.Join(extensions, ", "))
	index.WriteString(strings.Join(summaries, "\n") + "\n")
	index.WriteString("- [Roles and privileges](roles.md): the database identities, what each may reach, and the grant matrix.\n\n")
	index.WriteString("For why the schema has the shape it has, read the PostgreSQL pages under Architecture; for the order it " +
		"was built in, read the [migration catalogue](../migrations/overview.md).\n")
	out.pages = append(out.pages, page{Stage: "reference/schema/overview.md", Body: index.String()})
	out.nav = "- [Schema](reference/schema/overview.md)\n" +
		"  - [Control namespace](reference/schema/control.md)\n" +
		"  - [Memory namespace](reference/schema/memory.md)\n" +
		"  - [Roles and privileges](reference/schema/roles.md)\n"
	return out, tables, nil
}

// querier carries the first error so the rendering reads as a description rather than a ladder of
// error checks; nothing is written once err is set, and describe reports it.
type querier struct {
	ctx  context.Context
	pool *pgxpool.Pool
	err  error
}

func (q *querier) one(sql string, dest ...any) {
	if q.err == nil {
		q.err = q.pool.QueryRow(q.ctx, sql).Scan(dest...)
	}
}

func (q *querier) rows(sql string, args ...any) [][]string {
	if q.err != nil {
		return nil
	}
	rows, err := q.pool.Query(q.ctx, sql, args...)
	if err != nil {
		q.err = err
		return nil
	}
	defer rows.Close()
	var out [][]string
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			q.err = err
			return nil
		}
		row := make([]string, len(values))
		for i, v := range values {
			if v != nil {
				row[i] = fmt.Sprint(v)
			}
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		q.err = err
	}
	return out
}

func (q *querier) strings(sql string, args ...any) []string {
	var out []string
	for _, r := range q.rows(sql, args...) {
		out = append(out, r[0])
	}
	return out
}

// namespace renders one namespace and returns how many tables it holds.
func (q *querier) namespace(ns, label, about string) (string, int) {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s: `%s`\n\n%s\n\n", label, ns, about)
	relations := q.rows(`
		SELECT c.oid::text, c.relname, c.relkind::text, coalesce(obj_description(c.oid, 'pg_class'), ''),
		       c.relrowsecurity::text, c.relforcerowsecurity::text, pg_get_userbyid(c.relowner),
		       CASE WHEN c.relkind = 'p' THEN pg_get_partkeydef(c.oid) ELSE '' END
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relkind IN ('r', 'p') AND NOT c.relispartition
		ORDER BY c.relname`, ns)
	b.WriteString("## Tables at a glance\n\n| Table | What it holds |\n|---|---|\n")
	for _, r := range relations {
		fmt.Fprintf(&b, "| [%s](#%s) | %s |\n", r[1], r[1], cell(firstSentence(r[3])))
	}
	for _, r := range relations {
		q.table(&b, r)
	}
	q.views(&b, ns)
	q.functions(&b, ns)
	q.types(&b, ns)
	if seqs := q.strings(`SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relkind = 'S' ORDER BY 1`, ns); len(seqs) > 0 {
		b.WriteString("## Sequences\n\n")
		for _, s := range seqs {
			fmt.Fprintf(&b, "- `%s`\n", s)
		}
		b.WriteString("\n")
	}
	return b.String(), len(relations)
}

func (q *querier) table(b *strings.Builder, r []string) {
	oid, name, kind, comment, rls, forced, owner, partkey := r[0], r[1], r[2], r[3], r[4], r[5], r[6], r[7]
	fmt.Fprintf(b, "## %s {#%s}\n\n", name, name)
	if comment != "" {
		b.WriteString(comment + "\n\n")
	}
	facts := []string{"owner `" + owner + "`"}
	if kind == "p" {
		facts = append(facts, "partitioned by `"+partkey+"`")
	}
	if rls == "true" {
		security := "row-level security enabled"
		if forced == "true" {
			security += " and forced"
		}
		facts = append(facts, security)
	}
	b.WriteString(strings.Join(facts, " · ") + "\n\n")

	b.WriteString("| Column | Type | Null | Default | Comment |\n|---|---|---|---|---|\n")
	for _, c := range q.rows(`
		SELECT a.attname, format_type(a.atttypid, a.atttypmod), CASE WHEN a.attnotnull THEN 'not null' ELSE '' END,
		       CASE WHEN a.attgenerated = 's' THEN 'generated: ' ELSE '' END || coalesce(pg_get_expr(d.adbin, d.adrelid), '')
		         || CASE a.attidentity WHEN 'a' THEN 'identity always' WHEN 'd' THEN 'identity by default' ELSE '' END,
		       coalesce(col_description(a.attrelid, a.attnum), '')
		FROM pg_attribute a LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
		WHERE a.attrelid = $1::oid AND a.attnum > 0 AND NOT a.attisdropped ORDER BY a.attnum`, oid) {
		fmt.Fprintf(b, "| `%s` | `%s` | %s | %s | %s |\n", c[0], c[1], c[2], code(c[3]), cell(c[4]))
	}
	b.WriteString("\n")

	q.list(b, "Constraints", `SELECT conname, pg_get_constraintdef(oid) FROM pg_constraint
		WHERE conrelid = $1::oid ORDER BY contype, conname`, oid)
	q.list(b, "Indexes", `SELECT i.relname, pg_get_indexdef(x.indexrelid) FROM pg_index x
		JOIN pg_class i ON i.oid = x.indexrelid WHERE x.indrelid = $1::oid ORDER BY 1`, oid)
	q.list(b, "Triggers", `SELECT tgname, pg_get_triggerdef(oid) FROM pg_trigger
		WHERE tgrelid = $1::oid AND NOT tgisinternal ORDER BY 1`, oid)
	q.list(b, "Row-level security policies", `SELECT polname,
		  CASE polcmd WHEN 'r' THEN 'SELECT' WHEN 'a' THEN 'INSERT' WHEN 'w' THEN 'UPDATE' WHEN 'd' THEN 'DELETE' ELSE 'ALL' END
		  || ' to ' || coalesce(nullif(array_to_string(ARRAY(SELECT CASE WHEN o = 0 THEN 'PUBLIC' ELSE pg_get_userbyid(o) END
		       FROM unnest(polroles) o), ', '), ''), 'PUBLIC')
		  || coalesce(' using ' || pg_get_expr(polqual, polrelid), '')
		  || coalesce(' with check ' || pg_get_expr(polwithcheck, polrelid), '')
		FROM pg_policy WHERE polrelid = $1::oid ORDER BY 1`, oid)
	q.list(b, "Partitions", `SELECT c.relname, pg_get_expr(c.relpartbound, c.oid) FROM pg_inherits i
		JOIN pg_class c ON c.oid = i.inhrelid WHERE i.inhparent = $1::oid ORDER BY 1`, oid)
	q.list(b, "Privileges", `SELECT CASE WHEN a.grantee = 0 THEN 'PUBLIC' ELSE pg_get_userbyid(a.grantee) END,
		  string_agg(a.privilege_type, ', ' ORDER BY a.privilege_type)
		FROM pg_class c, aclexplode(c.relacl) a WHERE c.oid = $1::oid AND a.grantee <> c.relowner GROUP BY 1 ORDER BY 1`, oid)

	referrers := q.rows(`SELECT r.relname, c.conname FROM pg_constraint c JOIN pg_class r ON r.oid = c.conrelid
		WHERE c.confrelid = $1::oid AND c.contype = 'f' AND NOT r.relispartition ORDER BY 1, 2`, oid)
	if len(referrers) > 0 {
		b.WriteString("**Referenced by**\n\n")
		for _, ref := range referrers {
			fmt.Fprintf(b, "- [%s](#%s) through `%s`\n", ref[0], ref[0], ref[1])
		}
		b.WriteString("\n")
	}
}

// list renders name/definition pairs under a bold label, and nothing when there are none.
func (q *querier) list(b *strings.Builder, label, sql string, args ...any) {
	rows := q.rows(sql, args...)
	if len(rows) == 0 {
		return
	}
	fmt.Fprintf(b, "**%s**\n\n", label)
	for _, r := range rows {
		fmt.Fprintf(b, "- `%s`: %s\n", r[0], code(r[1]))
	}
	b.WriteString("\n")
}

func (q *querier) views(b *strings.Builder, ns string) {
	rows := q.rows(`SELECT c.relname, coalesce(obj_description(c.oid, 'pg_class'), ''), pg_get_viewdef(c.oid, true)
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relkind IN ('v', 'm') ORDER BY 1`, ns)
	if len(rows) == 0 {
		return
	}
	b.WriteString("## Views\n\n")
	for _, r := range rows {
		fmt.Fprintf(b, "### %s {#%s}\n\n%s\n\n```sql\n%s\n```\n\n", r[0], r[0], r[1], fenced(strings.TrimSpace(r[2])))
	}
}

func (q *querier) functions(b *strings.Builder, ns string) {
	rows := q.rows(`
		SELECT p.proname, pg_get_function_identity_arguments(p.oid), pg_get_function_result(p.oid), l.lanname,
		       CASE WHEN p.prosecdef THEN 'security definer' ELSE 'security invoker' END,
		       coalesce(obj_description(p.oid, 'pg_proc'), ''), pg_get_functiondef(p.oid),
		       coalesce((SELECT string_agg(CASE WHEN a.grantee = 0 THEN 'PUBLIC' ELSE pg_get_userbyid(a.grantee) END, ', ')
		                 FROM aclexplode(p.proacl) a WHERE a.grantee <> p.proowner), 'owner only (no ACL: PUBLIC may execute)')
		FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace JOIN pg_language l ON l.oid = p.prolang
		WHERE n.nspname = $1 AND p.prokind IN ('f', 'p') ORDER BY 1, 2`, ns)
	if len(rows) == 0 {
		return
	}
	b.WriteString("## Functions\n\n")
	for _, r := range rows {
		fmt.Fprintf(b, "### %s {#fn-%s}\n\n`%s(%s)` returns `%s` · %s · %s · execute: %s\n\n", r[0], r[0], r[0], r[1], r[2], r[3], r[4], r[7])
		if r[5] != "" {
			b.WriteString(r[5] + "\n\n")
		}
		def := strings.TrimSpace(r[6])
		fmt.Fprintf(b, "<details><summary>definition</summary>\n\n```sql\n%s\n```\n\n</details>\n\n", fenced(def))
	}
}

func (q *querier) types(b *strings.Builder, ns string) {
	rows := q.rows(`
		SELECT t.typname, CASE t.typtype WHEN 'e' THEN 'enum' ELSE 'domain' END,
		       CASE t.typtype WHEN 'e' THEN (SELECT string_agg(enumlabel, ', ' ORDER BY enumsortorder) FROM pg_enum WHERE enumtypid = t.oid)
		            ELSE format_type(t.typbasetype, t.typtypmod) || coalesce(' ' || (SELECT string_agg(pg_get_constraintdef(oid), ' ')
		                 FROM pg_constraint WHERE contypid = t.oid), '') END,
		       coalesce(obj_description(t.oid, 'pg_type'), '')
		FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace
		WHERE n.nspname = $1 AND t.typtype IN ('e', 'd') ORDER BY 1`, ns)
	if len(rows) == 0 {
		return
	}
	b.WriteString("## Types\n\n| Type | Kind | Definition | Comment |\n|---|---|---|---|\n")
	for _, r := range rows {
		fmt.Fprintf(b, "| `%s` | %s | %s | %s |\n", r[0], r[1], code(r[2]), cell(r[3]))
	}
	b.WriteString("\n")
}

// roles renders the identities and a grant matrix, which is the privilege boundary stated by the
// database rather than by anybody's description of it.
func (q *querier) roles() string {
	roles := []string{migrate.ControlRole, migrate.DataRole}
	var b strings.Builder
	b.WriteString("# Roles and privileges\n\n")
	b.WriteString("The privilege boundary as the database states it. Bootstrap creates two login roles: `" + migrate.ControlRole +
		"`, which reads the credential registry, and `" + migrate.DataRole + "`, which serves memory. Migrations and " +
		"project provisioning run as the administrative identity, which owns every object. A serving process refuses " +
		"to start on a connection whose effective privileges are wider than its role's.\n\n")
	b.WriteString("## Role attributes\n\n| Role | Login | Superuser | Create role | Create database | Replication | Bypass row security | Member of |\n|---|---|---|---|---|---|---|---|\n")
	for _, r := range q.rows(`
		SELECT r.rolname, r.rolcanlogin::text, r.rolsuper::text, r.rolcreaterole::text, r.rolcreatedb::text,
		       r.rolreplication::text, r.rolbypassrls::text,
		       coalesce((SELECT string_agg(m.rolname, ', ' ORDER BY m.rolname) FROM pg_auth_members am
		                 JOIN pg_roles m ON m.oid = am.roleid WHERE am.member = r.oid), '')
		FROM pg_roles r WHERE r.rolname = ANY($1) ORDER BY 1`, roles) {
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s | %s | %s | %s | %s |\n", r[0], yes(r[1]), yes(r[2]), yes(r[3]), yes(r[4]), yes(r[5]), yes(r[6]), orNone(r[7]))
	}
	b.WriteString("\n## Database and namespaces\n\n| Object | Role | Privileges |\n|---|---|---|\n")
	for _, r := range q.rows(`
		SELECT 'database ' || d.datname, CASE WHEN a.grantee = 0 THEN 'PUBLIC' ELSE pg_get_userbyid(a.grantee) END,
		       string_agg(a.privilege_type, ', ' ORDER BY a.privilege_type)
		FROM pg_database d, aclexplode(d.datacl) a WHERE d.datname = current_database() AND a.grantee <> d.datdba GROUP BY 1, 2
		UNION ALL
		SELECT 'namespace ' || n.nspname, CASE WHEN a.grantee = 0 THEN 'PUBLIC' ELSE pg_get_userbyid(a.grantee) END,
		       string_agg(a.privilege_type, ', ' ORDER BY a.privilege_type)
		FROM pg_namespace n, aclexplode(n.nspacl) a WHERE n.nspname = ANY($1) AND a.grantee <> n.nspowner GROUP BY 1, 2
		ORDER BY 1, 2`, []string{migrate.ControlSchema.String(), memoryNamespace}) {
		fmt.Fprintf(&b, "| %s | `%s` | %s |\n", strings.Replace(r[0], " ", " `", 1)+"`", r[1], r[2])
	}
	b.WriteString("\nThe database name is the scratch database the reference was built in; a deployment's has its own name and the same grants.\n")
	for _, ns := range []string{migrate.ControlSchema.String(), memoryNamespace} {
		fmt.Fprintf(&b, "\n## Tables in `%s`\n\nWhat each role may do to each table. The owner, which may do everything, is not listed.\n\n| Table | `%s` | `%s` | PUBLIC |\n|---|---|---|---|\n",
			ns, migrate.ControlRole, migrate.DataRole)
		for _, r := range q.rows(`
			SELECT c.relname,
			  coalesce((SELECT string_agg(lower(a.privilege_type), ', ' ORDER BY a.privilege_type) FROM aclexplode(c.relacl) a WHERE a.grantee = (SELECT oid FROM pg_roles WHERE rolname = $2)), ''),
			  coalesce((SELECT string_agg(lower(a.privilege_type), ', ' ORDER BY a.privilege_type) FROM aclexplode(c.relacl) a WHERE a.grantee = (SELECT oid FROM pg_roles WHERE rolname = $3)), ''),
			  coalesce((SELECT string_agg(lower(a.privilege_type), ', ' ORDER BY a.privilege_type) FROM aclexplode(c.relacl) a WHERE a.grantee = 0), '')
			FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname = $1 AND c.relkind IN ('r', 'p') AND NOT c.relispartition ORDER BY 1`, ns, migrate.ControlRole, migrate.DataRole) {
			fmt.Fprintf(&b, "| [%s](%s.md#%s) | %s | %s | %s |\n", r[0], ns, r[0], orNone(r[1]), orNone(r[2]), orNone(r[3]))
		}
	}
	return b.String()
}

func yes(v string) string {
	if v == "true" {
		return "yes"
	}
	return "no"
}

func orNone(v string) string {
	if v == "" {
		return "—"
	}
	return v
}

// code renders a definition as a code span, or nothing for an empty one.
func code(s string) string {
	if s == "" {
		return ""
	}
	s = strings.Join(strings.Fields(s), " ")
	return "`" + strings.ReplaceAll(cell(s), "`", "'") + "`"
}
