// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
)

// An operation written now is inside a window taken now.
//
// A ledger row is stamped by the database. A window whose end is computed by the process reading it
// is a comparison between two clocks, and where the database's is ahead — by any amount — every row
// it stamped in the interval between them is stamped later than the window's end and silently
// dropped. Nothing fails; the count is simply low, every time, and what it is missing is the newest
// operations.
//
// # Why the skew is supplied rather than observed
//
// The two clocks disagree on a development machine (measured: the database a hundred milliseconds
// ahead of the host process) and agree on a runner where both are the same kernel's. A test that
// waited for a real disagreement would assert nothing wherever they happen to agree, which is the
// one failure a green run must not be able to hide. So the second half of this test supplies the
// skew as data: over the same row the first half counted, it asks for a window whose end came from a
// clock behind the one that stamped the rows, through the same counting code, and requires that
// window to miss it. That is the defect, reproduced; the first half is the fix.
func TestAnOperationWrittenNowIsInsideAWindowTakenNow(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "audit_activity_window")
	store := pg.NewAuditStore(pool, schema)

	// Written the way the ledger writes it, with no time of this test's own: `occurred_at` takes the
	// column's default, so the row carries the database's clock and not this process's.
	if err := store.Append(ctx, domain.AuditEntry{
		Operation: domain.AuditObserve, Principal: "c1", PrincipalKind: "credential",
		Project: "p1", Magnitude: 3, Outcome: domain.OutcomeAllowed,
	}); err != nil {
		t.Fatalf("append a ledger row: %v", err)
	}

	// The window is a length. Its end is resolved where the row was stamped, so the row written a
	// moment ago is in it.
	a, err := store.Activity(ctx, "", time.Minute, 1)
	if err != nil {
		t.Fatalf("count the ledger: %v", err)
	}
	if a.Current.Operations != 1 || a.Current.TurnsStored != 1 {
		t.Fatalf("an operation written a moment ago was outside a window taken now: %+v", a.Current)
	}
	if !a.Until.After(a.Since) || a.Until.Sub(a.Since) != time.Minute {
		t.Fatalf("the window is %v long, from %v to %v", a.Until.Sub(a.Since), a.Since, a.Until)
	}

	// What the host's clock would have cost. The row's own stamp stands in for "the database's clock
	// now", and a window ending before it is what a process whose clock is behind asks for. Over the
	// same row, through the same counting code, that window is empty — which is the defect this
	// signature exists to make unreachable.
	var stamped time.Time
	if err := pool.QueryRow(ctx, schema.SQL(
		`SELECT max(occurred_at) FROM {schema}.audit_entry`)).Scan(&stamped); err != nil {
		t.Fatalf("read the row's stamp: %v", err)
	}
	behind := stamped.Add(-200 * time.Millisecond)
	missed, err := store.ActivityBetweenForTesting(ctx, "", behind.Add(-time.Minute), behind, 1)
	if err != nil {
		t.Fatalf("count the ledger over a window ending behind the row: %v", err)
	}
	if missed.Current.Operations != 0 {
		t.Fatalf("the skewed window counted %d operations, so this test cannot show what skew costs",
			missed.Current.Operations)
	}

	// A zero length is still the whole ledger, and the row is in that too: the length-based window
	// did not take the one window that has no length away.
	all, err := store.Activity(ctx, "", 0, 1)
	if err != nil {
		t.Fatalf("count the whole ledger: %v", err)
	}
	if all.HasPrevious || all.Current.Operations != 1 {
		t.Fatalf("the whole ledger is %+v", all.Current)
	}
}
