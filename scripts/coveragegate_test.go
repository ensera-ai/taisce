// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The coverage gate's answers, held by running the real script against profiles of a throwaway
// module whose coverage the test decides. The floor is the repository's own coverage.floor.
//   - **Below the floor** it refuses, and names the function no test reaches, because the untested
//     code is the defect and the percentage is only how it was noticed.
//   - **Above the floor** it passes, and states the rule instead of a number to write. A floor raised
//     to one run's figure fails the next ordinary run, which D7 rejects; the gate's earlier message
//     suggested exactly that, and was followed (#76).
func TestTheCoverageGateRefusesBelowTheFloorAndNeverSuggestsOneRunAsTheFloor(t *testing.T) {
	top, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatal(err)
	}
	root := strings.TrimSpace(string(top))
	raw, err := os.ReadFile(filepath.Join(root, "coverage.floor"))
	if err != nil {
		t.Fatal(err)
	}
	floor := strings.TrimSpace(string(raw))

	module := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(module, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module covgate\n\ngo 1.21\n")
	write("covgate.go", "package covgate\n\nfunc Covered() int { return 1 }\n\nfunc Uncovered() int { return 2 }\n")
	profile := func(name, tests string) string {
		t.Helper()
		write("covgate_test.go", "package covgate\n\nimport \"testing\"\n\n"+tests)
		out := filepath.Join(module, name)
		cmd := exec.Command("go", "test", "-count=1", "-coverprofile="+out, ".")
		cmd.Dir = module
		cmd.Env = append(os.Environ(), "GOFLAGS=", "GOWORK=off")
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("building a profile: %v\n%s", err, b)
		}
		return out
	}
	gate := func(profile string) (string, error) {
		t.Helper()
		cmd := exec.Command("bash", filepath.Join(root, "scripts", "coverage-gate.sh"), profile)
		cmd.Dir = module
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	half := profile("half.out", "func TestCovered(t *testing.T) { if Covered() != 1 { t.Fatal() } }\n")
	out, err := gate(half)
	if err == nil {
		t.Fatalf("a profile at 50%% passed a floor of %s%%:\n%s", floor, out)
	}
	if !strings.Contains(out, "below the floor of "+floor+"%") || !strings.Contains(out, "Uncovered") {
		t.Fatalf("the refusal did not name the floor and the function no test reaches:\n%s", out)
	}

	whole := profile("whole.out", "func TestBoth(t *testing.T) { if Covered() != 1 || Uncovered() != 2 { t.Fatal() } }\n")
	out, err = gate(whole)
	if err != nil {
		t.Fatalf("a profile at 100%% was refused: %v\n%s", err, out)
	}
	if !strings.Contains(out, "above the floor of "+floor+"%") || !strings.Contains(out, "lowest of several full runs") {
		t.Fatalf("above the floor, the gate did not state the rule:\n%s", out)
	}
	if strings.Contains(out, "raise coverage.floor to") || strings.Contains(out, "floor to 100") {
		t.Fatalf("above the floor, the gate suggested one run's figure as the floor:\n%s", out)
	}
}
