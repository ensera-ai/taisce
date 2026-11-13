// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package migrate

import (
	"os/exec"
	"path"
	"strconv"
	"strings"
	"testing"
)

// TestTheOldestBinaryServedIsThePreviousReleases holds the one number a release has to remember to
// move to the release before it.
//
// The schema tells a binary how old it may be and still serve, and the number it is told is the
// newest migration the previous release carries. That number lives in a file, because it changes
// with every release. A file somebody has to remember to edit is wrong the first time they forget:
// left behind, it lets a process two releases old go on serving; moved too far, it stands the
// previous release down in the middle of an ordinary upgrade.
//
// So it is read back from the repository's own history: the newest release before this commit, and
// the newest memory migration at its tag. The first change after a release fails here until the
// file names that release's schema.
//
// It needs the history and the tags, as the upgrade check beside it does, and does not pass
// without them.
func TestTheOldestBinaryServedIsThePreviousReleases(t *testing.T) {
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", "../.."}, args...)...).Output()
		if err != nil {
			t.Fatalf("git %s: %v. This test reads the previous release from the repository's history; "+
				"a shallow or tagless checkout cannot answer it (git fetch --tags --unshallow)", strings.Join(args, " "), err)
		}
		return strings.TrimSpace(string(out))
	}
	// HEAD^ and not HEAD, as the upgrade check does: on the commit a release is cut from, the
	// release to stay compatible with is the one before it.
	previous := git("describe", "--tags", "--abbrev=0", "--match", "v[0-9]*.[0-9]*.[0-9]*", "HEAD^")
	newest := 0
	for _, file := range strings.Split(git("ls-tree", "--name-only", previous, "internal/migrate/sql/"), "\n") {
		number, _, found := strings.Cut(path.Base(file), "_")
		if n, err := strconv.Atoi(number); found && err == nil && n > newest {
			newest = n
		}
	}
	if newest == 0 {
		t.Fatalf("%s carries no memory migration this test could read", previous)
	}
	recorded, err := OldestServed()
	if err != nil {
		t.Fatal(err)
	}
	if recorded != newest {
		t.Fatalf("internal/migrate/oldest-served says %d, and the previous release, %s, was built for schema version %d. "+
			"Set the file to %d: it is the oldest binary this tree's schema promises to serve", recorded, previous, newest, newest)
	}
	this, err := BuiltFor()
	if err != nil {
		t.Fatal(err)
	}
	if recorded > this {
		t.Fatalf("the oldest binary served (%d) is newer than this tree (%d)", recorded, this)
	}
}
