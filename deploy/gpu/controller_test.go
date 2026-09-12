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
	"regexp"
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

// ── The demo node ─────────────────────────────────────────────────────────────────────────────
//
// demo.py rents a GPU and sends conversation text to it, so what it refuses is what stops a typo
// from placing the node outside the named region, stranding a paid instance, or writing its keys
// where a commit would pick them up. None of this calls the provider.
func TestTheDemoNodeRefusesWhatWouldMisplaceOrStrandIt(t *testing.T) {
	if out, err := exec.Command("python3", "-m", "py_compile", filepath.Join("controller", "demo.py")).CombinedOutput(); err != nil {
		t.Fatalf("demo.py does not compile: %v\n%s", err, out)
	}
	if out, err := run(t, []string{"GPUAI_API_KEY="}, "demo.py", "up", "--workdir", t.TempDir(), "--check"); err == nil || !strings.Contains(out, "GPUAI_API_KEY") {
		t.Fatalf("the demo must refuse to run without the provider key, got %v\n%s", err, out)
	}
	for _, bad := range [][]string{
		{"--max-price", "0"}, {"--hours", "0"}, {"--hours", "49"}, {"--port", "80"}, {"--disk-gb", "5"},
		{"--region-prefix", ""}, {"--region-prefix", "eu;"}, {"--region-prefix", "EU"},
		// A model that is not one of the two, a card the demo has no memory figure for, the larger
		// model on an 80 GB card, and a disk that cannot hold its weights.
		{"--model", "4"}, {"--gpu-type", "l4"}, {"--model", "3.6", "--gpu-type", "a100_80gb"},
		{"--model", "3.6", "--disk-gb", "60"},
	} {
		if _, err := run(t, []string{"GPUAI_API_KEY=x"}, "demo.py", append([]string{"up", "--workdir", t.TempDir(), "--check"}, bad...)...); err == nil {
			t.Fatalf("the demo must refuse %v", bad)
		}
	}
	// The workdir holds an SSH private key and a bearer key; inside a Git worktree they are one
	// `git add .` from a commit. Refused by the argument check, so before the provider key is used.
	repo, _ := filepath.Abs("../..")
	out, err := run(t, []string{"GPUAI_API_KEY=x"}, "demo.py", "up", "--workdir", filepath.Join(repo, "demo-state"), "--check")
	if err == nil || !strings.Contains(out, "Git worktree") {
		t.Fatalf("a workdir inside the repository must be refused, got %v\n%s", err, out)
	}
	if _, statErr := os.Stat(filepath.Join(repo, "demo-state")); statErr == nil {
		t.Fatal("the refused workdir was created anyway")
	}
}

// The demo serves the arguments the qualification measured, on a release that does not float. A
// second copy of the serving arguments that drifts from the first is a demo of a configuration nobody
// tested, so every argument the qualification passes to its generation replica, other than the TLS
// files the tunnel makes unnecessary, has to appear in demo.py. The qualified image cannot run on
// container capacity (docs/36), so the install is what is pinned instead, and the setting that made
// the measured card start has to stay. And the launch carries no port, no image and no environment:
// the tunnel is the only way in, and the bearer key never goes to the provider.
func TestTheDemoServesTheQualifiedArgumentsOnAPinnedRelease(t *testing.T) {
	compose, err := os.ReadFile(filepath.Join("..", "..", "compose.gpu.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	demo, err := os.ReadFile(filepath.Join("controller", "demo.py"))
	if err != nil {
		t.Fatal(err)
	}
	var image string
	var args []string
	inReplica := false
	for _, line := range strings.Split(string(compose), "\n") {
		s := strings.TrimSpace(line)
		if strings.HasPrefix(s, "x-generation-replica") {
			inReplica = true
			continue
		}
		if !inReplica {
			continue
		}
		if strings.HasPrefix(s, "shm_size") {
			break
		}
		if strings.HasPrefix(s, "image:") {
			image = strings.TrimSpace(strings.TrimPrefix(s, "image:"))
		}
		if strings.HasPrefix(s, "- ") {
			args = append(args, strings.TrimPrefix(s, "- "))
		}
	}
	if image == "" || len(args) < 5 {
		t.Fatalf("could not read the generation replica from compose.gpu.yaml: image %q, %d arguments", image, len(args))
	}
	for _, arg := range args {
		if strings.HasPrefix(arg, "--ssl-") {
			continue
		}
		if !strings.Contains(string(demo), "'"+arg+"'") {
			t.Errorf("demo.py does not pass the qualified argument %s", arg)
		}
	}
	for _, pin := range []*regexp.Regexp{
		regexp.MustCompile(`VLLM = 'vllm==\d+\.\d+\.\d+'`),
		regexp.MustCompile(`UV = 'uv==\d+\.\d+\.\d+'`),
	} {
		if !pin.Match(demo) {
			t.Errorf("demo.py does not pin %s", pin)
		}
	}
	if !strings.Contains(string(demo), "VLLM_USE_FLASHINFER_SAMPLER=0") {
		t.Error("the setting that made the measured card start is gone")
	}
	for _, field := range []string{"'ports':", "'image':", "'env':", "'environment':"} {
		if strings.Contains(string(demo), field) {
			t.Errorf("the demo launch carries %s; the tunnel is the only way in and no secret goes to the provider", field)
		}
	}
}

// ── #220 ──────────────────────────────────────────────────────────────────────────────────────
//
// The read-path harness runs on a paid node for an hour or more, so the refusals that stop a typo
// from wasting that time are worth a test of their own. It has no provider to call and no database
// here, so what is checked is what can be: it compiles, and it refuses an unusable project name
// before it touches anything.
func TestTheReadPathHarnessCompilesAndRefusesAnUnusableProject(t *testing.T) {
	if out, err := exec.Command("python3", "-m", "py_compile", "readpath-scale.py").CombinedOutput(); err != nil {
		t.Fatalf("readpath-scale.py does not compile: %v\n%s", err, out)
	}
	for _, bad := range []string{"Readpath", "read path", "read-path", "1readpath", ""} {
		cmd := exec.Command("python3", "readpath-scale.py")
		cmd.Env = append(os.Environ(), "SCALE_PROJECT="+bad, "RESULTS="+t.TempDir())
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("the harness accepted project %q", bad)
		}
		if !strings.Contains(string(out), "SCALE_PROJECT") {
			t.Fatalf("the refusal of %q does not say which setting is wrong: %s", bad, out)
		}
	}
	// A project name it does accept gets past the check and fails later, for want of a deployment —
	// which is the proof that the name was not what stopped it.
	cmd := exec.Command("python3", "readpath-scale.py")
	cmd.Env = append(os.Environ(), "SCALE_PROJECT=readpath_scale", "RESULTS="+t.TempDir(),
		"TAISCE_COMPOSE=false", "SCALE_SIZES=1", "SCALE_CONCURRENCIES=1", "SCALE_REQUESTS=1")
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("the harness reported success with no deployment: %s", out)
	} else if strings.Contains(string(out), "SCALE_PROJECT") {
		t.Fatalf("a valid project name was refused: %s", out)
	}
}

// The corpus fetch is an acceptance of somebody else's licence, so it is off unless asked for, and
// the flag that asks for it has to reach the node. A default that fetched would accept a licence on
// an operator's behalf, which is not a default anything should have.
func TestTheCorpusIsFetchedOnlyWhenTheOperatorAsks(t *testing.T) {
	phases, err := os.ReadFile(filepath.Join("controller", "phases.sh"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(phases)
	if !strings.Contains(body, `[ "${TAISCE_FETCH_CORPUS:-0}" = "1" ] || return 1`) {
		t.Fatal("the fetch does not default to off")
	}
	// Pinned and verified: an archive that is not the one this was written against is one nobody
	// has read, and the digest is checked before anything is unpacked rather than after.
	for _, required := range []string{
		"799b78b6716a8f24fcd354b89a37b429ba1e587a",
		"2f70dda22a9f261f285c94f3ac13a8f0df60b69fe3df1b5853b47b372065a66f",
		"sha256sum -c -",
	} {
		if !strings.Contains(body, required) {
			t.Fatalf("the fetch does not pin or verify: %q is missing", required)
		}
	}
	if strings.Index(body, "sha256sum -c -") > strings.Index(body, "unzip") {
		t.Fatal("the archive is unpacked before it is verified")
	}
	deploy, err := os.ReadFile(filepath.Join("controller", "deploy.py"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(deploy), "--fetch-corpus") ||
		!strings.Contains(string(deploy), "TAISCE_FETCH_CORPUS=1 ") {
		t.Fatal("the flag does not reach the node")
	}
}
