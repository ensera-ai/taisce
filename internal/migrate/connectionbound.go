// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package migrate

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ── A deployment cannot ask for more connections than the substrate has ───────────────────────
//
// Each process opens a pool of its own; the chart chooses how many processes; the substrate chooses
// `max_connections`. Nothing compared the three, so a deployment scaled its workers and discovered
// the ceiling on its WRITE path: measured on an eight-A100 node, twenty-eight workers and an API
// against `max_connections=80` refused ten of 1,397 documents with an internal error and logged 357
// worker drain failures in thirty minutes, every one of them SQLSTATE 53300.
//
// The failure landed as far from the change as it could: the operator scaled workers, and callers
// lost writes.
//
// ── WHY THE CHECK IS PER PROCESS AND NOT PER DEPLOYMENT ──────────────────────────────────────
//
// A process cannot know how many siblings it has without being told, and a declared shape is a
// number somebody forgets to update — which under-counts, passes, and leaves the ceiling exactly
// where it was. Rule 8's fail-closed default rules that out.
//
// So each process reserves instead of declaring: at startup it asks the server how many slots exist
// and how many are already taken, and refuses to start if its own pool could not be satisfied. That
// composes without anyone counting. The Nth process to start is the one that finds the slots gone,
// and it fails at startup with both numbers in the message rather than serving until a caller's
// write finds the ceiling for it.
//
// ── WHAT IT DOES NOT COVER, WHICH IS WHY THE WRITE PATH IS ALSO FIXED ────────────────────────
//
// Two processes starting at the same instant can both see room and both take it. The window is
// small and this check does not close it, so exhaustion remains possible and `ErrNoConnectionSlots`
// exists for it: a write refused for want of a connection is answered retryably rather than as an
// internal error. A guarantee described as wider than it is would be relied on at its stated width.

// ErrConnectionBudget is a deployment that asked for more than the substrate has.
var ErrConnectionBudget = errors.New("the deployment's connection demand exceeds the server's slots")

// ConnectionBudget is what the check saw, so an operator is told both numbers rather than one.
type ConnectionBudget struct {
	// MaxConnections and Reserved are the server's own settings. Reserved is subtracted because
	// those slots are for superuser recovery: a runtime role that counted on them would find the
	// ceiling lower than it computed, at the moment an administrator most needs to get in.
	MaxConnections int
	Reserved       int
	// InUse is every client backend on the server, not only this database and not only this role.
	// A pooler, a replica's monitoring session and an operator's psql all take from the same pool,
	// and a check that counted only its own kind would pass a server that is already full.
	InUse int
	// Wanted is this process's pool maximum: what it may open, not what it has opened.
	Wanted int
}

// Available is what is left for this process after the server's reservation and everyone else.
func (b ConnectionBudget) Available() int {
	available := b.MaxConnections - b.Reserved - b.InUse
	if available < 0 {
		return 0
	}
	return available
}

func (b ConnectionBudget) String() string {
	return fmt.Sprintf("this process's pool may open %d connections, and the server has %d of %d left "+
		"(max_connections %d, superuser_reserved_connections %d, %d client backends already connected)",
		b.Wanted, b.Available(), b.Wanted, b.MaxConnections, b.Reserved, b.InUse)
}

// CheckConnectionBudget refuses a pool the server could not satisfy.
//
// Called after the pool is built and before anything is served, on the same pool, so the connection
// it uses is one of the ones being counted — which is the conservative direction: the check pays for
// itself out of its own budget rather than assuming a slot it has not taken.
func CheckConnectionBudget(ctx context.Context, pool *pgxpool.Pool) (ConnectionBudget, error) {
	budget := ConnectionBudget{Wanted: int(pool.Config().MaxConns)}
	err := pool.QueryRow(ctx, `SELECT current_setting('max_connections')::int,
        current_setting('superuser_reserved_connections')::int,
        (SELECT count(*) FROM pg_stat_activity WHERE backend_type='client backend')`).
		Scan(&budget.MaxConnections, &budget.Reserved, &budget.InUse)
	if err != nil {
		return budget, fmt.Errorf("read the server's connection settings: %w", err)
	}
	if budget.Available() < budget.Wanted {
		return budget, fmt.Errorf("%w: %s", ErrConnectionBudget, budget)
	}
	return budget, nil
}
