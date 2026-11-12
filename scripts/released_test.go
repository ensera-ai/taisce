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

// TestAFindingSaysWhetherTheReleaseCallsWhatItCarries holds the question the patch policy turns on
// to being answered by something that runs.
//
// The policy puts a date on an advisory the release's own code calls and none on one it only links.
// Which of the two a finding is decides whether a clock has started, so it is decided from the
// release's source at its tag, with the toolchain its binary names — not from this tree, and not
// with whatever toolchain the machine holds, which would analyse a different standard library and
// could call an affected release clean.
//
// The scanner is stood in for here, because the real one reads the advisory database over the
// network. What is under test is everything around it: where it runs, with which toolchain, and
// what each answer becomes. The policy's own decision entry records the real run.
func TestAFindingSaysWhetherTheReleaseCallsWhatItCarries(t *testing.T) {
	script, err := filepath.Abs("released-called.sh")
	if err != nil {
		t.Fatal(err)
	}
	repo, bin, seen := t.TempDir(), t.TempDir(), filepath.Join(t.TempDir(), "seen")
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.test",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.test", "GIT_CONFIG_GLOBAL=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	// The release holds a file the tree after it does not, so where the scanner ran can be told.
	if err := os.WriteFile(filepath.Join(repo, "only-in-the-release"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("init", "--quiet", "--initial-branch=main")
	git("add", "-A")
	git("commit", "--quiet", "-m", "the release")
	git("tag", "v0.1.0")
	git("rm", "--quiet", "only-in-the-release")
	git("commit", "--quiet", "-m", "after the release")

	// A scanner that says what it was asked and where, and answers as told.
	stand := "#!/bin/sh\n" +
		"{ echo \"toolchain=$GOTOOLCHAIN\"; echo \"args=$*\"; [ -f only-in-the-release ] && echo at-the-release; } > \"$SEEN\"\n" +
		"exit \"$ANSWER\"\n"
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte(stand), 0o755); err != nil {
		t.Fatal(err)
	}
	called := func(answer string, args ...string) (string, int) {
		t.Helper()
		_ = os.Remove(seen)
		cmd := exec.Command(script, args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
			"SEEN="+seen, "ANSWER="+answer, "GIT_CONFIG_GLOBAL=/dev/null")
		out, err := cmd.CombinedOutput()
		code := 0
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		} else if err != nil {
			t.Fatalf("the script did not run: %v", err)
		}
		return string(out), code
	}

	if out, code := called("3", "v0.1.0", "go1.2.3"); code != 3 {
		t.Errorf("a release whose code calls vulnerable code must exit 3, got %d\n%s", code, out)
	}
	asked, err := os.ReadFile(seen)
	if err != nil {
		t.Fatalf("the scanner was never run: %v", err)
	}
	for _, want := range []string{"toolchain=go1.2.3\n", "args=tool govulncheck ./...\n", "at-the-release\n"} {
		if !strings.Contains(string(asked), want) {
			t.Errorf("the scanner must be asked about the release's own source, with the release's toolchain; it saw:\n%s", asked)
		}
	}
	if out, code := called("0", "v0.1.0", "go1.2.3"); code != 0 {
		t.Errorf("a release that only links the code must exit 0, got %d\n%s", code, out)
	}
	// Undecided is neither answer, and is not allowed to look like "does not call".
	if out, code := called("1", "v0.1.0", "go1.2.3"); code == 0 || code == 3 || !strings.Contains(out, "could not judge") {
		t.Errorf("a scanner that failed must be reported as undecided, got %d\n%s", code, out)
	}
	if out, code := called("0", "v9.9.9", "go1.2.3"); code == 0 || code == 3 || !strings.Contains(out, "is not in this checkout") {
		t.Errorf("a release whose tag is not here must be undecided, not clean, got %d\n%s", code, out)
	}
	if _, err := os.Stat(seen); err == nil {
		t.Error("the scanner was run for a tag that is not in the checkout")
	}
}

// TestThePatchPolicyIsStatedOnceAndTheScanKeepsToIt holds the promise and the machinery to the
// same number and the same rule.
//
// A policy in a document and a date computed in a workflow are two files edited by different
// changes. If they part, the public issue promises one thing and the policy another, and the one
// an operator reads first is the issue.
func TestThePatchPolicyIsStatedOnceAndTheScanKeepsToIt(t *testing.T) {
	read := func(path string) string {
		t.Helper()
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	policy, workflow, scan := read("../SECURITY.md"), read("../.github/workflows/released.yml"), read("released-advisories.sh")

	stated := regexp.MustCompile(`within \*\*(\d+) days\*\*`).FindStringSubmatch(policy)
	kept := regexp.MustCompile(`PATCH_WITHIN_DAYS: (\d+)`).FindStringSubmatch(workflow)
	if stated == nil || kept == nil {
		t.Fatalf("the patch policy states no number of days, or the scan computes its date from none (policy %v, workflow %v)", stated, kept)
	}
	if stated[1] != kept[1] {
		t.Errorf("SECURITY.md promises a release within %s days and the scan's issue computes its date from %s", stated[1], kept[1])
	}
	for _, want := range []string{
		"## The patch policy",
		"the latest release",
		"does not call",
		"taken as yes",
	} {
		if !strings.Contains(policy, want) {
			t.Errorf("the patch policy no longer says %q", want)
		}
	}
	if strings.Contains(policy, "No time is promised between an advisory") {
		t.Error("SECURITY.md still says no time is promised, beside a policy that promises one")
	}
	// The finding is followed by the called-or-linked question, and the workflow has the release's
	// tag for it to read.
	if !strings.Contains(scan, `released-called.sh" "$tag" "$toolchain"`) {
		t.Error("the scan no longer asks whether the release calls what it carries")
	}
	if !strings.Contains(workflow, `git fetch --depth 1 origin "refs/tags/${TAG}:refs/tags/${TAG}"`) {
		t.Error("the scheduled scan does not fetch the release's tag, so the called-or-linked question is always undecided")
	}
	// Undecided starts the clock: only a positive "does not call" withholds the date.
	if !strings.Contains(workflow, "if grep -q 'links the vulnerable code and does not call it'") {
		t.Error("the issue's date must be withheld only when the scan positively says the code is not called")
	}
}
