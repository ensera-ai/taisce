// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ensera-ai/taisce/internal/migrate"
)

// A model that writes one report for whatever it is asked about. Enough to prove the command reaches
// the provider, stamps what it wrote and closes the window; the quality of a report is measured
// against a real model in docs/26, not here.
func reportingModel(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"title\":\"The Dublin office\",` +
			`\"summary\":\"Two people work at one organisation in Dublin.\",\"importance\":5,` +
			`\"importance_reason\":\"a small but connected group\",\"findings\":[]}"}}]}`))
	}))
	t.Cleanup(server.Close)
	return server
}

// ── A report carries what wrote it, at the command ────────────────────────────────────────────
//
// A report is deleted when the facts under it change and the worker writes it back a few per tick,
// so there is a window in which a recall that would return a theme returns nothing. `rebuild
// staleness` measures that window; this command is how an operator closes it without waiting.
func TestRebuildReportsClosesTheWindowAndSaysWhatIsLeft(t *testing.T) {
	ctx := context.Background()
	model := reportingModel(t)
	env := complete(t)
	env[envSchema] = "cmd_rebuild_reports"
	env[envAdminDSN] = env[envMemoryDSN]
	withEnv(t, env)
	t.Setenv("TAISCE_INFERENCE_ENDPOINT", model.URL+"/v1")
	t.Setenv("TAISCE_INFERENCE_EXTRACTOR_MODEL", "report-rebuild")
	t.Setenv("TAISCE_INFERENCE_ALLOWLIST", strings.TrimPrefix(model.URL, "http://"))

	pool, err := pgxpool.New(ctx, env[envAdminDSN])
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	dropSchema(t, env[envSchema])
	defer pool.Exec(ctx, "DROP SCHEMA "+env[envSchema]+" CASCADE")
	if err := migrate.ProvisionMemorySchema(ctx, pool, env[envSchema]); err != nil {
		t.Fatal(err)
	}
	if err := migrate.ProvisionScope(ctx, pool, env[envSchema], "p1"); err != nil {
		t.Fatal(err)
	}

	// A project with no graph: nothing to partition and nothing to write. Reported as zeros rather
	// than refused, because "there was nothing missing" is an answer an operator asked for.
	var empty bytes.Buffer
	if err := rebuildCommand(ctx, []string{"reports", "--project", "p1"}, &empty); err != nil {
		t.Fatal(err)
	}
	var quiet map[string]any
	if err := json.Unmarshal(empty.Bytes(), &quiet); err != nil {
		t.Fatalf("the output did not parse: %v", err)
	}
	if quiet["reports_written"] != float64(0) || quiet["communities"] != float64(0) {
		t.Fatalf("an empty project reported work: %v", quiet)
	}
	if _, told := quiet["stale"]; told {
		t.Fatalf("a project with nothing stale was told about staleness: %v", quiet)
	}

	// Two entities and a relation between them: a graph the partition can find a subject in.
	seedReportableGraph(t, ctx, pool, env[envSchema])

	var written bytes.Buffer
	if err := rebuildCommand(ctx, []string{"reports", "--project", "p1"}, &written); err != nil {
		t.Fatal(err)
	}
	var done map[string]any
	if err := json.Unmarshal(written.Bytes(), &done); err != nil {
		t.Fatal(err)
	}
	if done["reports_written"] != float64(1) || done["reports_failed"] != float64(0) {
		t.Fatalf("the missing report was not written: %v", done)
	}
	if done["communities"] != float64(1) || done["entities"] != float64(2) {
		t.Fatalf("the partition is not reported: %v", done)
	}
	// The window is closed, so nothing is stale and nothing is advised. That absence is the point:
	// the command exists to make a count go to zero, and saying so is how an operator knows to stop.
	if _, told := done["stale"]; told {
		t.Fatalf("the window was not closed: %v", done["stale"])
	}

	var stored int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM `+env[envSchema]+
		`.community_report WHERE scope='p1' AND written_by <> ''`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != 1 {
		t.Fatalf("%d reports carry the identity that wrote them", stored)
	}

	// Running it again writes nothing: `Unwritten` selects on the absence of a current report, so a
	// second invocation is not a second model call for work already done.
	var again bytes.Buffer
	if err := rebuildCommand(ctx, []string{"reports", "--project", "p1"}, &again); err != nil {
		t.Fatal(err)
	}
	var repeated map[string]any
	if err := json.Unmarshal(again.Bytes(), &repeated); err != nil {
		t.Fatal(err)
	}
	if repeated["reports_written"] != float64(0) {
		t.Fatalf("a second invocation rewrote what was already there: %v", repeated)
	}
}

// The fancy form is what an operator at a terminal sees, and it says which of three things happened
// rather than printing a count they have to interpret.
func TestRebuildReportsRendersForATerminalAndRefusesAProjectThatIsNotThere(t *testing.T) {
	ctx := context.Background()
	model := reportingModel(t)
	env := complete(t)
	env[envSchema] = "cmd_rebuild_reports_fancy"
	env[envAdminDSN] = env[envMemoryDSN]
	withEnv(t, env)
	t.Setenv("TAISCE_INFERENCE_ENDPOINT", model.URL+"/v1")
	t.Setenv("TAISCE_INFERENCE_EXTRACTOR_MODEL", "report-rebuild")
	t.Setenv("TAISCE_INFERENCE_ALLOWLIST", strings.TrimPrefix(model.URL, "http://"))

	pool, err := pgxpool.New(ctx, env[envAdminDSN])
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	dropSchema(t, env[envSchema])
	defer pool.Exec(ctx, "DROP SCHEMA "+env[envSchema]+" CASCADE")
	if err := migrate.ProvisionMemorySchema(ctx, pool, env[envSchema]); err != nil {
		t.Fatal(err)
	}
	if err := migrate.ProvisionScope(ctx, pool, env[envSchema], "p1"); err != nil {
		t.Fatal(err)
	}
	seedReportableGraph(t, ctx, pool, env[envSchema])

	t.Setenv(envCLIOutput, "fancy")
	var rendered bytes.Buffer
	if err := rebuildCommand(ctx, []string{"reports", "--project", "p1"}, &rendered); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered.String(), "reports written") || !strings.Contains(rendered.String(), "1 over 2 entities") {
		t.Fatalf("the terminal form does not say what happened: %q", rendered.String())
	}

	// A project this deployment does not have is refused after the flags and before the pass, so it
	// is never a model call — and the refusal names no project back to the caller.
	var missing bytes.Buffer
	if err := rebuildCommand(ctx, []string{"reports", "--project", "p2"}, &missing); err == nil {
		t.Fatalf("a project that is not there was accepted: %s", missing.String())
	}
	if missing.Len() != 0 {
		t.Fatalf("a refused project wrote output: %s", missing.String())
	}

	// And with no model configured it refuses before opening the pool: a rewrite needs a provider,
	// unlike `rebuild status` and `rebuild cancel`, and saying so early is the difference between an
	// operator reading one error and reading a connection failure that does not explain itself.
	t.Setenv("TAISCE_INFERENCE_EXTRACTOR_MODEL", "")
	var unconfigured bytes.Buffer
	if err := rebuildCommand(ctx, []string{"reports", "--project", "p1"}, &unconfigured); err == nil {
		t.Fatal("an unconfigured deployment wrote reports")
	}
}

// The bounds are refused before the provider is reached or the pool is opened, so a mistyped limit
// costs nothing and a project that is not there is not a model call.
func TestRebuildReportsRefusesAnUnusableRequestBeforeCallingAnything(t *testing.T) {
	ctx := context.Background()
	env := complete(t)
	env[envSchema] = "cmd_rebuild_reports_refused"
	env[envAdminDSN] = env[envMemoryDSN]
	withEnv(t, env)
	for _, args := range [][]string{
		{"reports"},
		{"reports", "--project", "p1", "--limit", "0"},
		{"reports", "--project", "p1", "--limit", "101"},
		{"reports", "--project", "not a project"},
		{"reports", "--project", "p1", "leftover"},
	} {
		var out bytes.Buffer
		if err := rebuildCommand(ctx, args, &out); err == nil {
			t.Fatalf("%v was accepted: %s", args, out.String())
		}
		if out.Len() != 0 {
			t.Fatalf("%v wrote output before refusing: %s", args, out.String())
		}
	}
}

// seedReportableGraph writes two entities and a fact between them, which is the least a partition
// needs to produce one community with material a report can be written from.
func seedReportableGraph(t *testing.T, ctx context.Context, pool *pgxpool.Pool, schema string) {
	t.Helper()
	var source string
	if err := pool.QueryRow(ctx, `INSERT INTO `+schema+`.observation
        (observation_id, scope, log_offset, kind, occurred_at, ingested_at, source_role, data_subject_id)
        VALUES (gen_random_uuid(), 'p1', 0, 'turn', now(), now(), 'user', 'subject-1')
        RETURNING observation_id::text`).Scan(&source); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO `+schema+`.entity
        (entity_id, scope, canonical_name, normalized_name, identity_kind, entity_type)
        VALUES (md5('marta')::uuid, 'p1', 'Marta', 'marta', 'named', 'person'),
               (md5('ensera')::uuid, 'p1', 'Ensera', 'ensera', 'named', 'organisation')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO `+schema+`.fact
        (fact_id, scope, subject_entity_id, object_entity_id, predicate, statement, cardinality,
         valid, source_role, data_subject_id)
        VALUES (md5('marta works at ensera')::uuid, 'p1', md5('marta')::uuid, md5('ensera')::uuid,
                'works_at', 'Marta works at Ensera', 'many', '[2020-01-01,)'::tstzrange, 'user', 'subject-1')`); err != nil {
		t.Fatal(err)
	}
	// The evidence row is what a report's provenance is registered from, so it is here rather than
	// a receipt: the pass reads the words behind a fact, not the formation checkpoint above it.
	if _, err := pool.Exec(ctx, `INSERT INTO `+schema+`.turn_message
        (observation_id, ordinal, role, content, group_ordinal, occurred_at)
        VALUES ($1::uuid, 0, 'user', 'Marta works at Ensera', 0, now())`, source); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO `+schema+`.fact_evidence
        (fact_id, scope, source_observation_id, source_ordinal, byte_start, byte_end, quote, extractor_version)
        VALUES (md5('marta works at ensera')::uuid, 'p1', $1::uuid, 0, 0, 21, 'Marta works at Ensera', 'fixture')`,
		source); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO `+schema+`.projection_dependency
        (source_observation_id, scope, projection_kind, projection_id, data_subject_id)
        VALUES ($1::uuid, 'p1', 'fact', md5('marta works at ensera')::text, 'subject-1'),
               ($1::uuid, 'p1', 'entity', md5('marta')::text, 'subject-1'),
               ($1::uuid, 'p1', 'entity', md5('ensera')::text, 'subject-1')`, source); err != nil {
		t.Fatal(err)
	}
}

// The operator's turn budget bounds a report as it bounds an extraction, and the command
// reads it before it connects: a budget that cannot be a bound is refused rather than ignored,
// which is how a mistyped setting is found at the command rather than in a half-run pass.
func TestRebuildReportsHonoursTheOperatorsTurnBudget(t *testing.T) {
	model := reportingModel(t)
	t.Setenv("TAISCE_INFERENCE_ENDPOINT", model.URL+"/v1")
	t.Setenv("TAISCE_INFERENCE_EXTRACTOR_MODEL", "report-rebuild")
	t.Setenv("TAISCE_INFERENCE_ALLOWLIST", strings.TrimPrefix(model.URL, "http://"))
	t.Setenv(envAdminDSN, "")
	t.Setenv(envMemoryDSN, "")
	t.Setenv(envTurnBudget, "soon")
	err := reportRebuildCommand(context.Background(), []string{"--project", "p1"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), envTurnBudget) {
		t.Fatalf("a budget that cannot bound must be refused before connecting, got %v", err)
	}
}
