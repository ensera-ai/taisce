// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestWhatIsPublishedIsJudgedOnAScheduleByTheCommandADeveloperRuns holds the scheduled scan of a
// release to the script a developer runs by hand.
//
// A scan that only the hosted build can run is one nobody can reproduce when it reports something,
// and a workflow that stopped calling the script would go on passing while judging nothing. So the
// workflow is read: it runs on a schedule, it calls the script, it may open an issue and nothing
// wider, and it runs nobody's code but pinned actions.
func TestWhatIsPublishedIsJudgedOnAScheduleByTheCommandADeveloperRuns(t *testing.T) {
	raw, err := os.ReadFile("../.github/workflows/released.yml")
	if err != nil {
		t.Fatalf("the workflow that judges a published release is not there: %v", err)
	}
	workflow := string(raw)
	for _, want := range []string{
		"schedule:", "- cron:", "workflow_dispatch:",
		"./scripts/released-advisories.sh",
		"gh issue create",
		"issues: write",
	} {
		if !strings.Contains(workflow, want) {
			t.Errorf("the workflow lacks %q", want)
		}
	}
	// The default is read-only, and the one job that writes writes issues and nothing else.
	if !strings.Contains(workflow, "\npermissions:\n  contents: read\n") {
		t.Error("the workflow's default permissions are not read-only")
	}
	for _, never := range []string{"contents: write", "packages: write", "id-token: write", "pull-requests: write"} {
		if strings.Contains(workflow, never) {
			t.Errorf("a workflow that reads a release and opens an issue holds %q", never)
		}
	}
	// A tag somebody typed reaches a shell only through the environment and only after it has
	// been held to the shape of a release tag.
	if strings.Contains(workflow, `"${{ inputs.release }}"`) || regexp.MustCompile(`run:[^\n]*\$\{\{\s*inputs\.`).MatchString(workflow) ||
		!strings.Contains(workflow, `'^v[0-9]+\.[0-9]+\.[0-9]+$'`) {
		t.Error("the release tag is interpolated into a script, or is used without being checked")
	}
	for _, line := range strings.Split(workflow, "\n") {
		if i := strings.Index(line, "uses:"); i >= 0 && !strings.HasPrefix(strings.TrimSpace(line), "#") {
			if !regexp.MustCompile(`uses: [A-Za-z0-9_./-]+@[0-9a-f]{40} # v`).MatchString(line) {
				t.Errorf("an action is not pinned to a commit with its tag beside it: %s", strings.TrimSpace(line))
			}
		}
	}

	script, err := os.ReadFile("released-advisories.sh")
	if err != nil {
		t.Fatal(err)
	}
	body := string(script)
	// It judges the binary that was shipped, with the scanner the commit pins.
	if !strings.Contains(body, "go tool govulncheck -mode binary") {
		t.Error("the script does not judge the published binary with the pinned scanner")
	}
	// It reads what it downloads and never runs it: the image is created, not started.
	for _, line := range strings.Split(body, "\n") {
		code, _, _ := strings.Cut(line, "#")
		for _, never := range []string{"docker run", "docker start", "docker exec"} {
			if strings.Contains(code, never) {
				t.Errorf("the script runs what it downloaded: %s", strings.TrimSpace(line))
			}
		}
	}
	// "Could not read the release" is not "clean": a release with no readable binary is an error.
	if !strings.Contains(body, "nothing was judged") || !strings.Contains(body, "exit 3") {
		t.Error("the script cannot tell an unreadable release from a clean one, or a finding from either")
	}

	makefile, err := os.ReadFile("../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(makefile), "vuln-released:") || !strings.Contains(string(makefile), "./scripts/released-advisories.sh $(RELEASE)") {
		t.Error("there is no make target that runs the same script by hand")
	}
}
