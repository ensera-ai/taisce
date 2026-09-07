// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

//go:build ignore

// Renders the public contract document from the surface that serves it.
//
// Under `ignore` and named explicitly by `make contract`, so it is not part of `go build ./...` and
// does not ship as a command. It is a development tool: the artefact it produces is what is
// published, and a test fails if the two disagree.
package main

import (
	"fmt"
	"os"

	"github.com/ensera-ai/taisce/internal/api"
)

func main() {
	const path = "docs/02-contract.md"
	if err := os.WriteFile(path, []byte(api.Contract()), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "contract:", err)
		os.Exit(1)
	}
	fmt.Println("contract: wrote", path)
}
