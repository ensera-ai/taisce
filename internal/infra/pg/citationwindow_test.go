// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
)

// ── The citation window is cut once, when the evidence is written ──────────────────────────────
//
// The words around a quote used to be cut out of the whole message on every read of every fact:
// `convert_to(m.content,'UTF8')` per returned row, to keep three hundred and twenty bytes of a
// message that can be a hundred kilobytes. Quote, span and message are immutable once stored, so the
// value could not drift — every read was recomputing the same bytes.
//
// What these tests hold is the two things that make moving it safe: the stored width is the width the
// domain declares, and the stored copy of message text goes when the message goes.

// The stored window is the width the domain declares.
//
// The width lives in two places now — `domain.ContextWindow` in Go, and the number the trigger cuts
// at in migration 0074 — and the honest answer to a constant stated twice is a test rather than a
// comment. This reads a stored window back for a quote with room either side and holds its length
// against the constant: the window is the span plus the window either side of it.
func TestTheStoredCitationWindowIsTheWidthTheDomainDeclares(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "citation_window_width")
	obs, facts := pg.NewObservationStore(pool), pg.NewFactStore(pool)

	// A message with far more than the window either side of the quote, so the cut is made at the
	// width rather than at the message's edges.
	before := strings.Repeat("a ", domain.ContextWindow)
	after := strings.Repeat("z ", domain.ContextWindow)
	const quote = "I work at Ensera"
	content := before + quote + after

	stored, err := obs.Append(ctx, schema, domain.Turn{
		Scope: "p1", DataSubjectID: "subject-1", OccurredAt: time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC),
		Messages: []domain.Message{{Ordinal: 0, Role: domain.RoleUser, Content: content}},
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if _, err := facts.Assert(ctx, schema, "p1", stored.ID, domain.RoleUser, "subject-1", domain.Claim{
		Subject: "Atlas", Predicate: "works_at", Cardinality: domain.CardinalityMany, Object: "Ensera",
		Statement: "Atlas works at Ensera", Quote: quote,
		ByteStart: len(before), ByteEnd: len(before) + len(quote),
	}); err != nil {
		t.Fatalf("assert: %v", err)
	}

	var window []byte
	var start, messageBytes int
	if err := pool.QueryRow(ctx, schema.SQL(
		`SELECT citation_window, citation_window_start, citation_message_bytes
		   FROM {schema}.fact_evidence`)).Scan(&window, &start, &messageBytes); err != nil {
		t.Fatalf("read the stored window: %v", err)
	}
	if want := len(quote) + 2*domain.ContextWindow; len(window) != want {
		t.Fatalf("the stored window is %d bytes; domain.ContextWindow declares %d either side of a %d-byte quote, so %d",
			len(window), domain.ContextWindow, len(quote), want)
	}
	if want := len(before) - domain.ContextWindow; start != want {
		t.Fatalf("the window starts at %d; %d bytes before a quote at %d is %d", start, domain.ContextWindow, len(before), want)
	}
	if messageBytes != len(content) {
		t.Fatalf("the stored message length is %d, and the message is %d bytes", messageBytes, len(content))
	}
	// The quote is inside the window it was cut around, which is the property that makes the window
	// usable at all: a window that lost the words it was cut around is a receipt pointing elsewhere.
	if !strings.Contains(string(window), quote) {
		t.Fatal("the stored window does not contain the quote it was cut around")
	}
}

// A stored citation window is erased with the evidence row it belongs to.
//
// This is the regression the change had to close rather than introduce. Before it, a fact whose
// message was gone came back with its quote and an empty context, because the context was cut from a
// message that was no longer there. A stored copy could instead outlive the message — and a quote
// outliving the message it came from is not worth a faster read.
//
// It does not, and the reason is stronger than an ordering in two sweeps: `evidence_message_fk`
// references `turn_message` with no delete action, so the substrate refuses to remove a message while
// an evidence row cites it. The evidence row — and the window on it — has to go first or the deletion
// fails. That is asserted here as its own case, because it is the rule the other two rest on, and
// then both paths that remove a message are checked for a residual: an erasure of the person, and a
// retention sweep of an expired turn.
func TestAStoredCitationWindowIsErasedWithTheEvidenceItBelongsTo(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)

	for _, remove := range []struct {
		name, tenant string
		do           func(t *testing.T, schema pg.Schema)
	}{
		{"refused by the substrate", "refused", func(t *testing.T, schema pg.Schema) {
			// The rule underneath. A message cannot be removed while its evidence cites it, so a
			// stored window cannot be orphaned by any statement — only by one that removes the
			// evidence row first, which removes the window with it.
			if _, err := pool.Exec(ctx, schema.SQL(`DELETE FROM {schema}.turn_message`)); err == nil {
				t.Fatal("a message was removed while its evidence cited it, so a stored window could outlive it")
			}
			// Then through the path that respects the rule, so this case ends where the others do.
			if _, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", "subject-1", "requested"); err != nil {
				t.Fatalf("erase: %v", err)
			}
		}},
		{"erasure", "erasure", func(t *testing.T, schema pg.Schema) {
			if _, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", "subject-1", "requested"); err != nil {
				t.Fatalf("erase: %v", err)
			}
		}},
		{"retention", "retention", func(t *testing.T, schema pg.Schema) {
			// Expired by moving the deadline into the past, which is what a project's retention
			// policy does over time; the sweep is the code under test, not the clock.
			if _, err := pool.Exec(ctx, schema.SQL(
				`UPDATE {schema}.observation SET retention_until = now() - interval '1 day'`)); err != nil {
				t.Fatalf("expire: %v", err)
			}
			if _, err := pg.NewRetentionStore(pool, schema).Sweep(ctx, "p1", 100); err != nil {
				t.Fatalf("sweep: %v", err)
			}
		}},
	} {
		t.Run(remove.name, func(t *testing.T) {
			schema := tenant(t, pool, "citation_window_"+remove.tenant)
			obs, facts := pg.NewObservationStore(pool), pg.NewFactStore(pool)
			const content = "I work at Ensera and that is where I have been since March."
			stored, err := obs.Append(ctx, schema, domain.Turn{
				Scope: "p1", DataSubjectID: "subject-1", OccurredAt: time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC),
				Messages: []domain.Message{{Ordinal: 0, Role: domain.RoleUser, Content: content}},
			})
			if err != nil {
				t.Fatalf("append: %v", err)
			}
			if _, err := facts.Assert(ctx, schema, "p1", stored.ID, domain.RoleUser, "subject-1", domain.Claim{
				Subject: "Atlas", Predicate: "works_at", Cardinality: domain.CardinalityMany, Object: "Ensera",
				Statement: "Atlas works at Ensera", Quote: "I work at Ensera",
				ByteStart: 0, ByteEnd: len("I work at Ensera"),
			}); err != nil {
				t.Fatalf("assert: %v", err)
			}

			// The fixture is only a fixture if the window is there to begin with.
			var windows int
			if err := pool.QueryRow(ctx, schema.SQL(
				`SELECT count(*) FROM {schema}.fact_evidence WHERE citation_window IS NOT NULL`)).Scan(&windows); err != nil {
				t.Fatalf("count windows: %v", err)
			}
			if windows != 1 {
				t.Fatalf("the fixture stored %d windows, so this test would prove nothing", windows)
			}

			remove.do(t, schema)

			// No message left, and no copy of one either. Counted over the column rather than over
			// the row, so a row that survived with its words in it would be caught as well as a row
			// that survived empty.
			var messages, residual int
			if err := pool.QueryRow(ctx, schema.SQL(
				`SELECT (SELECT count(*) FROM {schema}.turn_message),
				        (SELECT count(*) FROM {schema}.fact_evidence WHERE citation_window IS NOT NULL)`)).
				Scan(&messages, &residual); err != nil {
				t.Fatalf("count residuals: %v", err)
			}
			if messages != 0 || residual != 0 {
				t.Fatalf("%d messages and %d stored windows survived: a quote's words outlived the message they came from",
					messages, residual)
			}
		})
	}
}
