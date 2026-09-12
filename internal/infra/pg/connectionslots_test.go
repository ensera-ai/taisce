// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/jackc/pgx/v5/pgconn"
)

// ── Connection exhaustion is recognised two ways ──────────────────────────────────────────────
//
// The condition arrives two ways and both have to be recognised. A server that answered gives
// SQLSTATE 53300; a pool that could not hand out a connection at all returns the driver's connect
// error, where the code does not survive. Matching only the code would miss the second — which is
// the common one under a full pool, and the one the eight-A100 node produced 357 times.
func TestConnectionExhaustionIsRecognisedFromTheCodeAndFromTheConnectError(t *testing.T) {
	for name, err := range map[string]error{
		"the server's own code": &pgconn.PgError{Code: "53300",
			Message: "remaining connection slots are reserved for roles with the SUPERUSER attribute"},
		"the code, wrapped": fmt.Errorf("find unformed turn: %w",
			&pgconn.PgError{Code: "53300", Message: "remaining connection slots are reserved"}),
		"the pool's connect error": errors.New(`failed to connect to ` +
			"`user=taisce_data database=taisce`: server error: FATAL: remaining connection slots " +
			"are reserved for roles with the SUPERUSER attribute (SQLSTATE 53300)"),
		"this package's own sentinel": fmt.Errorf("append: %w", pg.ErrNoConnectionSlots),
	} {
		t.Run(name, func(t *testing.T) {
			if !pg.NoConnectionSlots(err) {
				t.Fatalf("not recognised: %v", err)
			}
		})
	}

	// And nothing else is. A predicate that matched widely would answer a real fault retryably, and
	// a caller would retry forever against something that is never going to clear.
	for name, err := range map[string]error{
		"no error":         nil,
		"a different code": &pgconn.PgError{Code: "23505", Message: "duplicate key value"},
		"a serialisation failure": fmt.Errorf("retry: %w",
			&pgconn.PgError{Code: "40001", Message: "could not serialize access"}),
		"an ordinary failure":            errors.New("connection reset by peer"),
		"text that mentions connections": errors.New("the deployment has no database connection available"),
	} {
		t.Run(name, func(t *testing.T) {
			if pg.NoConnectionSlots(err) {
				t.Fatalf("wrongly recognised: %v", err)
			}
		})
	}
}
