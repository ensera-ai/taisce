// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The public copy is governed by a test rather than by review, because review is exactly what
// misses the line that was fine when it was written and false six months later. Two kinds
// of line are refused. A secret or an internal address: a connection string with a password, a
// credential or provider key that looks real, or a key variable with a key-shaped value, none of
// which belongs in a document that becomes public with the next snapshot. A placeholder such as
// `<test database>` is what a document should say instead, and passes. And a
// claim the service does not make: a promise with no measurement behind it, which a customer can
// quote back. The register argues about such words and is not marketing copy, so the claim guard
// covers the documents a newcomer reads first: the READMEs and the goals.
//
// What it does not cover: a claim phrased in words this list does not name. The list grows when a
// line is found that should have failed; that is the ratchet, and it only moves one way.
func TestPublicCopyCarriesNoSecretAndNoUnmeasuredClaim(t *testing.T) {
	findings, files, err := publicCopyFindings("..")
	if err != nil {
		t.Fatal(err)
	}
	if files < 10 {
		t.Fatalf("expected the public documents, found %d", files)
	}
	for _, f := range findings {
		t.Error(f)
	}
}

// The guard refuses what it exists for: a planted connection string with a password, a planted
// credential, a key variable with a key-shaped value, and a promise in a README, each named with
// its line; a placeholder and a shell reference pass, and a promise-word in a document that is
// not a README is not the guard's business.
func TestThePublicCopyGuardRefusesPlantedSecretsAndClaims(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("README.md", "Run it with TAISCE_TEST_DSN=<test database> and GPUAI_API_KEY=$GPUAI_API_KEY.\n"+
		"postgres://taisce:hunter2@db.internal:5432/taisce\n"+
		"Authorization: Bearer tsk_0123456789abcdefghijklmnop\n"+
		"It offers zero downtime and never loses a byte.\n")
	write("docs/01-decisions.md", "GPUAI_API_KEY=abcdefghijklmnopqrstuvwxyz was rejected; nothing is guaranteed here.\n")
	for i := 0; i < 9; i++ {
		write(fmt.Sprintf("docs/%02d-plain.md", i+2), "A plain document.\n")
	}
	findings, _, err := publicCopyFindings(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"README.md:2 carries", "README.md:3 carries", "README.md:4 makes a claim", "README.md:4 makes a claim", "01-decisions.md:1 carries"}
	if len(findings) != len(want) {
		t.Fatalf("expected %d findings, got %d: %v", len(want), len(findings), findings)
	}
	for i, w := range want {
		if !strings.Contains(findings[i], w) {
			t.Fatalf("finding %d: expected %q in %q", i, w, findings[i])
		}
	}
}

// publicCopyFindings scans the documents a snapshot publishes and returns every line refused,
// with the count of documents scanned so an empty scan cannot pass as a clean one.
func publicCopyFindings(root string) ([]string, int, error) {
	secrets := []*regexp.Regexp{
		// A URL with a password in it, whatever the scheme.
		regexp.MustCompile(`[a-z][a-z0-9+.-]*://[^\s/:@]+:[^\s@/]+@`),
		// A key variable with a key-shaped value; a placeholder or a shell reference is not one.
		regexp.MustCompile(`GPUAI_API_KEY\s*=\s*[A-Za-z0-9_-]{16,}`),
		// A credential of this service, or the shapes of the common provider keys.
		regexp.MustCompile(`tsk_[A-Za-z0-9_-]{16,}`),
		regexp.MustCompile(`\bsk-[A-Za-z0-9]{16,}`),
		regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
		regexp.MustCompile(`Bearer [A-Za-z0-9_-]{24,}`),
	}
	claims := []string{
		"zero downtime", "zero data loss", "unlimited", "100% ", "never lose",
		"guaranteed", "enterprise-grade", "military-grade", "bank-grade", "infinitely scalable",
		"real-time", "instantly",
	}
	var public []string
	for _, pattern := range []string{"README.md", "docs/*.md", "deploy/*/README.md", "plugins/*/README.md", "conformance/README.md"} {
		matches, err := filepath.Glob(filepath.Join(root, pattern))
		if err != nil {
			return nil, 0, err
		}
		public = append(public, matches...)
	}
	sort.Strings(public)
	claimGuarded := func(path string) bool {
		base := filepath.Base(path)
		return base == "README.md" || base == "00-goals.md"
	}
	var findings []string
	for _, path := range public {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, 0, err
		}
		for n, line := range strings.Split(string(raw), "\n") {
			for _, re := range secrets {
				if re.MatchString(line) {
					findings = append(findings, fmt.Sprintf("%s:%d carries something that looks like a secret or an internal address: %q", path, n+1, re.String()))
					break
				}
			}
			if !claimGuarded(path) {
				continue
			}
			lower := strings.ToLower(line)
			for _, claim := range claims {
				if strings.Contains(lower, claim) {
					findings = append(findings, fmt.Sprintf("%s:%d makes a claim nothing measures: %q", path, n+1, claim))
				}
			}
		}
	}
	return findings, len(public), nil
}
