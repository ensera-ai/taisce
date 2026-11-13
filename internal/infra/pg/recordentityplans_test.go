// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The indexes that hold one entity's current facts in fact_id order. An entity's page may be
// selected through these and through nothing else; the second pair is the same order led by the
// project, which the page uses as soon as a schema has it.
var entityPageSeekIndexes = map[string]bool{
	"fact_subject_walk_idx": true, "fact_object_walk_idx": true,
	"fact_subject_project_walk_idx": true, "fact_object_project_walk_idx": true,
}

const (
	busyCurrentEachSide = 4000
	busyClosed          = 4000
	subjectsShare       = 30
)

// busyAmongQuiet fills a namespace the way an instance fills, because a plan is chosen for the
// typical entity and judged at the unusual one. Two projects, 2,500 entities in each with about
// sixteen facts as subject and sixteen as object, a quarter of them current — and in the first
// project two that stand out. `busy` is the subject of 4,000 facts that hold now, the object of
// 4,000 more, and the subject of 4,000 that stopped. `stopped` is named only by 4,000 facts that
// each held for an hour.
//
// Every fact of the busy entity has one registered source. They belong to "other-subject", except
// the thirty last in fact_id order, which belong to "subject-1": the arrangement in which
// narrowing an entity's page to a data subject costs the most.
func busyAmongQuiet(t *testing.T, name string) (ctx context.Context, pool *pgxpool.Pool, schema pg.Schema, quiet, stopped, busy string) {
	t.Helper()
	ctx = context.Background()
	pool = testPool(t)
	schema = tenant(t, pool, name)
	t.Cleanup(func() { pool.Exec(context.Background(), "DROP SCHEMA "+schema.String()+" CASCADE") })
	source, err := pg.NewObservationStore(pool).Append(ctx, schema, turnOf(domain.Message{Role: domain.RoleUser, Content: "plan fixture"}))
	if err != nil {
		t.Fatal(err)
	}
	// One statement each. A fact written takes a lock on each entity it names until its transaction
	// ends, and one transaction naming every entity of the fixture would hold more than a server has.
	for _, statement := range []string{
		`INSERT INTO {schema}.project(scope,label) VALUES('p2','p2')`,
		`INSERT INTO {schema}.entity(entity_id,scope,canonical_name,normalized_name)
SELECT md5(p||' entity '||n)::uuid,p,'entity '||n,'entity '||n FROM unnest(ARRAY['p1','p2']) p CROSS JOIN generate_series(1,2500) n`,
		quietFacts("p1"), quietFacts("p2"),
		`INSERT INTO {schema}.fact(fact_id,scope,subject_entity_id,predicate,statement,cardinality,valid,known,source_role)
SELECT md5('stopped '||n)::uuid,'p1',md5('p1 entity 1')::uuid,'works_at','plan fixture','many',
       tstzrange('2000-01-01'::timestamptz+n*interval '1 hour','2000-01-01'::timestamptz+(n+1)*interval '1 hour'),'[2000-01-01,)'::tstzrange,'user'
  FROM generate_series(1,` + fmt.Sprint(busyClosed) + `) n`,
		`INSERT INTO {schema}.fact(fact_id,scope,subject_entity_id,object_entity_id,predicate,statement,cardinality,valid,known,source_role)
SELECT md5('busy as subject '||n)::uuid,'p1',md5('p1 entity 2')::uuid,md5('p1 entity 3')::uuid,'works_at','plan fixture','many','[2001-01-01,)'::tstzrange,'[2001-01-01,)'::tstzrange,'user'
  FROM generate_series(1,` + fmt.Sprint(busyCurrentEachSide) + `) n`,
		`INSERT INTO {schema}.fact(fact_id,scope,subject_entity_id,object_entity_id,predicate,statement,cardinality,valid,known,source_role)
SELECT md5('busy as object '||n)::uuid,'p1',md5('p1 entity 3')::uuid,md5('p1 entity 2')::uuid,'works_at','plan fixture','many','[2001-01-01,)'::tstzrange,'[2001-01-01,)'::tstzrange,'user'
  FROM generate_series(1,` + fmt.Sprint(busyCurrentEachSide) + `) n`,
		`INSERT INTO {schema}.fact(fact_id,scope,subject_entity_id,predicate,statement,cardinality,valid,known,source_role)
SELECT md5('busy closed '||n)::uuid,'p1',md5('p1 entity 2')::uuid,'works_at','plan fixture','many',
       tstzrange('2000-01-01'::timestamptz+n*interval '1 hour','2000-01-01'::timestamptz+(n+1)*interval '1 hour'),'[2000-01-01,)'::tstzrange,'user'
  FROM generate_series(1,` + fmt.Sprint(busyClosed) + `) n`,
	} {
		if _, err := pool.Exec(ctx, schema.SQL(statement)); err != nil {
			t.Fatalf("fixture: %v\n%s", err, statement)
		}
	}
	if _, err := pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.projection_dependency(source_observation_id,scope,projection_kind,projection_id,data_subject_id)
SELECT $1,'p1','fact',fact_id::text,CASE WHEN row_number() OVER (ORDER BY fact_id DESC) <= $2 THEN 'subject-1' ELSE 'other-subject' END
  FROM {schema}.fact
 WHERE scope='p1' AND (subject_entity_id=md5('p1 entity 2')::uuid OR object_entity_id=md5('p1 entity 2')::uuid) AND upper_inf(valid) AND upper_inf(known)`), source.ID, subjectsShare); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`ANALYZE {schema}.fact; ANALYZE {schema}.entity; ANALYZE {schema}.projection_dependency`)); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT md5('p1 entity 1')::uuid::text, md5('p1 entity 2')::uuid::text, md5('p1 entity 100')::uuid::text`).Scan(&stopped, &busy, &quiet); err != nil {
		t.Fatal(err)
	}
	return ctx, pool, schema, quiet, stopped, busy
}

func quietFacts(project string) string {
	return `INSERT INTO {schema}.fact(fact_id,scope,subject_entity_id,object_entity_id,predicate,statement,cardinality,valid,known,source_role)
SELECT md5('` + project + ` fact '||n)::uuid,'` + project + `',md5('` + project + ` entity '||(4+n%2497))::uuid,md5('` + project + ` entity '||(4+(n*7)%2497))::uuid,'works_at','plan fixture','many',
       CASE WHEN n%4=0 THEN '[2020-01-01,)'::tstzrange
            ELSE tstzrange('2000-01-01'::timestamptz+(n%5000)*interval '1 hour','2000-01-01'::timestamptz+(n%5000+1)*interval '1 hour') END,
       '[2000-01-01,)'::tstzrange,'user'
  FROM generate_series(1,40000) n`
}

type entityPagePlanNode struct {
	NodeType   string  `json:"Node Type"`
	Index      string  `json:"Index Name"`
	Relation   string  `json:"Relation Name"`
	IndexCond  string  `json:"Index Cond"`
	ActualRows float64 `json:"Actual Rows"`
	Loops      float64 `json:"Actual Loops"`
	Removed    float64 `json:"Rows Removed by Filter"`
	SharedHit  float64 `json:"Shared Hit Blocks"`
	SharedRead float64 `json:"Shared Read Blocks"`
	Plans      []entityPagePlanNode
}

type entityPageRead struct {
	// sought is rows of fact read to select the page, through the indexes that keep an entity's
	// current facts in order. fetched is rows read by id afterwards, for the page's own text.
	sought, fetched, buffers float64
	plan                     string
}

// readEntityPage plans and runs one page of an entity's inventory as the store runs it — the same
// statement, the same arguments, inside a transaction that has run the same statement before it —
// and says what it read. `mode` is the plan the server would use for a prepared statement: one
// made for this entity, or one made without knowing which entity it is for. A connection uses the
// first for its first few executions and may settle on the second, so a bound has to hold in both.
//
// It fails if the fact table was read any other way: a scan of it, an index with nothing to seek
// by, or an index that does not keep the order.
func readEntityPage(t *testing.T, ctx context.Context, pool *pgxpool.Pool, schema pg.Schema, mode, entity, subject string, after *pg.RecordCursor, limit int) entityPageRead {
	t.Helper()
	query, args, err := pg.EntityRecordListQueryForTesting("p1", entity, subject, after, limit)
	if err != nil {
		t.Fatal(err)
	}
	literals := make([]string, 0, len(args))
	for _, arg := range args {
		switch value := arg.(type) {
		case string:
			literals = append(literals, "'"+strings.ReplaceAll(value, "'", "''")+"'")
		case int:
			literals = append(literals, fmt.Sprint(value))
		default:
			t.Fatalf("an argument of a kind this helper cannot write as a literal: %T", arg)
		}
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	// By name: the pool keeps its own prepared statements on this connection and expects them there.
	defer conn.Exec(context.Background(), "DEALLOCATE entity_page")
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	for _, statement := range []string{
		pg.OrderedSeekForTesting,
		"SET LOCAL plan_cache_mode = force_" + mode + "_plan",
		"PREPARE entity_page AS " + schema.SQL(query),
	} {
		if _, err := tx.Exec(ctx, statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	var raw json.RawMessage
	if err := tx.QueryRow(ctx, "EXPLAIN (ANALYZE, BUFFERS, TIMING OFF, FORMAT JSON) EXECUTE entity_page("+strings.Join(literals, ",")+")").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var plans []struct{ Plan entityPagePlanNode }
	if err := json.Unmarshal(raw, &plans); err != nil {
		t.Fatal(err)
	}
	read := entityPageRead{plan: string(raw), buffers: plans[0].Plan.SharedHit + plans[0].Plan.SharedRead}
	var inspect func(entityPagePlanNode)
	inspect = func(n entityPagePlanNode) {
		if n.NodeType == "Sort" || strings.HasPrefix(n.NodeType, "Bitmap") {
			t.Fatalf("the page was planned with a %s; the ordered seek is not in force\n%s", n.NodeType, raw)
		}
		if n.Relation == "fact" {
			rows := (n.ActualRows + n.Removed) * n.Loops
			switch {
			case entityPageSeekIndexes[n.Index] && n.IndexCond != "":
				read.sought += rows
			case (n.Index == "fact_scope_id_uniq" || n.Index == "fact_pkey") && strings.Contains(n.IndexCond, "fact_id = p.fact_id"):
				read.fetched += rows
			default:
				t.Fatalf("the page read the fact table through %s %q (%s), which keeps no entity's facts in order\n%s", n.NodeType, n.Index, n.IndexCond, raw)
			}
		}
		for _, child := range n.Plans {
			inspect(child)
		}
	}
	inspect(plans[0].Plan)
	return read
}

// TestAnEntitysPageReadsAPageAndNotTheEntity holds the cost of one page of an entity's inventory
// to the page.
//
// At an entity with 8,000 current facts and 4,000 closed ones, among 5,000 quiet entities, a page
// reads at most the page and one row from each side — the subject arm and the object arm each stop
// at the limit and one more — whether it is the first page, one in the middle, or the last, at the
// default page and at the largest, and under either plan a prepared statement can be given.
//
// Two things make that hold, and the test fails if either is removed. The current-fact predicate
// matches the partial indexes that keep an entity's facts in fact_id order, so the closed facts
// are never read: the entity with 4,000 closed facts and none current reads no row at all. And the
// statement runs with sorting and bitmap scans switched off for its transaction, without which
// the last pages of a busy entity read everything that remained on one side.
func TestAnEntitysPageReadsAPageAndNotTheEntity(t *testing.T) {
	ctx, pool, schema, quiet, stopped, busy := busyAmongQuiet(t, "record_entity_plans")
	store := pg.NewRecordStore(pool, schema)
	var cursors []string
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT array_agg(fact_id::text ORDER BY fact_id) FROM (
 SELECT fact_id, row_number() OVER (ORDER BY fact_id) AS position FROM {schema}.fact
  WHERE scope='p1' AND (subject_entity_id=$1::uuid OR object_entity_id=$1::uuid) AND upper_inf(valid) AND upper_inf(known)) ordered
 WHERE position IN ($2/2, $2-600, $2-150, $2-10)`), busy, 2*busyCurrentEachSide).Scan(&cursors); err != nil || len(cursors) != 4 {
		t.Fatalf("cursors into the busy entity's facts: %v %v", cursors, err)
	}
	positions := map[string]*pg.RecordCursor{"the first page": nil,
		"half way": {ID: cursors[0]}, "600 from the end": {ID: cursors[1]}, "150 from the end": {ID: cursors[2]}, "10 from the end": {ID: cursors[3]}}
	for _, mode := range []string{"custom", "generic"} {
		for _, limit := range []int{pg.DefaultRecordPage, pg.MaxRecordPage} {
			for position, cursor := range positions {
				read := readEntityPage(t, ctx, pool, schema, mode, busy, "", cursor, limit)
				t.Logf("busy entity, %s plan, limit %d, %s: %v rows sought, %v fetched, %v buffers", mode, limit, position, read.sought, read.fetched, read.buffers)
				if read.sought > float64(2*(limit+1)) || read.fetched > float64(limit+1) {
					t.Errorf("%s plan, limit %d, %s: %v rows read to select the page and %v to fill it, at an entity with %d current facts; the bound is %d and %d\n%s",
						mode, limit, position, read.sought, read.fetched, 2*busyCurrentEachSide, 2*(limit+1), limit+1, read.plan)
				}
			}
		}
		if read := readEntityPage(t, ctx, pool, schema, mode, stopped, "", nil, pg.MaxRecordPage); read.sought != 0 || read.fetched != 0 {
			t.Errorf("%s plan: %v rows read at an entity with %d closed facts and none current; a page must not read history", mode, read.sought+read.fetched, busyClosed)
		}
		var quietCurrent float64
		if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.fact WHERE scope='p1' AND (subject_entity_id=$1::uuid OR object_entity_id=$1::uuid) AND upper_inf(valid) AND upper_inf(known)`), quiet).Scan(&quietCurrent); err != nil {
			t.Fatal(err)
		}
		if read := readEntityPage(t, ctx, pool, schema, mode, quiet, "", nil, pg.MaxRecordPage); read.sought != quietCurrent || quietCurrent == 0 {
			t.Errorf("%s plan: %v rows read at a quiet entity with %v current facts", mode, read.sought, quietCurrent)
		}
	}
	// And the pages, read to the end through the store, are the entity's current facts: the plan
	// that is bounded is the plan of a statement that is also right.
	if all := everyPageAbout(t, ctx, store, "p1", busy, "", pg.MaxRecordPage); len(all) != 2*busyCurrentEachSide {
		t.Fatalf("the busy entity's pages held %d records, not its %d current facts", len(all), 2*busyCurrentEachSide)
	}
}

// TestAnEntitysPageNarrowedToADataSubjectCostsTheEntitysCurrentFacts says what the two filters
// together cost, and holds both halves of it.
//
// What is held: narrowed to a data subject, an entity's page still reads only that entity's
// current facts — never its closed ones, never another entity's — and when the subject's sources
// support most of them it stops at the page as the unnarrowed page does.
//
// What is not: no index holds a subject's facts by entity, so the subject is checked on each fact
// the seek reads. When the subject supports few of a busy entity's facts, the seek reads on until
// the page fills, and here that is every current fact the entity has: thirty of 8,000 belong to
// the subject and they are the last thirty. The cost is the entity's current facts and not the
// page. It is asserted so that it is a stated limit with a test named for it, and so that a change
// which removes it fails here and is written up.
func TestAnEntitysPageNarrowedToADataSubjectCostsTheEntitysCurrentFacts(t *testing.T) {
	ctx, pool, schema, _, _, busy := busyAmongQuiet(t, "record_entity_subject_plans")
	limit := pg.DefaultRecordPage
	for _, mode := range []string{"custom", "generic"} {
		most := readEntityPage(t, ctx, pool, schema, mode, busy, "other-subject", nil, limit)
		t.Logf("%s plan, the subject that supports nearly all of the entity's facts: %v rows sought, %v buffers", mode, most.sought, most.buffers)
		if most.sought > float64(2*(limit+1)) {
			t.Errorf("%s plan: %v rows read for a page of %d when the subject supports nearly every fact", mode, most.sought, limit)
		}
		few := readEntityPage(t, ctx, pool, schema, mode, busy, "subject-1", nil, limit)
		t.Logf("%s plan, the subject that supports the last %d of %d: %v rows sought, %v buffers", mode, subjectsShare, 2*busyCurrentEachSide, few.sought, few.buffers)
		if few.sought > float64(2*busyCurrentEachSide) {
			t.Errorf("%s plan: %v rows read at an entity with %d current facts; the closed ones or another entity's were read", mode, few.sought, 2*busyCurrentEachSide)
		}
		if few.sought <= float64(2*(limit+1)) {
			t.Errorf("%s plan: %v rows read for the subject that supports the last %d facts. The stated limit no longer holds: say so in docs/11-record-inspection.md and the register", mode, few.sought, subjectsShare)
		}
	}
	narrowed := everyPageAbout(t, ctx, pg.NewRecordStore(pool, schema), "p1", busy, "subject-1", 7)
	if len(narrowed) != subjectsShare {
		t.Fatalf("narrowed to the subject the pages held %d records, not the %d its source supports", len(narrowed), subjectsShare)
	}
}
