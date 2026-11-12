// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package api_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
)

// TestAPollerIsTwoLedgerRowsAWindowAndEveryReadIsStillCounted holds both halves of counting reads
// instead of listing them: the ledger stops growing with a poll, and nothing an operator reads
// from it gets smaller.
//
// Fifty freshness reads used to be fifty rows that can never be removed. They are now one row at
// once — so the credential is on the ledger the moment it acts — and one more when the window
// closes, whose magnitude is the other forty-nine. Everything that counts *operations* over the
// ledger still says fifty.
func TestAPollerIsTwoLedgerRowsAWindowAndEveryReadIsStillCounted(t *testing.T) {
	m := newManaged(t, "api_entitled_reads")
	ctx := context.Background()
	credentials := credential.NewStore(m.pool, string(migrate.ControlSchema))
	own, err := credentials.Resolve(ctx, m.token)
	if err != nil {
		t.Fatal(err)
	}
	other, otherGrant, err := credentials.IssueWithAccess(ctx, "another-poller", own.Project, credential.ReadOnly)
	if err != nil {
		t.Fatal(err)
	}
	audit := pg.NewAuditStore(m.pool, m.schema)

	rows := func(principal string) (magnitudes []int) {
		t.Helper()
		found, err := m.pool.Query(ctx, m.schema.SQL(
			`SELECT magnitude FROM {schema}.audit_entry WHERE operation = $1 AND principal = $2 AND outcome = 'allowed' ORDER BY entry_id`),
			domain.AuditFreshness, principal)
		if err != nil {
			t.Fatal(err)
		}
		defer found.Close()
		for found.Next() {
			var magnitude int
			if err := found.Scan(&magnitude); err != nil {
				t.Fatal(err)
			}
			magnitudes = append(magnitudes, magnitude)
		}
		return magnitudes
	}
	poll := func(token string, n int) {
		t.Helper()
		for i := 0; i < n; i++ {
			if status, body := m.raw(t, http.MethodGet, "/v1/freshness", nil, token); status != http.StatusOK {
				t.Fatalf("freshness answered %d %s", status, body)
			}
		}
	}
	freshnessIn := func() int64 {
		t.Helper()
		activity, err := audit.Activity(ctx, own.Project, time.Hour, 4)
		if err != nil {
			t.Fatal(err)
		}
		for _, o := range activity.Operations {
			if o.Operation == domain.AuditFreshness {
				return o.Allowed
			}
		}
		return 0
	}

	const polls = 50
	poll(m.token, polls)

	// One row, at once, for the first read: the credential is on the ledger as soon as it acts.
	if got := rows(own.CredentialID); len(got) != 1 || got[0] != 1 {
		t.Fatalf("%d freshness reads inside one window wrote ledger rows with magnitudes %v; the first read is one row and the rest wait for the window to close", polls, got)
	}
	acted, err := audit.LastActed(ctx, []string{own.CredentialID})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := acted[own.CredentialID]; !ok {
		t.Fatal("a credential that has just read freshness is not on the ledger yet, so an operator would see it as unused")
	}
	// Another credential's first read is its own row, in the same window.
	poll(other, 3)
	if got := rows(otherGrant.CredentialID); len(got) != 1 || got[0] != 1 {
		t.Fatalf("a second credential's reads were folded into somebody else's, or not written: %v", got)
	}

	// The window closes: one more row each, carrying the count.
	m.reads.Close(ctx, time.Now().Add(2*time.Minute))
	if got := rows(own.CredentialID); len(got) != 2 || got[1] != polls-1 {
		t.Fatalf("after the window closed the rows are %v; want the first read and a count of %d", got, polls-1)
	}
	if got := rows(otherGrant.CredentialID); len(got) != 2 || got[1] != 2 {
		t.Fatalf("after the window closed the second credential's rows are %v; want [1 2]", got)
	}
	// Closing again writes nothing: a count is written once.
	m.reads.Close(ctx, time.Now().Add(2*time.Minute))
	if got := rows(own.CredentialID); len(got) != 2 {
		t.Fatalf("closing a second time wrote again: %v", got)
	}

	// Four rows, fifty-three reads — and fifty-three is what the ledger is read as.
	if got := freshnessIn(); got != polls+3 {
		t.Errorf("the activity an operator reads counts %d freshness reads; %d were made", got, polls+3)
	}
	activity, err := audit.Activity(ctx, "", time.Hour, 4)
	if err != nil {
		t.Fatal(err)
	}
	var bucketed, ranked int64
	for _, b := range activity.Series {
		bucketed += b.Allowed
	}
	for _, p := range activity.Projects {
		if p.Project == own.Project {
			ranked = p.Operations
		}
	}
	if bucketed < polls+3 || ranked < polls+3 || activity.Current.Operations < polls+3 {
		t.Errorf("the window's total (%d), its buckets (%d) and the project's rank (%d) must each count every read; %d were made",
			activity.Current.Operations, bucketed, ranked, polls+3)
	}

	// A window that has closed is over: the next read is a row again.
	poll(m.token, 2)
	if got := rows(own.CredentialID); len(got) != 3 || got[2] != 1 {
		t.Fatalf("the first read after a window closed was not written at once: %v", got)
	}
	// A single read in a window is one row and no count: there is nothing more to say about it.
	m.reads.Close(ctx, time.Now().Add(2*time.Minute))
	poll(other, 1)
	m.reads.Close(ctx, time.Now().Add(2*time.Minute))
	if got := rows(otherGrant.CredentialID); len(got) != 3 || got[2] != 1 {
		t.Fatalf("a window holding one read wrote %v; want one more row of magnitude 1 and no count", got)
	}

	// A row written before reads were counted has a magnitude of zero and stood for one read.
	before := freshnessIn()
	if _, err := m.pool.Exec(ctx, m.schema.SQL(
		`INSERT INTO {schema}.audit_entry (operation, principal, principal_kind, project, magnitude, outcome) VALUES ($1, $2, $3, $4, 0, 'allowed')`),
		domain.AuditFreshness, own.CredentialID, string(domain.PrincipalCredential), own.Project); err != nil {
		t.Fatal(err)
	}
	if got := freshnessIn(); got != before+1 {
		t.Errorf("a freshness row from before reads were counted is read as %d reads; it was one", got-before)
	}

	// What is not counted this way: a recall is a row each time, however often it is made.
	for i := 0; i < 3; i++ {
		m.do(t, http.MethodPost, "/v1/recalls", map[string]any{"question": "where does Ada work", "data_subject_id": "subject-1"}, http.StatusOK, nil)
	}
	var recalls int
	if err := m.pool.QueryRow(ctx, m.schema.SQL(
		`SELECT count(*) FROM {schema}.audit_entry WHERE operation = $1 AND principal = $2`), domain.AuditRecall, own.CredentialID).Scan(&recalls); err != nil {
		t.Fatal(err)
	}
	if recalls != 3 {
		t.Errorf("three recalls wrote %d ledger rows; a read that discloses memory is listed, every time", recalls)
	}
}
