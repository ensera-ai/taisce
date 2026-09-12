// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

// The declared surface, tested from inside the package.
//
// Everything else about this surface is tested over HTTP against a real database, because that is
// what a client sees. These two properties cannot be: they are about what the package refuses to
// serve at all, and a surface that failed them would not start, so there would be nothing to send a
// request to.
package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/domain"
)

// The surface this binary actually serves passes its own check.
//
// init already panics if it does not, so this cannot fail on its own — it fails together with every
// other test in the package, which is a message about a package rather than about a route. This is
// the one that names the property.
func TestTheDeclaredSurfaceIsOneTheLedgerCanAccountFor(t *testing.T) {
	if err := checkOperations(operations); err != nil {
		t.Fatalf("the surface this binary serves is unservable: %v", err)
	}
	if len(operations) == 0 {
		t.Fatal("no operations are declared, so every property asserted over the surface is vacuous")
	}
}

// An operation the ledger cannot name is refused, rather than served unaccounted for.
//
// This is the failure the check exists for: the row is written by the same handler that serves the
// request, and a name the ledger rejects fails that write, is logged, and swallowed — so the
// operation succeeds and the account of it does not exist. Nothing about the request would show it.
func TestAnOperationTheLedgerCannotNameIsRefused(t *testing.T) {
	err := checkOperations([]route{
		{Operation{"summarise", http.MethodPost, "/summaries", http.StatusOK}, nil, nil, nil},
	})
	if err == nil {
		t.Fatal("a surface serving an operation the ledger cannot name was accepted")
	}
	if !strings.Contains(err.Error(), "summarise") {
		t.Fatalf("the refusal does not name the operation: %v", err)
	}
}

// Two routes under one name are refused, because the ledger could not tell them apart afterwards.
func TestTwoRoutesUnderOneNameAreRefused(t *testing.T) {
	err := checkOperations([]route{
		{Operation{domain.AuditRecall, http.MethodPost, "/recalls", http.StatusOK}, nil, nil, nil},
		{Operation{domain.AuditRecall, http.MethodPost, "/questions", http.StatusOK}, nil, nil, nil},
	})
	if err == nil {
		t.Fatal("two routes sharing one operation name were accepted")
	}
	if !strings.Contains(err.Error(), "/questions") {
		t.Fatalf("the refusal does not name the second route: %v", err)
	}
}

// What Operations() hands out is what the mux serves: the version prefix, and nothing a caller could
// mutate. The contract is published from this, so a path that were wrong here would be wrong there.
func TestOperationsReportsThePathsAsSent(t *testing.T) {
	ops := Operations()
	if len(ops) != len(operations) {
		t.Fatalf("Operations() reports %d of %d declared", len(ops), len(operations))
	}
	for _, o := range ops {
		if !strings.HasPrefix(o.Path, "/"+Version+"/") {
			t.Fatalf("operation %q is reported at %q, which is not a path a caller sends", o.Name, o.Path)
		}
	}
	ops[0].Path = "/rewritten"
	if Operations()[0].Path == "/rewritten" {
		t.Fatal("a caller holding the slice can rewrite the surface")
	}
}
