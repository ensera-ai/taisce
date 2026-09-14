// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package formation_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/extract"
	"github.com/ensera-ai/taisce/internal/formation"
	"github.com/ensera-ai/taisce/internal/infra/inference"
	"github.com/ensera-ai/taisce/internal/infra/pg"
)

// A turn that fails to form is reported in the worker's log, and the report carries none of its words.
//
// A drain records a failure on the turn and moves on, so without a report of its own a pass that
// failed every turn logs exactly what an idle pass logs: nothing. An endpoint rejecting the key was
// indistinguishable from a worker with no work until the backlog had parked.
//
// The endpoint here is a real HTTP server answering 401 with a body that quotes the message back,
// because that is the case the log must survive: the reason is kept on the turn, and the log gets the
// counts and the status only.
func TestATurnThatFailsToFormIsLoggedWithItsStatusAndNoneOfItsWords(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "fm_failure_log")

	const said = "Alice lives in Dublin"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"rejected request: ` + said + `"}}`))
	}))
	defer server.Close()

	vocabulary, err := pg.LoadVocabulary(ctx, pool, schema)
	if err != nil {
		t.Fatalf("load vocabulary: %v", err)
	}
	observations := pg.NewObservationStore(pool)
	former := formation.NewFormer(observations, pg.NewFactStore(pool),
		extract.NewWith(inference.NewModel(inference.Config{Endpoint: server.URL, Model: "m"}), vocabulary))
	policy := impatient()
	var logged bytes.Buffer
	driver := formation.NewDriver(formation.NewWorkerWithPolicy(pool, observations, former, policy),
		observations, pg.NewRetentionStore(pool, schema), pg.NewAuditStore(pool, schema),
		schema, policy, slog.New(slog.NewJSONHandler(&logged, nil)))

	if _, err := observations.Append(ctx, schema, turnOf(
		domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: said},
	)); err != nil {
		t.Fatalf("append: %v", err)
	}

	// With the wait taken out, one pass retries the turn until it parks. Every attempt is counted, and
	// the pass still writes one line rather than one per attempt.
	pass, err := driver.Once(ctx)
	if err != nil {
		t.Fatalf("pass: %v", err)
	}
	if pass.Failed != policy.MaxAttempts || pass.Parked != 1 || pass.Status != http.StatusUnauthorized {
		t.Fatalf("pass reported %+v, wanted %d failures, 1 parked, status 401", pass, policy.MaxAttempts)
	}
	// Still not a pass error. The drain did its job by recording the failure on the turn.
	if pass.Errored != 0 {
		t.Fatalf("a refused turn was reported as a pass error: %+v", pass)
	}

	line := warning(t, &logged, "turns did not form")
	if line["failed"] != float64(policy.MaxAttempts) || line["parked"] != float64(1) ||
		line["status"] != float64(http.StatusUnauthorized) {
		t.Fatalf("the pass logged %v", line)
	}
	if strings.Contains(logged.String(), said) || strings.Contains(logged.String(), "rejected request") {
		t.Fatalf("the log carries the provider's body: %s", logged.String())
	}

	// A pass with nothing failing says nothing: the warning is about failure, not about running.
	logged.Reset()
	if _, err := driver.Once(ctx); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if strings.Contains(logged.String(), "turns did not form") {
		t.Fatalf("a pass with nothing failing warned: %s", logged.String())
	}

	// The body is not lost, only kept out of the log: the parked turn still says why.
	parked, err := observations.Parked(ctx, schema, "p1")
	if err != nil {
		t.Fatalf("list parked: %v", err)
	}
	if len(parked) != 1 || !strings.Contains(parked[0].Reason, "401") {
		t.Fatalf("the parked turn does not keep its reason: %+v", parked)
	}
}

// A failure with no status, such as a model that cannot be reached, is logged without one.
func TestATurnThatFailsWithoutAStatusIsLoggedWithoutOne(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "fm_failure_nostatus")
	observations := pg.NewObservationStore(pool)

	if _, err := observations.Append(ctx, schema, turnOf(
		domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "I work at Ensera."},
	)); err != nil {
		t.Fatalf("append: %v", err)
	}
	var logged bytes.Buffer
	driver := formation.NewDriver(workerWith(t, pool, schema, &failingModel{}, impatient()),
		observations, pg.NewRetentionStore(pool, schema), pg.NewAuditStore(pool, schema),
		schema, impatient(), slog.New(slog.NewJSONHandler(&logged, nil)))

	pass, err := driver.Once(ctx)
	if err != nil {
		t.Fatalf("pass: %v", err)
	}
	if pass.Failed == 0 || pass.Status != 0 {
		t.Fatalf("pass reported %+v, wanted failures and no status", pass)
	}
	line := warning(t, &logged, "turns did not form")
	if _, ok := line["status"]; ok {
		t.Fatalf("a failure without a status logged one: %v", line)
	}
}

// warning returns the one WARN line with the given message, failing the test if there is not one.
func warning(t *testing.T, logged *bytes.Buffer, msg string) map[string]any {
	t.Helper()
	var found []map[string]any
	scanner := bufio.NewScanner(bytes.NewReader(logged.Bytes()))
	for scanner.Scan() {
		var line map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatalf("a log line is not JSON: %q", scanner.Text())
		}
		if line["level"] == "WARN" && line["msg"] == msg {
			found = append(found, line)
		}
	}
	if len(found) != 1 {
		t.Fatalf("got %d %q warnings, wanted 1, in:\n%s", len(found), msg, logged.String())
	}
	return found[0]
}
