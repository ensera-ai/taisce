// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/report"
)

// ── A report carries what wrote it ────────────────────────────────────────────────────────────
//
// A report is a model's prose written under a particular prompt, and until now nothing recorded
// which. The invalidation trigger fires on a changed FACT; a changed WRITER was invisible, so a
// deployment could tune its report prompt and keep serving paragraphs written under the old wording
// forever, with nothing recording which was which — which is precisely what rule 14 exists to stop.
//
// A report now carries what wrote it. A community whose report was written by something else is
// offered back to the pass, and writing it replaces the prose in place, keeping the report's own
// identity so every registration pointing at it stays correct.
func TestAReportSaysWhatWroteItAndIsRewrittenWhenTheWriterChanges(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "report_writer")
	store := pg.NewCommunityStore(pool, schema)

	formed(t, pool, schema, subjectTurn("subject-1", "Marta works at Ensera."))
	described(t, pool, schema)

	const first = "report/v1:one"
	waiting, err := store.Unwritten(ctx, "p1", 4, first)
	if err != nil {
		t.Fatal(err)
	}
	if len(waiting) == 0 {
		t.Fatal("no community is waiting for a report, so this test asserts nothing")
	}
	community := waiting[0]

	entities, facts, err := store.Material(ctx, "p1", community.Members)
	if err != nil {
		t.Fatal(err)
	}
	material := report.BuildContext(entities, facts, nil, nil, 10000)
	if err := store.Write(ctx, "p1", community.ID,
		report.Report{Title: "Ensera", Summary: "Written under the first prompt.", Importance: 1,
			ImportanceReason: "because", Findings: []report.Finding{{Summary: "a", Explanation: "b"}}},
		material, first); err != nil {
		t.Fatalf("write the first report: %v", err)
	}

	// The same writer asks again and is told there is nothing to do: the material has not changed
	// and neither would the paragraph.
	again, err := store.Unwritten(ctx, "p1", 4, first)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range again {
		if c.ID == community.ID {
			t.Fatal("a report was offered back to the writer that had just written it")
		}
	}

	// A different writer — a tuned prompt, a changed model — and the same community is offered back.
	const second = "report/v1:two"
	changed, err := store.Unwritten(ctx, "p1", 4, second)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range changed {
		if c.ID == community.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("a prompt edit did not reach a report already stored")
	}

	// Writing it replaces the prose and keeps the report's identity, so nothing pointing at it is
	// orphaned.
	before := reportIdentity(t, pool, schema, community.ID)
	if err := store.Write(ctx, "p1", community.ID,
		report.Report{Title: "Ensera", Summary: "Written under the second prompt.", Importance: 1,
			ImportanceReason: "because", Findings: []report.Finding{{Summary: "a", Explanation: "b"}}},
		material, second); err != nil {
		t.Fatalf("rewrite under the new writer: %v", err)
	}
	after := reportIdentity(t, pool, schema, community.ID)
	if before != after {
		t.Fatalf("a rewrite changed the report's identity, orphaning what pointed at it: %s then %s", before, after)
	}

	var summary, writer string
	if err := pool.QueryRow(ctx, schema.SQL(
		`SELECT summary, written_by FROM {schema}.community_report WHERE scope='p1' AND community_id=$1::uuid`),
		community.ID).Scan(&summary, &writer); err != nil {
		t.Fatal(err)
	}
	if summary != "Written under the second prompt." || writer != second {
		t.Fatalf("the report was not rewritten under the new writer: %q by %q", summary, writer)
	}

	// And it is no longer offered to either writer: the new one wrote it, and the old one is gone.
	settled, err := store.Unwritten(ctx, "p1", 4, second)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range settled {
		if c.ID == community.ID {
			t.Fatal("a rewritten report was offered back again")
		}
	}
}

func reportIdentity(t *testing.T, pool *pgxpool.Pool, schema pg.Schema, community string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), schema.SQL(
		`SELECT report_id::text FROM {schema}.community_report WHERE scope='p1' AND community_id=$1::uuid`),
		community).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
