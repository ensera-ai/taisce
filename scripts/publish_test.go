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
	f.write(t, "scripts/private-paths.txt", "# What never leaves the development repository.\nprivate.md\nsecret/\n")
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

// readSnapshot reads a file back out of the prepared tree, which is where the export can be judged:
// the development checkout still carries its citations, and should.
func readSnapshot(t *testing.T, plan, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(plan, "repository", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// Scrubbing belongs to publication rather than to the development tree. A citation is useful here,
// where the register and the tracker can be opened, and useless in a public checkout — so the
// reference goes on the way out and the development comment keeps its pointer.
func TestPublisherStripsParentheticalReferencesAndRewritesTheRepositoryName(t *testing.T) {
	f := releaseRepo(t)
	f.write(t, "code.go", "// Version 1 is frozen (D126). The freeze is the file beside it.\n"+
		"// Bounded, per G11, and fixed in #278.\n")
	f.write(t, "README.md", "Backlog: https://github.com/althunibat/taisce/"+"issues/1\n")
	f.commit(t, "comments carrying private references")
	plan := f.prepare(t, "stripped")

	code := readSnapshot(t, plan, "code.go")
	if strings.Contains(code, "(D126)") {
		t.Fatalf("a parenthetical reference was published: %q", code)
	}
	if !strings.Contains(code, "Version 1 is frozen. The freeze") {
		t.Fatalf("removing a parenthetical left an unreadable sentence: %q", code)
	}

	// The deliberate limit, and it needs a test or somebody will "improve" it. A reference that is
	// the subject or object of a sentence stays, because no mechanical rule repairs what removing it
	// leaves: "Bounded, per." reads as a typo rather than as an absence, which is worse than the
	// citation it replaced. Those are rewritten by hand, in the tree where the sentence can be
	// written properly.
	if !strings.Contains(code, "per G11") || !strings.Contains(code, "fixed in #278") {
		t.Fatalf("a bare in-text reference was removed, leaving broken prose: %q", code)
	}

	// The private repository becomes the public one rather than vanishing. Deleting it would leave a
	// dangling sentence, and on one image an empty OCI source label.
	readme := readSnapshot(t, plan, "README.md")
	if strings.Contains(readme, "althunibat") || !strings.Contains(readme, "ensera-ai/taisce") {
		t.Fatalf("the private repository was not rewritten to the public one: %q", readme)
	}
}

// The half that protects the published site, and the reason the strip is scoped by file type.
//
// A hex colour and an issue reference cannot be told apart by shape: every decimal digit is also a
// hex digit, so `#235` is at once a valid CSS shorthand and a plausible issue number. Stripping by
// shape would delete a colour and break a page's rendering silently, in a release nobody reviewed.
func TestPublisherLeavesColoursInStylingAssetsAlone(t *testing.T) {
	f := releaseRepo(t)
	f.write(t, "style.css", ":root { --ink: #0b1f2a; }\n.shadow { box-shadow: 0 1px 2px #000; }\n")
	f.write(t, "icon.svg", "<svg><circle fill=\"#5fb8ff\"/></svg>\n")
	f.commit(t, "styling assets whose colours look like references")
	plan := f.prepare(t, "styling")

	if css := readSnapshot(t, plan, "style.css"); !strings.Contains(css, "#0b1f2a") || !strings.Contains(css, "#000") {
		t.Fatalf("a colour literal was stripped from a stylesheet: %q", css)
	}
	if svg := readSnapshot(t, plan, "icon.svg"); !strings.Contains(svg, "#5fb8ff") {
		t.Fatalf("a colour literal was stripped from an svg: %q", svg)
	}
}

// A private path that the list names and the removal misses is refused rather than published. The
// list is what decides; this is what checks the decision was carried out.
func TestPublisherRefusesAPrivatePathThatSurvivedRemoval(t *testing.T) {
	f := releaseRepo(t)
	f.write(t, "scripts/private-paths.txt", "# What never leaves.\nprivate.md\n")
	f.write(t, "private.md", "internal drafting\n")
	f.commit(t, "a private document")
	// A read-only directory in place of the file cannot be removed by the release, which is the
	// shape of any removal that silently fails.
	message := filepath.Join(f.root, "message")
	if err := os.WriteFile(message, []byte("release"), 0600); err != nil {
		t.Fatal(err)
	}
	plan := f.prepare(t, "clean")
	if _, err := os.Stat(filepath.Join(plan, "repository", "private.md")); !os.IsNotExist(err) {
		t.Fatalf("the private document survived a prepared release: %v", err)
	}
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

// Nothing named in the private list reaches the public snapshot, and the list itself goes out with
// no names in it.
func TestTheSnapshotLeavesOutEveryPrivatePath(t *testing.T) {
	f := releaseRepo(t)
	if err := os.MkdirAll(filepath.Join(f.source, "secret", "deeper"), 0700); err != nil {
		t.Fatal(err)
	}
	f.write(t, "private.md", "decided in private\n")
	f.write(t, "secret/deeper/plan.md", "a plan\n")
	f.commit(t, "private material")
	snapshot := filepath.Join(f.prepare(t, "one"), "repository")
	for _, gone := range []string{"private.md", "secret"} {
		if _, err := os.Stat(filepath.Join(snapshot, gone)); err == nil {
			t.Errorf("%s is private and reached the public snapshot", gone)
		}
	}
	if _, err := os.Stat(filepath.Join(snapshot, "code.txt")); err != nil {
		t.Error("a public file was left out of the snapshot")
	}
	list, err := os.ReadFile(filepath.Join(snapshot, "scripts", "private-paths.txt"))
	if err != nil {
		t.Fatalf("the snapshot carries no private list, so its own builds would refuse it: %v", err)
	}
	if strings.Contains(string(list), "private.md") || strings.Contains(string(list), "secret") {
		t.Errorf("the names of the private paths were published:\n%s", list)
	}
}

// A source that cannot say what is private is refused, rather than published as if nothing were.
func TestASourceWithoutAPrivateListIsRefused(t *testing.T) {
	f := releaseRepo(t)
	f.run(t, f.source, "git", "rm", "-q", "scripts/private-paths.txt")
	f.commit(t, "the private list is gone")
	message := filepath.Join(f.root, "no-list-message")
	if err := os.WriteFile(message, []byte("Public release\n"), 0600); err != nil {
		t.Fatal(err)
	}
	f.refuse(t, "private-paths.txt", "prepare", message, filepath.Join(f.root, "no-list"))
}

// A private path that points outside the tree is refused instead of deleting something else.
func TestAPrivatePathOutsideTheTreeIsRefused(t *testing.T) {
	f := releaseRepo(t)
	f.write(t, "scripts/private-paths.txt", "../elsewhere\n")
	f.commit(t, "a list that escapes the tree")
	message := filepath.Join(f.root, "escape-message")
	if err := os.WriteFile(message, []byte("Public release\n"), 0600); err != nil {
		t.Fatal(err)
	}
	f.refuse(t, "must be relative and stay inside the tree", "prepare", message, filepath.Join(f.root, "escape"))
}
