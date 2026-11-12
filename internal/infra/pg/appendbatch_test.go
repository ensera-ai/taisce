// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package pg_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
)

// sends counts what a connection is asked to send: single statements by their text, and batches by
// how many statements each carried. It is the driver's own account of its requests.
type sends struct {
	mu         sync.Mutex
	statements []string
	batches    []int
}

func (s *sends) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statements = append(s.statements, strings.Join(strings.Fields(data.SQL), " "))
	return ctx
}
func (s *sends) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}
func (s *sends) TraceBatchStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceBatchStartData) context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.batches = append(s.batches, data.Batch.Len())
	return ctx
}
func (s *sends) TraceBatchQuery(context.Context, *pgx.Conn, pgx.TraceBatchQueryData) {}
func (s *sends) TraceBatchEnd(context.Context, *pgx.Conn, pgx.TraceBatchEndData)     {}

func (s *sends) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statements, s.batches = nil, nil
}

// TestAnAppendHoldsTheProjectsLockForOneStatementAndOneBatchHoweverLongTheTurn holds what an append
// sends while it holds the project's lock.
//
// Appends to one project are serialised by the watermark row the first statement takes, so how long
// that row is held decides the project's write rate. Sent one statement at a time, a turn of n
// messages was 3 + 3n requests under it, each waiting on the network before the next left. This
// counts the requests rather than timing them, because a count is the same on any machine: after
// the statement that takes the lock there is one batch, and its being one does not depend on n.
//
// What it does not show is how much faster that is. That is a measurement, and it needs hardware
// that can be held still.
func TestAnAppendHoldsTheProjectsLockForOneStatementAndOneBatchHoweverLongTheTurn(t *testing.T) {
	dsn := os.Getenv("TAISCE_TEST_DSN")
	if dsn == "" {
		t.Skip("TAISCE_TEST_DSN is not set")
	}
	schema := tenant(t, testPool(t), "append_batch")
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	counted := &sends{}
	config.ConnConfig.Tracer = counted
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	store := pg.NewObservationStore(pool)

	turnWith := func(n int) domain.Turn {
		messages := make([]domain.Message, n)
		for i := range messages {
			role := domain.RoleUser
			if i%2 == 1 {
				role = domain.RoleAssistant
			}
			messages[i] = domain.Message{Ordinal: i, Role: role, Content: fmt.Sprintf("message %d of a turn of %d", i, n)}
		}
		return turnOf(messages...)
	}

	underTheLock := func(n int) (statements []string, batches []int) {
		t.Helper()
		counted.reset()
		stored, err := store.Append(context.Background(), schema, turnWith(n))
		if err != nil {
			t.Fatalf("append a turn of %d messages: %v", n, err)
		}
		// The rows are the ones that were always written: one message, one chunk carrying the same
		// identity and the same words, and one projection, for each.
		var messages, chunks, projections, mismatched int
		if err := pool.QueryRow(context.Background(), schema.SQL(`
			SELECT (SELECT count(*) FROM {schema}.turn_message WHERE observation_id = $1),
			       (SELECT count(*) FROM {schema}.chunk WHERE source_observation_id = $1),
			       (SELECT count(*) FROM {schema}.projection_dependency WHERE source_observation_id = $1),
			       (SELECT count(*) FROM {schema}.turn_message m LEFT JOIN {schema}.chunk c
			          ON c.chunk_id = m.chunk_id AND c.source_message_ordinal = m.ordinal AND c.text = m.content
			         WHERE m.observation_id = $1 AND c.chunk_id IS NULL)`), stored.ID).
			Scan(&messages, &chunks, &projections, &mismatched); err != nil {
			t.Fatal(err)
		}
		if messages != n || chunks != n || projections != n || mismatched != 0 {
			t.Fatalf("a turn of %d messages stored %d messages, %d chunks and %d projections, %d of them not matching their message",
				n, messages, chunks, projections, mismatched)
		}
		counted.mu.Lock()
		defer counted.mu.Unlock()
		// From the statement that takes the watermark row to the commit.
		held := false
		for _, sql := range counted.statements {
			switch {
			case strings.Contains(sql, ".watermark"):
				held = true
				statements = append(statements, sql)
			case held && !strings.EqualFold(sql, "commit"):
				statements = append(statements, sql)
			}
		}
		if !held {
			t.Fatalf("the append never took the project's watermark row; statements sent: %v", counted.statements)
		}
		return statements, append([]int(nil), counted.batches...)
	}

	for _, n := range []int{1, 2, 64} {
		statements, batches := underTheLock(n)
		if len(batches) != 1 || batches[0] != 2+3*n {
			t.Errorf("a turn of %d messages was sent as batches of %v; one batch of %d statements is the whole of it", n, batches, 2+3*n)
		}
		// Whatever single statements follow the lock, their number is not a function of the turn.
		if len(statements) > 3 {
			t.Errorf("a turn of %d messages sent %d single statements with the lock held: %v", n, len(statements), statements)
		}
	}
	short, _ := underTheLock(1)
	long, _ := underTheLock(64)
	if len(short) != len(long) {
		t.Errorf("a turn of 64 messages sends %d single statements under the lock and a turn of one sends %d; the count must not grow with the turn", len(long), len(short))
	}
}
