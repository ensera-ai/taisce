// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
)

// The queue is read by seeking an index, not by scanning the table. Both reads are measured, because
// the two have different shapes: the open queue is the common one and almost every row it skips is
// promoted, and a partial index is the only thing that keeps it from reading them.
//
// Ten thousand reports, one in twenty of them open. A plan taken on an empty table proves nothing:
// PostgreSQL scans a small relation because scanning it is correct, and the plan that matters is the
// one it picks once the table is big enough for the choice to be real.
func TestFeedbackPagesSeekIndexesRatherThanScanningTheQueue(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "feedback_plans")
	march := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	target := statedAt(t, ctx, pg.NewObservationStore(pool), pg.NewFactStore(pool), schema, march,
		"Atlas lives in Dublin", domain.Claim{Subject: "Atlas", Predicate: "lives_in", Object: "Dublin",
			Statement: "Atlas lives in Dublin", Cardinality: domain.CardinalityOne, ValidFrom: march})
	// Five hundred records rather than one, because an index on the target says nothing when every
	// row has the same target: PostgreSQL scans a table whose filter matches all of it, and it is
	// right to. The measurement only means something when narrowing actually narrows.
	if _, err := pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.fact(fact_id,scope,predicate,statement,cardinality,valid,source_role)
        SELECT md5('feedback target '||n)::uuid,'p1','works_at','plan fixture','many','[2020-01-01,)'::tstzrange,'user'
          FROM generate_series(1,499) n`)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.memory_feedback
        (feedback_id,scope,target_fact_id,note,recorded_by,recorded_at,promoted_by,promoted_at)
        SELECT md5('feedback '||n)::uuid,'p1',
               CASE WHEN n%20=1 THEN $1::uuid ELSE md5('feedback target '||(1+n%499))::uuid END,
               'plan fixture',gen_random_uuid(),
               '2026-03-01'::timestamptz+n*interval '1 second',
               CASE WHEN n%20=0 THEN NULL ELSE gen_random_uuid() END,
               CASE WHEN n%20=0 THEN NULL ELSE '2026-04-01'::timestamptz END
          FROM generate_series(1,10000) n`), target); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`ANALYZE {schema}.memory_feedback`)); err != nil {
		t.Fatal(err)
	}
	middle := march.Add(9000 * time.Second)
	// The cursor compares (recorded_at, feedback_id) as a tuple, so the highest id at that instant
	// is what includes every report written in the same second rather than skipping the ones whose
	// id happens to sort above an arbitrary one.
	const highestUUID = "ffffffff-ffff-ffff-ffff-ffffffffffff"
	for _, tc := range []struct {
		name, index string
		recordID    string
		openOnly    bool
	}{
		{"the open queue", "memory_feedback_open_idx", "", true},
		{"one record", "memory_feedback_target_idx", target, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query, args, err := pg.FeedbackListQueryForTesting("p1", tc.recordID, tc.openOnly,
				&pg.FeedbackCursor{RecordedAt: middle, ID: highestUUID}, pg.MaxFeedbackPage)
			if err != nil {
				t.Fatal(err)
			}
			var raw json.RawMessage
			if err := pool.QueryRow(ctx, "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "+schema.SQL(query), args...).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			t.Logf("%s plan=%s", tc.name, raw)
			var plans []struct{ Plan anchorPlanNode }
			if err := json.Unmarshal(raw, &plans); err != nil {
				t.Fatal(err)
			}
			found := false
			var inspect func(anchorPlanNode)
			inspect = func(n anchorPlanNode) {
				if n.Index == tc.index {
					found = true
				}
				if n.NodeType == "Seq Scan" && n.Relation == "memory_feedback" {
					t.Fatal("the queue was read by scanning every report ever written")
				}
				for _, child := range n.Plans {
					inspect(child)
				}
			}
			inspect(plans[0].Plan)
			if !found {
				t.Fatalf("missing expected seek index %s", tc.index)
			}
		})
	}

	// And the paging itself terminates, returns each report once and never goes backwards.
	store := pg.NewFeedbackStore(pool, schema, pg.NewRecordStore(pool, schema))
	seen := map[string]bool{}
	var cursor *pg.FeedbackCursor
	var last *time.Time
	pages := 0
	for {
		page, err := store.List(ctx, "p1", "", true, cursor, pg.MaxFeedbackPage)
		if err != nil || len(page.Feedback) > pg.MaxFeedbackPage {
			t.Fatalf("page: %v", err)
		}
		for _, item := range page.Feedback {
			if seen[item.ID] || (last != nil && item.RecordedAt.After(*last)) {
				t.Fatal("a report was returned twice or out of order")
			}
			seen[item.ID] = true
			at := item.RecordedAt
			last = &at
		}
		pages++
		if page.Next == nil {
			break
		}
		cursor = page.Next
		if pages > 20 {
			t.Fatal("paging did not terminate")
		}
	}
	if len(seen) != 500 {
		t.Fatalf("the open queue holds 500 reports and paging saw %d", len(seen))
	}
}
