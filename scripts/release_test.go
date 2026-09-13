// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
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
		`gh attestation verify "$bin" --repo "$GITHUB_REPOSITORY" --signer-workflow "$workflow" --source-ref "$GITHUB_REF"`,
		`gh attestation verify "oci://${ref}" --repo "$GITHUB_REPOSITORY" --signer-workflow "$workflow" --source-ref "$GITHUB_REF"`,
		`cosign verify "$ref" --certificate-identity "$identity" --certificate-oidc-issuer "$issuer"`,
		`charts/taisce@${CHART_DIGEST}"`,
	} {
		if !strings.Contains(wf, want) {
			t.Errorf("the release workflow no longer contains %q", want)
		}
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
