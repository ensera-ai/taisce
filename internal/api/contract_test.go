// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

// The contract document, and the vocabulary it publishes.
package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// The contract renders the same way every time, and publishes every error code a refusal can name.
//
// The rendered document is compared with the committed one by a test kept in the development
// repository with the document (contractdoc_test.go). What every tree holds is this: rendering is
// deterministic, so regenerating the document shows what changed and nothing else, and a client
// reading it finds every code it can be refused with.
func TestTheRenderedContractIsStableAndPublishesEveryErrorCode(t *testing.T) {
	first, second := Contract(), Contract()
	if first != second {
		t.Fatalf("the contract renders differently on a second call; first difference at byte %d",
			firstDifference(first, second))
	}
	for _, code := range ErrorCodes() {
		if !strings.Contains(first, code) {
			t.Errorf("the contract does not publish the error code %q", code)
		}
	}
}

func firstDifference(a, b string) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return min(len(a), len(b))
}

// No refusal names a code that is not in the published vocabulary.
//
// Read from the source rather than from the refusals a test happens to trigger, because the gap is
// the path nothing exercises: a handler added under pressure, refusing with a code invented on the
// spot, on a branch that first runs in production. A client branching on codes falls through to a
// default it did not choose, and nothing here would have said so.
//
// What it does not cover: a code assembled at runtime. Nothing does that and this refuses the
// literal that would be the first step towards it.
func TestNoRefusalNamesAnUndeclaredCode(t *testing.T) {
	declared := map[string]bool{}
	for _, c := range ErrorCodes() {
		declared[c] = true
	}

	fset := token.NewFileSet()
	pkg, err := parser.ParseDir(fset, ".", nil, 0)
	if err != nil {
		t.Fatalf("parse the package: %v", err)
	}

	calls := 0
	for _, p := range pkg {
		for path, file := range p.Files {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				ident, ok := call.Fun.(*ast.Ident)
				if !ok || ident.Name != "writeError" || len(call.Args) < 3 {
					return true
				}
				calls++
				where := fset.Position(call.Pos())
				name, ok := call.Args[2].(*ast.Ident)
				if !ok {
					t.Errorf("%s: a refusal names a code that is not one of the declared "+
						"constants; a client cannot branch on what is not published", where)
					return true
				}
				value, ok := codeConstants(p)[name.Name]
				if !ok {
					t.Errorf("%s: a refusal names %q, which is not a declared error code",
						where, name.Name)
					return true
				}
				if !declared[value] {
					t.Errorf("%s: a refusal answers %q, which the contract does not publish",
						where, value)
				}
				return true
			})
		}
	}
	if calls == 0 {
		t.Fatal("no refusal was found at all, so this test asserted nothing")
	}
}

// codeConstants maps the name of each declared errorCode constant to the string a client sees.
func codeConstants(p *ast.Package) map[string]string {
	out := map[string]string{}
	for _, file := range p.Files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				if id, ok := vs.Type.(*ast.Ident); !ok || id.Name != "errorCode" {
					continue
				}
				for i, name := range vs.Names {
					if i >= len(vs.Values) {
						continue
					}
					lit, ok := vs.Values[i].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					out[name.Name] = strings.Trim(lit.Value, `"`)
				}
			}
		}
	}
	return out
}
