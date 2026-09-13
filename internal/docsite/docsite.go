// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

// Package docsite assembles the source of the documentation site: the written documents where they
// stand, and a reference generated from the repository and from a freshly migrated database.
//
// # Why the reference is generated
//
// A hand-written description of every declaration is a second copy of the code, and it is wrong the
// first time somebody edits a function without knowing the page exists. Nothing fails when that
// happens, so nobody notices. Generating the reference from the source means it is as current as the
// commit it was built from. It also means it is only as good as the doc comments it renders, which
// is the right pressure: the comment next to the code is the one a reviewer sees.
//
// The schema reference is introspected from a database the migrations have just built rather than
// parsed from their text. The SQL says what was asked for; the catalog says what PostgreSQL made of
// it after sixty-odd migrations altered one another, and that is what a reader needs.
//
// # What this package is allowed to decide
//
// Page layout, link resolution, the navigation, and what the build refuses. It does not decide what
// a written document says: those are in docs/ and are rendered as they are. It does not render
// anything either; the renderer in site/ does, and this package hands it markdown and a sidebar
// definition. It never writes into the repository's docs/. It writes only the directory and the file
// it is given, and the only database it creates is a scratch database, which it drops.
//
// # What the build refuses
//
//   - A package with no doc comment, because that comment is where a package says what it may
//     decide and what it must not.
//   - A link that names nothing, because a reader who follows one stops trusting the rest.
//   - A document in docs/ that the navigation neither lists nor keeps in the repository, because a
//     page nobody can reach looks published and is not, and a record written for contributors should
//     not reach the site by being forgotten.
//   - A document both listed and kept, or a kept name that is no document, because either means the
//     navigation no longer says what the site shows.
//
// The renderer then refuses a link or an anchor that names nothing on the rendered site. A build that
// published with any of these would be a site that looks complete and is not.
package docsite

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Options says where the repository is, where the site's source goes, and what source links point at.
type Options struct {
	// Root is the repository checkout.
	Root string
	// Out is the renderer's document directory. It is replaced on every build, so it must be a
	// directory of its own: never the repository, never an ancestor of it, never inside docs/.
	Out string
	// Sidebars is the file the navigation is written to, in the renderer's sidebar format.
	Sidebars string
	// DSN names a disposable PostgreSQL substrate. The build creates a scratch database there,
	// migrates and inspects it, and drops it. Bootstrapping resets the instance roles' passwords the
	// way the test suite does, so this must never be a deployment's database.
	DSN string
	// Repo and Ref are where source links point: "owner/name" and a commit or branch.
	Repo, Ref string
}

// Report counts what the build covered, so a caller can say it rather than claim it.
type Report struct {
	Documents, Packages, Files, Declarations, Tests, Migrations, Tables int
}

// page is one staged markdown file. Repo is its path in the repository ("" when generated), Stage is
// its path under the document directory.
type page struct {
	Repo, Stage string
	Body        string
}

// summaryMarker is where the generated reference is spliced into docs/SUMMARY.md.
const summaryMarker = "<!-- generated reference -->"

// Build writes the site's source and returns what it covered.
func Build(ctx context.Context, opts Options) (Report, error) {
	var report Report
	if err := opts.validate(); err != nil {
		return report, err
	}
	docs, summary, err := stageDocuments(opts.Root)
	if err != nil {
		return report, err
	}
	// A kept document is not staged, so a link to it from a published page resolves to its source on
	// GitHub, the way a link to any other file in the repository does.
	docs, err = publishedDocuments(summary, docs)
	if err != nil {
		return report, err
	}
	report.Documents = len(docs)

	packages, err := loadPackages(opts.Root)
	if err != nil {
		return report, err
	}
	code := renderCode(packages, opts)
	for _, p := range packages {
		report.Packages++
		report.Files += len(p.Files)
		report.Tests += len(p.Tests)
		for _, f := range p.Files {
			report.Declarations += len(f.Decls)
		}
	}

	migrations, err := renderMigrations(opts)
	if err != nil {
		return report, err
	}
	tooling, err := renderTooling(opts)
	if err != nil {
		return report, err
	}

	// Links are resolved, and the navigation checked, before the database is touched: a refusal that
	// needs no substrate should not wait for one.
	pages := append(append(append(docs, code.pages...), migrations.pages...), tooling)
	pages = append(pages, page{Repo: "docs/SUMMARY.md", Stage: "SUMMARY.md", Body: summary})
	if err := resolveLinks(pages, opts, packages); err != nil {
		return report, err
	}
	summary, pages = pages[len(pages)-1].Body, pages[:len(pages)-1]
	if err := checkListed(summary, docs); err != nil {
		return report, err
	}

	schema, tables, err := renderSchema(ctx, opts)
	if err != nil {
		return report, err
	}
	pages = append(pages, schema.pages...)
	report.Migrations = migrations.count
	report.Tables = tables

	nav := code.nav + schema.nav + migrations.nav + "- [Tooling](reference/tooling.md)\n"
	summary, err = spliceSummary(summary, nav)
	if err != nil {
		return report, err
	}
	bars, err := sidebars(summary)
	if err != nil {
		return report, err
	}
	for i := range pages {
		pages[i].Body = forRenderer(pages[i].Body)
	}
	if err := writeSite(opts, pages, bars); err != nil {
		return report, err
	}
	return report, nil
}

func (o Options) validate() error {
	switch {
	case o.Root == "":
		return errors.New("docsite: the repository root is required")
	case o.Out == "" || o.Sidebars == "":
		return errors.New("docsite: the document directory and the sidebar file are required")
	case o.DSN == "":
		// No skip-when-absent arm: a site without its schema reference looks complete and is not.
		return errors.New("docsite: a database DSN is required; the schema reference is read from a migrated database")
	case o.Repo == "" || o.Ref == "":
		return errors.New("docsite: the repository and ref that source links point at are required")
	}
	root, err := filepath.Abs(o.Root)
	if err != nil {
		return fmt.Errorf("docsite: %w", err)
	}
	out, err := filepath.Abs(o.Out)
	if err != nil {
		return fmt.Errorf("docsite: %w", err)
	}
	docs := filepath.Join(root, "docs")
	// The document directory is deleted and rewritten, so a mistyped path must not be able to name
	// the repository or the written documents.
	if root == out || strings.HasPrefix(root, out+string(filepath.Separator)) {
		return fmt.Errorf("docsite: the document directory %s would contain the repository, and it is replaced on every build", o.Out)
	}
	if out == docs || strings.HasPrefix(out, docs+string(filepath.Separator)) {
		return fmt.Errorf("docsite: the document directory %s is inside docs/, which the build must never write", o.Out)
	}
	return nil
}

// stageDocuments reads every markdown file under docs/ and returns the navigation file
// separately. The repository README is not a page: it is the repository's front door on GitHub, and
// the site has its own.
func stageDocuments(root string) ([]page, string, error) {
	var pages []page
	var summary string
	base := filepath.Join(root, "docs")
	err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(base, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() || !strings.HasSuffix(p, ".md") {
			return nil
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if rel == "SUMMARY.md" {
			summary = string(body)
			return nil
		}
		pages = append(pages, page{Repo: "docs/" + rel, Stage: rel, Body: string(body)})
		return nil
	})
	if err != nil {
		return nil, "", fmt.Errorf("docsite: read docs: %w", err)
	}
	if summary == "" {
		return nil, "", errors.New("docsite: docs/SUMMARY.md, the site navigation, is missing or empty")
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i].Stage < pages[j].Stage })
	return pages, summary, nil
}

func spliceSummary(summary, nav string) (string, error) {
	if !strings.Contains(summary, summaryMarker) {
		return "", fmt.Errorf("docsite: docs/SUMMARY.md has no %q line to place the reference at", summaryMarker)
	}
	return strings.Replace(summary, summaryMarker, strings.TrimRight(nav, "\n"), 1), nil
}

// checkListed refuses a published document the navigation does not reach.
func checkListed(summary string, docs []page) error {
	var missing []string
	for _, d := range docs {
		if !strings.Contains(summary, "]("+d.Stage+")") {
			missing = append(missing, d.Repo)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("docsite: %d document(s) are not in docs/SUMMARY.md, so the site would not show them; list them there, or name them in its kept list:\n  %s",
			len(missing), strings.Join(missing, "\n  "))
	}
	return nil
}

// keptMarker opens the comment in docs/SUMMARY.md naming the documents that stay in the repository:
// records for the people building Taisce, such as the decision register and dated measurement runs,
// rather than documentation for the people using it. The list is a comment so that GitHub, where the
// same file reads as a table of contents, shows nothing for it.
//
// A list rather than a directory: the register's path is the one the project's rules and code
// comments cite, and moving files to change what a website shows would break every one of those.
const keptMarker = "<!-- kept in the repository, not on the site"

var keptEntry = regexp.MustCompile(`^-\s+(\S+\.md)$`)

// publishedDocuments removes the kept documents from docs. A document is on the site or kept, never
// both and never neither; checkListed refuses neither, and this refuses both, together with a kept
// name that is no document, because a list naming nothing is how a moved record goes unnoticed. It
// reads the navigation as written, before links are resolved, since a link to a kept document is
// rewritten to its source and would no longer read as listed.
func publishedDocuments(summary string, docs []page) ([]page, error) {
	kept, err := keptDocuments(summary)
	if err != nil {
		return nil, err
	}
	var published []page
	var problems []string
	found := map[string]bool{}
	for _, d := range docs {
		if !kept[d.Stage] {
			published = append(published, d)
			continue
		}
		found[d.Stage] = true
		if strings.Contains(summary, "]("+d.Stage+")") {
			problems = append(problems, d.Repo+" is both listed and kept")
		}
	}
	for name := range kept {
		if !found[name] {
			problems = append(problems, "docs/"+name+" is kept but is not a document")
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return nil, fmt.Errorf("docsite: the kept list in docs/SUMMARY.md disagrees with docs/:\n  %s", strings.Join(problems, "\n  "))
	}
	return published, nil
}

// keptDocuments reads the kept comment: one "- name.md" line per document, up to the comment's close.
func keptDocuments(summary string) (map[string]bool, error) {
	kept := map[string]bool{}
	start := strings.Index(summary, keptMarker)
	if start < 0 {
		return kept, nil
	}
	block, _, closed := strings.Cut(summary[start+len(keptMarker):], "-->")
	if !closed {
		return nil, errors.New("docsite: the kept list in docs/SUMMARY.md is never closed with -->")
	}
	for _, line := range strings.Split(block, "\n") {
		if m := keptEntry.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			kept[m[1]] = true
		}
	}
	return kept, nil
}

var (
	leadingComments = regexp.MustCompile(`^(?:\s*<!--[\s\S]*?-->\s*\n)+`)
	alertStart      = regexp.MustCompile(`^>\s*\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]\s*$`)
	admonition      = map[string]string{"NOTE": "note", "TIP": "tip", "IMPORTANT": "info", "WARNING": "warning", "CAUTION": "danger"}
)

// forRenderer adapts a page written for GitHub to the renderer, without changing what it says: the
// licence comment belongs to the source file rather than the rendered page, and a GitHub alert
// becomes the renderer's admonition of the same kind.
func forRenderer(body string) string {
	body = leadingComments.ReplaceAllString(body, "")
	lines := strings.Split(body, "\n")
	out := make([]string, 0, len(lines))
	inFence := false
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if fence.MatchString(line) {
			inFence = !inFence
		}
		m := alertStart.FindStringSubmatch(line)
		if inFence || m == nil {
			out = append(out, line)
			continue
		}
		out = append(out, ":::"+admonition[m[1]], "")
		for i+1 < len(lines) && strings.HasPrefix(lines[i+1], ">") {
			i++
			out = append(out, strings.TrimPrefix(strings.TrimPrefix(lines[i], ">"), " "))
		}
		out = append(out, "", ":::")
	}
	return strings.Join(out, "\n")
}

// writeSite replaces the document directory with the pages and writes the navigation.
func writeSite(opts Options, pages []page, sidebars []byte) error {
	if err := os.RemoveAll(opts.Out); err != nil {
		return fmt.Errorf("docsite: clear %s: %w", opts.Out, err)
	}
	for _, p := range pages {
		target := filepath.Join(opts.Out, filepath.FromSlash(p.Stage))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("docsite: %w", err)
		}
		if err := os.WriteFile(target, []byte(p.Body), 0o644); err != nil {
			return fmt.Errorf("docsite: %w", err)
		}
	}
	if err := os.WriteFile(opts.Sidebars, sidebars, 0o644); err != nil {
		return fmt.Errorf("docsite: write the navigation: %w", err)
	}
	return nil
}

// relative returns the shortest link from one staged page to another staged path (which may carry
// an anchor).
func relative(fromStage, toStage string) string {
	fromDir := path.Dir(fromStage)
	if fromDir == "." {
		return toStage
	}
	from := strings.Split(fromDir, "/")
	to := strings.Split(toStage, "/")
	common := 0
	for common < len(from) && common < len(to)-1 && from[common] == to[common] {
		common++
	}
	return strings.Repeat("../", len(from)-common) + strings.Join(to[common:], "/")
}
