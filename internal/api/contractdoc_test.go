// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// contractDocument is the published reference for the v1 surface. `make contract` renders it from the
// same code that serves the surface, and CONTRIBUTING.md asks anyone changing an operation's shape to
// run that. These tests are what makes the request more than a request.
const contractDocument = "../../docs/02-contract.md"

// checkContractDocument accepts a document only if it is exactly what the code renders now.
//
// Byte equality rather than a structural comparison, because the document is generated: any
// difference at all means somebody changed the surface and did not regenerate, or edited a generated
// file by hand. Either is worth stopping for, and the byte offset is enough to find which.
func checkContractDocument(published string) error {
	rendered := Contract()
	if rendered == published {
		return nil
	}
	return fmt.Errorf("docs/02-contract.md no longer describes the surface this code serves; they first differ at byte %d. "+
		"Run `make contract` and read the diff before committing it: a field added to a response widens a contract "+
		"other people's code depends on, and the diff is where a reviewer sees that it happened",
		firstDifference(rendered, published))
}

// TestThePublishedContractDescribesTheSurfaceThatServesIt fails when the reference and the code part.
//
// The failure it prevents is not somebody editing the document. It is somebody adding a field to a
// response and shipping it, which changes what external code may rely on without anyone having to
// look at the change as a change to a contract.
func TestThePublishedContractDescribesTheSurfaceThatServesIt(t *testing.T) {
	published, err := os.ReadFile(contractDocument)
	if err != nil {
		t.Fatalf("the contract document is missing, so nothing a reader has describes this surface: %v", err)
	}
	if err := checkContractDocument(string(published)); err != nil {
		t.Fatal(err)
	}
}

// TestTheContractCheckRefusesADocumentThatHasDrifted proves the check above is not vacuous.
//
// A comparison that always passed would look exactly like one that compared the right things, right
// up until the reference was wrong. So the check is fed the three ways a document drifts: the code
// grew and the document did not, the code shrank and the document did not, and a hand edit changed
// one byte in the middle. Each must be refused, and the last must name the byte.
func TestTheContractCheckRefusesADocumentThatHasDrifted(t *testing.T) {
	current := Contract()
	if err := checkContractDocument(current); err != nil {
		t.Fatalf("the rendered contract was refused against itself: %v", err)
	}

	mid := len(current) / 2
	replacement := byte('X')
	if current[mid] == replacement {
		replacement = 'Y'
	}
	edited := current[:mid] + string(replacement) + current[mid+1:]

	for name, drifted := range map[string]string{
		"the code grew and the document did not":   current[:len(current)-len(current)/10],
		"the code shrank and the document did not": current + "\n## An operation this surface no longer serves\n",
		"one byte edited by hand":                  edited,
	} {
		err := checkContractDocument(drifted)
		if err == nil {
			t.Errorf("%s: the drifted document was accepted", name)
			continue
		}
		if !strings.Contains(err.Error(), "make contract") {
			t.Errorf("%s: the refusal does not say how to fix it: %v", name, err)
		}
	}
	if err := checkContractDocument(edited); err == nil || !strings.Contains(err.Error(), fmt.Sprintf("byte %d", mid)) {
		t.Errorf("a one-byte edit at %d was not located: %v", mid, err)
	}
}
