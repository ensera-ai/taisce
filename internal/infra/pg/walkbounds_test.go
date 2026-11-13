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
	"github.com/jackc/pgx/v5/pgxpool"
)

// instanceShaped fills a namespace the way an instance fills: two projects, three thousand
// entities in each with about twenty facts apiece, a quarter of them current — and in the first
// project three entities that stand out. One is named by a single fact. One has five thousand
// facts that each held for an hour and stopped. One has five thousand facts that all still hold.
//
// The shape is the point. A planner chooses a plan for a hop before it knows which entity the hop
// is at, so it plans for a typical entity, and what it does at the unusual ones is what these
// fixtures exist to show. A fixture where the typical entity is the busy one shows nothing.
func instanceShaped(t *testing.T, name string) (ctx context.Context, pool *pgxpool.Pool, schema pg.Schema, quiet, stopped, busy string) {
	t.Helper()
	ctx = context.Background()
	pool = testPool(t)
	schema = tenant(t, pool, name)
	t.Cleanup(func() { pool.Exec(context.Background(), "DROP SCHEMA "+schema.String()+" CASCADE") })
	fact := statedAt(t, ctx, pg.NewObservationStore(pool), pg.NewFactStore(pool), schema, time.Now().UTC(), "Atlas works at Ensera",
		domain.Claim{Subject: "Atlas", Predicate: "works_at", Object: "Ensera", Statement: "Atlas works at Ensera", Cardinality: domain.CardinalityMany})
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT subject_entity_id::text FROM {schema}.fact WHERE fact_id=$1`), fact).Scan(&quiet); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`
INSERT INTO {schema}.project(scope,label) VALUES('p2','p2');
INSERT INTO {schema}.entity(entity_id,scope,canonical_name,normalized_name)
SELECT md5(p||' entity '||n)::uuid,p,'entity '||n,'entity '||n FROM unnest(ARRAY['p1','p2']) p CROSS JOIN generate_series(1,3000) n;
INSERT INTO {schema}.fact(fact_id,scope,subject_entity_id,object_entity_id,predicate,statement,cardinality,valid,known,source_role)
SELECT md5(p||' fact '||n)::uuid,p,md5(p||' entity '||(3+n%2997))::uuid,md5(p||' entity '||(3+(n*7)%2997))::uuid,'works_at','plan fixture','many',
       CASE WHEN n%4=0 THEN '[2020-01-01,)'::tstzrange
            ELSE tstzrange('2000-01-01'::timestamptz+(n%5000)*interval '1 hour','2000-01-01'::timestamptz+(n%5000+1)*interval '1 hour') END,
       '[2000-01-01,)'::tstzrange,'user'
  FROM unnest(ARRAY['p1','p2']) p CROSS JOIN generate_series(1,60000) n;
INSERT INTO {schema}.fact(fact_id,scope,subject_entity_id,predicate,statement,cardinality,valid,known,source_role)
SELECT md5('stopped '||n)::uuid,'p1',md5('p1 entity 1')::uuid,'works_at','plan fixture','many',
       tstzrange('2000-01-01'::timestamptz+n*interval '1 hour','2000-01-01'::timestamptz+(n+1)*interval '1 hour'),'[2000-01-01,)'::tstzrange,'user'
  FROM generate_series(1,5000) n;
INSERT INTO {schema}.fact(fact_id,scope,subject_entity_id,predicate,statement,cardinality,valid,known,source_role)
SELECT md5('busy '||n)::uuid,'p1',md5('p1 entity 2')::uuid,'works_at','plan fixture','many','[2001-01-01,)'::tstzrange,'[2001-01-01,)'::tstzrange,'user'
  FROM generate_series(1,5000) n;
ANALYZE {schema}.fact; ANALYZE {schema}.entity; ANALYZE {schema}.fact_history`)); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT md5('p1 entity 1')::uuid::text, md5('p1 entity 2')::uuid::text`).Scan(&stopped, &busy); err != nil {
		t.Fatal(err)
	}
	return ctx, pool, schema, quiet, stopped, busy
}

// factsRead plans a walk and says how it read the fact table: rows by index, and whether any of
// it was a scan of the whole table or of an index with nothing to seek by.
func factsRead(t *testing.T, ctx context.Context, pool *pgxpool.Pool, schema pg.Schema, statement, entity string, more ...any) (byIndex map[string]float64, total float64) {
	t.Helper()
	args := append([]any{[]string{"p1"}, []string{entity}, 50, []string{"user", "assistant", "tool"}, 1, domain.Fanout, nil}, more...)
	var raw json.RawMessage
	if err := pool.QueryRow(ctx, "EXPLAIN (ANALYZE, TIMING OFF, FORMAT JSON) "+schema.SQL(statement), args...).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var plans []struct{ Plan rowSecurityPlanNode }
	if err := json.Unmarshal(raw, &plans); err != nil {
		t.Fatal(err)
	}
	byIndex = map[string]float64{}
	var inspect func(rowSecurityPlanNode)
	inspect = func(n rowSecurityPlanNode) {
		if n.Relation == "fact" {
			if n.NodeType == "Seq Scan" || (n.Index != "" && n.IndexCond == "") {
				t.Fatalf("the walk read the fact table with nothing to seek by: %s %s", n.NodeType, n.Index)
			}
			read := (n.ActualRows + n.Removed) * n.Loops
			byIndex[n.Index] += read
			total += read
		}
		for _, child := range n.Plans {
			inspect(child)
		}
	}
	inspect(plans[0].Plan)
	return byIndex, total
}

// TestTheFanoutCapBoundsABusyEntityAmongManyQuietOnes holds the cap to bounding what is read at a
// busy entity in an instance where most entities are quiet.
//
// The cap is 64 facts and one more to say it was cut. Before the walk named its project by
// equality, the planner — planning for a typical entity, which has twenty facts and costs nothing
// to sort — read all five thousand of the busy entity's facts and sorted them. The bound held only
// on a fixture where the typical entity was the busy one.
func TestTheFanoutCapBoundsABusyEntityAmongManyQuietOnes(t *testing.T) {
	ctx, pool, schema, quiet, _, busy := instanceShaped(t, "walk_bound_busy")
	byIndex, total := factsRead(t, ctx, pool, schema, pg.FactsAboutQueryForTesting, busy)
	t.Logf("the busy entity, current read: %v", byIndex)
	if rows, ok := byIndex["fact_subject_project_walk_idx"]; !ok || rows > float64(domain.Fanout+1) {
		t.Fatalf("the walk read %v rows at an entity with 5,000 current facts, through %v; the cap is %d and one row of signal",
			total, byIndex, domain.Fanout)
	}
	if total > float64(2*(domain.Fanout+1)) {
		t.Fatalf("the two sides of one hop read %v rows between them for a cap of %d each", total, domain.Fanout)
	}
	// And it costs a quiet entity nothing: one fact, one row.
	if _, rows := factsRead(t, ctx, pool, schema, pg.FactsAboutQueryForTesting, quiet); rows > 2 {
		t.Fatalf("the walk read %v rows at an entity named by one fact", rows)
	}
}

// TestAReadOfThePastReadsItsOwnEntityAndNeverTheInstance says what a read of the past costs, in
// rows, and holds the two halves of it.
//
// What is held: a read of what was believed at a moment, or of what was believed at one moment
// about another, reads the facts of the entity it is at and no other entity's — one row at a
// quiet entity, whatever the instance holds. A read of what was valid at a moment finds its facts
// by the moment and stops at the cap.
//
// What is not: at an entity with a long history, a read of what was believed reads all of that
// history. Which version of a fact was believed at a moment is found per fact, so there is no
// order an index could hold that would let the walk stop early. The cost is the entity's history
// and not the cap. That is asserted here too, so that it is a stated limit with a test named for
// it and not a surprise — and so that a change which removes it fails this test and is written up.
func TestAReadOfThePastReadsItsOwnEntityAndNeverTheInstance(t *testing.T) {
	ctx, pool, schema, quiet, stopped, _ := instanceShaped(t, "walk_bound_past")
	valid := time.Date(2000, 3, 1, 0, 30, 0, 0, time.UTC)
	believed := time.Date(2012, 6, 1, 12, 0, 0, 0, time.UTC)
	var history float64
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.fact WHERE scope='p1' AND (subject_entity_id=$1::uuid OR object_entity_id=$1::uuid)`), stopped).Scan(&history); err != nil {
		t.Fatal(err)
	}
	for name, read := range map[string]struct {
		statement string
		at        []any
	}{
		"believed at a moment":                 {pg.FactsKnownAtQueryForTesting, []any{believed}},
		"believed at one moment about another": {pg.FactsBitemporalQueryForTesting, []any{valid, believed}},
	} {
		if byIndex, rows := factsRead(t, ctx, pool, schema, read.statement, quiet, read.at...); rows > 2 {
			t.Errorf("%s, at an entity named by one fact: %v rows of fact read through %v", name, rows, byIndex)
		}
		byIndex, rows := factsRead(t, ctx, pool, schema, read.statement, stopped, read.at...)
		t.Logf("%s, at an entity with %v facts: %v rows read through %v", name, history, rows, byIndex)
		if rows > history {
			t.Errorf("%s read %v rows of fact at an entity that has %v: it read another entity's", name, rows, history)
		}
		if rows <= float64(2*(domain.Fanout+1)) {
			t.Errorf("%s read %v rows at an entity with %v facts, which is within the cap. The documents say this read costs the entity's history; if that is no longer so, they are wrong and this limit can be struck", name, rows, history)
		}
	}
	byIndex, rows := factsRead(t, ctx, pool, schema, pg.FactsValidAtQueryForTesting, stopped, valid)
	t.Logf("valid at a moment, at the same entity: %v rows read through %v", rows, byIndex)
	if rows > float64(2*(domain.Fanout+1)) {
		t.Errorf("the read of what was valid at a moment read %v rows at an entity with %v facts", rows, history)
	}
}
