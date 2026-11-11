// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Every decision is a file of its own, named by its number, and says what it beat.
//
// The register was one file that every decision was appended to. Two pull requests that each
// recorded a decision changed the same lines, so the second could not merge until somebody had
// merged it with the first, by hand, and waited for a build — for changes that did not overlap. One
// of those hand resolutions was pushed with its conflict markers still in it.
//
// A file per decision removes that conflict and keeps the one worth having: two pull requests that
// take the same number add the same file, which git reports before either merges. That is why the
// name is the number and nothing else. A title in the name would read better in a listing and would
// let two D40s land beside each other.
//
// The three things an entry must carry are checked here because the register says an entry without
// them is not finished, and nothing was holding it to that.
func TestEveryDecisionIsAFileOfItsOwnAndSaysWhatItBeat(t *testing.T) {
	root := filepath.Join("..", "docs")
	name := regexp.MustCompile(`^D([1-9][0-9]*)\.md$`)
	heading := regexp.MustCompile(`(?m)^# D([1-9][0-9]*) — \S`)

	entries, err := os.ReadDir(filepath.Join(root, "decisions"))
	if err != nil {
		t.Fatalf("the decisions are not in a directory of their own: %v", err)
	}
	var numbers []int
	for _, e := range entries {
		m := name.FindStringSubmatch(e.Name())
		if e.IsDir() || m == nil {
			t.Errorf("docs/decisions/%s is not a decision: a file there is named for its number, D12.md, and nothing else lives there", e.Name())
			continue
		}
		n, _ := strconv.Atoi(m[1])
		numbers = append(numbers, n)
		raw, err := os.ReadFile(filepath.Join(root, "decisions", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		body := string(raw)
		headings := heading.FindAllStringSubmatch(body, -1)
		if len(headings) != 1 || headings[0][1] != m[1] {
			t.Errorf("docs/decisions/%s must hold exactly one decision, headed \"# D%s — …\"; a file whose number disagrees with its heading is cited under one and read under the other", e.Name(), m[1])
		}
		for _, part := range []struct{ marker, why string }{
			{"**Date.**", "when it was decided, because what was true of a platform or a market was true on a day"},
			{"**Why it was open", "what forced the choice"},
			{"**Decided", "what was chosen"},
			{"**Rejected", "what it beat — a decision written down without its alternatives is reopened by whoever has only heard the winning argument"},
			{"**Undo cost", "how hard it would be to reverse"},
			{"**Impact", "what it actually moves"},
		} {
			if !strings.Contains(body, part.marker) {
				t.Errorf("docs/decisions/%s does not say %s (%s)", e.Name(), part.why, part.marker)
			}
		}
	}
	// Forty-two existed when the register was split, and a decision is never removed: a count below
	// that means entries were lost, which is the failure a move like this one risks.
	if len(numbers) < 42 {
		t.Fatalf("docs/decisions holds %d decisions; 42 existed when the register became a directory, and none is ever removed", len(numbers))
	}
	sort.Ints(numbers)
	if numbers[0] != 1 {
		t.Errorf("the first decision is D%d; they are numbered from D1", numbers[0])
	}

	// The old file stays at its path, because the project's rules and other documents cite it, and
	// it says how the register works. It must not hold an entry: one appended there by habit would
	// be a decision outside the place every reader now looks, and the conflict this removed.
	raw, err := os.ReadFile(filepath.Join(root, "01-decisions.md"))
	if err != nil {
		t.Fatalf("docs/01-decisions.md is the path the project's rules cite and must remain: %v", err)
	}
	index := string(raw)
	if stray := regexp.MustCompile(`(?m)^#+ D[0-9]+ `).FindString(index); stray != "" {
		t.Errorf("docs/01-decisions.md holds an entry (%q); a decision is a file in docs/decisions/ and this page only says how the register works", strings.TrimSpace(stray))
	}
	for _, want := range []string{"decisions/", "D12.md", "make decisions"} {
		if !strings.Contains(index, want) {
			t.Errorf("docs/01-decisions.md does not tell a reader where a decision is or how to list them; it lacks %q", want)
		}
	}
}
