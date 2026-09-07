// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
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

// ── Sealing ───────────────────────────────────────────────────────────────────────────────────

// Seal covers every entry written since the last seal, and chains to it.
//
// Batched rather than per-entry, deliberately. Chaining each row to the one before it would put a
// serialisation point on the read path — every recall writes an audit row, and each would queue
// behind every other. That is a cost paid on every call to protect against a rare case, which is the
// trade rule 9 says to refuse.
//
// The window between seals is the exposure: an entry written after the last seal is not covered, and
// that is a real hole rather than a rounding error. It is bounded by how often this runs.
func (s *AuditStore) Seal(ctx context.Context) (domain.AuditSeal, error) {
	var out domain.AuditSeal
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		// Serialised against another sealer, and against nothing else — entries keep being written
		// while this runs, and simply fall into the next seal.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1)::bigint)`,
			"taisce:audit-seal:"+s.schema.String()); err != nil {
			return fmt.Errorf("take the sealing lock: %w", err)
		}

		var from int64
		var previous []byte
		err := tx.QueryRow(ctx, s.schema.SQL(
			`SELECT to_entry + 1, digest FROM {schema}.audit_seal ORDER BY seal_id DESC LIMIT 1`)).
			Scan(&from, &previous)
		if errors.Is(err, pgx.ErrNoRows) {
			// The first seal covers from the beginning, and chains to a fixed zero rather than to
			// nothing — so verification has one rule instead of a special case for the head.
			from, previous = 0, make([]byte, 32)
		} else if err != nil {
			return fmt.Errorf("read the last seal: %w", err)
		}

		var to int64
		var count int
		if err := tx.QueryRow(ctx, s.schema.SQL(
			`SELECT coalesce(max(entry_id), 0), count(*) FROM {schema}.audit_entry WHERE entry_id >= $1`),
			from).Scan(&to, &count); err != nil {
			return fmt.Errorf("find entries to seal: %w", err)
		}
		if count == 0 {
			return nil
		}

		var entries []byte
		if err := tx.QueryRow(ctx, s.schema.SQL(
			`SELECT {schema}.audit_entries_digest($1, $2)`), from, to).Scan(&entries); err != nil {
			return fmt.Errorf("digest entries: %w", err)
		}

		// The chain link: this seal's digest covers the previous digest and its own entries, so
		// changing either changes it — and every seal after it.
		var digest []byte
		if err := tx.QueryRow(ctx, `SELECT sha256($1::bytea || $2::bytea)`, previous, entries).
			Scan(&digest); err != nil {
			return fmt.Errorf("chain the seal: %w", err)
		}

		if _, err := tx.Exec(ctx, s.schema.SQL(`
			INSERT INTO {schema}.audit_seal
			    (from_entry, to_entry, entry_count, entries_digest, previous_digest, digest)
			VALUES ($1, $2, $3, $4, $5, $6)`),
			from, to, count, entries, previous, digest); err != nil {
			return fmt.Errorf("write the seal: %w", err)
		}

		out = domain.AuditSeal{From: from, To: to, Entries: count, Digest: digest}
		return nil
	})
	return out, err
}

// Verify recomputes the chain and reports the first place it disagrees.
//
// The operator's own check, run against their own instance. It answers "has anything been altered
// since it was sealed" — and it distinguishes an altered ENTRY from a re-linked CHAIN, because those
// are different accusations and a verifier that says only "invalid" tells nobody what happened.
func (s *AuditStore) Verify(ctx context.Context) (domain.AuditVerification, error) {
	rows, err := s.pool.Query(ctx, s.schema.SQL(`
		SELECT seal_id, from_entry, to_entry, entry_count, entries_digest, previous_digest, digest
		  FROM {schema}.audit_seal ORDER BY seal_id`))
	if err != nil {
		return domain.AuditVerification{}, fmt.Errorf("read seals: %w", err)
	}
	type seal struct {
		id                        int64
		from, to                  int64
		count                     int
		entries, previous, digest []byte
	}
	var seals []seal
	for rows.Next() {
		var sl seal
		if err := rows.Scan(&sl.id, &sl.from, &sl.to, &sl.count, &sl.entries, &sl.previous, &sl.digest); err != nil {
			rows.Close()
			return domain.AuditVerification{}, err
		}
		seals = append(seals, sl)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return domain.AuditVerification{}, err
	}

	out := domain.AuditVerification{Seals: len(seals)}
	expectedPrevious := make([]byte, 32)
	for _, sl := range seals {
		var recomputed []byte
		if err := s.pool.QueryRow(ctx, s.schema.SQL(
			`SELECT {schema}.audit_entries_digest($1, $2)`), sl.from, sl.to).Scan(&recomputed); err != nil {
			return domain.AuditVerification{}, fmt.Errorf("recompute seal %d: %w", sl.id, err)
		}
		if !bytes.Equal(recomputed, sl.entries) {
			out.Failure = fmt.Sprintf("seal %d covers entries %d-%d and they no longer digest to what "+
				"it recorded: an entry in that range was altered or removed", sl.id, sl.from, sl.to)
			return out, nil
		}
		if !bytes.Equal(sl.previous, expectedPrevious) {
			out.Failure = fmt.Sprintf("seal %d does not follow the seal before it: the chain was "+
				"re-linked, which is what removing a seal looks like", sl.id)
			return out, nil
		}
		var chained []byte
		if err := s.pool.QueryRow(ctx, `SELECT sha256($1::bytea || $2::bytea)`, sl.previous, sl.entries).
			Scan(&chained); err != nil {
			return domain.AuditVerification{}, err
		}
		if !bytes.Equal(chained, sl.digest) {
			out.Failure = fmt.Sprintf("seal %d records a digest that is not the one its own contents "+
				"produce", sl.id)
			return out, nil
		}
		expectedPrevious = sl.digest
		// The count the seal recorded, not the width of its range. The first seal starts at zero
		// while entry ids start at one, so range arithmetic overstates coverage by one — and a
		// verification that overstates what it covers is the one kind of wrong this must not be.
		// Found by a concurrency test asserting the seals cover exactly the entries that exist.
		out.Entries += sl.count
	}

	out.Head = expectedPrevious
	// Entries written since the last seal are not covered, and saying how many is the difference
	// between a verification and a reassurance.
	if err := s.pool.QueryRow(ctx, s.schema.SQL(
		`SELECT count(*) FROM {schema}.audit_entry
		  WHERE entry_id > coalesce((SELECT max(to_entry) FROM {schema}.audit_seal), -1)`)).
		Scan(&out.Unsealed); err != nil {
		return domain.AuditVerification{}, fmt.Errorf("count unsealed: %w", err)
	}
	out.Valid = true
	return out, nil
}
