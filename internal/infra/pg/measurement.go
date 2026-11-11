// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// How long things take, counted in the process that did them and kept where the scrape can read.
//
// # What this is allowed to decide, and what it must not
//
// It decides which bucket an observation falls in and when a process's counts are written down. It
// decides nothing about a request: an observation is recorded after the work is done, a failed write
// of the totals is retried on the next tick and never returned to a caller, and no code path waits
// on this to answer anybody.
//
// What it must not do is carry anything from memory. A measurement has a kind from the closed set
// below and a name that is an operation or a counter — names the code declares, never a value a
// request supplied.

// MeasurementKind is what was measured. A closed set: each one has its own bucket bounds, and the
// scrape publishes exactly these.
type MeasurementKind string

const (
	// OperationDuration is how long one request to a memory route took, by operation, from the
	// door to the last byte handed to the response.
	OperationDuration MeasurementKind = "operation_duration"
	// FormationTurnDuration is how long one turn took to form: every model call for its messages
	// and the writes that followed.
	FormationTurnDuration MeasurementKind = "formation_turn_duration"
	// FormationLag is how long a turn waited between being stored and being formed — what an agent
	// experiences as memory not yet knowing what it was just told. It includes the time in the
	// backlog, which the turn duration does not.
	FormationLag MeasurementKind = "formation_lag"
	// Counter is a plain rising count, with no distribution.
	Counter MeasurementKind = "counter"
)

// The counters. Named here because a counter with a name nobody declared is a series nobody can
// explain.
const (
	// CounterAdmissionRefused counts requests turned away at the door for want of capacity. It is
	// the one thing the ledger cannot say: a refusal for capacity writes no ledger row, so a
	// process shedding load looked identical to an idle one.
	CounterAdmissionRefused = "admission_refused"

	// What generation calls cost, each kept per purpose (see PurposeCounter). Calls is how many
	// were made and Unmetered how many of those the provider reported no usage for — so a reader
	// can tell tokens that were not spent from tokens nobody counted. Cut is how many replies
	// reached a ceiling and were refused whole; they are in the token counts too, because a reply
	// that was cut was still paid for.
	CounterModelCalls            = "model_calls"
	CounterModelCallsUnmetered   = "model_calls_unmetered"
	CounterModelPromptTokens     = "model_prompt_tokens"
	CounterModelCompletionTokens = "model_completion_tokens"
	CounterModelRepliesCut       = "model_replies_cut"
)

// PurposeCounters are the counters kept once per purpose, in the order they are published.
var PurposeCounters = []string{
	CounterModelCalls, CounterModelCallsUnmetered, CounterModelPromptTokens,
	CounterModelCompletionTokens, CounterModelRepliesCut,
}

// PurposeCounter names one of the per-purpose counters for one purpose. The purpose is whatever the
// caller's closed set says it is; this only joins the two, and the name's shape is checked where
// every name is.
func PurposeCounter(counter, purpose string) string { return counter + "." + purpose }

// Bounds are the upper edges of a kind's buckets, in milliseconds. An observation above the last
// falls in a final bucket of its own.
//
// Wide and evenly spread on a logarithmic scale, deliberately, because no latency target has been
// agreed and buckets chosen around a guess would put all their resolution where the guess was. The
// request bounds end at the request deadline, the turn bounds at the turn budget, and the lag bounds
// at a day, since a turn can wait as long as a provider is down.
//
// Changing a bound changes what an existing row's counts mean. The stored array's length is checked
// against these on every write and read, so a binary with different bounds refuses rather than
// adding its counts into buckets that mean something else.
var Bounds = map[MeasurementKind][]int64{
	OperationDuration:     {5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000, 30000},
	FormationTurnDuration: {100, 250, 500, 1000, 2500, 5000, 10000, 30000, 60000, 120000, 300000},
	FormationLag:          {1000, 2500, 5000, 10000, 30000, 60000, 300000, 900000, 3600000, 21600000, 86400000},
}

var measurementName = regexp.MustCompile(`^[a-z][a-z0-9_.]{0,63}$`)

type measurementKey struct {
	kind MeasurementKind
	name string
}

type measured struct {
	buckets []int64
	sumMs   int64
	count   int64
}

// Measurements collects observations in memory and writes them down when asked.
//
// A nil *Measurements is valid and records nothing, so a process wired without one — a test, a
// command — needs no branch at any call site.
type Measurements struct {
	pool   *pgxpool.Pool
	schema Schema

	mu      sync.Mutex
	pending map[measurementKey]*measured
}

// NewMeasurements takes the pool the process already serves from.
func NewMeasurements(pool *pgxpool.Pool, schema Schema) *Measurements {
	return &Measurements{pool: pool, schema: schema, pending: map[measurementKey]*measured{}}
}

// Observe records that something of this kind took this long.
//
// It takes a lock and adds to three integers. It does not touch the database, and it cannot fail:
// a kind with no bounds or a name that is not one is dropped, because the place to learn that a
// measurement is misdeclared is a test and not a request.
func (m *Measurements) Observe(kind MeasurementKind, name string, took time.Duration) {
	bounds, ok := Bounds[kind]
	if m == nil || !ok || (name != "" && !measurementName.MatchString(name)) {
		return
	}
	ms := took.Milliseconds()
	if ms < 0 {
		ms = 0
	}
	slot := sort.Search(len(bounds), func(i int) bool { return ms <= bounds[i] })
	m.mu.Lock()
	defer m.mu.Unlock()
	entry := m.entry(measurementKey{kind, name}, len(bounds)+1)
	entry.buckets[slot]++
	entry.sumMs += ms
	entry.count++
}

// Add raises a counter.
func (m *Measurements) Add(name string, n int64) {
	if m == nil || n <= 0 || !measurementName.MatchString(name) {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entry(measurementKey{Counter, name}, 0).count += n
}

func (m *Measurements) entry(key measurementKey, buckets int) *measured {
	entry, ok := m.pending[key]
	if !ok {
		entry = &measured{buckets: make([]int64, buckets)}
		m.pending[key] = entry
	}
	return entry
}

// addMeasurementSQL adds one process's counts to the instance's totals.
//
// Element by element, and only when the two arrays are the same length: a binary built with
// different bounds must not add its counts into buckets that mean something else. The WHERE on the
// update is what refuses that, and a refused row is reported by the count of rows the batch changed.
const addMeasurementSQL = `
INSERT INTO {schema}.measurement AS t (kind, name, buckets, sum_ms, count)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (kind, name) DO UPDATE
   SET buckets = (SELECT coalesce(array_agg(a.v + b.v ORDER BY a.i), '{}')
                    FROM unnest(t.buckets) WITH ORDINALITY AS a(v, i)
                    JOIN unnest(excluded.buckets) WITH ORDINALITY AS b(v, i) USING (i)),
       sum_ms = t.sum_ms + excluded.sum_ms,
       count  = t.count + excluded.count
 WHERE cardinality(t.buckets) = cardinality(excluded.buckets)`

// Flush writes down what has been observed since the last flush, and forgets it.
//
// What it takes is put back if the write fails, so a database that is briefly away costs a late
// total rather than a lost one. The set of keys is closed, so what is held while it is away is
// bounded by the number of operations and not by how long the outage lasts.
func (m *Measurements) Flush(ctx context.Context) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	taken := m.pending
	m.pending = map[measurementKey]*measured{}
	m.mu.Unlock()
	if len(taken) == 0 {
		return nil
	}
	keys := make([]measurementKey, 0, len(taken))
	for key := range taken {
		keys = append(keys, key)
	}
	// A fixed order, so two processes flushing at once take the rows' locks in the same sequence
	// and cannot deadlock on each other.
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].kind != keys[j].kind {
			return keys[i].kind < keys[j].kind
		}
		return keys[i].name < keys[j].name
	})
	batch := &pgx.Batch{}
	for _, key := range keys {
		entry := taken[key]
		batch.Queue(m.schema.SQL(addMeasurementSQL), string(key.kind), key.name, entry.buckets, entry.sumMs, entry.count)
	}
	err := func() error {
		results := m.pool.SendBatch(ctx, batch)
		defer results.Close()
		for _, key := range keys {
			tag, err := results.Exec()
			if err != nil {
				return fmt.Errorf("add the %s measurement %q: %w", key.kind, key.name, err)
			}
			if tag.RowsAffected() != 1 {
				return fmt.Errorf("the stored %s measurement %q has different buckets from this binary's; "+
					"its counts were not added to totals that would mean something else", key.kind, key.name)
			}
		}
		return nil
	}()
	if err != nil {
		m.restore(taken)
		return err
	}
	return nil
}

// restore puts back counts a failed flush took, under whatever has been observed since.
func (m *Measurements) restore(taken map[measurementKey]*measured) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, lost := range taken {
		entry := m.entry(key, len(lost.buckets))
		for i := range lost.buckets {
			entry.buckets[i] += lost.buckets[i]
		}
		entry.sumMs += lost.sumMs
		entry.count += lost.count
	}
}

// Run flushes on an interval until the context ends, then once more.
//
// The last flush runs on a context of its own: the one it was given has just been cancelled, and
// the counts a process holds at shutdown are the ones a rolling restart would otherwise lose every
// time. Failures are returned to the caller's logger through report, never acted on.
func (m *Measurements) Run(ctx context.Context, every time.Duration, report func(error)) {
	if m == nil {
		return
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			last, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			if err := m.Flush(last); err != nil && report != nil {
				report(err)
			}
			cancel()
			return
		case <-ticker.C:
			if err := m.Flush(ctx); err != nil && report != nil && ctx.Err() == nil {
				report(err)
			}
		}
	}
}

// Distribution is one measured thing's totals as the scrape publishes them.
type Distribution struct {
	Kind MeasurementKind
	Name string
	// Bounds are the bucket edges in milliseconds, and Cumulative[i] is how many observations were
	// at or under Bounds[i]. Count is all of them, including those above the last bound.
	Bounds     []int64
	Cumulative []int64
	SumMs      int64
	Count      int64
}

// Measured is everything the instance has counted.
type Measured struct {
	Distributions []Distribution
	Counters      map[string]int64
}

// ReadMeasurements reads the totals in a fixed order: by kind, then by name.
//
// A row whose buckets do not match this binary's bounds is an error and not a row skipped. The
// scrape that reads this fails as a whole when any number cannot be served, because a series that
// silently stops is read as a condition that stopped happening.
func ReadMeasurements(ctx context.Context, pool *pgxpool.Pool, schema Schema) (Measured, error) {
	rows, err := pool.Query(ctx, schema.SQL(
		`SELECT kind, name, buckets, sum_ms, count FROM {schema}.measurement ORDER BY kind, name`))
	if err != nil {
		return Measured{}, fmt.Errorf("read measurements: %w", err)
	}
	defer rows.Close()
	out := Measured{Counters: map[string]int64{}}
	for rows.Next() {
		var kind, name string
		var buckets []int64
		var sumMs, count int64
		if err := rows.Scan(&kind, &name, &buckets, &sumMs, &count); err != nil {
			return Measured{}, fmt.Errorf("read measurements: %w", err)
		}
		if MeasurementKind(kind) == Counter {
			out.Counters[name] = count
			continue
		}
		bounds, ok := Bounds[MeasurementKind(kind)]
		if !ok || len(buckets) != len(bounds)+1 {
			return Measured{}, fmt.Errorf("the stored %s measurement %q does not have this binary's buckets", kind, name)
		}
		cumulative := make([]int64, len(bounds))
		var running int64
		for i := range bounds {
			running += buckets[i]
			cumulative[i] = running
		}
		out.Distributions = append(out.Distributions, Distribution{
			Kind: MeasurementKind(kind), Name: name, Bounds: bounds, Cumulative: cumulative, SumMs: sumMs, Count: count,
		})
	}
	return out, rows.Err()
}
