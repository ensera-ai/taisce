// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package docsite

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// The tooling page describes how the repository is built, tested and released, from the comments the
// Makefile, the scripts and the workflows already carry. Those comments are where the reasons live:
// why the suite runs with -count=1, why the coverage floor sits below the noise.

var makeTarget = regexp.MustCompile(`^([A-Za-z][\w-]*)\s*:([^=]|$)`)

func renderTooling(opts Options) (page, error) {
	var b strings.Builder
	b.WriteString("# Tooling\n\nHow the repository is built, tested, checked and released. Everything here is " +
		"generated from the comments in the Makefile, the scripts and the workflows, which is where the reasons for " +
		"each step are written.\n\n")

	makefile, err := os.ReadFile(filepath.Join(opts.Root, "Makefile"))
	if err != nil {
		return page{}, fmt.Errorf("docsite: read Makefile: %w", err)
	}
	b.WriteString("## Make targets\n\n")
	for _, t := range makeTargets(string(makefile)) {
		fmt.Fprintf(&b, "### `make %s`\n\n", t.name)
		if t.doc != "" {
			b.WriteString(t.doc + "\n")
		} else {
			b.WriteString("No comment explains this target; its recipe is in the [Makefile](" + sourceURL(opts, "blob", "Makefile") + ").\n\n")
		}
	}

	b.WriteString("## Scripts\n\n")
	scripts, _ := filepath.Glob(filepath.Join(opts.Root, "scripts", "*"))
	sort.Strings(scripts)
	var tests []goTest
	fset := token.NewFileSet()
	for _, s := range scripts {
		name := filepath.Base(s)
		rel := "scripts/" + name
		var doc string
		switch {
		case strings.HasSuffix(name, "_test.go"):
			f, err := parser.ParseFile(fset, s, nil, parser.ParseComments)
			if err != nil {
				return page{}, fmt.Errorf("docsite: parse %s: %w", rel, err)
			}
			tests = append(tests, readTests(fset, f, name)...)
			continue
		case strings.HasSuffix(name, ".go"):
			f, err := parser.ParseFile(fset, s, nil, parser.ParseComments|parser.PackageClauseOnly)
			if err != nil {
				return page{}, fmt.Errorf("docsite: parse %s: %w", rel, err)
			}
			doc = strings.TrimSpace(f.Doc.Text()) + "\n"
		default:
			body, err := os.ReadFile(s)
			if err != nil {
				return page{}, fmt.Errorf("docsite: %w", err)
			}
			marker := "#"
			if strings.HasSuffix(name, ".sql") {
				marker = "--"
			}
			doc = commentHeader(string(body), marker)
		}
		fmt.Fprintf(&b, "### [`%s`](%s)\n\n%s\n", rel, sourceURL(opts, "blob", rel), doc)
	}

	b.WriteString("## Continuous integration\n\n")
	workflows, _ := filepath.Glob(filepath.Join(opts.Root, ".github", "workflows", "*.yml"))
	sort.Strings(workflows)
	for _, w := range workflows {
		body, err := os.ReadFile(w)
		if err != nil {
			return page{}, fmt.Errorf("docsite: %w", err)
		}
		rel := ".github/workflows/" + filepath.Base(w)
		fmt.Fprintf(&b, "### [`%s`](%s)\n\n%s\n", rel, sourceURL(opts, "blob", rel), commentHeader(string(body), "#"))
	}

	if len(tests) > 0 {
		b.WriteString("## What the tooling's own tests hold\n\n")
		for _, t := range tests {
			fmt.Fprintf(&b, "- **%s** — [`%s`](%s#L%d)\n", cell(humanize(t.Name)), t.Name, sourceURL(opts, "blob", "scripts/"+t.File), t.Line)
		}
	}
	return page{Stage: "reference/tooling.md", Body: b.String()}, nil
}

type target struct{ name, doc string }

// makeTargets pairs each target with the comment block directly above it. A blank line or an
// assignment between the two means the comment is about something else.
func makeTargets(makefile string) []target {
	var out []target
	var pending []string
	for _, line := range strings.Split(makefile, "\n") {
		switch {
		case strings.HasPrefix(line, "#"):
			pending = append(pending, strings.TrimPrefix(line, "#"))
		case makeTarget.MatchString(line) && !strings.HasPrefix(line, ".PHONY"):
			name := makeTarget.FindStringSubmatch(line)[1]
			doc := ""
			if len(pending) > 0 {
				doc = renderComment(pending)
			}
			out = append(out, target{name, doc})
			pending = nil
		case strings.HasPrefix(line, "\t"):
		default:
			pending = nil
		}
	}
	return out
}
