// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

//go:build ignore

// Stages the documentation site's source for the renderer in site/: the public version, with
// nothing named in scripts/private-paths.txt.
//
// Under `ignore` and named explicitly by `make site`, so it is not part of `go build ./...` and does
// not ship as a command. The logic, and the tests that hold it, are in internal/docsite.
//
//	go run scripts/docsite.go -dsn … -out site/docs -sidebars site/sidebars.generated.json -repo owner/name -ref sha
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/ensera-ai/taisce/internal/docsite"
)

func main() {
	opts := docsite.Options{Root: "."}
	flag.StringVar(&opts.DSN, "dsn", os.Getenv("TAISCE_TEST_DSN"), "a disposable substrate; bootstrapping resets the instance roles' passwords, as the test suite does")
	flag.StringVar(&opts.Out, "out", "site/docs", "the renderer's document directory, replaced on every build")
	flag.StringVar(&opts.Sidebars, "sidebars", "site/sidebars.generated.json", "where the navigation is written")
	flag.StringVar(&opts.Repo, "repo", "ensera-ai/taisce", "owner/name that source links point at")
	flag.StringVar(&opts.Ref, "ref", "main", "the commit or branch that source links point at")
	flag.Parse()

	report, err := docsite.Build(context.Background(), opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("docsite: %d documents; %d packages, %d files, %d declarations, %d tests; %d migrations; %d tables\n",
		report.Documents, report.Packages, report.Files, report.Declarations, report.Tests, report.Migrations, report.Tables)
}
