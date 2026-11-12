// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestTheReleaseVerifiesWhatItPublishedBeforeAnnouncingIt holds the release to running the checks
// SECURITY.md gives consumers, pinned as tightly as they are there, before the release page.
//
// A documented verification command that nothing runs is a claim, and the day it stops working is the
// day a consumer finds out. Running it inside the release against the digests just pushed makes a
// broken check fail the release instead. The test reads the workflow because the workflow is the only
// place this can be enforced: it cannot be exercised without cutting a release.
func TestTheReleaseVerifiesWhatItPublishedBeforeAnnouncingIt(t *testing.T) {
	raw, err := os.ReadFile("../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	wf := string(raw)
	for _, want := range []string{
		`identity="https://github.com/${workflow}@${GITHUB_REF}"`,
		`gh attestation verify "$file" --repo "$GITHUB_REPOSITORY" --signer-workflow "$workflow" --source-ref "$GITHUB_REF"`,
		`gh attestation verify "oci://${ref}" --repo "$GITHUB_REPOSITORY" --signer-workflow "$workflow" --source-ref "$GITHUB_REF"`,
		`cosign verify "$ref" --certificate-identity "$identity" --certificate-oidc-issuer "$issuer"`,
		`charts/taisce@${CHART_DIGEST}"`,
	} {
		if !strings.Contains(wf, want) {
			t.Errorf("the release workflow no longer contains %q", want)
		}
	}
	// The publish job's own block runs from its key to the next top-level job, or to the end of the
	// file; the environment has to be inside it, not merely somewhere in the workflow.
	publish := strings.Index(wf, "\n  publish:\n")
	if publish < 0 {
		t.Fatal("the release workflow has no publish job")
	}
	block := wf[publish+1:]
	for i, line := range strings.Split(block, "\n") {
		if i > 0 && len(line) > 2 && line[:2] == "  " && line[2] != ' ' && line[2] != '#' {
			block = strings.Join(strings.Split(block, "\n")[:i], "\n")
			break
		}
	}
	if !strings.Contains(block, "\n    environment: release\n") {
		t.Error("the publish job must run in the release environment, so nothing reaches a registry without the owner's approval")
	}
	verify := strings.Index(wf, "- name: Verify the published artifacts")
	push := strings.Index(wf, `helm push "dist/taisce-`)
	page := strings.Index(wf, "- name: Release page")
	if verify < 0 || push < 0 || page < 0 || !(push < verify && verify < page) {
		t.Errorf("verification must run after everything is pushed and before the release page (push=%d verify=%d page=%d)", push, verify, page)
	}
	for _, loose := range []string{"--certificate-identity-regexp", "--cert-identity-regex", "attestation verify --owner"} {
		if strings.Contains(wf, loose) {
			t.Errorf("the release workflow verifies with %q, which accepts artifacts this release did not produce", loose)
		}
	}
}

// TestTheSecurityPolicyNamesTheLevelAndThePinnedChecks holds SECURITY.md to the claim D6 decided.
//
// The document is where a consumer learns what a release guarantees, so its width is the width they
// will rely on. It must name the level, and the commands it gives must be the pinned ones — a pattern
// or an organisation-wide lookup would accept an artifact from another workflow or another ref.
func TestTheSecurityPolicyNamesTheLevelAndThePinnedChecks(t *testing.T) {
	raw, err := os.ReadFile("../SECURITY.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	for _, want := range []string{
		"SLSA v1.0 Build Level 2",
		"not Level 3",
		"--signer-workflow ensera-ai/taisce/.github/workflows/release.yml",
		"--source-ref refs/tags/vX.Y.Z",
		"--certificate-identity https://github.com/ensera-ai/taisce/.github/workflows/release.yml@refs/tags/vX.Y.Z",
		"https://slsa.dev/spec/v1.0/levels",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("SECURITY.md no longer contains %q", want)
		}
	}
	for _, loose := range []string{"--owner ensera-ai", "certificate-identity-regexp"} {
		if strings.Contains(doc, loose) {
			t.Errorf("SECURITY.md recommends %q, which accepts artifacts this release did not produce", loose)
		}
	}
}

// releasePage fills a directory the way the release does: files under the page's name, then a
// checksums file written from them.
func releasePage(t *testing.T, files map[string]string) (dir string) {
	t.Helper()
	dir = t.TempDir()
	var sums strings.Builder
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&sums, "%x  %s\n", sha256.Sum256([]byte(contents)), name)
	}
	if err := os.WriteFile(filepath.Join(dir, "taisce-v9.9.9-checksums.txt"), []byte(sums.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestNothingReachesAReleasePageWithoutALineInTheChecksumsFile holds the page to its own checksums.
//
// The page is filled by a glob and the checksums file is written at one moment, so a file that
// arrives in between is published with nothing a reader can compare it against. The chart package
// was that file for every release that carried one. The script is run here against directories
// built to be wrong in each way that matters.
func TestNothingReachesAReleasePageWithoutALineInTheChecksumsFile(t *testing.T) {
	check := func(dir string) (string, error) {
		out, err := exec.Command("./release-page-check.sh", dir, "v9.9.9").CombinedOutput()
		return string(out), err
	}
	page := map[string]string{
		"taisce-v9.9.9-linux-amd64": "a binary", "taisce-v9.9.9-darwin-arm64": "another", "taisce-9.9.9.tgz": "a chart package",
	}

	if out, err := check(releasePage(t, page)); err != nil {
		t.Fatalf("a page whose every file is in the checksums file was refused: %v\n%s", err, out)
	}

	// The defect itself: a file that reached the directory after the checksums were written.
	late := releasePage(t, map[string]string{"taisce-v9.9.9-linux-amd64": "a binary"})
	if err := os.WriteFile(filepath.Join(late, "taisce-9.9.9.tgz"), []byte("a chart package"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := check(late); err == nil || !strings.Contains(out, "taisce-9.9.9.tgz would be on the release page and is not in") {
		t.Errorf("a file added after the checksums were written must be refused by name, got %v\n%s", err, out)
	}

	// A file changed after its line was written is not the file the line vouches for.
	changed := releasePage(t, page)
	if err := os.WriteFile(filepath.Join(changed, "taisce-9.9.9.tgz"), []byte("a different chart package"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := check(changed); err == nil {
		t.Errorf("a file whose bytes no longer match its line was accepted:\n%s", out)
	}

	// A name that only contains a listed name is not that file: the line is matched whole.
	prefixed := releasePage(t, page)
	if err := os.WriteFile(filepath.Join(prefixed, "taisce-9.9.9.tgz.sig"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := check(prefixed); err == nil || !strings.Contains(out, "taisce-9.9.9.tgz.sig would be") {
		t.Errorf("a file whose name extends a listed one must be refused, got %v\n%s", err, out)
	}

	// No checksums file, or an empty one, vouches for nothing. It does not pass for want of lines.
	for _, contents := range []string{"", "absent"} {
		none := releasePage(t, page)
		sums := filepath.Join(none, "taisce-v9.9.9-checksums.txt")
		if contents == "absent" {
			if err := os.Remove(sums); err != nil {
				t.Fatal(err)
			}
		} else if err := os.WriteFile(sums, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := check(none); err == nil || !strings.Contains(out, "missing or empty") {
			t.Errorf("a page with no usable checksums file must be refused, got %v\n%s", err, out)
		}
	}
}

// TestEverythingOnTheReleasePageIsBuiltBeforeItIsVouchedFor holds the order the workflow depends on.
//
// The check above fails a release that got the order wrong, after its images are already pushed.
// This reads the workflow so the order is wrong on a pull request instead: every file the page
// carries is written before the checksums, the provenance step covers the same glob the page
// uploads, and nothing writes into that directory afterwards.
func TestEverythingOnTheReleasePageIsBuiltBeforeItIsVouchedFor(t *testing.T) {
	raw, err := os.ReadFile("../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	wf := string(raw)
	at := func(what string) int {
		t.Helper()
		i := strings.Index(wf, what)
		if i < 0 {
			t.Fatalf("the release workflow no longer contains %q", what)
		}
		return i
	}
	binaries := at(`-o "dist/taisce-${tag}-${os}-${arch}"`)
	chart := at(`helm package deploy/helm/taisce`)
	sums := at(`sha256sum taisce-* > "taisce-${GITHUB_REF_NAME}-checksums.txt"`)
	provenance := at("subject-path: dist/taisce-*")
	check := at(`scripts/release-page-check.sh dist "$TAG"`)
	page := at("files: dist/taisce-*")
	if !(binaries < sums && chart < sums && sums < provenance && provenance < check && check < page) {
		t.Errorf("the page's files must be built, then summed, then given provenance, then checked, then published (binaries=%d chart=%d sums=%d provenance=%d check=%d page=%d)",
			binaries, chart, sums, provenance, check, page)
	}
	if !strings.Contains(wf, "for file in dist/taisce-*; do") {
		t.Error("provenance must be verified for the files the page uploads, by the same glob, not for a list of expected names")
	}
	// Anything written to dist/ after the checksums is either refused at the check or, worse, is
	// not under the page's name and is silently absent. Neither should be reachable.
	for _, line := range strings.Split(wf[sums+1:], "\n") {
		code, _, _ := strings.Cut(line, " #")
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if strings.Contains(code, "-d dist") || strings.Contains(code, "> dist/") || strings.Contains(code, "tee dist/") || strings.Contains(code, `-o "dist/`) {
			t.Errorf("a step writes into dist/ after the checksums file is written: %s", strings.TrimSpace(line))
		}
	}
	if !strings.Contains(wf, `cmp "$RUNNER_TEMP/registry-chart/taisce-${SEMVER}.tgz" "dist/taisce-${SEMVER}.tgz"`) {
		t.Error("the chart on the page and the chart in the registry must be compared before the page is published")
	}
}
