// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/infra/pg"
)

// What each process counted is added to the instance's totals, and a failed write loses nothing.
//
// Several processes measure and one reports, so the totals have to be the sum of what each added,
// whichever wrote first. And a database that is briefly away must cost a late total rather than a
// lost one: the counts a failed flush took are put back and written with the next.
func TestWhatEachProcessCountedIsAddedToTheTotalsAndAFailedWriteLosesNothing(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "measurement_totals")
	first, second := pg.NewMeasurements(pool, schema), pg.NewMeasurements(pool, schema)

	first.Observe(pg.OperationDuration, "recall", 3*time.Millisecond)   // at or under 5
	first.Observe(pg.OperationDuration, "recall", 40*time.Millisecond)  // at or under 50
	second.Observe(pg.OperationDuration, "recall", 40*time.Millisecond) // the other process
	second.Observe(pg.OperationDuration, "recall", 2*time.Minute)       // above every bound
	second.Observe(pg.FormationLag, "", 7*time.Second)
	first.Add(pg.CounterAdmissionRefused, 2)
	second.Add(pg.CounterAdmissionRefused, 3)

	// The first process's write fails: its context is already over.
	gone, cancel := context.WithCancel(ctx)
	cancel()
	if err := first.Flush(gone); err == nil {
		t.Fatal("a write on a finished context reported success")
	}
	if err := second.Flush(ctx); err != nil {
		t.Fatalf("flush: %v", err)
	}
	read, err := pg.ReadMeasurements(ctx, pool, schema)
	if err != nil {
		t.Fatal(err)
	}
	if got := read.Counters[pg.CounterAdmissionRefused]; got != 3 {
		t.Fatalf("after one process wrote, the counter is %d, want 3", got)
	}
	// It tries again, with one more observation made in between, and nothing is missing.
	first.Observe(pg.OperationDuration, "recall", 3*time.Millisecond)
	if err := first.Flush(ctx); err != nil {
		t.Fatalf("the second attempt: %v", err)
	}
	// And a flush with nothing to write is not a write.
	if err := first.Flush(ctx); err != nil {
		t.Fatal(err)
	}

	read, err = pg.ReadMeasurements(ctx, pool, schema)
	if err != nil {
		t.Fatal(err)
	}
	if got := read.Counters[pg.CounterAdmissionRefused]; got != 5 {
		t.Fatalf("the counter is %d; two processes counted 2 and 3", got)
	}
	var recall, lag *pg.Distribution
	for i := range read.Distributions {
		switch d := &read.Distributions[i]; {
		case d.Kind == pg.OperationDuration && d.Name == "recall":
			recall = d
		case d.Kind == pg.FormationLag:
			lag = d
		}
	}
	if recall == nil || lag == nil || len(read.Distributions) != 2 {
		t.Fatalf("expected the recall and the lag distributions and nothing else: %+v", read.Distributions)
	}
	if recall.Count != 5 || recall.SumMs != 3+40+40+120000+3 {
		t.Fatalf("recall: %d observations summing to %dms; five were made, summing to %d", recall.Count, recall.SumMs, 3+40+40+120000+3)
	}
	// Cumulative, at the edges 5, 10, 25, 50, …: two at or under 5, still two at 25, four at 50,
	// and four at the last edge because one observation was above all of them.
	for edge, want := range map[int]int64{0: 2, 2: 2, 3: 4, len(recall.Bounds) - 1: 4} {
		if recall.Cumulative[edge] != want {
			t.Errorf("recall: %d observations at or under %dms, want %d", recall.Cumulative[edge], recall.Bounds[edge], want)
		}
	}
	if lag.Count != 1 || lag.Cumulative[2] != 0 || lag.Cumulative[3] != 1 {
		t.Fatalf("a 7s wait is not counted where it belongs: %+v", lag)
	}
}

// Nothing that is not a declared name can be counted, and a binary with different buckets does not
// add its counts to totals that mean something else.
//
// A measurement's name becomes a label on a scrape, so a name with anything but lower-case letters,
// digits, a dot or an underscore is dropped before it is held, and refused by the table if written
// around this code. And a total is only meaningful while every process that adds to it agrees on
// the bucket edges: a binary built with others refuses to add, and says why.
func TestOnlyDeclaredNamesAreCountedAndMismatchedBucketsAreRefused(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "measurement_refusals")
	m := pg.NewMeasurements(pool, schema)

	for _, name := range []string{`recall",project="p1`, "Recall", "with space", strings.Repeat("a", 65)} {
		m.Observe(pg.OperationDuration, name, time.Millisecond)
		m.Add(name, 1)
	}
	m.Observe("no_such_kind", "recall", time.Millisecond)
	m.Add(pg.CounterAdmissionRefused, 0)
	m.Add(pg.CounterAdmissionRefused, -4)
	if err := m.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.measurement`)).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("%d rows were written for names and kinds nobody declared (%v)", rows, err)
	}
	// A nil recorder is a process wired without one, and counts nothing without a branch anywhere.
	var none *pg.Measurements
	none.Observe(pg.OperationDuration, "recall", time.Millisecond)
	none.Add(pg.CounterAdmissionRefused, 1)
	if err := none.Flush(ctx); err != nil {
		t.Fatal(err)
	}

	// Totals left by a binary with other edges.
	if _, err := pool.Exec(ctx, schema.SQL(
		`INSERT INTO {schema}.measurement(kind,name,buckets,sum_ms,count) VALUES('operation_duration','recall','{1,2,3}',10,6)`)); err != nil {
		t.Fatal(err)
	}
	m.Observe(pg.OperationDuration, "recall", time.Millisecond)
	if err := m.Flush(ctx); err == nil || !strings.Contains(err.Error(), "different buckets") {
		t.Fatalf("counts were added to totals with other bucket edges, or the refusal does not say so: %v", err)
	}
	var count int64
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count FROM {schema}.measurement WHERE name='recall'`)).Scan(&count); err != nil || count != 6 {
		t.Fatalf("the stored total moved to %d (%v)", count, err)
	}
	if _, err := pg.ReadMeasurements(ctx, pool, schema); err == nil {
		t.Fatal("totals with other bucket edges were read as though they were this binary's")
	}
}

// The totals are written on a timer and once more when the process is told to stop.
//
// The counts a process holds at shutdown are the ones a rolling restart would otherwise lose on
// every restart, so the last write runs after the stop, on a context of its own.
func TestCountsAreWrittenOnATimerAndOnceMoreAtShutdown(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "measurement_run")
	m := pg.NewMeasurements(pool, schema)
	running, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	var reported []error
	go func() {
		defer close(done)
		m.Run(running, 20*time.Millisecond, func(err error) { reported = append(reported, err) })
	}()

	m.Add(pg.CounterAdmissionRefused, 1)
	deadline := time.Now().Add(5 * time.Second)
	for {
		read, err := pg.ReadMeasurements(ctx, pool, schema)
		if err != nil {
			t.Fatal(err)
		}
		if read.Counters[pg.CounterAdmissionRefused] == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("a count was never written on the timer")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Counted, then stopped at once: only the last write can carry it.
	m.Add(pg.CounterAdmissionRefused, 4)
	stop()
	<-done
	read, err := pg.ReadMeasurements(ctx, pool, schema)
	if err != nil {
		t.Fatal(err)
	}
	if got := read.Counters[pg.CounterAdmissionRefused]; got != 5 {
		t.Fatalf("after shutdown the counter is %d; 5 were counted, so the last write lost some", got)
	}
	if len(reported) != 0 {
		t.Fatalf("writes that worked were reported as failures: %v", reported)
	}
}
