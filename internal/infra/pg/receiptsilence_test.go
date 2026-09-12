// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"sort"
	"testing"

	"github.com/ensera-ai/taisce/internal/compaction"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
)

// Every table an erasure empties is a table the receipt names.
//
// The claim under test is that erasure and export name their source-owned tables by hand, so
// a table added later is missed by both. Measured rather than argued: seed a subject across every
// surface, count every table in the namespace, erase, count again, and compare what actually lost
// rows against what the receipt says it deleted. A table that lost rows and is not in the receipt is
// an erasure understating itself, which is the same failure as a quote removed uncounted.
func TestEveryTableAnErasureEmptiesIsNamedInItsReceipt(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "er_silence")

	// The widest fixture available: turns formed into facts and entities, a described subject with
	// reports, an artifact, and a compaction segment over the turns.
	formed(t, pool, schema,
		subjectTurn("subject-1", "Marta works at Ensera and cycles to work."),
		subjectTurn("subject-1", "Marta lives in Dublin."))
	described(t, pool, schema)
	if _, err := pg.NewArtifactStore(pool, schema).Put(ctx, "p1", uuid.NewString(), pg.ArtifactPut{
		ID: uuid.NewString(), DataSubjectID: "subject-1", Kind: "state", Content: []byte("opaque session state")}); err != nil {
		t.Fatal(err)
	}
	segments := pg.NewSegmentStore(pool, schema)
	turns, err := segments.Formed(ctx, "p1", "subject-1")
	if err != nil || len(turns) == 0 {
		t.Fatalf("the subject's formed turns are the material a segment covers: %d %v", len(turns), err)
	}
	if _, err := segments.Write(ctx, "p1", "subject-1",
		compaction.Plan{Level: 1, From: turns[0].Offset, To: turns[len(turns)-1].Offset, Turns: len(turns)},
		compaction.Summary{Text: "Marta works at Ensera, cycles to work and lives in Dublin."}); err != nil {
		t.Fatal(err)
	}

	// Count every ordinary table in the namespace. Partitions are excluded: a write goes through the
	// parent, and a child losing rows is the parent losing them.
	counts := func() map[string]int {
		t.Helper()
		rows, err := pool.Query(ctx, `
			SELECT c.relname, (xpath('/row/c/text()',
			         query_to_xml(format('SELECT count(*) AS c FROM %I.%I', n.nspname, c.relname), false, true, '')))[1]::text::int
			  FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
			 WHERE n.nspname = $1 AND c.relkind = 'r' AND NOT c.relispartition`, schema.String())
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := map[string]int{}
		for rows.Next() {
			var table string
			var n int
			if err := rows.Scan(&table, &n); err != nil {
				t.Fatal(err)
			}
			out[table] = n
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return out
	}

	before := counts()
	receipt, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", "subject-1", "measuring the receipt")
	if err != nil {
		t.Fatal(err)
	}
	after := counts()

	named := map[string]bool{}
	for key := range receipt.Deleted {
		named[key] = true
	}
	// The receipt names the messages as the export does, by the section both sides compare.
	if named["message"] {
		named["turn_message"] = true
	}
	// Two tables lose rows and are deliberately not counted, because they carry no words and the
	// thing they describe is counted beside them:
	//
	//   projection_dependency — a registration of a projection, and every projection kind is
	//   counted by name already. Counting the registrations too would report the same deletion
	//   twice under two names.
	//
	//   community_member — three identifiers tying an entity to a theme. It cascades from the
	//   entity, and entities are counted; membership without its entity is not a thing anybody
	//   holds.
	//
	// A table added later is in neither list, so it fails here until somebody decides which it is.
	structure := map[string]bool{"projection_dependency": true, "community_member": true}
	var silent []string
	for table, was := range before {
		if after[table] < was && !named[table] && !structure[table] {
			silent = append(silent, table)
		}
	}
	sort.Strings(silent)
	if len(silent) > 0 {
		t.Errorf("these tables lost rows and the receipt does not name them: %v\nreceipt: %v", silent, receipt.Deleted)
	}
	// And the fixture must actually have exercised something, or this measures nothing.
	if len(before) < 20 || receipt.Deleted["observation"] == 0 {
		t.Fatalf("the fixture did not produce an erasure worth measuring: %d tables, receipt %v", len(before), receipt.Deleted)
	}
}
