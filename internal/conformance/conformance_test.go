// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package conformance_test

import (
	"context"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/api"
	"github.com/ensera-ai/taisce/internal/conformance"
	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/ensera-ai/taisce/internal/recall"
	"github.com/jackc/pgx/v5/pgxpool"
)

// helperEnv makes the test binary serve the reference driver over stdio when set, which is how
// the subprocess protocol is proved end to end without a second binary.
const helperEnv = "TAISCE_CONFORMANCE_SERVE_REFERENCE"

func TestMain(m *testing.M) {
	if os.Getenv(helperEnv) == "1" {
		if err := conformance.Serve(context.Background(), os.Stdin, os.Stdout, &conformance.Reference{}); err != nil {
			os.Stderr.WriteString(err.Error() + "\n")
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// deployment is a live API over the test database with one project and a write-enabled
// credential: what an adapter's conformance run points at, minus the network.
func deployment(t *testing.T) conformance.Deployment {
	t.Helper()
	ctx := context.Background()
	dsn := os.Getenv("TAISCE_TEST_DSN")
	if dsn == "" {
		t.Skip("TAISCE_TEST_DSN is not set")
	}
	const name = "conformance"
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.EstablishPlanes(ctx, pool, "taisce-test-control", "taisce-test-data"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "DROP SCHEMA IF EXISTS "+name+" CASCADE"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DROP SCHEMA IF EXISTS "+name+" CASCADE") })
	if err := migrate.ProvisionMemorySchema(ctx, pool, name); err != nil {
		t.Fatal(err)
	}
	if err := migrate.ProvisionScope(ctx, pool, name, "p1"); err != nil {
		t.Fatal(err)
	}
	schema, err := pg.NewSchema(name)
	if err != nil {
		t.Fatal(err)
	}
	credentials := credential.NewStore(pool, string(migrate.ControlSchema))
	token, _, err := credentials.IssueWithAccess(ctx, "conformance-test", "p1", credential.ReadWrite)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM `+string(migrate.ControlSchema)+`.credential WHERE name='conformance-test'`)
	})
	server := httptest.NewServer(api.NewServer(credentials, api.Stores{
		Audit: pg.NewAuditStore(pool, schema), Exporter: pg.NewExporter(pool, schema),
		Projects: pg.NewProjectStore(pool, schema), Observations: pg.NewObservationStore(pool),
		Eraser: pg.NewEraser(pool), Recaller: recall.NewWithBudget(pg.NewRecallStore(pool, schema), recall.Budget{Characters: 100000, MaxRows: 50}),
		Citations: pg.NewCitationStore(pool, schema), Records: pg.NewRecordStore(pool, schema),
		Artifacts: pg.NewArtifactStore(pool, schema), Subjects: pg.NewSubjectStore(pool, schema),
	}, schema, nil).Handler())
	t.Cleanup(server.Close)
	return conformance.Deployment{API: server.URL, Token: token}
}

func suite(t *testing.T) conformance.Suite {
	t.Helper()
	s, err := conformance.Load("../../conformance/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// The suite passes with an adapter that keeps every rule, against a live deployment, with the
// memory message carrying the seeded fact, the watermark and the plan.
func TestTheReferenceAdapterPassesEveryCase(t *testing.T) {
	results, err := conformance.Run(context.Background(), deployment(t), &conformance.Reference{}, suite(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 10 {
		t.Fatalf("expected ten cases, got %d", len(results))
	}
	for _, r := range results {
		if !r.Passed {
			t.Errorf("%s: %v", r.Case, r.Failures)
		}
	}
}

// Every rule is one the suite has been seen to refuse: an adapter that breaks it fails the case
// that holds it, by name, and passes the others. A suite that has never failed has asserted
// nothing; this is where each assertion is watched failing.
func TestTheSuiteRefusesEachBrokenRule(t *testing.T) {
	dep := deployment(t)
	s := suite(t)
	mutants := []struct {
		name   string
		driver *conformance.Reference
		fails  string
		reason string
	}{
		{"memory as a system message", &conformance.Reference{InjectAsRole: "system"}, "memory_is_one_untrusted_user_message", "must be \"user\""},
		{"memory as an assistant message", &conformance.Reference{InjectAsRole: "assistant"}, "memory_is_one_untrusted_user_message", "must be \"user\""},
		{"memory not marked untrusted", &conformance.Reference{UnmarkedMemory: true}, "memory_is_one_untrusted_user_message", "not marked untrusted"},
		{"recall failure raised", &conformance.Reference{RecallFatal: true}, "recall_failure_is_not_fatal", "fatal=true"},
		{"write failure swallowed", &conformance.Reference{SilentWriteFailure: true}, "write_failure_is_never_silent", "surfaced nothing"},
		{"stored after the model failed", &conformance.Reference{StoreOnFailure: true}, "store_only_on_success", "failed turn is not a memory"},
		{"tool traffic stored", &conformance.Reference{StoreToolTraffic: true}, "filters_default_to_external_messages", "internal tool traffic"},
		{"synthetic message stored", &conformance.Reference{StoreSynthetic: true}, "no_synthetic_message_reaches_observe", "never observed"},
		{"memory message stored", &conformance.Reference{StoreMemoryMessage: true}, "no_synthetic_message_reaches_observe", "never observed"},
		{"compaction written locally", &conformance.Reference{CompactLocally: true}, "compaction_hands_the_model_the_deployments_context", "expected 1 memory message(s)"},
		{"history handed beside the context", &conformance.Reference{KeepHistory: true}, "compaction_hands_the_model_the_deployments_context", "which the compaction replaced"},
		{"the person's message dropped", &conformance.Reference{DropInput: true}, "compaction_hands_the_model_the_deployments_context", "not handed"},
		{"context failure raised", &conformance.Reference{ContextFatal: true}, "context_failure_is_not_fatal", "fatal=true"},
		{"a fresh key on every store", &conformance.Reference{FreshKeyEachRun: true}, "a_retried_turn_is_stored_once", "different idempotency keys"},
		{"every message under one group", &conformance.Reference{CollapseGroups: true}, "filters_default_to_external_messages", "must not be observed as one"},
	}
	for _, m := range mutants {
		results, err := conformance.Run(context.Background(), dep, m.driver, s)
		if err != nil {
			t.Fatalf("%s: %v", m.name, err)
		}
		failed := map[string][]string{}
		for _, r := range results {
			if !r.Passed {
				failed[r.Case] = r.Failures
			}
		}
		reasons, ok := failed[m.fails]
		if !ok {
			t.Errorf("%s: the suite did not fail %s; failures were %v", m.name, m.fails, failed)
			continue
		}
		if !strings.Contains(strings.Join(reasons, " "), m.reason) {
			t.Errorf("%s: %s failed for another reason: %v", m.name, m.fails, reasons)
		}
	}
}

// The subprocess protocol carries the whole suite: the reference served over stdio by this test
// binary passes every case exactly as it does in-process.
func TestADriverOverTheSubprocessProtocolPasses(t *testing.T) {
	t.Setenv(helperEnv, "1")
	driver := &conformance.Subprocess{Command: []string{os.Args[0]}, Stderr: os.Stderr}
	t.Cleanup(func() { _ = driver.Close() })
	results, err := conformance.Run(context.Background(), deployment(t), driver, suite(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if !r.Passed {
			t.Errorf("%s: %v", r.Case, r.Failures)
		}
	}
	if err := driver.Close(); err != nil {
		t.Fatalf("the driver did not exit cleanly: %v", err)
	}
}

// The memory message is one rendering: what the reference renders is what the parser reads, and
// anything else is not a memory message.
func TestTheMemoryMessageIsOneRendering(t *testing.T) {
	stored := int64(4)
	content, err := conformance.RenderMemoryMessage(conformance.MemoryMessage{Watermark: conformance.Watermark{Stored: &stored}, Plan: []byte(`{"hops":1}`), Facts: []byte(`[{"statement":"x"}]`)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(content, conformance.MemoryMessagePrefix+"\n") {
		t.Fatalf("unexpected rendering %q", content)
	}
	m, err := conformance.ParseMemoryMessage(content)
	if err != nil || m.Watermark.Stored == nil || *m.Watermark.Stored != 4 {
		t.Fatalf("round trip failed: %+v %v", m, err)
	}
	for _, not := range []string{"hello", "taisce-memory/v1 untrusted", "taisce-memory/v1 untrusted\nnot json", "taisce-memory/v2 untrusted\n{}"} {
		if _, err := conformance.ParseMemoryMessage(not); err == nil {
			t.Fatalf("%q must not parse as a memory message", not)
		}
	}
}

// A suite file with a field this runner does not know, another prefix, or no cases is refused,
// so a suite and a runner that disagree cannot produce a passing run of nothing.
func TestAMismatchedSuiteFileIsRefused(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		path := dir + "/" + name
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	for _, bad := range []string{
		`{"suite":"x","memory_message_prefix":"taisce-memory/v1 untrusted","cases":[]}`,
		`{"suite":"x","memory_message_prefix":"other","cases":[{"name":"a","turn":{"user":"hi"},"expect":{}}]}`,
		`{"suite":"x","memory_message_prefix":"taisce-memory/v1 untrusted","cases":[{"name":"a","turn":{"user":"hi"},"expect":{"unknown":1}}]}`,
	} {
		if _, err := conformance.Load(write("suite.json", bad)); err == nil {
			t.Fatalf("expected refusal for %s", bad)
		}
	}
	if _, err := conformance.Load(dir + "/missing.json"); err == nil {
		t.Fatal("a missing suite must be an error")
	}
}

// A run whose seeding or reading the deployment refuses stops with the reason rather than judging
// a turn against a deployment it could not prepare: a credential the deployment does not know, and
// a seed with a relation the vocabulary refuses.
func TestARunTheDeploymentRefusesStopsWithTheReason(t *testing.T) {
	dep := deployment(t)
	s := suite(t)
	if _, err := conformance.Run(context.Background(), conformance.Deployment{API: dep.API, Token: "not-a-credential"}, &conformance.Reference{}, s); err == nil || !strings.Contains(err.Error(), "could not run") {
		t.Fatalf("an unknown credential must stop the run by reason, got %v", err)
	}
	bad := s
	bad.Cases = []conformance.Case{{Name: "bad_seed", Seed: []conformance.Seed{{Subject: "Marta", Predicate: "not_a_relation", Object: "x", Statement: "x"}}, Turn: conformance.Turn{User: "hi"}}}
	if _, err := conformance.Run(context.Background(), dep, &conformance.Reference{}, bad); err == nil || !strings.Contains(err.Error(), "seeding refused") {
		t.Fatalf("a refused seed must stop the run by reason, got %v", err)
	}
	// A driver that errors stops the run too: the suite cannot judge what did not happen.
	broken := &conformance.Subprocess{Command: []string{"sh", "-c", "exit 1"}}
	if _, err := conformance.Run(context.Background(), dep, broken, s); err == nil || !strings.Contains(err.Error(), "driver") {
		t.Fatalf("a driver that dies must stop the run by name, got %v", err)
	}
}

// A credential the deployment refuses stops a seedless case at the first read: freshness answers
// with its status and the run says so.
func TestAnUnreadableWatermarkStopsTheRun(t *testing.T) {
	dep := deployment(t)
	s := suite(t)
	s.Cases = []conformance.Case{{Name: "seedless", Turn: conformance.Turn{User: "hi"}}}
	_, err := conformance.Run(context.Background(), conformance.Deployment{API: dep.API, Token: "not-a-credential"}, &conformance.Reference{}, s)
	if err == nil || !strings.Contains(err.Error(), "freshness answered 401") {
		t.Fatalf("expected the watermark refusal by status, got %v", err)
	}
}
