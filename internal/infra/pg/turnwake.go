// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Telling an idle worker that a turn was stored, so it does not wait out its tick.
//
// # What this is allowed to decide, and what it must not
//
// It decides when a worker looks for work sooner than it otherwise would. It decides nothing about
// what there is to form: a worker that is woken runs the same pass a tick runs, and reads the
// backlog from the tables. A notification that is lost, late, duplicated or forged therefore costs
// at most a wait or a pass that finds nothing. The tick stays, and is what makes that true.
//
// # Why the notification is sent after the write, and not in it
//
// The obvious place is the transaction that stores the turn: NOTIFY there is delivered exactly when
// the turn commits. It is not sent there for three reasons, in order of how much each is known.
//
// It would put a statement on every write for something only an idle instance benefits from. A
// busy worker is already forming and does not wait for a tick at all.
//
// It could not be bounded. One notification per stored turn means a bulk ingest sends a thousand
// wake-ups to tell a worker, which is already awake, one thing.
//
// And a transaction that has notified commits under a lock the database takes for its notification
// queue, shared by every such transaction in the database. That is PostgreSQL's own design as its
// source describes it, and it was not measured here; it is why this is careful, and not why it is
// correct. The first two reasons hold whatever that lock costs.
//
// So the write path only marks that something was stored, in memory, and one goroutine per process
// sends at most one notification per interval while there is something to say. What that gives up
// is a turn stored by a process that dies before it notifies — which waits for the tick, as every
// turn did before this existed.
//
// # What a notification carries
//
// The schema's name and nothing else. Any role that can connect to the database can listen on any
// channel, so a payload is readable by the registry's role and by anything else that logs in. A
// project's name, a subject or a count in it would be a way around the grants that keep those
// apart. The schema is the deployment's own setting, and a worker needs it only to ignore another
// schema's turns in a database that holds more than one.

// turnChannel is the one channel. Fixed, because a channel is an identifier limited to 63 bytes and
// a schema's name may already be that long.
const turnChannel = "taisce_turn_stored"

// wakeGap is the least time between two notifications from one process. Short enough that a turn
// stored on an idle instance starts forming well inside the tick it would have waited for, long
// enough that a burst of writes is one notification and not one each.
const wakeGap = 200 * time.Millisecond

// TurnWaker sends the notifications, for a process that stores turns.
type TurnWaker struct {
	pool    *pgxpool.Pool
	schema  Schema
	pending chan struct{}
}

// NewTurnWaker takes the pool the process already writes through.
func NewTurnWaker(pool *pgxpool.Pool, schema Schema) *TurnWaker {
	return &TurnWaker{pool: pool, schema: schema, pending: make(chan struct{}, 1)}
}

// Stored marks that a turn was stored. It never blocks and never fails: it is called on the write
// path after the write has already succeeded, and nothing about waking a worker may change what the
// caller is told about their turn. A mark made while one is already waiting is the same mark.
func (w *TurnWaker) Stored() {
	if w == nil {
		return
	}
	select {
	case w.pending <- struct{}{}:
	default:
	}
}

// Run sends a notification for each mark, at most one per gap, until the context ends.
//
// A notification that cannot be sent is reported and dropped, not retried: the tick will find the
// turn, and a queue of wake-ups for a database that is not answering is the last thing it needs
// when it comes back.
func (w *TurnWaker) Run(ctx context.Context, failed func(error)) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.pending:
		}
		sendCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		_, err := w.pool.Exec(sendCtx, `SELECT pg_notify($1, $2)`, turnChannel, w.schema.String())
		cancel()
		if err != nil && ctx.Err() == nil && failed != nil {
			failed(fmt.Errorf("tell the workers a turn was stored: %w", err))
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wakeGap):
		}
	}
}

// ListenForTurns calls wake whenever a turn was stored in this schema, until the context ends.
//
// It holds one connection from the pool for as long as it runs — a listening session cannot be
// shared — so a worker's pool serves formation with one fewer than it declares. The connection is
// taken from the pool rather than opened beside it so that what the process holds against the
// database is still the number its DSN states.
//
// wake is also called each time the connection is established, the first time included: whatever
// was stored while nobody was listening was not announced to this process, and one pass that finds
// nothing is cheaper than a turn that waits. wake must not block.
//
// A connection that fails is replaced after a pause. While there is none the worker is exactly as
// responsive as its tick, which is the behaviour this adds to rather than replaces.
func ListenForTurns(ctx context.Context, pool *pgxpool.Pool, schema Schema, wake func(), failed func(error)) {
	for ctx.Err() == nil {
		err := listenOnce(ctx, pool, schema, wake)
		if ctx.Err() != nil {
			return
		}
		if err != nil && failed != nil {
			failed(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

func listenOnce(ctx context.Context, pool *pgxpool.Pool, schema Schema, wake func()) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("take a connection to listen for stored turns: %w", err)
	}
	// Closed rather than returned as it is. A session handed back to the pool still listening
	// would receive every notification into a buffer nobody reads, for as long as the pool kept
	// it; closing it makes the pool discard it.
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = conn.Conn().Close(closeCtx)
		cancel()
		conn.Release()
	}()
	if _, err := conn.Exec(ctx, "LISTEN "+turnChannel); err != nil {
		return fmt.Errorf("listen for stored turns: %w", err)
	}
	wake()
	for {
		notification, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			return fmt.Errorf("wait for a stored turn: %w", err)
		}
		// Another schema's turn, in a database that holds more than one deployment's worth.
		if notification.Payload == schema.String() {
			wake()
		}
	}
}
