// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package docsite

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/doc/comment"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// module is the import path prefix that marks a package as this repository's own.
const module = "github.com/ensera-ai/taisce/"

type goPackage struct {
	Dir, Name, Doc string
	Files          []goFile
	Tests          []goTest
	Imports        []string
	ImportedBy     []string
	Data           []string
	symbols        map[string]string // symbol → anchor, for doc links
}

type goFile struct {
	Name, Doc, Constraint string
	Lines                 int
	CarriesPackageDoc     bool
	Decls                 []goDecl
}

type goDecl struct {
	Kind, Name, ID, Code, Doc string
	Line                      int
}

type goTest struct {
	File, Name, Doc string
	Line            int
}

// loadPackages finds every Go package under cmd/ and internal/ and reads it. A package with no doc
// comment is refused: that comment is where a package says what it may
// decide and what it must not, and nothing else in the code says so.
func loadPackages(root string) ([]goPackage, error) {
	var dirs []string
	for _, top := range []string{"cmd", "internal"} {
		if _, err := os.Stat(filepath.Join(root, top)); os.IsNotExist(err) {
			continue
		}
		err := filepath.WalkDir(filepath.Join(root, top), func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			rel = filepath.ToSlash(rel)
			if name := d.Name(); name == "testdata" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
				return filepath.SkipDir
			}
			dirs = append(dirs, rel)
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("docsite: walk %s: %w", top, err)
		}
	}
	var packages []goPackage
	var undocumented []string
	for _, dir := range dirs {
		p, ok, err := loadPackage(root, dir)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		if p.Doc == "" {
			undocumented = append(undocumented, dir)
		}
		packages = append(packages, p)
	}
	if len(undocumented) > 0 {
		return nil, fmt.Errorf("docsite: %d package(s) have no doc comment saying what they are for:\n  %s",
			len(undocumented), strings.Join(undocumented, "\n  "))
	}
	byDir := map[string]*goPackage{}
	for i := range packages {
		byDir[packages[i].Dir] = &packages[i]
	}
	for i := range packages {
		for _, imported := range packages[i].Imports {
			if target, ok := byDir[imported]; ok {
				target.ImportedBy = append(target.ImportedBy, packages[i].Dir)
			}
		}
	}
	return packages, nil
}

// loadPackage reads one directory. ok is false when it holds no non-test Go file.
func loadPackage(root, dir string) (goPackage, bool, error) {
	entries, err := os.ReadDir(filepath.Join(root, dir))
	if err != nil {
		return goPackage{}, false, fmt.Errorf("docsite: %w", err)
	}
	p := goPackage{Dir: dir, symbols: map[string]string{}}
	fset := token.NewFileSet()
	imports := map[string]bool{}
	var testFiles []*ast.File
	var testNames []string
	for _, e := range entries {
		name := e.Name()
		full := filepath.Join(root, dir, name)
		switch {
		case e.IsDir():
			p.Data = append(p.Data, dataFiles(root, dir, name)...)
		case strings.HasSuffix(name, "_test.go"):
			f, err := parser.ParseFile(fset, full, nil, parser.ParseComments)
			if err != nil {
				return goPackage{}, false, fmt.Errorf("docsite: parse %s/%s: %w", dir, name, err)
			}
			testFiles = append(testFiles, f)
			testNames = append(testNames, name)
		case strings.HasSuffix(name, ".go"):
			src, err := os.ReadFile(full)
			if err != nil {
				return goPackage{}, false, fmt.Errorf("docsite: %w", err)
			}
			f, err := parser.ParseFile(fset, full, src, parser.ParseComments)
			if err != nil {
				return goPackage{}, false, fmt.Errorf("docsite: parse %s/%s: %w", dir, name, err)
			}
			p.Name = f.Name.Name
			if doc := packageDoc(f); doc != "" && p.Doc == "" {
				p.Doc = doc
			}
			for _, spec := range f.Imports {
				if ip := strings.Trim(spec.Path.Value, `"`); strings.HasPrefix(ip, module) {
					imports[strings.TrimPrefix(ip, module)] = true
				}
			}
			p.Files = append(p.Files, readFile(fset, f, name, src))
		default:
			p.Data = append(p.Data, path.Join(dir, name))
		}
	}
	if len(p.Files) == 0 {
		return goPackage{}, false, nil
	}
	for ip := range imports {
		p.Imports = append(p.Imports, ip)
	}
	sort.Strings(p.Imports)
	for i, f := range testFiles {
		p.Tests = append(p.Tests, readTests(fset, f, testNames[i])...)
	}
	for _, f := range p.Files {
		for _, d := range f.Decls {
			p.symbols[d.Name] = fileStage(dir, f.Name) + "#" + d.ID
		}
	}
	return p, true, nil
}

// dataFiles lists the non-Go files a package carries in a subdirectory that is not itself a package:
// embedded migrations, templates, prompts. Test fixtures are not part of what the package is.
func dataFiles(root, dir, sub string) []string {
	if sub == "testdata" {
		return nil
	}
	var out []string
	_ = filepath.WalkDir(filepath.Join(root, dir, sub), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.HasSuffix(p, ".go") {
			out = nil // a Go package of its own, documented on its own page
			return filepath.SkipAll
		}
		rel, _ := filepath.Rel(root, p)
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	return out
}

// packageDoc is a file's package documentation. The parser takes whatever comment sits directly on
// the package clause, and in a file with no documentation of its own that is the licence header; it
// is stripped, so a header is never mistaken for a package saying what it is for.
func packageDoc(f *ast.File) string {
	if f.Doc == nil {
		return ""
	}
	lines := strings.Split(f.Doc.Text(), "\n")
	for len(lines) > 0 {
		first := strings.TrimSpace(lines[0])
		if first != "" && !strings.HasPrefix(first, "Copyright") && !strings.HasPrefix(first, "SPDX-License-Identifier") {
			break
		}
		lines = lines[1:]
	}
	doc := strings.TrimSpace(strings.Join(lines, "\n"))
	if doc == "" {
		return ""
	}
	return doc + "\n"
}

func readFile(fset *token.FileSet, f *ast.File, name string, src []byte) goFile {
	file := goFile{Name: name, Lines: bytes.Count(src, []byte("\n")), CarriesPackageDoc: packageDoc(f) != ""}
	// A file's own comment is whatever it says above the package clause that is neither the licence
	// header, nor a build constraint, nor the package documentation.
	for _, group := range f.Comments {
		if group.Pos() >= f.Package {
			break
		}
		if group == f.Doc {
			continue
		}
		text := group.Text()
		if strings.HasPrefix(text, "Copyright") {
			continue
		}
		if strings.HasPrefix(group.List[0].Text, "//go:build") {
			file.Constraint = strings.TrimSpace(strings.TrimPrefix(group.List[0].Text, "//go:build"))
			continue
		}
		file.Doc += text
	}
	seen := map[string]int{}
	add := func(kind, name string, node ast.Node, doc *ast.CommentGroup) {
		id := strings.ReplaceAll(name, " ", "-")
		if seen[id]++; seen[id] > 1 {
			id = fmt.Sprintf("%s-%d", id, seen[id])
		}
		file.Decls = append(file.Decls, goDecl{
			Kind: kind, Name: name, ID: id,
			Code: printNode(fset, f, node), Doc: doc.Text(),
			Line: fset.Position(node.Pos()).Line,
		})
	}
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			// The signature is the declaration; the body is on the source link, one click away.
			signature := *d
			signature.Body, signature.Doc = nil, nil
			kind, name := "func", d.Name.Name
			if d.Recv != nil && len(d.Recv.List) > 0 {
				kind, name = "method", receiverName(d.Recv.List[0].Type)+"."+name
			}
			add(kind, name, &signature, d.Doc)
		case *ast.GenDecl:
			if d.Tok == token.IMPORT {
				continue
			}
			bare := *d
			bare.Doc = nil
			if d.Tok == token.TYPE {
				for _, spec := range d.Specs {
					ts := spec.(*ast.TypeSpec)
					doc := ts.Doc
					if doc == nil && len(d.Specs) == 1 {
						doc = d.Doc
					}
					node := ast.Node(&bare)
					if len(d.Specs) > 1 {
						node = &ast.GenDecl{Tok: token.TYPE, TokPos: ts.Pos(), Specs: []ast.Spec{ts}}
					}
					add("type", ts.Name.Name, node, doc)
				}
				continue
			}
			var names []string
			for _, spec := range d.Specs {
				for _, n := range spec.(*ast.ValueSpec).Names {
					names = append(names, n.Name)
				}
			}
			label := strings.Join(names, ", ")
			if len(names) > 3 {
				label = strings.Join(names[:3], ", ") + fmt.Sprintf(" and %d more", len(names)-3)
			}
			doc := d.Doc
			if doc == nil && len(d.Specs) == 1 {
				doc = d.Specs[0].(*ast.ValueSpec).Doc
			}
			add(d.Tok.String(), label, &bare, doc)
		}
	}
	return file
}

func receiverName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return receiverName(t.X)
	case *ast.IndexExpr:
		return receiverName(t.X)
	case *ast.IndexListExpr:
		return receiverName(t.X)
	case *ast.Ident:
		return t.Name
	}
	return "?"
}

// printNode prints a declaration with the comments inside it, which for a struct are the field
// documentation and for a const block are the reasons each value is what it is.
func printNode(fset *token.FileSet, f *ast.File, node ast.Node) string {
	var b bytes.Buffer
	cfg := printer.Config{Mode: printer.UseSpaces | printer.TabIndent, Tabwidth: 4}
	_ = cfg.Fprint(&b, fset, &printer.CommentedNode{Node: node, Comments: f.Comments})
	return b.String()
}

// readTests lists the tests in one file. The names state the properties the tests hold (rule 6),
// which makes them the most precise description of behaviour the repository has.
func readTests(fset *token.FileSet, f *ast.File, name string) []goTest {
	var out []goTest
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || !isTestName(fn.Name.Name) {
			continue
		}
		out = append(out, goTest{File: name, Name: fn.Name.Name, Doc: firstSentence(fn.Doc.Text()), Line: fset.Position(fn.Pos()).Line})
	}
	return out
}

func isTestName(name string) bool {
	rest, ok := strings.CutPrefix(name, "Test")
	if !ok || rest == "" {
		return false
	}
	r := []rune(rest)[0]
	return r == '_' || unicode.IsUpper(r)
}

// humanize turns a test name back into the sentence it was written from:
// TestATurnThatWillNotFormIsParked becomes "A turn that will not form is parked".
func humanize(name string) string {
	rest := strings.TrimLeft(strings.TrimPrefix(name, "Test"), "_")
	var parts []string
	for _, part := range strings.Split(rest, "_") {
		if part == "" {
			continue
		}
		parts = append(parts, strings.Join(splitWords(part), " "))
	}
	sentence := strings.Join(parts, " — ")
	if sentence == "" {
		return name
	}
	r := []rune(sentence)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

func splitWords(s string) []string {
	r := []rune(s)
	var words []string
	start := 0
	for i := 1; i < len(r); i++ {
		prev, cur := r[i-1], r[i]
		boundary := unicode.IsLower(prev) && unicode.IsUpper(cur) ||
			unicode.IsLetter(prev) && unicode.IsDigit(cur) ||
			unicode.IsDigit(prev) && unicode.IsLetter(cur) ||
			unicode.IsUpper(prev) && unicode.IsUpper(cur) && i+1 < len(r) && unicode.IsLower(r[i+1])
		if boundary {
			words = append(words, string(r[start:i]))
			start = i
		}
	}
	words = append(words, string(r[start:]))
	for i, w := range words {
		// An acronym keeps its case; an ordinary word is lower-cased back to prose.
		if len([]rune(w)) > 1 && strings.ToUpper(w) == w {
			continue
		}
		words[i] = strings.ToLower(w)
	}
	return words
}

func firstSentence(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if i := strings.Index(text, ". "); i >= 0 {
		return text[:i+1]
	}
	return text
}

// ── Rendering ─────────────────────────────────────────────────────────────────────────────────────

type rendered struct {
	pages []page
	nav   string
	count int
}

func packagePage(dir string) string { return "reference/code/" + dir + ".md" }

// fileStage names a file's page after the file with its dot spelt as a hyphen: api.go is api-go.md.
// Dropping the extension instead would give api/api.md, which the renderer takes to be the index of
// the api directory and so the same address as the package page.
func fileStage(dir, file string) string {
	return "reference/code/" + dir + "/" + strings.ReplaceAll(file, ".", "-") + ".md"
}

func renderCode(packages []goPackage, opts Options) rendered {
	var out rendered
	var nav strings.Builder
	nav.WriteString("- [Code](reference/code/overview.md)\n")
	out.pages = append(out.pages, page{Stage: "reference/code/overview.md", Body: codeIndex(packages)})
	for _, p := range packages {
		out.pages = append(out.pages, page{Stage: packagePage(p.Dir), Body: packageBody(p, opts)})
		fmt.Fprintf(&nav, "  - [%s](%s)\n", p.Dir, packagePage(p.Dir))
		for _, f := range p.Files {
			out.pages = append(out.pages, page{Stage: fileStage(p.Dir, f.Name), Body: fileBody(p, f, packages, opts)})
			fmt.Fprintf(&nav, "    - [%s](%s)\n", f.Name, fileStage(p.Dir, f.Name))
		}
		if len(p.Tests) > 0 {
			stage := "reference/code/" + p.Dir + "/tests.md"
			out.pages = append(out.pages, page{Stage: stage, Body: testsBody(p, opts)})
			fmt.Fprintf(&nav, "    - [Tests](%s)\n", stage)
		}
	}
	out.nav = nav.String()
	return out
}

func codeIndex(packages []goPackage) string {
	var b strings.Builder
	b.WriteString("# Code reference\n\n")
	b.WriteString("Every package, file and declaration in the service, generated from the source at the " +
		"commit this site was built from. Each package page opens with the package's own statement of " +
		"what it is allowed to decide; each file page lists every declaration with its documentation " +
		"and a link to the source; each tests page lists the properties the suite holds, in the words " +
		"the tests are named with.\n\n")
	b.WriteString("Nothing on these pages is written by hand. A page that says too little is a doc comment " +
		"that says too little, and the fix belongs next to the code.\n\n")
	b.WriteString("## How the packages depend on each other\n\n")
	b.WriteString("An arrow points from a package to one it imports. Only this repository's packages are shown.\n\n")
	b.WriteString("```mermaid\nflowchart TB\n")
	for _, p := range packages {
		fmt.Fprintf(&b, "  %s[\"%s\"]\n", nodeID(p.Dir), p.Dir)
	}
	for _, p := range packages {
		for _, imported := range p.Imports {
			fmt.Fprintf(&b, "  %s --> %s\n", nodeID(p.Dir), nodeID(imported))
		}
	}
	b.WriteString("```\n\n## Packages\n\n| Package | Files | Lines | Tests | What it is for |\n|---|---:|---:|---:|---|\n")
	for _, p := range packages {
		lines := 0
		for _, f := range p.Files {
			lines += f.Lines
		}
		fmt.Fprintf(&b, "| [%s](%s) | %d | %d | %d | %s |\n", p.Dir, relative("reference/code/overview.md", packagePage(p.Dir)),
			len(p.Files), lines, len(p.Tests), cell(firstSentence(p.Doc)))
	}
	return b.String()
}

func nodeID(dir string) string { return strings.NewReplacer("/", "_", "-", "_").Replace(dir) }

func packageBody(p goPackage, opts Options) string {
	stage := packagePage(p.Dir)
	var b strings.Builder
	lines := 0
	for _, f := range p.Files {
		lines += f.Lines
	}
	fmt.Fprintf(&b, "# %s\n\n`%s%s` · %d files · %d lines · %d tests · [source](%s)\n\n",
		p.Dir, module, p.Dir, len(p.Files), lines, len(p.Tests), sourceURL(opts, "tree", p.Dir))
	b.WriteString(renderDoc(p.Doc, p, stage, 2))
	b.WriteString("\n## Where it sits\n\n")
	b.WriteString("**Imports:** " + packageList(p.Imports, stage) + "\n\n")
	b.WriteString("**Imported by:** " + packageList(p.ImportedBy, stage) + "\n\n")
	b.WriteString("## Files\n\n| File | Lines | Declarations | What it is for |\n|---|---:|---:|---|\n")
	for _, f := range p.Files {
		fmt.Fprintf(&b, "| [%s](%s) | %d | %d | %s |\n", f.Name, relative(stage, fileStage(p.Dir, f.Name)),
			f.Lines, len(f.Decls), cell(fileSummary(f)))
	}
	if len(p.Tests) > 0 {
		fmt.Fprintf(&b, "\n%d tests hold this package's behaviour; [the list](%s) is named for what each one proves.\n",
			len(p.Tests), relative(stage, "reference/code/"+p.Dir+"/tests.md"))
	}
	if len(p.Data) > 0 {
		b.WriteString("\n## Files it carries\n\nNot Go, and part of what the package is: embedded, rendered or read at build time.\n\n")
		for _, d := range p.Data {
			fmt.Fprintf(&b, "- [`%s`](%s)\n", strings.TrimPrefix(d, p.Dir+"/"), sourceURL(opts, "blob", d))
		}
	}
	return b.String()
}

func packageList(dirs []string, from string) string {
	if len(dirs) == 0 {
		return "none in this repository"
	}
	sort.Strings(dirs)
	var links []string
	for _, d := range dirs {
		links = append(links, fmt.Sprintf("[%s](%s)", d, relative(from, packagePage(d))))
	}
	return strings.Join(links, ", ")
}

func fileSummary(f goFile) string {
	switch {
	case f.Doc != "":
		return firstSentence(f.Doc)
	case f.CarriesPackageDoc:
		return "Carries the package documentation."
	}
	for _, d := range f.Decls {
		if d.Doc != "" {
			return firstSentence(d.Doc)
		}
	}
	return ""
}

func fileBody(p goPackage, f goFile, packages []goPackage, opts Options) string {
	stage := fileStage(p.Dir, f.Name)
	repoPath := p.Dir + "/" + f.Name
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n[%s](%s) · %d lines · %d declarations · [source](%s)\n\n",
		repoPath, p.Dir, relative(stage, packagePage(p.Dir)), f.Lines, len(f.Decls), sourceURL(opts, "blob", repoPath))
	if f.Constraint != "" {
		fmt.Fprintf(&b, "> Built only when `%s` holds.\n\n", f.Constraint)
	}
	if f.Doc != "" {
		b.WriteString(renderDoc(f.Doc, p, stage, 2) + "\n")
	}
	if f.CarriesPackageDoc {
		fmt.Fprintf(&b, "This file carries the package documentation, rendered on [the package page](%s).\n\n",
			relative(stage, packagePage(p.Dir)))
	}
	if len(f.Decls) == 0 {
		return b.String()
	}
	b.WriteString("## Declarations\n\n")
	for _, d := range f.Decls {
		fmt.Fprintf(&b, "### %s `%s` {#%s}\n\n", d.Kind, d.Name, d.ID)
		code := strings.TrimRight(d.Code, "\n")
		if strings.Count(code, "\n") >= 25 {
			fmt.Fprintf(&b, "<details><summary>%d lines of declaration</summary>\n\n```go\n%s\n```\n\n</details>\n\n",
				strings.Count(code, "\n")+1, fenced(code))
		} else {
			fmt.Fprintf(&b, "```go\n%s\n```\n\n", fenced(code))
		}
		if d.Doc != "" {
			b.WriteString(renderDoc(d.Doc, p, stage, 4) + "\n")
		}
		fmt.Fprintf(&b, "[source](%s#L%d)\n\n", sourceURL(opts, "blob", repoPath), d.Line)
	}
	return b.String()
}

func testsBody(p goPackage, opts Options) string {
	var b strings.Builder
	stage := "reference/code/" + p.Dir + "/tests.md"
	fmt.Fprintf(&b, "# Tests: %s\n\n[%s](%s) · %d tests\n\n", p.Dir, p.Dir, relative(stage, packagePage(p.Dir)), len(p.Tests))
	b.WriteString("Each test is named for the property it holds, and runs against a real deployment: " +
		"there is no mock of the database and no arm that skips when it is absent. " +
		"The sentence is the test's name read back; the name is what `go test -run` takes.\n")
	current := ""
	for _, t := range p.Tests {
		if t.File != current {
			current = t.File
			fmt.Fprintf(&b, "\n## %s\n\n", t.File)
		}
		fmt.Fprintf(&b, "- **%s** — [`%s`](%s#L%d)", cell(humanize(t.Name)), t.Name,
			sourceURL(opts, "blob", p.Dir+"/"+t.File), t.Line)
		if t.Doc != "" && !strings.HasPrefix(t.Doc, t.Name) {
			b.WriteString(". " + cell(t.Doc))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// renderDoc renders a doc comment as markdown. A reference to a symbol of this package, or of another
// package in the repository, links to its declaration; a standard library reference links to its
// documentation; anything else stays text.
func renderDoc(text string, p goPackage, from string, level int) string {
	parser := comment.Parser{
		LookupSym: func(recv, name string) bool {
			if recv != "" {
				name = recv + "." + name
			}
			_, ok := p.symbols[name]
			return ok
		},
	}
	printer := comment.Printer{
		HeadingLevel: level,
		HeadingID:    func(*comment.Heading) string { return "" },
		DocLinkURL: func(link *comment.DocLink) string {
			name := link.Name
			if link.Recv != "" {
				name = link.Recv + "." + name
			}
			switch {
			case link.ImportPath == "":
				if anchor, ok := p.symbols[name]; ok {
					return relative(from, anchor)
				}
			case strings.HasPrefix(link.ImportPath, module):
				return ""
			case !strings.Contains(link.ImportPath, "."):
				u := "https://pkg.go.dev/" + link.ImportPath
				if name != "" {
					u += "#" + name
				}
				return u
			}
			return ""
		},
	}
	return string(printer.Markdown(parser.Parse(text)))
}

// cell makes text safe inside a table cell or a single list item.
func cell(s string) string {
	return strings.NewReplacer("|", `\|`, "\n", " ").Replace(s)
}

// fenced keeps a code block from being closed early by a declaration that itself contains a fence.
func fenced(code string) string { return strings.ReplaceAll(code, "```", "`​``") }
