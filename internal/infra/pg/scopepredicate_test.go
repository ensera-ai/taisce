// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ensera-ai/taisce/internal/infra/pg"
)

// The boundary between projects is a predicate each statement carries. Nothing in the database
// refuses a statement that forgot it: one login serves every project, and a read without
// `scope = …` returns every project's rows, correctly typed and plausibly ordered, to whoever
// asked. This file is what looks.
//
// # What it is, and what it is not
//
// It is a lint. It reads the source of every package that issues SQL, puts each statement back
// together from the constants it is built from, and fails on a reference to a table that has a
// `scope` column where nothing in the statement ties that reference's scope to anything.
//
// It is not a boundary. It reads text, so it can be satisfied by a predicate that is present and
// wrong, and it cannot see a statement assembled at run time from pieces it cannot follow — those
// it reports as pieces, each held to the rule on its own. A statement that passes here is one
// somebody remembered the predicate in, which is the property that was unchecked. A statement the
// database itself refuses to answer without a project is a different and stronger thing.
//
// # Why the tables come from the database
//
// Which tables are scoped is a fact about the schema, and a list kept here would be right on the
// day it was written. The migrated schema is asked.

// sqlSource is one statement, or one piece of one, as the source spells it.
type sqlSource struct {
	where string // file:line
	name  string // the constant it was read from, or the function it sits in
	text  string
	whole bool // false when part of it is computed at run time and could not be followed
}

// unknown stands in a folded statement for an expression that is computed at run time.
const unknown = "\x00"

// statementsIn reads every string the package can hand the database: each maximal constant
// expression that names a table through `{schema}.`, with the package's own constants followed.
func statementsIn(t *testing.T, dir string) []sqlSource {
	t.Helper()
	fset := token.NewFileSet()
	parsed, err := parser.ParseDir(fset, dir, func(info os.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", dir, err)
	}
	var files []*ast.File
	for _, pkg := range parsed {
		for _, file := range pkg.Files {
			files = append(files, file)
		}
	}
	// Package-level constants and variables that hold text, by name.
	named := map[string]ast.Expr{}
	for _, file := range files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || (gen.Tok != token.CONST && gen.Tok != token.VAR) {
				continue
			}
			for _, spec := range gen.Specs {
				value := spec.(*ast.ValueSpec)
				for i, name := range value.Names {
					if i < len(value.Values) {
						named[name.Name] = value.Values[i]
					}
				}
			}
		}
	}
	var fold func(expr ast.Expr, depth int) (string, bool)
	fold = func(expr ast.Expr, depth int) (string, bool) {
		if depth > 20 {
			return unknown, false
		}
		switch e := expr.(type) {
		case *ast.BasicLit:
			if e.Kind != token.STRING {
				return unknown, false
			}
			text, err := strconv.Unquote(e.Value)
			return text, err == nil
		case *ast.ParenExpr:
			return fold(e.X, depth+1)
		case *ast.BinaryExpr:
			if e.Op != token.ADD {
				return unknown, false
			}
			left, lok := fold(e.X, depth+1)
			right, rok := fold(e.Y, depth+1)
			return left + right, lok && rok
		case *ast.Ident:
			if value, ok := named[e.Name]; ok {
				return fold(value, depth+1)
			}
		case *ast.CallExpr:
			// fmt.Sprintf(template, …) and strings.ReplaceAll(template, …): the template is the
			// statement, with holes.
			if sel, ok := e.Fun.(*ast.SelectorExpr); ok && len(e.Args) > 0 && (sel.Sel.Name == "Sprintf" || sel.Sel.Name == "ReplaceAll") {
				text, _ := fold(e.Args[0], depth+1)
				return text, false
			}
		}
		return unknown, false
	}

	var found []sqlSource
	for _, file := range files {
		var stack []ast.Node
		enclosing := func() string {
			for i := len(stack) - 1; i >= 0; i-- {
				switch n := stack[i].(type) {
				case *ast.FuncDecl:
					return n.Name.Name
				case *ast.ValueSpec:
					if len(n.Names) > 0 {
						return n.Names[0].Name
					}
				}
			}
			return ""
		}
		ast.Inspect(file, func(node ast.Node) bool {
			if node == nil {
				stack = stack[:len(stack)-1]
				return true
			}
			parent := ast.Node(nil)
			if len(stack) > 0 {
				parent = stack[len(stack)-1]
			}
			stack = append(stack, node)
			expr, ok := node.(ast.Expr)
			if !ok {
				return true
			}
			switch expr.(type) {
			case *ast.BasicLit, *ast.BinaryExpr, *ast.Ident:
			default:
				return true
			}
			// Maximal: a piece of a larger concatenation is read as part of that.
			switch p := parent.(type) {
			case *ast.BinaryExpr:
				if p.Op == token.ADD {
					return true
				}
			case *ast.ParenExpr:
				return true
			case *ast.ValueSpec:
				// A declaration is read where it is used, so that a fragment only ever used inside
				// a larger statement is judged as that statement.
				for _, value := range p.Values {
					if value == expr {
						if _, isBinary := expr.(*ast.BinaryExpr); isBinary {
							return false
						}
						return true
					}
				}
				return true
			case *ast.SelectorExpr, *ast.KeyValueExpr:
				if ident, isIdent := expr.(*ast.Ident); isIdent {
					if sel, isSel := parent.(*ast.SelectorExpr); isSel && sel.Sel == ident {
						return true
					}
					if kv, isKV := parent.(*ast.KeyValueExpr); isKV && kv.Key == expr {
						return true
					}
				}
			}
			if ident, isIdent := expr.(*ast.Ident); isIdent {
				if _, isNamed := named[ident.Name]; !isNamed {
					return true
				}
			}
			text, whole := fold(expr, 0)
			if strings.Contains(text, "{schema}.") {
				position := fset.Position(expr.Pos())
				found = append(found, sqlSource{
					where: fmt.Sprintf("%s:%d", filepath.Base(position.Filename), position.Line),
					name:  enclosing(), text: text, whole: whole,
				})
			}
			// What was folded is not read again piece by piece.
			_, isBinary := expr.(*ast.BinaryExpr)
			return !isBinary
		})
	}
	return found
}

var (
	tableReference = regexp.MustCompile(`(?i)(\b[a-z]+\s+(?:only\s+)?)?\{schema\}\.([a-z_][a-z0-9_]*)(?:\s+(?:as\s+)?([a-z_][a-z0-9_]*))?`)
	notAnAlias     = map[string]bool{
		"where": true, "set": true, "on": true, "using": true, "join": true, "left": true, "right": true, "inner": true,
		"cross": true, "full": true, "natural": true, "values": true, "select": true, "order": true, "group": true, "limit": true,
		"returning": true, "for": true, "union": true, "except": true, "intersect": true, "as": true, "and": true, "or": true,
		"when": true, "then": true, "do": true, "default": true, "window": true, "having": true, "offset": true, "fetch": true,
		"lateral": true, "tablesample": true, "partition": true, "to": true, "in": true, "is": true, "not": true,
	}
)

// unscoped returns the references in a statement to a scoped table whose scope nothing constrains.
//
// A reference is tied when a predicate names its scope through its alias or its table name. A
// predicate on a bare `scope` names nobody; in a statement PostgreSQL accepts it is unambiguous,
// so it belongs to the one table at its level that has the column. Levels are not parsed here, so
// bare predicates are counted instead: each ties one reference that nothing else did. Three reads
// of a scoped table and one bare predicate leaves two reads untied, whichever two they are.
var (
	sqlComment    = regexp.MustCompile(`--[^\n]*`)
	barePredicate = regexp.MustCompile(`(^|[^a-z0-9_.])scope\s*(=|in\b|<@)|=\s*scope\b|using\s*\(\s*scope\b`)
)

func unscoped(text string, scoped map[string]bool) []string {
	// A comment carries no predicate, and one that mentions scope must not satisfy the rule.
	lower := strings.ToLower(sqlComment.ReplaceAllString(text, " "))
	credit := len(barePredicate.FindAllString(lower, -1))
	var untied []string
	for _, at := range tableReference.FindAllStringSubmatchIndex(lower, -1) {
		group := func(n int) string {
			if at[2*n] < 0 {
				return ""
			}
			return lower[at[2*n]:at[2*n+1]]
		}
		lead, table, alias := strings.TrimSpace(group(1)), group(2), group(3)
		if !scoped[table] {
			continue
		}
		// The target of an INSERT is written, not read: its scope is a value in the row.
		if strings.HasSuffix(lead, "into") {
			continue
		}
		// `{schema}.t.column` is a column of a table referenced elsewhere, and `::{schema}.t` is
		// the table's row type. Neither reads the table.
		end := at[5]
		if end < len(lower) && lower[end] == '.' {
			continue
		}
		if start := at[4] - len("{schema}."); start >= 2 && lower[start-2:start] == "::" {
			continue
		}
		if notAnAlias[alias] {
			alias = ""
		}
		tied := false
		for _, name := range []string{alias, table} {
			if name == "" {
				continue
			}
			q := regexp.QuoteMeta(name)
			if regexp.MustCompile(`\b` + q + `\.scope\s*(=|in\b|<@)|=\s*` + q + `\.scope\b|\(\s*` + q + `\.scope\s*,|,\s*` + q + `\.scope\s*\)`).MatchString(lower) {
				tied = true
			}
		}
		if tied {
			continue
		}
		label := table
		if alias != "" {
			label += " " + alias
		}
		untied = append(untied, label)
	}
	if len(untied) <= credit {
		return nil
	}
	return untied[credit:]
}

// excuse is a statement that names a scoped table without tying its scope, and why that is right.
//
// An excuse is not a comment. Where the reason is a fact about the schema — the row is addressed
// by a key unique across the whole instance, or reached through a foreign key that carries the
// project — the excuse names the fact and the test asks the database whether it is still true.
// An excuse nothing matches any longer fails too, so the list cannot outlive what it excuses.
type excuse struct {
	file, name string   // the file, and the constant or function the statement is read from
	untied     []string // the references it leaves untied, as the lint reports them
	why        string
	// unique: {table, "column[,column]"} — a unique index on exactly these columns, so the
	// statement addresses at most one row in the instance, whichever project it belongs to.
	unique [][2]string
	// together: {child, parent} — a foreign key from child to parent that includes the project,
	// so a row reached from one through that key is in the same project.
	together [][2]string
}

var excused = []excuse{
	// ── Meant to span projects ──────────────────────────────────────────────────────────────
	{file: "operationalhealth.go", name: "ReadOperationalHealth", untied: []string{"observation"},
		why: "the instance's health is an aggregate over every project, returned to an operator as counts and ages"},
	{file: "observationstore.go", name: "ScopesWithBacklog", untied: []string{"observation"},
		why: "the worker asks which projects have unformed turns; it returns project names and no row of any"},
	{file: "projectstore.go", name: "List", untied: []string{"project"},
		why: "an operator lists the instance's projects"},
	{file: "commands.go", name: "project", untied: []string{"project"},
		why: "`taisce project list`, over the administrative connection"},
	{file: "notificationstore.go", name: "Claim", untied: []string{"notification_delivery d", "notification_endpoint e"},
		why:    "the delivery worker takes the next due delivery in the instance, and reads its endpoint by key",
		unique: [][2]string{{"notification_delivery", "delivery_id"}, {"notification_endpoint", "endpoint_id"}}},

	// ── One row, by a key unique across the instance ────────────────────────────────────────
	{file: "observationstore.go", name: "appendTurnTx", untied: []string{"observation o"},
		why: "the turn just written in this transaction, by its identifier", unique: [][2]string{{"observation", "observation_id"}}},
	{file: "observationstore.go", name: "NextUnformed", untied: []string{"observation"},
		why: "the turn chosen by the subquery, which names the project", unique: [][2]string{{"observation", "observation_id"}}},
	{file: "observationstore.go", name: "MarkFormed", untied: []string{"observation"},
		why: "a turn the worker claimed under its project's lock", unique: [][2]string{{"observation", "observation_id"}}},
	{file: "observationstore.go", name: "RecordFormationFailure", untied: []string{"observation"},
		why: "a turn the worker claimed under its project's lock", unique: [][2]string{{"observation", "observation_id"}}},
	{file: "observationstore.go", name: "Park", untied: []string{"observation"},
		why: "a turn the worker claimed under its project's lock", unique: [][2]string{{"observation", "observation_id"}}},
	{file: "embeddingbuild.go", name: "commitEmbeddingInputs", untied: []string{"observation o"},
		why:    "the turn a message belongs to, reached from a chunk whose project is named",
		unique: [][2]string{{"observation", "observation_id"}, {"turn_message", "chunk_id"}}},
	{file: "notificationstore.go", name: "Delivered", untied: []string{"notification_delivery"},
		why: "the delivery this worker claimed", unique: [][2]string{{"notification_delivery", "delivery_id"}}},
	{file: "notificationstore.go", name: "Failed", untied: []string{"notification_delivery"},
		why: "the delivery this worker claimed", unique: [][2]string{{"notification_delivery", "delivery_id"}}},
	{file: "notificationstore.go", name: "Deliveries", untied: []string{"notification_endpoint e"},
		why:    "the endpoint of a delivery whose project is named, by the endpoint's key; that the two are in one project is the writer's doing, and no key holds it",
		unique: [][2]string{{"notification_endpoint", "endpoint_id"}}},
	{file: "erasurestore.go", name: "erase", untied: []string{"erasure_request"},
		why: "the request this erasure opened", unique: [][2]string{{"erasure_request", "request_id"}}},
	{file: "erasurestore.go", name: "Held", untied: []string{"erasure_request"},
		why: "whether this instance has a receipt for an erasure, by the receipt's identifier; a replay asks before it knows the project is here at all", unique: [][2]string{{"erasure_request", "request_id"}}},
	{file: "chunkrecovery.go", name: "recoverMessageChunkTx", untied: []string{"projection_dependency"},
		why:    "one registration by its whole key; the project is what the statement reads back and compares",
		unique: [][2]string{{"projection_dependency", "source_observation_id,projection_kind,projection_id"}}},
	{file: "factstore.go", name: "assertFactVersionTx", untied: []string{"fact f"},
		why: "the facts the first step selected by project, updated by identifier", unique: [][2]string{{"fact", "fact_id"}}},

	// ── Reached through a key that carries the project ──────────────────────────────────────
	{file: "recallstore.go", name: "FactsAboutForSubject", untied: []string{"fact_evidence ev", "entity s", "entity o"},
		why: "the evidence and the two ends of a fact whose project is named", together: [][2]string{{"fact_evidence", "fact"}, {"fact", "entity"}}},
	{file: "recallstore.go", name: "selectFactsAboutSQL", untied: []string{"fact_evidence ev", "entity s", "entity o"},
		why: "the evidence and the two ends of a fact whose project is named", together: [][2]string{{"fact_evidence", "fact"}, {"fact", "entity"}}},
	{file: "recallstore.go", name: "selectFactsValidAtSQL", untied: []string{"fact_evidence ev", "entity s", "entity o"},
		why: "the evidence and the two ends of a fact whose project is named", together: [][2]string{{"fact_evidence", "fact"}, {"fact", "entity"}}},
	{file: "recallstore.go", name: "historicalFactsSQL", untied: []string{"fact_evidence ev", "entity s", "entity o", "fact f"},
		why:      "the same read at an earlier moment; `FROM {schema}.fact f` on its own is the text this function finds and replaces",
		together: [][2]string{{"fact_evidence", "fact"}, {"fact", "entity"}}},
	{file: "entityembedding.go", name: "loadEntityEmbeddingInput", untied: []string{"fact_evidence fe"},
		why: "the evidence of facts whose project is named", together: [][2]string{{"fact_evidence", "fact"}}, unique: [][2]string{{"fact", "fact_id"}}},
	{file: "erasurestore.go", name: "erase", untied: []string{"fact_evidence e"},
		why: "evidence of a turn whose project is named", together: [][2]string{{"fact_evidence", "observation"}}},
	{file: "erasurestore.go", name: "erase", untied: []string{"observation o"},
		why: "the turn a message belongs to; the count this fragment starts names the project in the text added to it", unique: [][2]string{{"observation", "observation_id"}}},

	// ── Finished at run time, where the lint cannot follow ──────────────────────────────────
	{file: "feedbackstore.go", name: "feedbackListQuery", untied: []string{"memory_feedback f"},
		why: "the WHERE is assembled in this function, and its first term is f.scope=$1"},
	{file: "erasurestore.go", name: "selectErasableSQL", untied: []string{"projection_dependency other"},
		why: "the sharing rule: another registration of the same projection. It names no project, and is right only because every kind that survives sharing has an identifier unique across the instance — which TestAProjectionThatSurvivesSharingIsNamedAcrossTheInstance holds"},
}

// scopedTables asks the migrated schema which tables carry a project.
func scopedTables(t *testing.T, ctx context.Context, pool *pgxpool.Pool, schema pg.Schema) map[string]bool {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT table_name FROM information_schema.columns WHERE table_schema=$1 AND column_name='scope'`, schema.String())
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	scoped := map[string]bool{}
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatal(err)
		}
		scoped[table] = true
	}
	if err := rows.Err(); err != nil || len(scoped) < 10 {
		t.Fatalf("the migrated schema reports %d tables with a project, which is not a schema this lint can judge: %v", len(scoped), err)
	}
	return scoped
}

// uniqueAcrossTheInstance reports whether a unique index covers exactly these columns of a table,
// with no predicate narrowing it.
func uniqueAcrossTheInstance(t *testing.T, ctx context.Context, pool *pgxpool.Pool, schema pg.Schema, table, columns string) bool {
	t.Helper()
	var found bool
	err := pool.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid=i.indrelid JOIN pg_namespace n ON n.oid=c.relnamespace
   WHERE n.nspname=$1 AND c.relname=$2 AND i.indisunique AND i.indpred IS NULL
     AND (SELECT string_agg(a.attname, ',' ORDER BY a.attname) FROM unnest(i.indkey) k(attnum)
            JOIN pg_attribute a ON a.attrelid=i.indrelid AND a.attnum=k.attnum)
       = (SELECT string_agg(col, ',' ORDER BY col) FROM unnest(string_to_array($3, ',')) col))`,
		schema.String(), table, columns).Scan(&found)
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// heldToOneProject reports whether a foreign key from child to parent includes the project.
func heldToOneProject(t *testing.T, ctx context.Context, pool *pgxpool.Pool, schema pg.Schema, child, parent string) bool {
	t.Helper()
	var found bool
	err := pool.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1 FROM pg_constraint c JOIN pg_namespace n ON n.oid=c.connamespace
   WHERE n.nspname=$1 AND c.contype='f'
     AND c.conrelid=(quote_ident($1)||'.'||quote_ident($2))::regclass
     AND c.confrelid=(quote_ident($1)||'.'||quote_ident($3))::regclass
     AND EXISTS (SELECT 1 FROM unnest(c.conkey) k(attnum) JOIN pg_attribute a
                   ON a.attrelid=c.conrelid AND a.attnum=k.attnum WHERE a.attname='scope'))`,
		schema.String(), child, parent).Scan(&found)
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// TestEveryStatementTiesTheProjectOfEachScopedTableItNames is the check the project boundary did
// not have.
//
// One login serves every project, and what separates them is `scope = …` written into each
// statement. A statement that forgot it is valid, fast, and returns or removes every project's
// rows. The first run of this found one: a retention sweep deleted its projections by identifier
// alone, and so removed another project's artifact of the same name
// (TestASweepOfOneProjectLeavesAnotherProjectsRowWithTheSameIdentifier).
//
// Every statement the store packages can issue is read from their source. One that names a scoped
// table and ties its project to nothing fails here, unless it is on the list above with a reason —
// and where the reason is a fact about the schema, that fact is asked of the database.
func TestEveryStatementTiesTheProjectOfEachScopedTableItNames(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "scope_predicate_lint")
	scoped := scopedTables(t, ctx, pool, schema)

	used := make([]bool, len(excused))
	read := 0
	for _, dir := range []string{".", "../../migrate", "../../../cmd/taisce", "../../docsite"} {
		for _, statement := range statementsIn(t, dir) {
			read++
			for _, reference := range unscoped(statement.text, scoped) {
				covered := false
				for i, e := range excused {
					if e.file != strings.SplitN(statement.where, ":", 2)[0] || e.name != statement.name {
						continue
					}
					for _, label := range e.untied {
						if label == reference {
							covered, used[i] = true, true
						}
					}
				}
				if !covered {
					head := strings.Join(strings.Fields(statement.text), " ")
					if len(head) > 220 {
						head = head[:220] + " …"
					}
					t.Errorf("%s (%s) reads or changes %q and ties its project to nothing, so it reaches that table in every project.\n"+
						"Name the project on it (scope = …, or through its alias), or add it to the list in this file with the reason it is right.\n  %s",
						statement.where, statement.name, reference, head)
				}
			}
		}
	}
	// A lint that read nothing passes everything.
	if read < 300 {
		t.Fatalf("read %d statements from the store packages; there were more than four hundred when this was written, so the reader has stopped finding them", read)
	}
	for i, e := range excused {
		if !used[i] {
			t.Errorf("the excuse for %s (%s) %v matches no statement any more; remove it, so the list holds only what it excuses", e.file, e.name, e.untied)
		}
		if e.why == "" {
			t.Errorf("the excuse for %s (%s) gives no reason", e.file, e.name)
		}
		for _, key := range e.unique {
			if !uniqueAcrossTheInstance(t, ctx, pool, schema, key[0], key[1]) {
				t.Errorf("%s (%s) is excused because %s(%s) is unique across the instance, and the schema no longer has a unique index on exactly that",
					e.file, e.name, key[0], key[1])
			}
		}
		for _, pair := range e.together {
			if !heldToOneProject(t, ctx, pool, schema, pair[0], pair[1]) {
				t.Errorf("%s (%s) is excused because a foreign key from %s to %s carries the project, and the schema has no such key",
					e.file, e.name, pair[0], pair[1])
			}
		}
	}
}

// TestAProjectionThatSurvivesSharingIsNamedAcrossTheInstance holds the fact the erasure's sharing
// rule rests on.
//
// An erasure keeps a projection somebody else also contributed to, and reads "somebody else" from
// the other registrations of that projection — matched by kind and identifier, with no project.
// That is right only while an identifier of such a kind cannot occur in two projects. For every
// kind the schema declares as surviving sharing, this asks the database for the key that makes it
// so. A kind added later that survives sharing and is keyed by project and identifier fails here,
// before an erasure in one project can be stopped by a row in another.
func TestAProjectionThatSurvivesSharingIsNamedAcrossTheInstance(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "scope_sharing_lint")
	// Where each such kind's identifier is unique by itself. A chunk is keyed by project and
	// identifier in its own table; what makes its identifier unique is the message it is the text
	// of, which is in a table that has no project at all.
	uniqueIn := map[string][2]string{
		"entity": {"entity", "entity_id"}, "fact": {"fact", "fact_id"},
		"rejected_claim": {"rejected_claim", "rejected_claim_id"}, "chunk": {"turn_message", "chunk_id"},
	}
	rows, err := pool.Query(ctx, schema.SQL(`SELECT kind FROM {schema}.projection_kind WHERE survives_sharing ORDER BY kind`))
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for rows.Next() {
		var kind string
		if err := rows.Scan(&kind); err != nil {
			t.Fatal(err)
		}
		kinds = append(kinds, kind)
	}
	rows.Close()
	if len(kinds) == 0 {
		t.Fatal("the schema declares no projection kind that survives sharing, so this test is no longer reading the declaration")
	}
	for _, kind := range kinds {
		key, known := uniqueIn[kind]
		if !known {
			t.Errorf("projection kind %q survives sharing and nothing here says its identifier is unique across the instance; "+
				"an erasure matches its registrations by identifier alone, so a row of that name in another project would stop it", kind)
			continue
		}
		if !uniqueAcrossTheInstance(t, ctx, pool, schema, key[0], key[1]) {
			t.Errorf("projection kind %q survives sharing, and %s(%s) is no longer unique across the instance", kind, key[0], key[1])
		}
	}
	sort.Strings(kinds)
	t.Logf("kinds that survive sharing: %s", strings.Join(kinds, ", "))
}
