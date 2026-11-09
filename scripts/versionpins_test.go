// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// VERSION is checked against the tag it is released under, in the release workflow. Five other files
// name a release version, and one of them decides what a new reader installs: compose.yaml defaults
// all three images to a tag, so a release that leaves it behind publishes a new version and keeps
// installing the old one. Nobody's build breaks, nothing turns red, and the quickstart quietly hands
// out the previous release with every defect it fixed.
//
// They agreed with the published line until somebody forgot, which is exactly how VERSION itself
// drifted through two releases (#152). A release checklist is a person remembering, and that is the
// thing that already failed. So this is the check: a pinned release version in a shipped file matches
// VERSION's line, and a release that leaves one behind fails before it publishes.
//
// Scope: the files where a pinned version is OPERATIVE — something a reader runs, or installs, or
// compares their build against. compose.yaml decides what `docker compose up` pulls, README.md is
// the path a new reader follows, and CONTRIBUTING.md names the published image a working-tree build
// must not be mistaken for.
//
// Deliberately NOT covered: prose that names a version as a historical fact. docs/architecture/
// deployment.md says compose files from v0.3.0 to v0.4.0 fixed the project name, that a v0.3.2
// deployment served passage search on a particular date, and that charts 0.3.0 and 0.3.1 need an
// explicit image tag. Every one of those is true of a version that shipped, and bumping it would
// make the document wrong. A first draft of this test held that file to the rule and failed on five
// such lines, which is how the distinction was found: a pin that tells a reader what to run must
// track the release, and a pin that records what happened must not move at all.
//
// So the rule cannot be "no old version strings anywhere". It is "the versions a reader acts on are
// the current one", and the files above are the ones that decide that.
func TestEveryPinnedReleaseVersionMatchesTheVersionLine(t *testing.T) {
	root := pinsRoot(t)
	line, err := os.ReadFile(filepath.Join(root, "VERSION"))
	if err != nil {
		t.Fatal(err)
	}
	want := strings.TrimSpace(string(line)) + ".0"

	pin := regexp.MustCompile(`v[0-9]+\.[0-9]+\.[0-9]+`)
	for _, name := range []string{"compose.yaml", "README.md", "CONTRIBUTING.md"} {
		body, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		found := pin.FindAllString(string(body), -1)
		if len(found) == 0 {
			t.Errorf("%s names no release version, so this file is no longer held to VERSION", name)
			continue
		}
		for _, got := range found {
			if got != "v"+want {
				t.Errorf("%s pins %s; VERSION says the line is %s, so the release is v%s. "+
					"Either the pin was not bumped with VERSION, or VERSION is wrong.",
					name, got, strings.TrimSpace(string(line)), want)
			}
		}
	}
}

// pinsRoot asks git where the repository is, the way this package's other tests do, rather than
// walking up from the working directory and guessing when to stop.
func pinsRoot(t *testing.T) string {
	t.Helper()
	top, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(top))
}
