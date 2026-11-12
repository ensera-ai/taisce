// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"math"
	"strconv"
	"sync"
	"time"
)

// DrainRate estimates how long a writer refused for capacity should wait before trying again.
//
// # What it is allowed to decide, and what it must not
//
// It decides the number in one response header. It does not decide whether a write is refused —
// the budget does, in the database — and nothing reads it but a client choosing when to retry. A
// wrong estimate costs a client a retry that is refused again, or a wait a little longer than it
// needed.
//
// # What the number is
//
// A write is refused when the backlog is full, and it stops being full when one more turn forms.
// So the wait is how long the instance currently takes to form one turn: the time between two
// readings of the instance's count of formed turns, divided by how many formed between them. It is
// taken from the most recent stretch in which the count moved, because that is the rate now and an
// average over the instance's life is the rate of some other day.
//
// # What it does not know
//
//   - Which project's turn will form next. With several projects waiting the workers take them in
//     turn, so for any one project this is a floor, low by up to the number of projects waiting.
//   - Anything, when nothing has formed lately: a provider that is down forms no turns and gives no
//     rate. Then it says the ceiling, which is the honest answer to "when" — not soon.
//
// The count it reads is written down by the workers every few seconds, so the estimate trails the
// instance by that much. It is a hint with a stated meaning, not a promise.
type DrainRate struct {
	mu       sync.Mutex
	count    int64
	changed  time.Time
	seen     bool
	perTurn  time.Duration
	measured bool
}

const (
	// retryCeiling is the longest wait a refusal states. Past it the client is better served by
	// asking again than by a number this cannot stand behind.
	retryCeiling = 30 * time.Second
	// drainStale is how long a rate is believed after the count last moved. A rate from before a
	// provider went down describes an instance that no longer exists.
	drainStale = 2 * time.Minute
)

// NewDrainRate returns an estimator that knows nothing yet, and says the ceiling until it does.
func NewDrainRate() *DrainRate { return &DrainRate{} }

// Observe is told the instance's count of formed turns, as read at a moment.
//
// The first reading only sets the starting point. A reading at which the count has risen gives a
// rate; one at which it has not changes nothing, so a quiet minute between two bursts does not
// erase what the last burst showed until it is stale. A count that fell — a table rebuilt, another
// instance's reading — starts over.
func (d *DrainRate) Observe(count int64, at time.Time) {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.seen || count < d.count {
		d.count, d.changed, d.seen, d.measured = count, at, true, false
		return
	}
	if formed := count - d.count; formed > 0 && at.After(d.changed) {
		d.perTurn = at.Sub(d.changed) / time.Duration(formed)
		d.measured = true
		d.count, d.changed = count, at
	}
}

// RetryAfter is the whole seconds a refused writer is told to wait, as the header's value.
//
// Never below one second, which is what was always said, and never above the ceiling.
func (d *DrainRate) RetryAfter(now time.Time) string {
	wait := retryCeiling
	if d != nil {
		d.mu.Lock()
		if d.measured && now.Sub(d.changed) <= drainStale && d.perTurn < retryCeiling {
			wait = d.perTurn
		}
		d.mu.Unlock()
	}
	seconds := int(math.Ceil(wait.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	return strconv.Itoa(seconds)
}
