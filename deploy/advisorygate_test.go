// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package deploy_test

import (
	"os"
	"strings"
	"testing"
)

// The reachable-advisory check is one target, `make vuln`, and three callers: the hosted build on
// every pull request, the local gate that is meant to be the same command, and a scheduled run that
// exists because an advisory published against an unchanged dependency arrives with no commit.
//
// A gate is only a gate while something invokes it. Each of those three is a line in a file no
// compiler reads, so losing one is a silent change in what the build enforces: the target still
// exists, `make vuln` still passes by hand, and nothing anywhere says that pull requests stopped
// being checked. A reviewer cannot see an absence.
//
// So this test is the thing that notices. It asserts the target exists and that each caller invokes
// it, which is a weaker claim than "the build is safe" and the strongest one a test in this package
// can make.
//
// What it does not cover: whether govulncheck's verdict is right, whether the advisory database was
// reachable on the day, and whether the scheduled run actually ran — a workflow that is present and
// disabled in the repository's settings passes this test. The first is the scanner's business, and
// the last two are observable only from outside the repository, which is why a red scheduled run is
// a signal somebody has to be watching rather than a property this suite can hold.
func TestTheReachableAdvisoryCheckIsWiredIntoEveryGateThatClaimsToRunIt(t *testing.T) {
	for _, c := range []struct {
		file string
		want string
		what string
	}{
		{"../Makefile", "\nvuln:\n", "the target itself"},
		{"../scripts/gate.sh", "make vuln", "the local gate, which is meant to run what the build runs"},
		{"../.github/workflows/test.yml", "make vuln", "every pull request and every push to main"},
		{"../.github/workflows/advisories.yml", "make vuln", "the scheduled run, for an advisory that arrives without a commit"},
	} {
		raw, err := os.ReadFile(c.file)
		if err != nil {
			t.Errorf("%s (%s): %v", c.file, c.what, err)
			continue
		}
		if !strings.Contains(string(raw), c.want) {
			t.Errorf("%s no longer contains %q, so %s is not checking for reachable advisories any more",
				c.file, c.want, c.what)
		}
	}

	// The scanner is pinned as a tool so that the version judging a commit is named by that commit.
	// Without the tool directive `go tool govulncheck` does not resolve and the target fails for a
	// reason that looks like an outage rather than a missing pin.
	raw, err := os.ReadFile("../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "tool golang.org/x/vuln/cmd/govulncheck") {
		t.Error("go.mod no longer pins govulncheck as a tool, so what judges a commit is whatever the builder installed")
	}
}
