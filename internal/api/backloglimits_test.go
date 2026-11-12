// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
)

// TestAnOperatorSetsTheBacklogLimitsAndTheLedgerSaysSo is the limit changed without a database
// session, taking effect on the very next write, with an entry naming who did it.
func TestAnOperatorSetsTheBacklogLimitsAndTheLedgerSaysSo(t *testing.T) {
	m := newManaged(t, "api_backlog_limits")
	ctx := context.Background()
	store := func(want int) {
		t.Helper()
		m.do(t, http.MethodPost, "/v1/observations", map[string]any{
			"data_subject_id": "subject-1", "messages": []map[string]any{{"role": "user", "content": "I work at Ensera."}},
		}, want, nil)
	}

	var limits pg.BacklogLimits
	m.manageDo(t, "/backlog/limits", map[string]any{"max_pending_per_project": 2}, m.operator, http.StatusOK, &limits)
	if limits.MaxPendingPerProject != 2 || limits.MaxPending != 4096 {
		t.Fatalf("one limit was stated and the limits in force are %+v; the other must be left as it was", limits)
	}
	// It is the limit the write path enforces, not a number in a response: two turns wait, and
	// the third is refused for capacity.
	store(http.StatusCreated)
	store(http.StatusCreated)
	store(http.StatusTooManyRequests)

	// Raised, the same write is accepted, with nothing formed in between.
	m.manageDo(t, "/backlog/limits", map[string]any{"max_pending_per_project": 3, "max_pending": 100}, m.operator, http.StatusOK, &limits)
	if limits.MaxPendingPerProject != 3 || limits.MaxPending != 100 || limits.Pending != 2 {
		t.Fatalf("after raising both: %+v", limits)
	}
	store(http.StatusCreated)
	store(http.StatusTooManyRequests)

	// What the budget's own constraints refuse is refused here, and nothing changes.
	for _, bad := range []map[string]any{
		{},
		{"max_pending": 0},
		{"max_pending": 1000001},
		{"max_pending_per_project": 0},
		{"max_pending_per_project": 101}, // above the instance's limit of 100
		{"max_pending": 2, "max_pending_per_project": 3}, // jointly invalid: the row as it would be
		{"max_pending": 2}, // would leave the per-project limit of 3 above it
	} {
		m.manageDo(t, "/backlog/limits", bad, m.operator, http.StatusBadRequest, nil)
	}
	var inForce pg.BacklogLimits
	if err := m.pool.QueryRow(ctx, m.schema.SQL(
		`SELECT max_pending, max_pending_per_project, pending FROM {schema}.ingestion_budget`)).
		Scan(&inForce.MaxPending, &inForce.MaxPendingPerProject, &inForce.Pending); err != nil {
		t.Fatal(err)
	}
	if inForce.MaxPending != 100 || inForce.MaxPendingPerProject != 3 || inForce.Pending != 3 {
		t.Fatalf("a refused change changed something: %+v", inForce)
	}

	// The ledger: two changes allowed, by this operator, each carrying the per-project limit it
	// left in force; and the refusals the constraints made. A request that stated nothing never
	// reached the store and is not an act.
	rows, err := m.pool.Query(ctx, m.schema.SQL(
		`SELECT outcome, magnitude, principal_kind, project FROM {schema}.audit_entry WHERE operation = $1 ORDER BY entry_id`), domain.AuditBacklogLimits)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var allowed []int
	refusals := 0
	for rows.Next() {
		var outcome, kind, project string
		var magnitude int
		if err := rows.Scan(&outcome, &magnitude, &kind, &project); err != nil {
			t.Fatal(err)
		}
		if kind != string(domain.PrincipalOperator) || project != "" {
			t.Errorf("a backlog change is recorded as by %q against project %q; it is an operator's, and the instance's", kind, project)
		}
		if outcome == string(domain.OutcomeAllowed) {
			allowed = append(allowed, magnitude)
		} else {
			refusals++
		}
	}
	if len(allowed) != 2 || allowed[0] != 2 || allowed[1] != 3 || refusals != 6 {
		t.Fatalf("the ledger holds allowed changes %v and %d refusals; want [2 3] and 6", allowed, refusals)
	}

	// A project credential, which reaches memory, does not reach this.
	m.manageDo(t, "/backlog/limits", map[string]any{"max_pending_per_project": 500}, m.token, http.StatusUnauthorized, nil)
}
