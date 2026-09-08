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

// The publisher is exercised against real disposable Git remotes. URL rewriting keeps the same
// destination validation as production while preventing these tests from contacting GitHub.
type releaseFixture struct {
	root, source, public, private string
	env                           []string
}

func releaseRepo(t *testing.T) *releaseFixture {
	t.Helper()
	f := &releaseFixture{root: t.TempDir()}
	f.source = filepath.Join(f.root, "source")
	f.public = filepath.Join(f.root, "github", "ensera-ai", "taisce.git")
	f.private = filepath.Join(f.root, "github", "althunibat", "taisce.git")
	f.env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+filepath.Join(f.root, "gitconfig"), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	for _, path := range []string{f.source, f.public, f.private} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	f.run(t, f.root, "git", "config", "--global", "url.file://"+filepath.Join(f.root, "github")+"/.insteadOf", "git@github.com:")
	f.run(t, f.root, "git", "config", "--global", "user.name", "Release test")
	f.run(t, f.root, "git", "config", "--global", "user.email", "release@example.invalid")
	f.run(t, f.root, "git", "init", "--bare", f.public)
	f.run(t, f.root, "git", "init", "--bare", f.private)
	f.run(t, f.source, "git", "init", "-b", "genesis")
	f.run(t, f.source, "git", "remote", "add", "origin", "git@github.com:ensera-ai/taisce.git")
	f.run(t, f.source, "git", "remote", "add", "althunibat", "git@github.com:althunibat/taisce.git")
	if err := os.Mkdir(filepath.Join(f.source, "scripts"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"publish.sh", "version.sh"} {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(f.source, "scripts", file), data, 0700); err != nil {
			t.Fatal(err)
		}
	}
	f.write(t, "VERSION", "0.0\n")
	f.write(t, "Makefile", ".PHONY: licence test\nlicence:\n\t@echo fixture licence checked\ntest:\n\t@echo fixture tests checked\n")
	f.write(t, "code.txt", "current source\n")
	f.commit(t, "private development starts")
	return f
}
func (f *releaseFixture) write(t *testing.T, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.source, name), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}
func (f *releaseFixture) run(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = f.env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return strings.TrimSpace(string(out))
}
func (f *releaseFixture) commit(t *testing.T, message string) string {
	t.Helper()
	f.run(t, f.source, "git", "add", "-A")
	f.run(t, f.source, "git", "commit", "-m", message)
	f.run(t, f.source, "git", "push", "althunibat", "genesis")
	return f.run(t, f.source, "git", "rev-parse", "HEAD")
}
func (f *releaseFixture) prepare(t *testing.T, name string) string {
	t.Helper()
	message := filepath.Join(f.root, name+"-message")
	if err := os.WriteFile(message, []byte("Public release "+name+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	plan := filepath.Join(f.root, name)
	out := f.run(t, f.source, "bash", "scripts/publish.sh", "prepare", message, plan)
	if !strings.Contains(out, "fixture licence checked") || !strings.Contains(out, "fixture tests checked") {
		t.Fatal("preparation bypassed validation")
	}
	return plan
}
func (f *releaseFixture) refuse(t *testing.T, fragment string, args ...string) {
	t.Helper()
	cmd := exec.Command("bash", append([]string{"scripts/publish.sh"}, args...)...)
	cmd.Dir = f.source
	cmd.Env = f.env
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), fragment) {
		t.Fatalf("expected refusal %q: %v\n%s", fragment, err, out)
	}
}

func TestPublicReleasesPreserveSnapshotsWithoutPrivateAncestry(t *testing.T) {
	f := releaseRepo(t)
	firstSource := f.run(t, f.source, "git", "rev-parse", "HEAD")
	first := f.prepare(t, "first")
	if refs := f.run(t, f.root, "git", "--git-dir", f.public, "for-each-ref", "--format=%(refname)"); refs != "" {
		t.Fatalf("prepare published refs: %s", refs)
	}
	f.run(t, f.source, "bash", "scripts/publish.sh", "publish", first)
	firstPublic := f.run(t, f.root, "git", "--git-dir", f.public, "rev-parse", "main")
	if version := f.run(t, filepath.Join(first, "repository"), "bash", "scripts/version.sh", "current"); version != "0.1.0" {
		t.Fatalf("public stamp: %s", version)
	}
	f.run(t, f.source, "bash", "scripts/publish.sh", "publish", first) // Retry changes nothing.
	if got := f.run(t, f.source, "git", "rev-parse", "HEAD"); got != firstSource {
		t.Fatal("publication moved developer HEAD")
	}
	if got := f.run(t, f.root, "git", "--git-dir", f.private, "rev-parse", "genesis"); got != firstSource {
		t.Fatal("publication pushed the private branch")
	}
	f.write(t, "VERSION", "0.1\n")
	f.write(t, "code.txt", "second source\n")
	f.commit(t, "private detail one")
	f.write(t, "code.txt", "final source\n")
	secondSource := f.commit(t, "private detail two")
	second := f.prepare(t, "second")
	f.run(t, f.source, "bash", "scripts/publish.sh", "publish", second)
	if count := f.run(t, f.root, "git", "--git-dir", f.public, "rev-list", "--count", "main"); count != "2" {
		t.Fatalf("public history has %s commits", count)
	}
	if parent := f.run(t, f.root, "git", "--git-dir", f.public, "rev-parse", "main^"); parent != firstPublic {
		t.Fatal("second release discarded the first")
	}
	if content := f.run(t, f.root, "git", "--git-dir", f.public, "show", "main:code.txt"); content != "final source" {
		t.Fatalf("wrong snapshot: %s", content)
	}
	if version := f.run(t, filepath.Join(second, "repository"), "bash", "scripts/version.sh", "current"); version != "0.2.0" {
		t.Fatalf("second public stamp: %s", version)
	}
	for _, source := range []string{firstSource, secondSource} {
		cmd := exec.Command("git", "--git-dir", f.public, "cat-file", "-e", source)
		cmd.Env = f.env
		if err := cmd.Run(); err == nil {
			t.Fatal("private commit object reached the public repository")
		}
	}
	if state := f.run(t, f.source, "git", "status", "--porcelain"); state != "" {
		t.Fatalf("publication dirtied developer checkout: %s", state)
	}
}

func TestPublisherRefusesChangedRemoteAndPreparedState(t *testing.T) {
	f := releaseRepo(t)
	plan := f.prepare(t, "reviewed")
	f.run(t, f.source, "git", "remote", "set-url", "origin", "git@github.com:althunibat/taisce.git")
	f.refuse(t, "origin must identify", "publish", plan)
	f.run(t, f.source, "git", "remote", "set-url", "origin", "git@github.com:ensera-ai/taisce.git")
	f.write(t, "code.txt", "unreviewed source")
	f.refuse(t, "checkout must be clean", "publish", plan)
	f.run(t, f.source, "git", "restore", "code.txt")
	if err := os.WriteFile(filepath.Join(plan, "repository", "code.txt"), []byte("unreviewed snapshot"), 0600); err != nil {
		t.Fatal(err)
	}
	f.refuse(t, "snapshot has unreviewed edits", "publish", plan)
	f.run(t, filepath.Join(plan, "repository"), "git", "restore", "code.txt")
	f.write(t, "code.txt", "a new private commit")
	f.commit(t, "changed after release preparation")
	f.refuse(t, "source changed since preparation", "publish", plan)
	if refs := f.run(t, f.root, "git", "--git-dir", f.public, "for-each-ref", "--format=%(refname)"); refs != "" {
		t.Fatalf("refusal published refs: %s", refs)
	}
}

func TestPublisherRefusesPublicAdvanceAndUnexpectedHistory(t *testing.T) {
	f := releaseRepo(t)
	first := f.prepare(t, "first")
	f.run(t, f.source, "bash", "scripts/publish.sh", "publish", first)
	f.write(t, "VERSION", "0.1\n")
	f.commit(t, "next private line")
	second := f.prepare(t, "second")
	parent := f.run(t, f.root, "git", "--git-dir", f.public, "rev-parse", "main")
	tree := f.run(t, f.root, "git", "--git-dir", f.public, "rev-parse", "main^{tree}")
	advanced := f.run(t, f.root, "git", "--git-dir", f.public, "commit-tree", tree, "-p", parent, "-m", "unexpected public commit")
	f.run(t, f.root, "git", "--git-dir", f.public, "update-ref", "refs/heads/main", advanced)
	f.refuse(t, "public main changed", "publish", second)
	if tip := f.run(t, f.root, "git", "--git-dir", f.public, "rev-parse", "main"); tip != advanced {
		t.Fatal("refusal overwrote concurrent work")
	}
	message := filepath.Join(f.root, "another-message")
	if err := os.WriteFile(message, []byte("another release"), 0600); err != nil {
		t.Fatal(err)
	}
	f.refuse(t, "unaccounted-for commit", "prepare", message, filepath.Join(f.root, "another"))
}

func TestPublisherRefusesPrivateTrackingLinks(t *testing.T) {
	f := releaseRepo(t)
	f.write(t, "README.md", "Private backlog: https://github.com/althunibat/taisce/"+"issues/1\n")
	f.commit(t, "a private development document")
	message := filepath.Join(f.root, "message")
	if err := os.WriteFile(message, []byte("release"), 0600); err != nil {
		t.Fatal(err)
	}
	f.refuse(t, "export contains private issue links", "prepare", message, filepath.Join(f.root, "plan"))
}

func TestPublicationLeaseRefusesARaceWithoutPublishingHalfARelease(t *testing.T) {
	f := releaseRepo(t)
	first := f.prepare(t, "first")
	f.run(t, f.source, "bash", "scripts/publish.sh", "publish", first)
	f.write(t, "VERSION", "0.1\n")
	f.commit(t, "next line")
	second := f.prepare(t, "second")
	parent := f.run(t, f.root, "git", "--git-dir", f.public, "rev-parse", "main")
	tree := f.run(t, f.root, "git", "--git-dir", f.public, "rev-parse", "main^{tree}")
	advanced := f.run(t, f.root, "git", "--git-dir", f.public, "commit-tree", tree, "-p", parent, "-m", "concurrent public release")
	f.env = append(f.env, "TAISCE_RELEASE_FIXTURE_PUBLIC="+f.public, "TAISCE_RELEASE_FIXTURE_ADVANCED="+advanced)
	hook := []byte("#!/usr/bin/env bash\nset -e\ngit --git-dir \"$TAISCE_RELEASE_FIXTURE_PUBLIC\" update-ref refs/heads/main \"$TAISCE_RELEASE_FIXTURE_ADVANCED\"\n")
	if err := os.WriteFile(filepath.Join(second, "repository", ".git", "hooks", "pre-push"), hook, 0700); err != nil {
		t.Fatal(err)
	}
	f.refuse(t, "failed to push", "publish", second)
	if tip := f.run(t, f.root, "git", "--git-dir", f.public, "rev-parse", "main"); tip != advanced {
		t.Fatal("push overwrote a racing update")
	}
	if tags := f.run(t, f.root, "git", "--git-dir", f.public, "tag", "--list", "v0.2.0"); tags != "" {
		t.Fatal("failed push published a tag without its branch")
	}
}

func TestPrivateVersionCountsFromItsOwnReleaseSource(t *testing.T) {
	f := releaseRepo(t)
	source := f.run(t, f.source, "git", "rev-parse", "HEAD")
	plan := f.prepare(t, "first")
	f.run(t, f.source, "bash", "scripts/publish.sh", "publish", plan)
	f.run(t, f.source, "git", "fetch", "origin", "refs/tags/v0.1.0:refs/tags/v0.1.0")
	f.run(t, f.source, "git", "tag", "-a", "dev/v0.1.0", source, "-m", "private source of public 0.1.0")
	f.write(t, "VERSION", "0.1\n")
	f.commit(t, "begin the next private patch")
	if got := f.run(t, f.source, "bash", "scripts/version.sh", "current"); got != "0.1.1" {
		t.Fatalf("private version counted public ancestry: %s", got)
	}
	f.write(t, "code.txt", "next development step")
	f.commit(t, "private patch two")
	if got := f.run(t, f.source, "bash", "scripts/version.sh", "current"); got != "0.1.2" {
		t.Fatalf("private patch count: %s", got)
	}
}
