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
	raw, err := os.ReadFile("../../.github/workflows/test.yml")
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
		t.Fatalf("test.yml does not parse: %v", err)
	}
	job, ok := workflow.Jobs["test"]
	if !ok {
		t.Fatal("test.yml has no test job")
	}
	for _, step := range job.Steps {
		if step.Name == "Helm" {
			return step.Env["HELM_VERSION"], step.Env["HELM_SHA256"], step.Run
		}
	}
	t.Fatal("the test job installs no helm, so the chart tests render with whatever the runner ships")
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
