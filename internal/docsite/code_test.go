// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package docsite

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// TestReadingAFileKeepsItsConstraintItsOwnCommentAndEveryDeclarationApart holds the parts of reading a
// Go file that the repository's own sources happen not to exercise.
//
// The site shows each file's build constraint, the comment a file carries about itself, and one
// anchor per declaration. Each has a way to go quietly wrong: a constraint read as prose, the licence
// header or the package documentation shown as the file's comment, two declarations of one name
// sharing an anchor so a link lands on the wrong one, and a grouped type block rendered whole under
// each of its names. The file below does all four.
func TestReadingAFileKeepsItsConstraintItsOwnCommentAndEveryDeclarationApart(t *testing.T) {
	src := []byte(`// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

//go:build ignore

// This file explains itself.

// Package probe is documented here.
package probe

func init() {}

func init() {}

type (
	First  int
	Second string
)
`)
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "probe.go", src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	got := readFile(fset, f, "probe.go", src)

	if got.Constraint != "ignore" {
		t.Errorf("the build constraint was read as %q", got.Constraint)
	}
	if !strings.Contains(got.Doc, "This file explains itself.") || strings.Contains(got.Doc, "Copyright") || strings.Contains(got.Doc, "Package probe") {
		t.Errorf("the file's own comment was read as %q", got.Doc)
	}
	if !got.CarriesPackageDoc {
		t.Error("the package documentation was not noticed")
	}
	ids := map[string]string{}
	for _, d := range got.Decls {
		ids[d.ID] = d.Code
	}
	if _, ok := ids["init"]; !ok {
		t.Errorf("the first init has no anchor: %v", ids)
	}
	if _, ok := ids["init-2"]; !ok {
		t.Errorf("the second init does not have an anchor of its own: %v", ids)
	}
	if first, second := ids["First"], ids["Second"]; strings.Contains(first, "Second") || strings.Contains(second, "First") {
		t.Errorf("a grouped type block was shown whole under each name: First=%q Second=%q", first, second)
	}
}
