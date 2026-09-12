// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package docsite

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// The site is the public version of the documentation, so it is built as if from the public tree.
//
// scripts/private-paths.txt names what never leaves the development repository, and the release
// snapshot is made by removing exactly those paths. The generator reads the same list: a private
// document is not staged, and a link into one is refused, because in the public tree there is nothing
// at the other end. One list, two readers, so the site cannot show what the public repository will
// not have.
//
// A written document is also refused when it cites something only the development repository can
// open: a decision number, a goal number, an issue number, or the private repository itself. A public
// reader following one would find nothing. Code comments are held to the same rule when a release is
// exported, not here: the generated reference renders what the code says, and the fix for that is
// in the code.

const privateList = "scripts/private-paths.txt"

// privatePaths reads the list. A tree without one is refused: the public snapshot carries the file
// back empty, so a missing list means something went wrong, not that nothing is private.
func privatePaths(root string) (map[string]bool, error) {
	f, err := os.Open(filepath.Join(root, filepath.FromSlash(privateList)))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("docsite: %s is missing; without it the site cannot tell what is private", privateList)
	}
	if err != nil {
		return nil, fmt.Errorf("docsite: %w", err)
	}
	defer f.Close()
	paths := map[string]bool{privateList: true}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		paths[path.Clean(strings.TrimSuffix(line, "/"))] = true
	}
	return paths, scanner.Err()
}

// isPrivate reports whether a repository path is listed, or lies under a listed directory.
func isPrivate(private map[string]bool, repoPath string) bool {
	for p := path.Clean(repoPath); p != "." && p != "/"; p = path.Dir(p) {
		if private[p] {
			return true
		}
	}
	return false
}

var privateReferences = []struct {
	pattern *regexp.Regexp
	what    string
}{
	{regexp.MustCompile(`\bD[0-9]{1,3}\b`), "a decision number"},
	{regexp.MustCompile(`\bG[0-9]{1,2}b?\b`), "a goal number"},
	{regexp.MustCompile(`(?:^|[\s(\[])#[0-9]{1,4}\b`), "an issue number"},
	{regexp.MustCompile(`github\.com/althunibat/`), "the private repository"},
}

// citesPrivate lists every line of a written document that cites private material.
func citesPrivate(p page) []string {
	var found []string
	for n, line := range strings.Split(p.Body, "\n") {
		for _, ref := range privateReferences {
			if ref.pattern.MatchString(line) {
				found = append(found, fmt.Sprintf("%s:%d cites %s", p.Repo, n+1, ref.what))
				break
			}
		}
	}
	return found
}
