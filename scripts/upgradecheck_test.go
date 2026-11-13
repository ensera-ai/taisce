// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// released builds a repository with one release in it and one commit after: a migration in each
// plane, and a package whose test opens the database and stands for what that release needs from
// the schema — it fails if a migration drops the column it reads.
//
// The commit after the release rewrites that test to pass whatever the schema is. So a check that
// ran this tree's tests, and not the release's, would pass where it must fail.
func released(t *testing.T) (repo string) {
	t.Helper()
	repo = t.TempDir()
	write := func(path, contents string) {
		t.Helper()
		full := filepath.Join(repo, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.test",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.test", "GIT_CONFIG_GLOBAL=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write("go.mod", "module example.test/released\n\ngo "+strings.TrimPrefix(runtime.Version(), "go")+"\n")
	write("internal/migrate/sql/0001_the_first.sql", "CREATE TABLE {schema}.thing (kept text);\n")
	write("internal/migrate/control/0001_the_first.sql", "CREATE TABLE control.thing (kept text);\n")
	write("store/store_test.go", `package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTheReleaseStillFindsTheColumnItReads(t *testing.T) {
	_ = os.Getenv("TAISCE_TEST_DSN")
	for _, plane := range []string{"sql", "control"} {
		files, _ := filepath.Glob("../internal/migrate/" + plane + "/*.sql")
		for _, file := range files {
			raw, _ := os.ReadFile(file)
			if strings.Contains(string(raw), "DROP COLUMN kept") {
				t.Fatalf("%s drops the column this release reads", filepath.Base(file))
			}
		}
	}
}

// A test that depends on timing, as a published release may carry: it fails the first time it is
// run and passes after, when the environment asks it to.
func TestThatIsSlowTheFirstTime(t *testing.T) {
	marker := os.Getenv("FAILS_THE_FIRST_TIME")
	if marker == "" {
		return
	}
	if _, err := os.Stat(marker); err != nil {
		_ = os.WriteFile(marker, nil, 0o600)
		t.Fatal("the runner was slow this time")
	}
}
`)
	git("init", "--quiet", "--initial-branch=main")
	git("add", "-A")
	git("commit", "--quiet", "-m", "the release")
	git("tag", "v0.1.0")
	write("store/store_test.go", "package store\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestNothing(t *testing.T) { _ = os.Getenv(\"TAISCE_TEST_DSN\") }\n")
	git("add", "-A")
	git("commit", "--quiet", "-m", "after the release")
	return repo
}

// upgradeCheck runs the script in a repository, with or without a database named.
func upgradeCheck(t *testing.T, repo string, database bool) (string, error) {
	t.Helper()
	script, err := filepath.Abs("upgrade-check.sh")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(script)
	cmd.Dir = repo
	for _, kept := range os.Environ() {
		if !strings.HasPrefix(kept, "TAISCE_TEST_DSN=") && !strings.HasPrefix(kept, "GOFLAGS=") {
			cmd.Env = append(cmd.Env, kept)
		}
	}
	// The stand-in suite reads the variable and opens nothing, so any value will do. What is under
	// test is the check's own behaviour; docs/postgresql/migrations.md records it run for real.
	if database {
		cmd.Env = append(cmd.Env, "TAISCE_TEST_DSN=postgres://unused.invalid/none")
	}
	cmd.Env = append(cmd.Env, "GIT_CONFIG_GLOBAL=/dev/null", "GOWORK=off")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestAMigrationThePreviousReleaseCannotServeOnIsRefused holds what an upgrade promises.
//
// An upgrade is a roll, so processes of the previous release serve on the new schema until the last
// of them is replaced. The promise is that they can, and the check is that release's own tests run
// against this tree's migrations. Here a release is built that reads a column, and each kind of
// change is made after it.
func TestAMigrationThePreviousReleaseCannotServeOnIsRefused(t *testing.T) {
	add := func(repo, path, contents string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, path), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("nothing added is the schema the release was tested on", func(t *testing.T) {
		out, err := upgradeCheck(t, released(t), false)
		if err != nil || !strings.Contains(out, "no migration has been added since v0.1.0") {
			t.Fatalf("an unchanged schema must pass, and say why it ran nothing: %v\n%s", err, out)
		}
	})

	t.Run("an addition the release never reads passes, on the release's own tests", func(t *testing.T) {
		repo := released(t)
		add(repo, "internal/migrate/sql/0002_a_thing_may_have_more.sql", "ALTER TABLE {schema}.thing ADD COLUMN more text;\n")
		out, err := upgradeCheck(t, repo, true)
		if err != nil || !strings.Contains(out, "v0.1.0 passes on this tree's schema") {
			t.Fatalf("an addition was refused: %v\n%s", err, out)
		}
		if !strings.Contains(out, "0002_a_thing_may_have_more.sql") || !strings.Contains(out, "./store") {
			t.Errorf("the check must say which migrations it found and which packages it ran:\n%s", out)
		}
	})

	// The previous release's suite cannot be edited, so a test in it that depends on timing would
	// fail every change at its own rate and blame the schema each time.
	t.Run("a failure of the release's own test that does not repeat is reported and not believed", func(t *testing.T) {
		repo := released(t)
		add(repo, "internal/migrate/sql/0002_a_thing_may_have_more.sql", "ALTER TABLE {schema}.thing ADD COLUMN more text;\n")
		t.Setenv("FAILS_THE_FIRST_TIME", filepath.Join(t.TempDir(), "ran-once"))
		out, err := upgradeCheck(t, repo, true)
		if err != nil {
			t.Fatalf("a test that failed once and then passed failed the check:\n%s", out)
		}
		for _, want := range []string{"TestThatIsSlowTheFirstTime", "run once more", "example.test/released/store", "on a second run", "depends on timing"} {
			if !strings.Contains(out, want) {
				t.Errorf("the check must show the first failure and say the pass was a second run; it lacks %q:\n%s", want, out)
			}
		}
		if strings.Contains(out, "v0.1.0 passes on this tree's schema\n") {
			t.Errorf("a pass on a second run was reported in the words of a clean pass:\n%s", out)
		}
	})

	for _, plane := range []string{"sql", "control"} {
		t.Run("a removal of what the release reads is refused, in "+plane, func(t *testing.T) {
			repo := released(t)
			add(repo, "internal/migrate/"+plane+"/0002_a_thing_keeps_less.sql", "ALTER TABLE thing DROP COLUMN kept;\n")
			out, err := upgradeCheck(t, repo, true)
			if err == nil {
				t.Fatalf("a migration that drops what the previous release reads was accepted:\n%s", out)
			}
			// Refused by the release's own test, by name — not by this tree's, which passes anything.
			if !strings.Contains(out, "TestTheReleaseStillFindsTheColumnItReads") || !strings.Contains(out, "v0.1.0 does not pass on this tree's schema") {
				t.Errorf("the refusal must come from the previous release's test and say so:\n%s", out)
			}
			// What the schema broke is broken on the second run too, and that is what refuses it.
			if strings.Count(out, "--- FAIL: TestTheReleaseStillFindsTheColumnItReads") != 2 || !strings.Contains(out, "run once more") {
				t.Errorf("a failure that repeats must be seen to repeat before it is refused:\n%s", out)
			}
		})
	}

	t.Run("with migrations to check and no database, it refuses rather than passes", func(t *testing.T) {
		repo := released(t)
		add(repo, "internal/migrate/sql/0002_a_thing_may_have_more.sql", "ALTER TABLE {schema}.thing ADD COLUMN more text;\n")
		out, err := upgradeCheck(t, repo, false)
		if err == nil || !strings.Contains(out, "TAISCE_TEST_DSN is not set") {
			t.Fatalf("a check that could not run its tests must fail, got %v\n%s", err, out)
		}
	})

	t.Run("a released migration is not edited", func(t *testing.T) {
		repo := released(t)
		add(repo, "internal/migrate/sql/0001_the_first.sql", "CREATE TABLE {schema}.thing (kept text, sneaked text);\n")
		out, err := upgradeCheck(t, repo, true)
		if err == nil || !strings.Contains(out, "a migration released in v0.1.0 has been changed") || !strings.Contains(out, "0001_the_first.sql") {
			t.Fatalf("an edit to a released migration must be refused by name, got %v\n%s", err, out)
		}
	})

	t.Run("a released migration is not removed", func(t *testing.T) {
		repo := released(t)
		if err := os.Remove(filepath.Join(repo, "internal/migrate/control/0001_the_first.sql")); err != nil {
			t.Fatal(err)
		}
		out, err := upgradeCheck(t, repo, true)
		if err == nil || !strings.Contains(out, "a migration released in v0.1.0 has been changed") {
			t.Fatalf("a removed released migration must be refused, got %v\n%s", err, out)
		}
	})

	t.Run("with no release to compare against, it refuses rather than passes", func(t *testing.T) {
		repo := released(t)
		cmd := exec.Command("git", "tag", "-d", "v0.1.0")
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		out, err := upgradeCheck(t, repo, true)
		if err == nil || !strings.Contains(out, "no release tag is reachable") {
			t.Fatalf("a checkout with no tags must fail and say what to fetch, got %v\n%s", err, out)
		}
	})
}

// TestTheUpgradeCheckRunsWhereverTheSuiteDoes holds the check to being run, and to having what it
// needs when it is.
//
// It is the last step of `make test`, so it runs in the build, in the release's gate and in `make
// gate` without any of them naming it. It finds the previous release by tag, and a hosted checkout
// is one commit deep with no tags unless asked — where the check refuses, correctly, and the build
// fails for a reason nobody caused. So every job that runs the suite checks out the whole history.
func TestTheUpgradeCheckRunsWhereverTheSuiteDoes(t *testing.T) {
	makefile, err := os.ReadFile("../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	_, recipe, found := strings.Cut(string(makefile), "\ntest: licence\n")
	recipe, _, _ = strings.Cut(recipe, "\n\n")
	if !found || !strings.Contains(recipe, "./scripts/upgrade-check.sh") {
		t.Errorf("`make test` no longer ends with the upgrade check, so a migration is merged without it:\n%s", recipe)
	}

	paths, err := filepath.Glob("../.github/workflows/*.yml")
	if err != nil || len(paths) == 0 {
		t.Fatalf("no workflows found: %v", err)
	}
	ran := 0
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var workflow struct {
			Jobs map[string]struct {
				Steps []struct {
					Uses string         `yaml:"uses"`
					With map[string]any `yaml:"with"`
					Run  string         `yaml:"run"`
				} `yaml:"steps"`
			} `yaml:"jobs"`
		}
		if err := yaml.Unmarshal(raw, &workflow); err != nil {
			t.Fatalf("%s does not parse: %v", path, err)
		}
		for name, job := range workflow.Jobs {
			whole, suite := false, false
			for _, step := range job.Steps {
				if strings.HasPrefix(step.Uses, "actions/checkout@") && step.With["fetch-depth"] == 0 {
					whole = true
				}
				if strings.Contains(step.Run, "make test ") || strings.HasSuffix(strings.TrimSpace(step.Run), "make test") {
					suite = true
				}
			}
			if suite {
				ran++
				if !whole {
					t.Errorf("%s, job %s runs `make test` on a checkout without its history and tags (fetch-depth: 0), so the upgrade check has no release to compare against",
						filepath.Base(path), name)
				}
			}
		}
	}
	if ran < 2 {
		t.Fatalf("found %d jobs that run the suite; the build and the release's gate both do, so this test is no longer reading them", ran)
	}
}
