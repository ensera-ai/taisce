// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/infra/inference"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// isolatedInstance gives a test an empty database of its own, initialised the way the shipped
// PostgreSQL image initialises one, and returns a pool connected to it.
//
// Bootstrap establishes roles, and roles are cluster-wide rather than database-local, so two
// suites bootstrapping at once would race on the same names. The advisory lock is taken on the
// parent test database to serialise that; the child database is dropped when the test ends.
func isolatedInstance(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	env := complete(t)
	owner, err := pgxpool.New(ctx, env[envMemoryDSN])
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.Close)
	locked, err := owner.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := locked.Exec(ctx, `SELECT pg_advisory_lock(hashtext('taisce:establish-planes')::bigint)`); err != nil {
		t.Fatal(err)
	}
	name := "taisce_instance_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err := owner.Exec(ctx, "CREATE DATABASE "+quoted); err != nil {
		t.Fatal(err)
	}
	// Cleanups run in reverse, so this one is registered before the two that give the lock up and
	// therefore runs after them: the unlock has to reach the connection holding the lock while it
	// still holds it, and the database cannot be dropped while anything is connected to it.
	t.Cleanup(func() {
		if _, err := owner.Exec(context.WithoutCancel(ctx), "DROP DATABASE "+quoted); err != nil {
			t.Errorf("remove isolated database: %v", err)
		}
	})
	t.Cleanup(locked.Release)
	t.Cleanup(func() {
		locked.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock(hashtext('taisce:establish-planes')::bigint)`)
	})

	parsed, err := url.Parse(env[envMemoryDSN])
	if err != nil {
		t.Fatal(err)
	}
	parsed.Path = "/" + name
	for _, key := range []string{envMemoryDSN, envRegistryDSN, envAdminDSN} {
		env[key] = parsed.String()
	}
	env[envSchema] = ""
	env["TAISCE_CONTROL_PASSWORD"] = "taisce-test-control"
	env["TAISCE_DATA_PASSWORD"] = "taisce-test-data"
	withEnv(t, env)
	pool, err := pgxpool.New(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	// Closed before the database is dropped, because a dropped database cannot have connections.
	t.Cleanup(pool.Close)
	initSQL, err := os.ReadFile("../../deploy/postgres/initdb/00-extensions.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(initSQL)); err != nil {
		t.Fatal(err)
	}
	return ctx, pool
}

func TestDefaultInstanceBootstrapRestartAndProjectCreationUseOneMemoryNamespace(t *testing.T) {
	ctx, pool := isolatedInstance(t)
	for i := 0; i < 2; i++ {
		if err := bootstrap(ctx, quiet(), []string{"--project", "first"}, io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	var before int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_namespace`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := dispatch(ctx, quiet(), []string{"project", "create", "second"}); err != nil {
		t.Fatal(err)
	}
	var after, projects, credentials, operators int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM pg_namespace),(SELECT count(*) FROM memory.project),
		(SELECT count(*) FROM control.credential WHERE kind='project'),(SELECT count(*) FROM control.credential WHERE kind='operator')`).Scan(&after, &projects, &credentials, &operators); err != nil {
		t.Fatal(err)
	}
	// Two bootstraps mint one project credential and one operator credential, not two of either.
	if before != after || projects != 2 || credentials != 1 || operators != 1 {
		t.Fatalf("restart or project creation changed namespaces/credentials: %d -> %d, projects=%d credentials=%d operators=%d", before, after, projects, credentials, operators)
	}
	var registry bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema='control' AND table_name LIKE '%tenant%')`).Scan(&registry); err != nil || registry {
		t.Fatalf("unexpected tenant registry: %v %v", registry, err)
	}
}

// The two tokens bootstrap prints are the only copies of themselves, and in a cluster the resource
// holding that output does not survive the next upgrade. Nothing is lost that cannot be minted
// again — but only if the operator is told so beside the tokens, with the command, at the moment
// they are looking at them. Printing a secret and saying nothing about replacing it is what turns
// an ordinary upgrade into a dead end.
//
// This test holds that property for the run that mints; the one below holds it for the run that
// does not, which is the run an operator reaches when they go back for a token already gone.
func TestBootstrapSaysHowToReplaceTheTokensItShowsOnce(t *testing.T) {
	ctx, _ := isolatedInstance(t)
	var shown bytes.Buffer
	if err := bootstrap(ctx, quiet(), []string{"--project", "first"}, &shown); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"operator token: ",
		"only digests are stored",
		"Copy it now",
		"taisce operator issue <name>",
		"taisce credential issue <name> -project first",
		"taisce credential revoke <id>",
	} {
		if !strings.Contains(shown.String(), want) {
			t.Errorf("the output that shows the tokens does not say %q:\n%s", want, shown.String())
		}
	}
}

// A second bootstrap mints nothing, so it shows no token — and must not print advice about tokens
// it did not show, which would read as if something had just been issued. The way out belongs in
// the line that says nothing happened, because that line is the whole of what an operator sees in
// the log of every bootstrap after the first.
func TestABootstrapThatMintsNothingShowsNoTokenAndStillNamesTheWayToMintOne(t *testing.T) {
	ctx, _ := isolatedInstance(t)
	if err := bootstrap(ctx, quiet(), []string{"--project", "first"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	var shown, logged bytes.Buffer
	if err := bootstrap(ctx, slog.New(slog.NewTextHandler(&logged, nil)), []string{"--project", "first"}, &shown); err != nil {
		t.Fatal(err)
	}
	if shown.Len() != 0 {
		t.Errorf("a bootstrap that minted nothing printed something:\n%s", shown.String())
	}
	if !strings.Contains(logged.String(), "no credential minted") {
		t.Errorf("the log does not say that nothing was minted:\n%s", logged.String())
	}
	if !strings.Contains(logged.String(), "taisce credential issue <name> -project first") {
		t.Errorf("the log does not name the command that mints one:\n%s", logged.String())
	}
}

// A bootstrap told not to mint writes no token anywhere, and leaves the door for the operator.
//
// A container's standard output is not a place that forgets. On a cluster that ships logs it is
// copied to the log store the moment it is written, and kept under that store's retention and that
// store's access rules. The first operator credential — the one that can mint a credential for any
// project — was printed there by every install, and outlived the Job that printed it, the release
// that ran the Job, and the argument that deleting the Job removed it.
//
// So where output is a log, bootstrap provisions and mints nothing, and the first credential is
// minted by a command the operator runs themselves, whose output goes to their terminal.
func TestABootstrapToldNotToMintWritesNoTokenAndLeavesNoCredential(t *testing.T) {
	ctx, admin := isolatedInstance(t)
	var shown, logged bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logged, nil))

	for run := 1; run <= 2; run++ {
		if err := bootstrap(ctx, log, []string{"--project", "first", "--mint-credentials=false"}, &shown); err != nil {
			t.Fatalf("bootstrap %d: %v", run, err)
		}
	}
	for name, written := range map[string]string{"standard output": shown.String(), "the log": logged.String()} {
		if strings.Contains(written, credential.Prefix) {
			t.Errorf("a bootstrap told not to mint wrote a token to %s", name)
		}
	}
	if shown.Len() != 0 {
		t.Errorf("a bootstrap that minted nothing printed something:\n%s", shown.String())
	}
	operators, err := operatorCredentials(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	projects, err := credentialsForProject(ctx, admin, "first")
	if err != nil {
		t.Fatal(err)
	}
	if operators != 0 || projects != 0 {
		t.Fatalf("a bootstrap told not to mint left %d operator and %d project credentials", operators, projects)
	}
	// An instance nobody can reach has to say how to reach it, in the one place an operator looks
	// when nothing works: the log of the thing that just ran.
	for _, want := range []string{
		"no credential exists",
		"taisce operator issue <name>",
		"taisce credential issue <name> -project first",
	} {
		if !strings.Contains(logged.String(), want) {
			t.Errorf("the log does not say %q:\n%s", want, logged.String())
		}
	}

	// And the way it names works: the direct path mints against the instance bootstrap left.
	if err := operatorCommand(ctx, []string{"issue", "first-operator"}); err != nil {
		t.Fatalf("the command the log names did not mint an operator credential: %v", err)
	}
	if operators, err = operatorCredentials(ctx, admin); err != nil || operators != 1 {
		t.Fatalf("after the operator minted one, %d operator credentials are live (%v)", operators, err)
	}

	// Once somebody can reach it, a later bootstrap has nothing to say about minting.
	logged.Reset()
	if err := bootstrap(ctx, log, []string{"--project", "first", "--mint-credentials=false"}, &shown); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logged.String(), "no credential exists") && strings.Contains(logged.String(), "operator issue") {
		t.Errorf("a bootstrap on an instance with an operator still says none exists:\n%s", logged.String())
	}
}

// A process waiting for a migration says so once, and says once more when the wait is over.
//
// The readiness probe answers ready or not and nothing else, so the log is the only place an
// operator can learn why a new pod is not becoming ready. A line per probe would bury the one that
// matters; none at all is what there was. Other reasons for not being ready are not this line's to
// report, and pass through untouched.
func TestAProcessWaitingForAMigrationSaysSoOnceAndOnceWhenItEnds(t *testing.T) {
	var logged bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logged, nil))
	w := &schemaWait{}
	behind := fmt.Errorf("%w: it is at version 75 and this binary was built for version 76; waiting for the migration", pg.ErrSchemaBehind)
	other := errors.New("the database is unreachable")

	for i := 0; i < 5; i++ {
		if err := w.note(log, behind); !errors.Is(err, pg.ErrSchemaBehind) {
			t.Fatalf("the reason for not being ready was not passed on: %v", err)
		}
	}
	if n := strings.Count(logged.String(), "not ready"); n != 1 {
		t.Fatalf("five probes during one wait logged the reason %d times:\n%s", n, logged.String())
	}
	for _, want := range []string{"version 75", "version 76", "becomes ready on its own"} {
		if !strings.Contains(logged.String(), want) {
			t.Errorf("the line does not say %q:\n%s", want, logged.String())
		}
	}
	// A different failure in the middle of the wait is returned and is not mistaken for its end...
	if err := w.note(log, other); err != other {
		t.Fatalf("another reason was not passed on unchanged: %v", err)
	}
	logged.Reset()
	// ...so the wait is announced again if it is still going on, and its end is said once.
	_ = w.note(log, behind)
	for i := 0; i < 3; i++ {
		if err := w.note(log, nil); err != nil {
			t.Fatalf("a ready process was reported as not ready: %v", err)
		}
	}
	if n := strings.Count(logged.String(), "has reached the version"); n != 1 {
		t.Fatalf("the end of the wait was logged %d times:\n%s", n, logged.String())

// A generation call is counted under its purpose, and one the provider said nothing about is counted
// as unknown rather than as free.
func TestGenerationCallsAreCountedByPurposeAndUnknownIsNotZero(t *testing.T) {
	ctx, pool := isolatedInstance(t)
	if err := bootstrap(ctx, quiet(), []string{"--project", "first"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	schema, err := pg.NewSchema("memory")
	if err != nil {
		t.Fatal(err)
	}
	measurements := pg.NewMeasurements(pool, schema)
	count := meterCalls(measurements)
	count(inference.Call{Purpose: inference.PurposeExtraction, Reported: true, PromptTokens: 500, CompletionTokens: 60})
	count(inference.Call{Purpose: inference.PurposeExtraction})
	count(inference.Call{Purpose: inference.PurposeCompaction, Reported: true, PromptTokens: 80, CompletionTokens: 2048, Cut: true})
	if err := measurements.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	read, err := pg.ReadMeasurements(ctx, pool, schema)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]int64{
		"model_calls.extraction":             2,
		"model_calls_unmetered.extraction":   1,
		"model_prompt_tokens.extraction":     500,
		"model_completion_tokens.extraction": 60,
		"model_calls.compaction":             1,
		"model_prompt_tokens.compaction":     80,
		"model_completion_tokens.compaction": 2048,
		"model_replies_cut.compaction":       1,
	} {
		if got := read.Counters[name]; got != want {
			t.Errorf("%s is %d, want %d", name, got, want)
		}
	}
	// A call nobody metered adds no tokens, and no counter exists that it did not move.
	if len(read.Counters) != 8 {
		t.Errorf("counters were written that no call moved: %v", read.Counters)
	}
}
