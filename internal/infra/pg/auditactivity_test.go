package pg_test

import (
	"context"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/infra/pg"
)

// The console's activity is the ledger counted, and counted exactly: this window against the one of
// the same length before it, cut into buckets aligned to the window's start, by operation busiest
// first and by project busiest first — and narrowed to one project when asked, with the instance's
// own operations kept out of the project ranking. Rows outside both windows count for nothing.
func TestTheLedgerIsCountedByWindowBucketOperationAndProject(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "audit_activity")
	store := pg.NewAuditStore(pool, schema)
	until := time.Now().UTC().Truncate(time.Hour)
	since := until.Add(-24 * time.Hour)
	at := func(operation, project, outcome string, magnitude int, when time.Time) {
		t.Helper()
		if _, err := pool.Exec(ctx, schema.SQL(`
			INSERT INTO {schema}.audit_entry (operation, principal, principal_kind, project, magnitude, outcome, occurred_at)
			VALUES ($1, 'c1', 'credential', $2, $3, $4, $5)`), operation, project, magnitude, outcome, when); err != nil {
			t.Fatal(err)
		}
	}
	// This window. Two turns, of three messages and two, and a replay of the second, which wrote
	// nothing and is recorded with magnitude 0: turns stored is 2, not 5 messages and not 3 rows (#54).
	at("observe", "p1", "allowed", 3, since.Add(30*time.Minute))
	at("observe", "p1", "allowed", 2, since.Add(5*time.Hour+time.Minute))
	at("observe", "p1", "allowed", 0, since.Add(5*time.Hour+time.Minute+time.Second))
	at("recall", "p1", "allowed", 4, since.Add(5*time.Hour+2*time.Minute))
	at("erase", "p2", "allowed", 9, since.Add(12*time.Hour))
	at("project.list", "", "allowed", 2, since.Add(12*time.Hour))
	at("context.assemble", "p2", "allowed", 7, until.Add(-time.Minute))
	at("recall", "p2", "refused", 0, until.Add(-2*time.Minute))
	// The window before.
	at("observe", "p1", "allowed", 10, since.Add(-time.Hour))
	at("recall", "p1", "refused", 0, since.Add(-2*time.Hour))
	// Neither: before the previous window, and at the window's end, which is not in it.
	at("observe", "p1", "allowed", 100, since.Add(-49*time.Hour))
	at("observe", "p1", "allowed", 100, until)

	a, err := store.Activity(ctx, "", since, until, 24)
	if err != nil {
		t.Fatal(err)
	}
	if a.Bucket != time.Hour || !a.HasPrevious || len(a.Series) != 24 {
		t.Fatalf("a day in 24 buckets is 24 hours with a day before it: %v %v %d", a.Bucket, a.HasPrevious, len(a.Series))
	}
	if want := (pg.ActivityTotals{Operations: 8, Refused: 1, TurnsStored: 2, Recalls: 2, Erasures: 1, ErasedRows: 9}); a.Current != want {
		t.Fatalf("this window counted %+v, want %+v", a.Current, want)
	}
	if want := (pg.ActivityTotals{Operations: 2, Refused: 1, TurnsStored: 1}); a.Previous != want {
		t.Fatalf("the window before counted %+v, want %+v", a.Previous, want)
	}
	for i, want := range map[int]pg.ActivityBucket{0: {Allowed: 1}, 5: {Allowed: 3}, 12: {Allowed: 2}, 23: {Allowed: 1, Refused: 1}, 3: {}} {
		got := a.Series[i]
		if got.Allowed != want.Allowed || got.Refused != want.Refused || !got.Start.Equal(since.Add(time.Duration(i)*time.Hour)) {
			t.Fatalf("bucket %d is %+v, want %+v starting %v", i, got, want, since.Add(time.Duration(i)*time.Hour))
		}
	}
	var total int64
	for _, b := range a.Series {
		total += b.Allowed + b.Refused
	}
	if total != a.Current.Operations {
		t.Fatalf("the buckets hold %d operations and the window %d", total, a.Current.Operations)
	}
	if o := a.Operations; len(o) != 5 || o[0].Operation != "observe" || o[0].Allowed != 3 || o[0].Magnitude != 5 ||
		o[1].Operation != "recall" || o[1].Allowed != 1 || o[1].Refused != 1 || o[2].Operation != "context.assemble" {
		t.Fatalf("operations are not busiest first, then by name: %+v", o)
	}
	if p := a.Projects; len(p) != 2 || p[0].Project != "p1" || p[0].Operations != 4 || p[1].Project != "p2" || p[1].Refused != 1 {
		t.Fatalf("projects are not busiest first without the instance's own operations: %+v", p)
	}

	narrowed, err := store.Activity(ctx, "p2", since, until, 24)
	if err != nil {
		t.Fatal(err)
	}
	if want := (pg.ActivityTotals{Operations: 3, Refused: 1, Recalls: 1, Erasures: 1, ErasedRows: 9}); narrowed.Current != want ||
		narrowed.Previous != (pg.ActivityTotals{}) || narrowed.Projects != nil {
		t.Fatalf("one project's window counted %+v before %+v, ranking %+v", narrowed.Current, narrowed.Previous, narrowed.Projects)
	}

	// The whole ledger starts at its first row and has nothing before it.
	all, err := store.Activity(ctx, "", time.Time{}, until, 10)
	if err != nil {
		t.Fatal(err)
	}
	if all.HasPrevious || !all.Since.Equal(since.Add(-49*time.Hour)) || all.Current.Operations != 11 || len(all.Series) != 10 {
		t.Fatalf("the whole ledger is %+v", all)
	}
	// An empty ledger's whole history is the last hour, in one bucket when none are asked for.
	empty, err := pg.NewAuditStore(pool, tenant(t, pool, "audit_activity_empty")).Activity(ctx, "", time.Time{}, until, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !empty.Since.Equal(until.Add(-time.Hour)) || len(empty.Series) != 1 || empty.Current.Operations != 0 {
		t.Fatalf("an empty ledger's history is %+v", empty)
	}
	if _, err := store.Activity(ctx, "", until, since, 24); err == nil {
		t.Fatal("a window that ends before it starts was counted")
	}

	rows, err := store.RecentIn(ctx, "p2", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("one project's rows are %d, want 3", len(rows))
	}
	for _, r := range rows {
		if r.Project != "p2" {
			t.Fatalf("a row from %q was in p2's ledger", r.Project)
		}
	}
}
