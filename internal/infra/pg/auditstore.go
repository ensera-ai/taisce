// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

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

// RecentIn is Recent narrowed to one project, or to none when project is empty.
//
// The filter is an equality on a column the ledger already holds, served by the time index it
// already has: an operator asking what happened in one project reads the same newest-first page,
// with every other project's rows left out.
func (s *AuditStore) RecentIn(ctx context.Context, project string, limit int) ([]domain.AuditEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, s.schema.SQL(`
		SELECT operation, principal, principal_kind, project, magnitude, outcome, occurred_at
		  FROM {schema}.audit_entry
		 WHERE $2 = '' OR project = $2
		 ORDER BY occurred_at DESC, entry_id DESC
		 LIMIT $1`), limit, project)
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

// Activity is what the ledger says happened in a window: counted, never listed.
//
// Everything here is an aggregate of rows that already hold no words — how many operations, of
// which kinds, allowed or refused, and in which project — so the console can draw an instance's
// activity without reading anything anybody said.
type Activity struct {
	Since, Until time.Time
	Bucket       time.Duration
	// Current is this window; Previous the window of the same length before it, so a number can be
	// read against the one it replaced. HasPrevious is false when the window is the whole ledger,
	// which has nothing before it.
	Current, Previous ActivityTotals
	HasPrevious       bool
	// Series is the window cut into equal buckets, oldest first, every bucket present — an empty
	// hour is a zero, not a gap somebody reads as missing data.
	Series []ActivityBucket
	// Operations is this window by operation, busiest first.
	Operations []OperationActivity
	// Projects is the busiest projects in this window, when the window is not already one project's.
	Projects []ProjectActivity
}

// ActivityTotals are the counts an operator reads first.
type ActivityTotals struct {
	Operations, Refused int64
	// TurnsStored is the turns observations wrote; Recalls the recall and context requests served;
	// Erasures the erasures performed and ErasedRows what they removed. Each counts allowed rows
	// only — a refused recall served nothing.
	TurnsStored, Recalls, Erasures, ErasedRows int64
}

// ActivityBucket is one slice of a window.
type ActivityBucket struct {
	Start            time.Time
	Allowed, Refused int64
}

// OperationActivity is one operation's share of a window.
type OperationActivity struct {
	Operation                   string
	Allowed, Refused, Magnitude int64
}

// ProjectActivity is one project's share of a window.
type ProjectActivity struct {
	Project             string
	Operations, Refused int64
}

// activityProjects bounds the busiest-projects list. A console shows the few worth looking at; the
// rest are one filter away.
const activityProjects = 6

// written is the number of rows with a non-zero magnitude. For observe that is the turns written: an
// observation's magnitude is its message count, and a replay of an idempotency key is recorded with
// magnitude 0 because it wrote nothing. Summing magnitude would count messages, and a turn of three
// messages would show as three turns stored (#54).
func (t *ActivityTotals) add(operation, outcome string, n, magnitude, written int64) {
	t.Operations += n
	if outcome == domain.OutcomeRefused {
		t.Refused += n
		return
	}
	switch operation {
	case domain.AuditObserve:
		t.TurnsStored += written
	case domain.AuditRecall, domain.AuditContextAssemble:
		t.Recalls += n
	case domain.AuditErase:
		t.Erasures += n
		t.ErasedRows += magnitude
	}
}

// Activity counts the ledger over [since, until), for one project or, with project empty, for all
// of them, cut into the given number of buckets.
//
// A zero since means the whole ledger: it starts at the earliest row (or an hour before until, for
// an empty ledger), and it has no previous window. Three statements, each bounded by the window and
// served by the time index, rather than one that returns every row for the caller to count: the
// work is proportional to the window's rows and happens where they are.
func (s *AuditStore) Activity(ctx context.Context, project string, since, until time.Time, buckets int) (Activity, error) {
	if buckets <= 0 {
		buckets = 1
	}
	previous := !since.IsZero()
	if since.IsZero() {
		var earliest *time.Time
		if err := s.pool.QueryRow(ctx, s.schema.SQL(
			`SELECT min(occurred_at) FROM {schema}.audit_entry WHERE $1 = '' OR project = $1`), project).
			Scan(&earliest); err != nil {
			return Activity{}, fmt.Errorf("find the ledger's first entry: %w", err)
		}
		since = until.Add(-time.Hour)
		if earliest != nil && earliest.Before(since) {
			since = *earliest
		}
	}
	if !until.After(since) {
		return Activity{}, fmt.Errorf("an activity window must end after it starts")
	}
	length := until.Sub(since)
	out := Activity{Since: since, Until: until, HasPrevious: previous,
		Bucket: (length / time.Duration(buckets)).Truncate(time.Second)}
	if out.Bucket < time.Second {
		out.Bucket = time.Second
	}
	from := since
	if previous {
		from = since.Add(-length)
	}

	rows, err := s.pool.Query(ctx, s.schema.SQL(`
		SELECT occurred_at >= $2, operation, outcome, count(*), coalesce(sum(magnitude), 0),
		       count(*) FILTER (WHERE magnitude > 0)
		  FROM {schema}.audit_entry
		 WHERE occurred_at >= $1 AND occurred_at < $3 AND ($4 = '' OR project = $4)
		 GROUP BY 1, 2, 3`), from, since, until, project)
	if err != nil {
		return Activity{}, fmt.Errorf("count the ledger: %w", err)
	}
	byOperation := map[string]*OperationActivity{}
	for rows.Next() {
		var current bool
		var operation, outcome string
		var n, magnitude, written int64
		if err := rows.Scan(&current, &operation, &outcome, &n, &magnitude, &written); err != nil {
			rows.Close()
			return Activity{}, fmt.Errorf("scan a ledger count: %w", err)
		}
		if !current {
			out.Previous.add(operation, outcome, n, magnitude, written)
			continue
		}
		out.Current.add(operation, outcome, n, magnitude, written)
		o := byOperation[operation]
		if o == nil {
			o = &OperationActivity{Operation: operation}
			byOperation[operation] = o
		}
		if outcome == domain.OutcomeRefused {
			o.Refused += n
		} else {
			o.Allowed += n
		}
		o.Magnitude += magnitude
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Activity{}, fmt.Errorf("count the ledger: %w", err)
	}
	for _, o := range byOperation {
		out.Operations = append(out.Operations, *o)
	}
	sort.Slice(out.Operations, func(i, j int) bool {
		a, b := out.Operations[i], out.Operations[j]
		if a.Allowed+a.Refused != b.Allowed+b.Refused {
			return a.Allowed+a.Refused > b.Allowed+b.Refused
		}
		return a.Operation < b.Operation
	})

	out.Series = make([]ActivityBucket, buckets)
	for i := range out.Series {
		out.Series[i].Start = since.Add(time.Duration(i) * out.Bucket)
	}
	rows, err = s.pool.Query(ctx, s.schema.SQL(`
		SELECT date_bin(make_interval(secs => $1), occurred_at, $2),
		       count(*) FILTER (WHERE outcome = 'allowed'), count(*) FILTER (WHERE outcome = 'refused')
		  FROM {schema}.audit_entry
		 WHERE occurred_at >= $2 AND occurred_at < $3 AND ($4 = '' OR project = $4)
		 GROUP BY 1`), out.Bucket.Seconds(), since, until, project)
	if err != nil {
		return Activity{}, fmt.Errorf("bucket the ledger: %w", err)
	}
	for rows.Next() {
		var start time.Time
		var allowed, refused int64
		if err := rows.Scan(&start, &allowed, &refused); err != nil {
			rows.Close()
			return Activity{}, fmt.Errorf("scan a ledger bucket: %w", err)
		}
		// A bucket width truncated to whole seconds leaves the last bucket a little longer than the
		// rest; what falls past the last boundary belongs to it rather than to one off the end.
		i := int(start.Sub(since) / out.Bucket)
		if i >= buckets {
			i = buckets - 1
		}
		out.Series[i].Allowed += allowed
		out.Series[i].Refused += refused
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Activity{}, fmt.Errorf("bucket the ledger: %w", err)
	}

	if project != "" {
		return out, nil
	}
	rows, err = s.pool.Query(ctx, s.schema.SQL(`
		SELECT project, count(*), count(*) FILTER (WHERE outcome = 'refused')
		  FROM {schema}.audit_entry
		 WHERE occurred_at >= $1 AND occurred_at < $2 AND project <> ''
		 GROUP BY 1 ORDER BY 2 DESC, 1 LIMIT $3`), since, until, activityProjects)
	if err != nil {
		return Activity{}, fmt.Errorf("rank projects: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var p ProjectActivity
		if err := rows.Scan(&p.Project, &p.Operations, &p.Refused); err != nil {
			return Activity{}, fmt.Errorf("scan a project's activity: %w", err)
		}
		out.Projects = append(out.Projects, p)
	}
	return out, rows.Err()
}

// Recent reads the ledger back, newest first.
//
// The reader an operator needs, and the one that proves the ledger is not write-only — a record
// nobody can read is a record nobody checks.
func (s *AuditStore) Recent(ctx context.Context, limit int) ([]domain.AuditEntry, error) {
	return s.RecentIn(ctx, "", limit)
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
	// The substrate seals: it waits for every entry that already drew an id, covers at most 100,000
	// entries, and writes the seal the memory role may not write itself (0064).
	var out domain.AuditSeal
	err := s.pool.QueryRow(ctx, s.schema.SQL(
		`SELECT from_entry, to_entry, entry_count, digest FROM {schema}.audit_seal_now()`)).
		Scan(&out.From, &out.To, &out.Entries, &out.Digest)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AuditSeal{}, nil
	}
	if err != nil {
		return domain.AuditSeal{}, fmt.Errorf("seal the ledger: %w", err)
	}
	return out, nil
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
