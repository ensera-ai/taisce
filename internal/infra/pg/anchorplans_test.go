// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
)

// These are reproducible plan diagnostics, not production latency benchmarks. Normal planner
// settings are retained: forcing a scan off would only prove that an index can be used.
func TestAnchorQueryPlans(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "anchor_plans")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	if _, err := pool.Exec(ctx, schema.SQL(`
INSERT INTO {schema}.project(scope,label) VALUES('p2','p2');
INSERT INTO {schema}.entity(entity_id,scope,canonical_name,normalized_name,aliases)
SELECT gen_random_uuid(),p,'entity '||n,'entity '||n,
 ARRAY(SELECT repeat(' ',a)||'Entity '||n FROM generate_series(1,16) a)
FROM unnest(ARRAY['p1','p2']) p CROSS JOIN generate_series(1,10000) n;
ANALYZE {schema}.entity`)); err != nil {
		t.Fatal(err)
	}
	many := make([]string, 128)
	for i := range many {
		many[i] = fmt.Sprintf("missing %d", i)
	}
	for _, tc := range []struct {
		name  string
		terms []string
		want  int
	}{
		{"canonical", []string{"entity 9000"}, 1},
		{"many_spelling_variants", []string{"entity 9000"}, 1},
		{"miss", []string{"absent"}, 0},
		{"large_question", many, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var plan json.RawMessage
			if err := pool.QueryRow(ctx, "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "+schema.SQL(pg.AnchorQueryForTesting),
				[]string{"p1"}, tc.terms, nil, domain.MaxRecallMatches+1).Scan(&plan); err != nil {
				t.Fatal(err)
			}
			t.Logf("plan=%s", plan)
			var explained []struct{ Plan anchorPlanNode }
			if err := json.Unmarshal(plan, &explained); err != nil {
				t.Fatal(err)
			}
			indexes := map[string]bool{}
			var inspect func(anchorPlanNode)
			inspect = func(n anchorPlanNode) {
				if n.NodeType == "Seq Scan" && n.Relation == "entity" {
					t.Fatal("anchor lookup scanned the entity table")
				}
				if n.Index != "" {
					indexes[n.Index] = true
				}
				for _, child := range n.Plans {
					inspect(child)
				}
			}
			inspect(explained[0].Plan)
			if !indexes["entity_named_identity_uniq"] {
				t.Fatalf("missing lookup indexes: %v", indexes)
			}
			anchors, err := pg.NewRecallStore(pool, schema).Anchors(ctx, []string{"p1"}, tc.terms)
			if err != nil || len(anchors) != tc.want {
				t.Fatalf("anchors=%d want=%d err=%v", len(anchors), tc.want, err)
			}
			for _, a := range anchors {
				if a.Entity.Scope != "p1" {
					t.Fatal("foreign project anchor")
				}
			}
		})
	}
}

type anchorPlanNode struct {
	NodeType string `json:"Node Type"`
	Relation string `json:"Relation Name"`
	Index    string `json:"Index Name"`
	Plans    []anchorPlanNode
}
