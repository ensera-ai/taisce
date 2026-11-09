// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package deploy_test

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
)

// The Go version appears in six places: the `toolchain` line in `go.mod`, the two image tags that
// build the binary and the gate container, the compiler the GPU qualification overlay runs, and the
// gate image tag written twice — a Makefile default and the shell fallback beside it. Five of those
// are strings in files no compiler reads.
//
// Nothing made them agree. They agreed because whoever moved one remembered the others, which is a
// property of a person rather than of the repository, and the failure it produces is silent: a build
// succeeds on the wrong compiler and says nothing. That is not hypothetical here. A local run and a
// hosted run measured the same tree at 92.0% and 92.4% because one used 1.27.3 and the other 1.27.1,
// and the only reason anybody noticed was a coverage floor sitting between the two numbers (#168,
// #171).
//
// So `go.mod` is the one place that decides, and this test is what makes the other five follow it.
// It fails when they disagree, which is the whole of its job — a version moved in one file and not
// the rest is the defect, whichever file was right.
//
// What this does not cover, deliberately: whether 1.27.3 is the version that *should* be pinned. That
// is a decision, it is D33, and it is re-argued when an advisory or a toolchain release says so. This
// test asserts consistency, not correctness, and a reader who wants the second should read the
// register.
//
// What it also cannot cover: whether the compiler that actually ran honoured the pin. The `toolchain`
// line binds `GOTOOLCHAIN=auto`, the default, and an environment that sets GOTOOLCHAIN to `local` or
// to a version has chosen its own compiler — which is the override that measured both toolchains
// against one tree, so it is wanted rather than closed off. A test cannot assert what the next build
// elsewhere will use; it can only make the files agree about what was asked for.
func TestEveryPlaceThatNamesTheGoToolchainNamesTheOnePinnedInGoMod(t *testing.T) {
	pinned := toolchainFromGoMod(t)

	// Each entry names a file, the pattern that finds a version in it, and what that version is for.
	// A pattern that matches nothing fails rather than passing quietly: a file renamed or a line
	// rewritten would otherwise turn this test into one that asserts nothing.
	for _, c := range []struct {
		file    string
		pattern *regexp.Regexp
		what    string
	}{
		{"../Dockerfile", regexp.MustCompile(`golang:([0-9]+\.[0-9]+\.[0-9]+)-`), "the image that builds the published binary"},
		{"../deploy/gate/Dockerfile", regexp.MustCompile(`golang:([0-9]+\.[0-9]+\.[0-9]+)-`), "the image the local gate runs the suite in"},
		{"../compose.gpu.yaml", regexp.MustCompile(`golang:([0-9]+\.[0-9]+\.[0-9]+)-`), "the compiler the GPU qualification overlay uses"},
		{"../Makefile", regexp.MustCompile(`taisce-gate:([0-9]+\.[0-9]+\.[0-9]+)`), "the gate image tag the Makefile builds"},
		{"../scripts/gate.sh", regexp.MustCompile(`taisce-gate:([0-9]+\.[0-9]+\.[0-9]+)`), "the gate image tag gate.sh runs"},
	} {
		raw, err := os.ReadFile(c.file)
		if err != nil {
			t.Errorf("%s (%s): %v", c.file, c.what, err)
			continue
		}
		found := c.pattern.FindAllStringSubmatch(string(raw), -1)
		if len(found) == 0 {
			t.Errorf("%s (%s): no version matched %s, so this file is no longer checked against go.mod",
				c.file, c.what, c.pattern)
			continue
		}
		for _, m := range found {
			if m[1] != pinned {
				t.Errorf("%s (%s) names Go %s; go.mod pins %s. One of them is building something nobody asked for.",
					c.file, c.what, m[1], pinned)
			}
		}
	}
}

// The `toolchain` line rather than the `go` directive: the directive is the oldest release the source
// needs and a builder holding anything newer satisfies it, so it is not what decides the compiler.
// Its absence is the defect #171 recorded, and a test that fell back to it would pass on the tree that
// had the defect.
func toolchainFromGoMod(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "toolchain go"); ok {
			if _, err := fmt.Sscanf(rest, "%s", &rest); err != nil {
				t.Fatalf("go.mod toolchain line is not a version: %q", line)
			}
			return rest
		}
	}
	t.Fatal("go.mod carries no toolchain line, so the compiler that builds this is whatever the builder happens to have (#171)")
	return ""
}
