// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package migrate_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ── #197 ──────────────────────────────────────────────────────────────────────────────────────
//
// The failure this holds against was measured, not imagined: twenty-eight workers and an API
// against max_connections=80 lost ten of 1,397 documents and logged 357 worker drain failures in
// thirty minutes, every one of them the server refusing a session. The operator scaled workers and
// callers lost writes.
//
// A process whose pool the server could not satisfy now refuses to start, and says both numbers.
func TestAPoolTheServerCannotSatisfyIsRefusedAtStartupWithBothNumbers(t *testing.T) {
	ctx := context.Background()
	dsn := os.Getenv("TAISCE_TEST_DSN")
	if dsn == "" {
		t.Skip("TAISCE_TEST_DSN is not set")
	}

	// A pool small enough to fit: the check passes and reports what it saw.
	modest := openSizedPool(t, ctx, dsn, 2)
	budget, err := migrate.CheckConnectionBudget(ctx, modest)
	if err != nil {
		t.Fatalf("a two-connection pool was refused: %v", err)
	}
	if budget.Wanted != 2 || budget.MaxConnections < 1 || budget.InUse < 1 {
		t.Fatalf("the budget did not read the server: %+v", budget)
	}
	// InUse counts this connection, which is the conservative direction: the check pays for itself
	// out of its own budget rather than assuming a slot it has not taken.
	if budget.Available() > budget.MaxConnections-budget.Reserved-1 {
		t.Fatalf("the check did not count its own connection: %+v", budget)
	}

	// A pool larger than the server could ever satisfy: refused, and the message carries both the
	// number asked for and the number available, so an operator is not left to find one of them.
	greedy := openSizedPool(t, ctx, dsn, int32(budget.MaxConnections)+1)
	_, err = migrate.CheckConnectionBudget(ctx, greedy)
	if !errors.Is(err, migrate.ErrConnectionBudget) {
		t.Fatalf("an oversubscribed pool was accepted: %v", err)
	}
	for _, number := range []string{
		fmt.Sprintf("%d connections", budget.MaxConnections+1),
		fmt.Sprintf("max_connections %d", budget.MaxConnections),
	} {
		if !strings.Contains(err.Error(), number) {
			t.Fatalf("the refusal does not carry %q: %v", number, err)
		}
	}
	// And it names the setting an operator changes, rather than describing the problem abstractly.
	if !strings.Contains(err.Error(), "superuser_reserved_connections") {
		t.Fatalf("the refusal does not name the reservation: %v", err)
	}
}

// A budget that has been oversubscribed by other processes reports no headroom rather than a
// negative one: below zero would read as a different kind of fault than full.
func TestAnOversubscribedServerReportsNoHeadroomRatherThanANegativeOne(t *testing.T) {
	full := migrate.ConnectionBudget{MaxConnections: 10, Reserved: 3, InUse: 20, Wanted: 4}
	if full.Available() != 0 {
		t.Fatalf("available is %d", full.Available())
	}
	if !strings.Contains(full.String(), "20 client backends already connected") {
		t.Fatalf("the description does not say who took them: %s", full)
	}
}

func openSizedPool(t *testing.T, ctx context.Context, dsn string, max int32) *pgxpool.Pool {
	t.Helper()
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.MaxConns = max
	config.MinConns = 0
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}
