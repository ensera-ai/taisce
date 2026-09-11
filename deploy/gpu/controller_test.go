// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
//
// The operator-side controller's argument handling, without a provider call: what it refuses is
// what protects a paid node from a typo, and a check that never runs is a check nobody has watched.
package gpu_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func run(t *testing.T, env []string, script string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("python3", append([]string{filepath.Join("controller", script)}, args...)...)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestTheControllerCompilesAndRefusesWhatWouldWasteANode(t *testing.T) {
	for _, script := range []string{"guard.py", "deploy.py", "attempts.py"} {
		if out, err := exec.Command("python3", "-m", "py_compile", filepath.Join("controller", script)).CombinedOutput(); err != nil {
			t.Fatalf("%s does not compile: %v\n%s", script, err, out)
		}
	}
	// The guard: no key is a refusal before any argument is used; a zero price and a runtime past
	// the provider's guard are refused.
	if out, err := run(t, []string{"GPUAI_API_KEY="}, "guard.py", "--workdir", t.TempDir(), "--check"); err == nil || !strings.Contains(out, "GPUAI_API_KEY") {
		t.Fatalf("the guard must refuse to run without the provider key, got %v\n%s", err, out)
	}
	for _, bad := range [][]string{{"--max-price", "0"}, {"--hours", "0"}, {"--hours", "49"}, {"--gpus", "0"}} {
		if _, err := run(t, []string{"GPUAI_API_KEY=x"}, "guard.py", append([]string{"--workdir", t.TempDir(), "--check"}, bad...)...); err == nil {
			t.Fatalf("the guard must refuse %v", bad)
		}
	}
	// The deploy: a commit that is not a hash, or not in the repository, a corpus that is not a
	// directory, and a .NET repository without its commit are refused; the repository's own head
	// with its phases file is accepted.
	repo, _ := filepath.Abs("../..")
	head, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	sha := strings.TrimSpace(string(head))
	good := []string{"--workdir", t.TempDir(), "--repo", repo, "--commit", sha, "--check"}
	if out, err := run(t, nil, "deploy.py", good...); err != nil || !strings.Contains(out, "usable") {
		t.Fatalf("a good deploy must be accepted, got %v\n%s", err, out)
	}
	for _, bad := range [][]string{
		{"--workdir", t.TempDir(), "--repo", repo, "--commit", "not-a-hash", "--check"},
		{"--workdir", t.TempDir(), "--repo", repo, "--commit", "0123456789abcdef0123456789abcdef01234567", "--check"},
		{"--workdir", t.TempDir(), "--repo", repo, "--commit", sha, "--corpus", filepath.Join("controller", "README.md"), "--check"},
		{"--workdir", t.TempDir(), "--repo", repo, "--commit", sha, "--dotnet-repo", repo, "--check"},
		{"--workdir", t.TempDir(), "--repo", repo, "--commit", sha, "--hours", "0", "--check"},
	} {
		if _, err := run(t, nil, "deploy.py", bad...); err == nil {
			t.Fatalf("the deploy must refuse %v", bad)
		}
	}
	if _, err := run(t, nil, "attempts.py", "--attempts", "0"); err == nil {
		t.Fatal("attempts must be bounded")
	}
}
