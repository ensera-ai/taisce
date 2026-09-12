// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// ── When the substrate has no connection left ─────────────────────────────────────────────────
//
// PostgreSQL refuses a new session with SQLSTATE 53300 once its slots are gone. Every statement this
// package runs can meet that, and until now every one of them turned it into an internal error: a
// caller was told the request could not be served, which says nothing they can act on. Measured on
// an eight-A100 node, that was ten documents lost and 357 worker drain failures in half an hour.
//
// It is the one database failure that is both transient and entirely the deployment's own doing. The
// condition clears when a sibling process finishes, the caller did nothing wrong, and an observation
// carries an idempotency key that makes the retry safe. So it is worth telling apart from
// every other database failure, which is what this predicate is for.

// ErrNoConnectionSlots is the substrate having no session left to give.
var ErrNoConnectionSlots = errors.New("the database has no connection slot available")

// NoConnectionSlots reports whether an error is the server refusing a new session for want of slots.
//
// Two forms, because the same condition arrives two ways. A server that answered gives SQLSTATE
// 53300 in a PgError. A pool that could not hand out a connection at all wraps the same message
// without a code surviving — pgxpool returns the connect error, and the driver's text is what is
// left. Matching the code alone would miss the second, which is the common one under a full pool.
//
// The text match is deliberately narrow and is a second chance rather than the first: the code is
// checked first and answers on its own wherever it survives.
func NoConnectionSlots(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrNoConnectionSlots) {
		return true
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "53300"
	}
	return strings.Contains(err.Error(), "remaining connection slots are reserved")
}
