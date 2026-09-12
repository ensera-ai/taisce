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
	"flag"
	"fmt"
	"os"

	"github.com/ensera-ai/taisce/internal/api"
)

func main() {
	// -freeze writes the snapshot a version is held to, once, when the version is declared frozen.
	// It is a separate flag rather than part of every regeneration because regenerating the
	// document is routine and freezing is a decision.
	freeze := flag.String("freeze", "", "write the frozen surface snapshot to this path instead of the document")
	flag.Parse()
	if *freeze != "" {
		body, err := api.FreezeJSON()
		if err == nil {
			err = os.WriteFile(*freeze, body, 0o644)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "contract:", err)
			os.Exit(1)
		}
		fmt.Println("contract: froze the surface at", *freeze)
		return
	}
	const path = "docs/02-contract.md"
	if err := os.WriteFile(path, []byte(api.Contract()), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "contract:", err)
		os.Exit(1)
	}
	fmt.Println("contract: wrote", path)
}
