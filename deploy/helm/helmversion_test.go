// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
//
// Which helm renders the chart decides what every other test in this package asserts. The majors
// differ in what they will render at all — a client-side dry run needs no cluster on one and asks a
// cluster for its version on the other — so a chart checked by two helms is a chart checked twice
// against two different claims, and the louder failure (one of them refuses) is the better case.
// The quieter one is a rendering both accept for different reasons, which nobody has looked at.
//
// So the build and `make gate` install one version. The two pins live in files that are edited
// months apart by somebody changing something else, and nothing but this holds them together.
package helm_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// helmStep returns the build's helm step: the version it pins, the digest it checks, and the script
// it runs.
//
// Read out of the parsed workflow rather than matched in its text, because a regular expression over
// the file is satisfied by a comment, and a comment installs nothing.
func helmStep(t *testing.T) (version, digest, script string) {
	t.Helper()
	return helmStepIn(t, "../../.github/workflows/test.yml", "test")
}

// helmStepIn reads the Helm install step out of one job in one workflow. Both the build and the
// release gate render the chart, so both have to install the same engine, and the two files are
// edited months apart by somebody changing something else.
func helmStepIn(t *testing.T, path, job string) (string, string, string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Name string            `yaml:"name"`
				Env  map[string]string `yaml:"env"`
				Run  string            `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(raw, &workflow); err != nil {
		t.Fatalf("%s does not parse: %v", path, err)
	}
	found, ok := workflow.Jobs[job]
	if !ok {
		t.Fatalf("%s has no %s job", path, job)
	}
	for _, step := range found.Steps {
		if step.Name == "Helm" {
			return step.Env["HELM_VERSION"], step.Env["HELM_SHA256"], step.Run
		}
	}
	t.Fatalf("the %s job in %s installs no helm, so the chart tests there render with whatever the "+
		"runner image ships — which is #192, and it failed a release", job, path)
	return "", "", ""
}

func pinnedInDockerfile(t *testing.T, arg string) string {
	t.Helper()
	raw, err := os.ReadFile("../gate/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`(?m)^ARG ` + regexp.QuoteMeta(arg) + `=(\S+)`).FindStringSubmatch(string(raw))
	if match == nil {
		t.Fatalf("the gate's Dockerfile pins no %s", arg)
	}
	return match[1]
}

func TestTheBuildAndTheGateInstallTheSameHelm(t *testing.T) {
	version, digest, _ := helmStep(t)
	if want := pinnedInDockerfile(t, "HELM_VERSION"); version != want {
		t.Errorf("the build installs helm %q and the gate installs %q; the chart tests would be rendered by two engines, and a local green and a build green would not mean the same thing", version, want)
	}
	// The same version is not the same artefact unless it is the same bytes. The runner is linux
	// amd64, which is the digest the gate pins for that architecture.
	if want := pinnedInDockerfile(t, "HELM_SHA256_AMD64"); digest != want {
		t.Errorf("the build checks digest %q for linux amd64 and the gate pins %q", digest, want)
	}
}

// A version named in an environment variable is a pin only if something refuses the download that
// does not match it. Without the check the step installs whatever the host served, under a name that
// reads as though it had been pinned — which is worse than an unpinned install, because it is the
// one nobody re-examines.
func TestTheBuildVerifiesTheHelmItDownloads(t *testing.T) {
	version, digest, script := helmStep(t)
	if digest == "" {
		t.Fatal("the build's helm step names no digest")
	}
	for _, want := range []string{"sha256sum -c", "${HELM_SHA256}", "${HELM_VERSION}", "set -euo pipefail"} {
		if !strings.Contains(script, want) {
			t.Errorf("the build's helm step does not %q, so its version pin refuses nothing", want)
		}
	}
	// A digest is for one file. The step must name the archive the digest belongs to, rather than
	// checking a pinned sum against whatever else it happened to fetch.
	if !strings.Contains(script, "helm-${HELM_VERSION}-linux-amd64.tar.gz") {
		t.Error("the build's helm step does not name the archive its digest is for")
	}
	if !strings.HasPrefix(version, "v") {
		t.Errorf("helm's archives are named with the leading v; %q would not resolve", version)
	}
}

// The release gate installs the same helm the build does.
//
// It did not, and the way that surfaced is the argument for this test. v0.6.0 was tagged at a commit
// whose own run of this suite had just passed on main; the release gate failed two tests that assert
// the content of NOTES.txt, because it rendered the chart with whatever helm the runner image ships
// while the build renders it with v4.3.0 (#192). The commit was fine. The gate job's own comment
// claimed it ran the complete gate the build runs.
//
// A release is the worst place for an environmental difference to appear. A tag must not move — a
// moved tag is different bytes under a name somebody may already hold — so a gate that fails for a
// reason the commit did not cause forces a choice between deleting a tag and shipping a version
// nobody can account for. This repository already carries one unpublished tag on the wrong commit
// from a related mess.
//
// So the release's gate pins helm as the build workflow and the gate image's Dockerfile do.
// TestTheBuildAndTheGateInstallTheSameHelm holds the build and the image together; this holds the
// release's gate to the build, digest included, because a matching version fetched from a different
// tarball is not the same engine. TestEveryJobThatRunsHelmInstallsTheBuildsHelmFirst holds every
// other job, including ones not written yet.
func TestTheReleaseGateInstallsTheSameHelmAsTheBuild(t *testing.T) {
	version, digest, _ := helmStep(t)
	releaseVersion, releaseDigest, script := helmStepIn(t, "../../.github/workflows/release.yml", "gate")

	if releaseVersion != version {
		t.Errorf("the build installs helm %q and the release gate installs %q; a release would be "+
			"gated by a rendering nobody has checked", version, releaseVersion)
	}
	if releaseDigest != digest {
		t.Errorf("the release gate verifies helm against %q and the build against %q; the same "+
			"version from a different tarball is not the same engine", releaseDigest, digest)
	}
	// Verified rather than fetched: a pipe into a shell is a release gated by whatever a host served
	// that morning.
	for _, want := range []string{"sha256sum -c", "${HELM_SHA256}", "${HELM_VERSION}", "set -euo pipefail"} {
		if !strings.Contains(script, want) {
			t.Errorf("the release gate's helm install does not contain %q", want)
		}
	}
}

// runsHelm reports whether a step's script calls helm, or something here that does: the script
// that renders the packaged chart, and `make test`, which runs this package on the runner's own
// helm. `make gate` is not among them — it runs in an image that carries its own.
//
// Comment lines are dropped first: a remark about helm installs nothing and runs nothing.
var helmCall = regexp.MustCompile(`(^|[\s;&|(])helm\s+[a-z]|scripts/chart-image-check\.sh|(^|[\s;&|(])make\s+test(\s|$)`)

func runsHelm(script string) bool {
	for _, line := range strings.Split(script, "\n") {
		if code := strings.TrimSpace(line); !strings.HasPrefix(code, "#") && helmCall.MatchString(code) {
			return true
		}
	}
	return false
}

// TestEveryJobThatRunsHelmInstallsTheBuildsHelmFirst holds every job to the engine the chart was
// tested with, and not only the jobs somebody remembered.
//
// The pins above were added one job at a time, each after that job had failed for want of one. The
// job nobody had looked at was the one that publishes: it packaged and pushed the chart with
// whatever helm the runner image shipped, under the release's signing identity, while the gate
// beside it in the same file was pinned (#255). A chart packaged by an engine nobody tested it with
// is published under a tag that cannot be moved.
//
// So this does not name jobs. It reads every workflow, finds each job with a step that calls helm,
// and requires a step before it that installs the build's version from the build's digest, checked.
// A job added next year is held the day it is written.
func TestEveryJobThatRunsHelmInstallsTheBuildsHelmFirst(t *testing.T) {
	version, digest, _ := helmStep(t)
	paths, err := filepath.Glob("../../.github/workflows/*.yml")
	if err != nil || len(paths) == 0 {
		t.Fatalf("no workflows found: %v", err)
	}
	held := 0
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var workflow struct {
			Jobs map[string]struct {
				Steps []struct {
					Name string            `yaml:"name"`
					Env  map[string]string `yaml:"env"`
					Run  string            `yaml:"run"`
				} `yaml:"steps"`
			} `yaml:"jobs"`
		}
		if err := yaml.Unmarshal(raw, &workflow); err != nil {
			t.Fatalf("%s does not parse: %v", path, err)
		}
		for name, job := range workflow.Jobs {
			installed := false
			for _, step := range job.Steps {
				if step.Name == "Helm" {
					if step.Env["HELM_VERSION"] != version || step.Env["HELM_SHA256"] != digest {
						t.Errorf("%s, job %s: installs helm %q (%q); the build installs %q (%q)",
							filepath.Base(path), name, step.Env["HELM_VERSION"], step.Env["HELM_SHA256"], version, digest)
					}
					for _, want := range []string{"set -euo pipefail", "sha256sum -c", "${HELM_SHA256}", "helm-${HELM_VERSION}-linux-amd64.tar.gz"} {
						if !strings.Contains(step.Run, want) {
							t.Errorf("%s, job %s: the helm install does not contain %q, so its pin refuses nothing",
								filepath.Base(path), name, want)
						}
					}
					installed = true
					continue
				}
				if runsHelm(step.Run) {
					if !installed {
						t.Errorf("%s, job %s: step %q calls helm before the job has installed the pinned one, "+
							"so it runs whatever the runner image ships", filepath.Base(path), name, step.Name)
					}
					held++
				}
			}
		}
	}
	// The release's publish job calls helm in three steps, and its gate and the build each run the
	// suite. Fewer than that means the pattern has stopped matching, not that the workflows have
	// stopped calling helm.
	if held < 5 {
		t.Fatalf("found %d steps that call helm across %d workflows; there were five when this was written, so this test is no longer reading them", held, len(paths))
	}
}
