package docsite

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ensera-ai/taisce/internal/migrate"
)

// The site is only worth publishing if it is complete, public, and every link on it goes somewhere.
// These tests build it from this repository against the real substrate, and make each refusal refuse.

func testDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("TAISCE_TEST_DSN")
	if dsn == "" {
		t.Skip("TAISCE_TEST_DSN is not set")
	}
	return dsn
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// output returns a document directory and sidebar file in a fresh temporary directory.
func output(t *testing.T) (string, string) {
	dir := t.TempDir()
	return filepath.Join(dir, "docs"), filepath.Join(dir, "sidebars.json")
}

func TestTheSiteCoversEveryPackageEveryMigrationAndEveryTableFromThisRepository(t *testing.T) {
	dsn := testDSN(t)
	root := repoRoot(t)
	out, bars := output(t)
	report, err := Build(context.Background(), Options{Root: root, Out: out, Sidebars: bars, DSN: dsn, Repo: "ensera-ai/taisce", Ref: "main"})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	nav := read(t, bars)

	// Every directory holding a non-test Go file under cmd/ and internal/ has a page and a place in
	// the navigation, found here by a walk of its own rather than by asking the code under test.
	packages := 0
	for _, top := range []string{"cmd", "internal"} {
		_ = filepath.WalkDir(filepath.Join(root, top), func(p string, d os.DirEntry, err error) error {
			if err != nil || !d.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			rel = filepath.ToSlash(rel)
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			matches, _ := filepath.Glob(filepath.Join(p, "*.go"))
			for _, m := range matches {
				if strings.HasSuffix(m, "_test.go") {
					continue
				}
				packages++
				if _, err := os.Stat(filepath.Join(out, "reference", "code", rel+".md")); err != nil {
					t.Errorf("package %s has no page", rel)
				}
				if !strings.Contains(nav, `"reference/code/`+rel+`"`) {
					t.Errorf("package %s is not in the navigation", rel)
				}
				break
			}
			return nil
		})
	}
	if report.Packages != packages {
		t.Errorf("the build documented %d packages; the repository has %d", report.Packages, packages)
	}

	memory, _ := migrate.Load()
	control, _ := migrate.LoadControl()
	if report.Migrations != len(memory)+len(control) {
		t.Errorf("the catalogue has %d migrations; the binary embeds %d", report.Migrations, len(memory)+len(control))
	}
	if report.Tables == 0 || report.Declarations == 0 || report.Tests == 0 || report.Documents == 0 {
		t.Errorf("a section of the site came out empty: %+v", report)
	}
	var decoded map[string][]json.RawMessage
	if err := json.Unmarshal([]byte(nav), &decoded); err != nil || len(decoded["guide"]) == 0 || len(decoded["reference"]) == 0 {
		t.Errorf("the navigation is not two non-empty sidebars: %v", err)
	}
	intro := read(t, filepath.Join(out, "introduction.md"))
	if strings.HasPrefix(intro, "<!--") {
		t.Error("the licence comment was left on a rendered page")
	}
}

func TestTheSchemaReferenceDescribesEveryTableTheMigrationsBuild(t *testing.T) {
	dsn := testDSN(t)
	ctx := context.Background()
	err := withScratchDatabase(ctx, repoRoot(t), dsn, func(pool *pgxpool.Pool) error {
		out, tables, err := describe(ctx, pool)
		if err != nil {
			return err
		}
		bodies := map[string]string{}
		for _, p := range out.pages {
			bodies[p.Stage] = p.Body
		}
		// pg_tables is asked independently of the catalog query under test; a partition is a
		// project's storage, described under its parent rather than as a table of its own.
		rows, err := pool.Query(ctx, `
			SELECT schemaname, tablename FROM pg_tables t
			WHERE schemaname IN ('memory', 'control')
			  AND NOT EXISTS (SELECT 1 FROM pg_inherits i WHERE i.inhrelid = (quote_ident(schemaname) || '.' || quote_ident(tablename))::regclass)`)
		if err != nil {
			return err
		}
		defer rows.Close()
		count := 0
		for rows.Next() {
			var schema, table string
			if err := rows.Scan(&schema, &table); err != nil {
				return err
			}
			count++
			if !strings.Contains(bodies["reference/schema/"+schema+".md"], "## "+table+" {#"+table+"}") {
				t.Errorf("%s.%s is not described", schema, table)
			}
		}
		if count != tables {
			t.Errorf("the reference counts %d tables; the database holds %d", tables, count)
		}
		if !strings.Contains(bodies["reference/schema/memory.md"], "FOR VALUES IN ('"+exampleProject+"')") {
			t.Error("the provisioned project's partition is not shown")
		}
		if !strings.Contains(bodies["reference/schema/roles.md"], migrate.DataRole) {
			t.Error("the grant matrix does not name the memory role")
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAScratchDatabaseIsRefusedWithoutTheSubstratesBootstrapOrAUsableDSN(t *testing.T) {
	ctx := context.Background()
	noop := func(*pgxpool.Pool) error { return nil }
	if err := withScratchDatabase(ctx, t.TempDir(), "postgres://x@127.0.0.1:1/x", noop); err == nil || !strings.Contains(err.Error(), "initdb") {
		t.Errorf("got %v, want a refusal naming the missing extension bootstrap", err)
	}
	if err := withScratchDatabase(ctx, repoRoot(t), "host=x user=y", noop); err == nil || !strings.Contains(err.Error(), "postgres:// URL") {
		t.Errorf("got %v, want a refusal of a DSN that is not a URL", err)
	}
	if err := withScratchDatabase(ctx, repoRoot(t), "postgres://nobody@127.0.0.1:1/none?connect_timeout=1", noop); err == nil {
		t.Error("a substrate that does not answer was accepted")
	}
}

func TestABuildWithoutADatabaseIsRefusedRatherThanPublishedWithoutItsSchema(t *testing.T) {
	out, bars := output(t)
	_, err := Build(context.Background(), Options{Root: ".", Out: out, Sidebars: bars, Repo: "o/r", Ref: "main"})
	if err == nil || !strings.Contains(err.Error(), "database") {
		t.Fatalf("got %v, want a refusal naming the database", err)
	}
	for _, missing := range []Options{
		{Out: "o", Sidebars: "s", DSN: "d", Repo: "o/r", Ref: "m"},
		{Root: ".", Sidebars: "s", DSN: "d", Repo: "o/r", Ref: "m"},
		{Root: ".", Out: "o", DSN: "d", Repo: "o/r", Ref: "m"},
		{Root: ".", Out: "o", Sidebars: "s", DSN: "d"},
	} {
		if err := missing.validate(); err == nil {
			t.Errorf("%+v was accepted", missing)
		}
	}
}

func TestTheBuildRefusesADocumentDirectoryItWouldBeDangerousToReplace(t *testing.T) {
	root := t.TempDir()
	for out, want := range map[string]string{
		filepath.Join(root, "docs", "site"): "inside docs/",
		root:                                "would contain the repository",
		filepath.Dir(root):                  "would contain the repository",
	} {
		opts := Options{Root: root, Out: out, Sidebars: "s", DSN: "d", Repo: "o/r", Ref: "m"}
		if err := opts.validate(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("out=%s: got %v, want a refusal saying %q", out, err, want)
		}
	}
}

// fixture is the smallest repository the build can read: one documented package and the documents
// the caller asks for.
func fixture(t *testing.T, docs map[string]string) string {
	t.Helper()
	root := t.TempDir()
	write(t, root, "README.md", "# Fixture\n")
	write(t, root, "Makefile", "# Builds it.\nbuild:\n\tgo build ./...\n")
	write(t, root, "internal/ok/ok.go", "// Package ok is documented.\npackage ok\n\n// Answer is documented too.\nconst Answer = 42\n")
	for name, body := range docs {
		write(t, root, "docs/"+name, body)
	}
	return root
}

func buildFixture(t *testing.T, root string) error {
	t.Helper()
	out, bars := output(t)
	// The DSN names nothing that answers: every refusal below has to happen before it is needed.
	_, err := Build(context.Background(), Options{
		Root: root, Out: out, Sidebars: bars, DSN: "postgres://nobody@127.0.0.1:1/none?connect_timeout=1", Repo: "o/r", Ref: "m",
	})
	return err
}

const navigation = "- [A](a.md)\n" + summaryMarker + "\n"

func TestAPackageWithNoDocCommentIsRefused(t *testing.T) {
	root := fixture(t, map[string]string{"SUMMARY.md": navigation, "a.md": "# A\n"})
	write(t, root, "internal/silent/silent.go", "package silent\n")
	// A licence header sitting directly on the package clause is what the parser calls the package
	// doc; it says nothing about what the package is for, and does not count as documentation.
	write(t, root, "internal/licensed/licensed.go", "// Copyright 2026 The Taisce Authors\n// SPDX-License-Identifier: Apache-2.0\npackage licensed\n")
	// Where another file carries the real documentation, that is the package's doc.
	write(t, root, "internal/ok/header.go", "// Copyright 2026 The Taisce Authors\n// SPDX-License-Identifier: Apache-2.0\npackage ok\n")
	err := buildFixture(t, root)
	if err == nil || !strings.Contains(err.Error(), "internal/silent") || !strings.Contains(err.Error(), "internal/licensed") {
		t.Fatalf("got %v, want a refusal naming internal/silent and internal/licensed", err)
	}
	if strings.Contains(err.Error(), "internal/ok") {
		t.Errorf("a package documented in another file was refused: %v", err)
	}
	packages, err := loadPackages(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range packages {
		if strings.HasPrefix(p.Doc, "Copyright") {
			t.Errorf("%s: the licence header was taken for the package documentation", p.Dir)
		}
	}
}

func TestALinkThatNamesNothingIsRefused(t *testing.T) {
	root := fixture(t, map[string]string{
		"SUMMARY.md": navigation,
		"a.md":       "# A\n\n[gone](missing.md) and [out](../../elsewhere.md) and [bad](%zz)\n\n```\n[example](not-a-link.md)\n```\n\n`[code](nor-this.md)`\n",
	})
	err := buildFixture(t, root)
	if err == nil {
		t.Fatal("a broken link was accepted")
	}
	for _, want := range []string{"docs/a.md: missing.md names nothing", "docs/a.md: ../../elsewhere.md leaves the repository", "%zz is not a valid path"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q:\n%v", want, err)
		}
	}
	for _, example := range []string{"not-a-link.md", "nor-this.md"} {
		if strings.Contains(err.Error(), example) {
			t.Errorf("%s is an example inside code, and was treated as a link", example)
		}
	}
}

func TestADocumentTheNavigationDoesNotReachIsRefused(t *testing.T) {
	root := fixture(t, map[string]string{"SUMMARY.md": navigation, "a.md": "# A\n", "b.md": "# B\n"})
	err := buildFixture(t, root)
	if err == nil || !strings.Contains(err.Error(), "docs/b.md") {
		t.Fatalf("got %v, want a refusal naming docs/b.md", err)
	}
}

func TestADocumentKeptInTheRepositoryIsNotPublishedAndALinkToItGoesToItsSource(t *testing.T) {
	summary := "- [A](a.md)\n" + keptMarker + "\n\n     Records for contributors.\n\n- b.md\n-->\n" + summaryMarker + "\n"
	root := fixture(t, map[string]string{"SUMMARY.md": summary, "a.md": "# A\n\n[the run](b.md#results)\n", "b.md": "# B\n"})

	docs, staged, err := stageDocuments(root)
	if err != nil {
		t.Fatal(err)
	}
	published, err := publishedDocuments(staged, docs)
	if err != nil {
		t.Fatalf("a consistent kept list was refused: %v", err)
	}
	if len(published) != 1 || published[0].Stage != "a.md" {
		t.Fatalf("published %+v, want only a.md", published)
	}
	if err := checkListed(summary, published); err != nil {
		t.Errorf("a kept document was demanded in the navigation: %v", err)
	}
	if err := resolveLinks(published, Options{Root: root, Repo: "o/r", Ref: "m"}, nil); err != nil {
		t.Fatalf("a link to a kept document was refused: %v", err)
	}
	if !strings.Contains(published[0].Body, "[the run](https://github.com/o/r/blob/m/docs/b.md#results)") {
		t.Errorf("a link to a kept document does not go to its source:\n%s", published[0].Body)
	}
	bars, err := sidebars(summary)
	if err != nil || strings.Contains(string(bars), `"b"`) {
		t.Errorf("a kept document reached the sidebar: %v\n%s", err, bars)
	}

	// The whole build accepts it too, getting as far as the database this fixture does not have.
	if err := buildFixture(t, root); err == nil || strings.Contains(err.Error(), "SUMMARY.md") {
		t.Errorf("got %v, want the build to pass the navigation and stop only at the database", err)
	}
	// No kept list at all is an empty one.
	if kept, err := keptDocuments(navigation); err != nil || len(kept) != 0 {
		t.Errorf("a navigation without a kept list read as %v, %v", kept, err)
	}
}

func TestAKeptListThatContradictsTheNavigationOrNamesNothingIsRefused(t *testing.T) {
	contradicting := "- [A](a.md)\n- [B](b.md)\n" + keptMarker + "\n- b.md\n- gone.md\n-->\n" + summaryMarker + "\n"
	root := fixture(t, map[string]string{"SUMMARY.md": contradicting, "a.md": "# A\n", "b.md": "# B\n"})
	err := buildFixture(t, root)
	if err == nil {
		t.Fatal("a document both listed and kept, and a kept name that is no document, were accepted")
	}
	for _, want := range []string{"docs/b.md is both listed and kept", "docs/gone.md is kept but is not a document"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q:\n%v", want, err)
		}
	}

	// The reference marker closes a comment of its own, so an unclosed list is one written after it.
	unclosed := "- [A](a.md)\n" + summaryMarker + "\n" + keptMarker + "\n- b.md\n"
	root = fixture(t, map[string]string{"SUMMARY.md": unclosed, "a.md": "# A\n", "b.md": "# B\n"})
	if err := buildFixture(t, root); err == nil || !strings.Contains(err.Error(), "never closed") {
		t.Errorf("got %v, want a refusal of a kept list with no end", err)
	}
}

func TestANavigationWithoutTheReferenceMarkerOrWithoutAnyFileIsRefused(t *testing.T) {
	if _, err := spliceSummary("- [A](a.md)\n", "- [Code](x.md)\n"); err == nil {
		t.Error("a navigation with nowhere to put the reference was accepted")
	}
	root := fixture(t, map[string]string{"a.md": "# A\n"})
	if err := buildFixture(t, root); err == nil || !strings.Contains(err.Error(), "SUMMARY.md") {
		t.Errorf("got %v, want a refusal naming the missing navigation", err)
	}
}

func TestAnImageUnderTheStaticDirectoryIsServedFromTheSiteRootAndAMissingOneIsRefused(t *testing.T) {
	opts := Options{Root: repoRoot(t), Repo: "o/r", Ref: "abc"}
	p := page{Repo: "docs/start/x.md", Stage: "start/x.md", Body: strings.Join([]string{
		`![the mark](../../site/static/img/favicon.svg "Taisce")`,
		"[the directory](../../site/static/img/)",
		"![gone](../../site/static/img/no-such-screenshot.png)",
	}, "\n\n")}
	body, broken := rewriteLinks(p, map[string]string{}, opts)
	// On GitHub the relative path opens the file; on the site the renderer serves it from the root.
	if !strings.Contains(body, `![the mark](/img/favicon.svg "Taisce")`) {
		t.Errorf("an image under site/static/ was not rewritten to the path it is served at:\n%s", body)
	}
	// A directory there is not something the renderer serves as a file, so it stays a source link.
	if !strings.Contains(body, "[the directory](https://github.com/o/r/tree/abc/site/static/img)") {
		t.Errorf("a directory under site/static/ did not become a source link:\n%s", body)
	}
	if len(broken) != 1 || !strings.Contains(broken[0], "no-such-screenshot.png names nothing") {
		t.Errorf("a screenshot that does not exist was not refused: %v", broken)
	}
}

func TestTheNavigationBecomesAGuideAndAReferenceSidebar(t *testing.T) {
	summary := strings.Join([]string{
		"<!-- a comment -->",
		"# Summary",
		"[Introduction](introduction.md)",
		"# Architecture",
		"- [Overview](architecture/overview.md)",
		"  - [Detail](architecture/detail.md)",
		"    - [Deeper](architecture/deeper.md)",
		"- [Flat](flat.md)",
		"# Reference",
		"- [Code](reference/code/overview.md)",
		"  - [pkg](reference/code/pkg.md)",
	}, "\n")
	body, err := sidebars(summary)
	if err != nil {
		t.Fatal(err)
	}
	var bars map[string][]*sidebarItem
	if err := json.Unmarshal(body, &bars); err != nil {
		t.Fatal(err)
	}
	guide := bars["guide"]
	if len(guide) != 2 || guide[0].ID != "introduction" || guide[1].Label != "Architecture" {
		t.Fatalf("guide = %s", body)
	}
	overview := guide[1].Items[0]
	if overview.Type != "category" || overview.Link.ID != "architecture/overview" || overview.Items[0].Items[0].ID != "architecture/deeper" {
		t.Errorf("a page with pages under it did not become a category linking to itself: %s", body)
	}
	if guide[1].Items[1].ID != "flat" {
		t.Errorf("a sibling after a nested run was misplaced: %s", body)
	}
	if ref := bars["reference"]; len(ref) != 1 || ref[0].Link.ID != "reference/code/overview" {
		t.Errorf("the reference part did not become its own sidebar: %s", body)
	}
	if _, err := sidebars("# Part\n- [A](a.md)\n      - [Too deep](b.md)\n"); err == nil || !strings.Contains(err.Error(), "line 3") {
		t.Errorf("got %v, want a refusal naming the over-nested line", err)
	}
}

func TestAPageKeepsWhatItSaysAndLosesOnlyItsLicenceCommentAndGitHubAlertSyntax(t *testing.T) {
	in := "<!-- Copyright 2026 The Taisce Authors -->\n<!-- SPDX-License-Identifier: Apache-2.0 -->\n\n# Title\n\n" +
		"> [!WARNING]\n> Mind the allowlist.\n> It is host and port.\n\nText.\n\n```\n> [!NOTE]\n```\n\n> an ordinary quote\n"
	got := forRenderer(in)
	for _, want := range []string{"# Title", ":::warning\n\nMind the allowlist.\nIt is host and port.\n\n:::", "```\n> [!NOTE]\n```", "> an ordinary quote"} {
		if !strings.Contains(got, want) {
			t.Errorf("want %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Copyright") {
		t.Errorf("the licence comment survived:\n%s", got)
	}
}

func TestLinksResolveToPagesPackagesOrSource(t *testing.T) {
	root := repoRoot(t)
	opts := Options{Root: root, Repo: "o/r", Ref: "abc"}
	p := page{Repo: "docs/architecture/x.md", Stage: "architecture/x.md", Body: strings.Join([]string{
		"[pkg](../../internal/formation/)",
		"[file](../../Makefile#L3)",
		"[dir](../../deploy/)",
		"[doc](../12-recall-controls.md#budgets)",
		"[web](https://ensera.ai)",
		"[here](#local)",
		"[ref]: ../07-operational-health.md",
	}, "\n")}
	targets := map[string]string{"docs/12-recall-controls.md": "12-recall-controls.md", "docs/07-operational-health.md": "07-operational-health.md",
		"internal/formation": packagePage("internal/formation")}
	body, broken := rewriteLinks(p, targets, opts)
	if len(broken) > 0 {
		t.Fatalf("unexpected broken links: %v", broken)
	}
	for _, want := range []string{
		"[pkg](../reference/code/internal/formation.md)",
		"[file](https://github.com/o/r/blob/abc/Makefile#L3)",
		"[dir](https://github.com/o/r/tree/abc/deploy)",
		"[doc](../12-recall-controls.md#budgets)",
		"[web](https://ensera.ai)",
		"[here](#local)",
		"[ref]: ../07-operational-health.md",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("want %s in:\n%s", want, body)
		}
	}
	// A code span that wraps onto the next line does not hide the link after it closes, and a link
	// inside a span is left alone however many lines the span takes.
	wrapped := page{Repo: "docs/a.md", Stage: "a.md", Body: "see `group\nordinals` ([m](../Makefile)) and ``x\n[not](y.md) x``\n\n`[also not](z.md)`"}
	body, broken = rewriteLinks(wrapped, targets, opts)
	if len(broken) > 0 || !strings.Contains(body, "([m](https://github.com/o/r/blob/abc/Makefile))") || !strings.Contains(body, "[not](y.md)") {
		t.Errorf("a wrapped code span was mishandled: %v\n%s", broken, body)
	}
	generated := page{Stage: "reference/x.md", Body: "[a](b.md)"}
	if body, broken := rewriteLinks(generated, targets, opts); body != "[a](b.md)" || len(broken) > 0 {
		t.Errorf("a generated page's staged link was rewritten: %s %v", body, broken)
	}
	if got := relative("reference/code/internal/x/b.md", "reference/code/internal/x/a.md#T"); got != "a.md#T" {
		t.Errorf("relative within a directory = %s", got)
	}
	if got := relative("introduction.md", "architecture/overview.md"); got != "architecture/overview.md" {
		t.Errorf("relative from the root = %s", got)
	}
}

func TestATestNameReadsBackAsTheSentenceItWasWrittenFrom(t *testing.T) {
	for name, want := range map[string]string{
		"TestATurnThatWillNotFormIsParked":             "A turn that will not form is parked",
		"TestMCPListsExactlyTheSixToolsAndNoneDeletes": "MCP lists exactly the six tools and none deletes",
		"TestMigration0023ValidatesRows_UnderLoad":     "Migration 0023 validates rows — under load",
		"Test_": "Test_",
	} {
		if got := humanize(name); got != want {
			t.Errorf("humanize(%s) = %q, want %q", name, got, want)
		}
	}
	for name, want := range map[string]bool{"TestX": true, "Test_x": true, "Testing": false, "Test": false, "Helper": false} {
		if isTestName(name) != want {
			t.Errorf("isTestName(%s) = %v", name, !want)
		}
	}
}

func TestAMigrationHeaderRendersAsProseAndItsStatementsSkipFunctionBodies(t *testing.T) {
	sql := strings.Join([]string{
		"-- Copyright 2026 The Taisce Authors",
		"-- ── WHY THIS EXISTS ──────────",
		"--",
		"--  Because a reason is written down.",
		"--      indented   layout",
		"-- ═══════════",
		"CREATE TABLE {schema}.a (x int); -- trailing",
		"CREATE FUNCTION {schema}.f() RETURNS trigger AS $body$ BEGIN UPDATE t SET x = 1; RETURN NEW; END $body$ LANGUAGE plpgsql;",
		"GRANT SELECT ON {schema}.a TO somebody;",
		"CREATE TABLE {schema}.a (x int);",
	}, "\n")
	header := commentHeader(sql, "--")
	for _, want := range []string{"### Why this exists", "Because a reason is written down.", "```text\nindented   layout\n```"} {
		if !strings.Contains(header, want) {
			t.Errorf("header lacks %q:\n%s", want, header)
		}
	}
	if strings.Contains(header, "Copyright") || strings.Contains(header, "═") {
		t.Errorf("the licence line or a rule survived:\n%s", header)
	}
	got := strings.Join(statements(sql), "\n")
	for _, want := range []string{"CREATE TABLE a", "CREATE FUNCTION f()", "GRANT SELECT ON a TO somebody"} {
		if !strings.Contains(got, want) {
			t.Errorf("statements lack %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "UPDATE t") {
		t.Errorf("a function body was listed as a migration step:\n%s", got)
	}
	if strings.Count(got, "CREATE TABLE a") != 1 {
		t.Errorf("a repeated statement was listed twice:\n%s", got)
	}
	if stripBodies("x $q$ never closed") != "x " {
		t.Error("an unterminated body leaked into the statement list")
	}
	if headingCase("Already in case") != "Already in case" || plain("# h\n```\ncode\n```\ntext") != "text" {
		t.Error("heading case or plain text rendering changed")
	}
}

func TestAMakeTargetIsDocumentedByTheCommentDirectlyAboveIt(t *testing.T) {
	targets := makeTargets("# Explains a.\na: b\n\trecipe\n\n# About the variable.\nVAR ?= 1\nb:\n.PHONY: a b\n")
	if len(targets) != 2 || targets[0].name != "a" || !strings.Contains(targets[0].doc, "Explains a.") {
		t.Fatalf("got %+v", targets)
	}
	if targets[1].name != "b" || targets[1].doc != "" {
		t.Errorf("a comment separated by an assignment was attached to b: %+v", targets[1])
	}
}

func TestTheReferenceRendersDocLinksAndLongDeclarations(t *testing.T) {
	p := goPackage{Dir: "internal/x", symbols: map[string]string{"Thing": "reference/code/internal/x/a.md#Thing", "Thing.Do": "reference/code/internal/x/a.md#Thing.Do"}}
	got := renderDoc("See [Thing], [Thing.Do], [io.Reader] and [pg.Schema].", p, "reference/code/internal/x/b.md", 4)
	for _, want := range []string{"[Thing](a.md#Thing)", "[Thing.Do](a.md#Thing.Do)", "(https://pkg.go.dev/io#Reader)"} {
		if !strings.Contains(got, want) {
			t.Errorf("doc links: want %s in %q", want, got)
		}
	}
	long := strings.Repeat("\t// a field\n", 30)
	f := goFile{Name: "a.go", Decls: []goDecl{{Kind: "type", Name: "Big", ID: "Big", Code: "type Big struct {\n" + long + "}", Line: 3}}}
	body := fileBody(p, f, nil, Options{Repo: "o/r", Ref: "m"})
	if !strings.Contains(body, "<details><summary>32 lines of declaration</summary>") {
		t.Errorf("a long declaration was not folded:\n%s", body)
	}
	if fenced("a ``` b") == "a ``` b" {
		t.Error("a fence inside a declaration was left able to close its block")
	}
}

func TestWritingTheSiteIntoAnUnwritablePlaceIsRefused(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	write(t, filepath.Dir(blocker), "file", "not a directory")
	pages := []page{{Stage: "a.md", Body: "# A"}}
	if err := writeSite(Options{Out: filepath.Join(blocker, "docs"), Sidebars: "s"}, pages, nil); err == nil {
		t.Error("a document directory under a file was accepted")
	}
	out := filepath.Join(t.TempDir(), "docs")
	if err := writeSite(Options{Out: out, Sidebars: filepath.Join(blocker, "s.json")}, pages, nil); err == nil {
		t.Error("a sidebar file under a file was accepted")
	}
}
