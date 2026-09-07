// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ensera-ai/taisce/internal/domain"
)

// AuditStore appends to the ledger.
//
// # Why writing here never fails a request
//
// An audit write that could fail an operation would make the ledger a second thing that has to be
// working for memory to work — and the failure would arrive as a customer's write being refused for
// a reason that has nothing to do with their data. A ledger that takes the system down is one an
// operator eventually switches off.
//
// So a failed append is logged and swallowed by the caller. That is a real weakness and it is the
// right one: the alternative is a durability guarantee bought with availability, on a table whose
// value is completeness over time rather than atomicity per row. When the ledger becomes something a
// third party verifies — see the open question about a tamper-evident log — that trade has to be
// revisited, and this comment is where it will be found.
type AuditStore struct {
	pool   *pgxpool.Pool
	schema Schema
}

func NewAuditStore(pool *pgxpool.Pool, schema Schema) *AuditStore {
	return &AuditStore{pool: pool, schema: schema}
}

// Append records one operation.
//
// It takes a domain.AuditEntry rather than a spread of arguments so that adding a field is a change
// the compiler shows every caller, rather than one that silently defaults at three call sites and
// not the fourth.
func (s *AuditStore) Append(ctx context.Context, entry domain.AuditEntry) error {
	if err := entry.Validate(); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, s.schema.SQL(`
		INSERT INTO {schema}.audit_entry
		    (operation, principal, principal_kind, project, magnitude, outcome)
		VALUES ($1, $2, $3, $4, $5, $6)`),
		entry.Operation, entry.Principal, entry.PrincipalKind, entry.Project,
		entry.Magnitude, entry.Outcome)
	if err != nil {
		return fmt.Errorf("append audit entry: %w", err)
	}
	return nil
}

// Recent reads the ledger back, newest first.
//
// The reader an operator needs, and the one that proves the ledger is not write-only — a record
// nobody can read is a record nobody checks.
func (s *AuditStore) Recent(ctx context.Context, limit int) ([]domain.AuditEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, s.schema.SQL(`
		SELECT operation, principal, principal_kind, project, magnitude, outcome, occurred_at
		  FROM {schema}.audit_entry
		 ORDER BY occurred_at DESC, entry_id DESC
		 LIMIT $1`), limit)
	if err != nil {
		return nil, fmt.Errorf("read audit ledger: %w", err)
	}
	defer rows.Close()

	var out []domain.AuditEntry
	for rows.Next() {
		var e domain.AuditEntry
		if err := rows.Scan(&e.Operation, &e.Principal, &e.PrincipalKind, &e.Project,
			&e.Magnitude, &e.Outcome, &e.OccurredAt); err != nil {
			return nil, fmt.Errorf("scan audit entry: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
