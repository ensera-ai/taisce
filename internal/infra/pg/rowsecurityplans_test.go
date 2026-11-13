// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/jackc/pgx/v5"
)

// TestARowSecurityPolicyTakesTheIndexFromAReadOfThePast is why the boundary between projects is
// not a row-security policy, and it is the test to re-run before proposing one again.
//
// A policy that compares a row's project to a setting the transaction states would turn a
// forgotten predicate from everybody's rows into no rows. What it costs is decided by one rule of
// the database: on a table with a policy, a condition is allowed to drive an index only if its
// operator is marked leakproof — unable to reveal its arguments through an error or a side
// channel. Equality on text and on a uuid is. A range containing a moment (`@>`) is not, and that
// operator is how every read of the past finds the version that was true or believed then.
//
// So the policy is applied here to every table that carries a project, and the statements recall
// runs are planned before and after, as the role the service runs as, with the planner left to
// choose. Rows read are counted, because they do not depend on the machine: a node's rows
// returned plus the rows its filter threw away, times the number of times it ran.
//
//   - The current read is unchanged: the same indexes, the same rows.
//   - A read of what was valid at a moment reads every fact ever recorded about the entity, where
//     it read only those valid then.
//   - A read of what was believed at a moment reads every version of each fact it walks, where it
//     read one.
//
// The second and third grow with history, which is the thing this system keeps. That is the
// measurement the decision rests on.
//
// It says nothing about latency: this is a count of rows on a fixture, on whatever machine runs
// it. And it holds for the database it ran on. If range containment is ever marked leakproof, or
// the past is found some other way, this test fails — and that failure is the signal to reopen
// the decision, not a defect to patch.
func TestARowSecurityPolicyTakesTheIndexFromAReadOfThePast(t *testing.T) {
	const versions, pastFacts = 2000, 5000
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "row_security_plans")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")

	fact := statedAt(t, ctx, pg.NewObservationStore(pool), pg.NewFactStore(pool), schema, time.Now().UTC(), "Atlas works at Ensera",
		domain.Claim{Subject: "Atlas", Predicate: "works_at", Object: "Ensera", Statement: "Atlas works at Ensera", Cardinality: domain.CardinalityMany})
	var revised, storied string
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT subject_entity_id::text,object_entity_id::text FROM {schema}.fact WHERE fact_id=$1`), fact).Scan(&revised, &storied); err != nil {
		t.Fatal(err)
	}
	// A long past, of both kinds. One entity has many facts that each held for an hour and then
	// stopped. The other has one fact, revised every day for years.
	if _, err := pool.Exec(ctx, schema.SQL(`
		INSERT INTO {schema}.fact(fact_id,scope,subject_entity_id,predicate,statement,cardinality,valid,known,source_role)
		SELECT md5('past '||n)::uuid,'p1',$1::uuid,'works_at','plan fixture','many',
		       tstzrange('2000-01-01'::timestamptz+n*interval '1 hour','2000-01-01'::timestamptz+(n+1)*interval '1 hour'),
		       '[2000-01-01,)'::tstzrange,'user'
		  FROM generate_series(1,$2::int) n`), storied, pastFacts); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`
		INSERT INTO {schema}.fact_history(history_id,scope,fact_id,valid,known,source_observation_id)
		SELECT gen_random_uuid(),'p1',$1,'[2010-01-01,)'::tstzrange,
		       tstzrange('2010-01-01'::timestamptz+n*interval '1 day','2010-01-02'::timestamptz+n*interval '1 day'),e.source_observation_id
		  FROM generate_series(0,$2::int-1) n CROSS JOIN {schema}.fact_evidence e WHERE e.fact_id=$1`), fact, versions); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`ANALYZE {schema}.fact; ANALYZE {schema}.fact_history; ANALYZE {schema}.entity`)); err != nil {
		t.Fatal(err)
	}

	about := func(entity string, more ...any) []any {
		return append([]any{[]string{"p1"}, []string{entity}, 50, []string{"user"}, 1, domain.Fanout, nil}, more...)
	}
	statements := map[string]struct {
		query string
		args  []any
	}{
		"current":  {pg.FactsAboutQueryForTesting, about(revised)},
		"valid at": {pg.FactsValidAtQueryForTesting, about(storied, time.Date(2000, 3, 1, 0, 30, 0, 0, time.UTC))},
		"known at": {pg.FactsKnownAtQueryForTesting, about(revised, time.Date(2012, 6, 1, 12, 0, 0, 0, time.UTC))},
	}
	// planned runs each statement as the service's role, in a transaction that states its project,
	// and returns what it read: rows per table, and whether a range condition drove an index.
	type reading struct {
		rows    map[string]float64
		byRange map[string]bool
	}
	planned := func() map[string]reading {
		t.Helper()
		out := map[string]reading{}
		for name, s := range statements {
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(ctx, "SET LOCAL ROLE "+pgx.Identifier{migrate.DataRole}.Sanitize()); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(ctx, `SELECT set_config('taisce.project','p1',true)`); err != nil {
				t.Fatal(err)
			}
			var raw json.RawMessage
			if err := tx.QueryRow(ctx, "EXPLAIN (ANALYZE, TIMING OFF, FORMAT JSON) "+schema.SQL(s.query), s.args...).Scan(&raw); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			_ = tx.Rollback(ctx)
			var plans []struct{ Plan rowSecurityPlanNode }
			if err := json.Unmarshal(raw, &plans); err != nil {
				t.Fatal(err)
			}
			r := reading{map[string]float64{}, map[string]bool{}}
			var inspect func(rowSecurityPlanNode)
			inspect = func(n rowSecurityPlanNode) {
				if n.Relation != "" {
					r.rows[n.Relation] += (n.ActualRows + n.Removed) * n.Loops
					t.Logf("   %s %s %s via %q cond %s: %v", name, n.NodeType, n.Relation, n.Index, n.IndexCond, (n.ActualRows+n.Removed)*n.Loops)
					if strings.Contains(n.IndexCond, "@>") {
						r.byRange[n.Relation] = true
					}
				}
				for _, child := range n.Plans {
					inspect(child)
				}
			}
			inspect(plans[0].Plan)
			t.Logf("%s: rows read %v, found by range %v", name, r.rows, r.byRange)
			out[name] = r
		}
		return out
	}

	without := planned()

	// The policy, on every table that carries a project, and forced so that the owner is held to
	// it as well.
	rows, err := pool.Query(ctx, `
		SELECT k.relname FROM pg_class k
		  JOIN pg_attribute a ON a.attrelid=k.oid AND a.attname='scope' AND NOT a.attisdropped
		 WHERE k.relnamespace=to_regnamespace($1) AND k.relkind IN ('r','p') AND NOT k.relispartition`, schema.String())
	if err != nil {
		t.Fatal(err)
	}
	tables, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) < 40 {
		t.Fatalf("only %d tables were found to carry a project; the policy would not cover the schema", len(tables))
	}
	for _, table := range tables {
		name := schema.String() + "." + pgx.Identifier{table}.Sanitize()
		for _, statement := range []string{
			"ALTER TABLE " + name + " ENABLE ROW LEVEL SECURITY",
			"ALTER TABLE " + name + " FORCE ROW LEVEL SECURITY",
			"CREATE POLICY one_project ON " + name + " USING (scope = current_setting('taisce.project', true))",
		} {
			if _, err := pool.Exec(ctx, statement); err != nil {
				t.Fatalf("%s: %v", statement, err)
			}
		}
	}
	with := planned()

	// The policy is in force for the role that read: with no project stated, it sees nothing.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE "+pgx.Identifier{migrate.DataRole}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	var seen int
	if err := tx.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.fact`)).Scan(&seen); err != nil || seen != 0 {
		t.Fatalf("with a policy and no project stated the role saw %d facts (%v); the comparison below was not made under a policy", seen, err)
	}

	// The current read keeps its plan: nothing in it is found by a range.
	for table, before := range without["current"].rows {
		if after := with["current"].rows[table]; after > before {
			t.Errorf("the current read read %v rows of %s without a policy and %v with one", before, table, after)
		}
	}

	// What was valid at a moment: found by the range, and then not.
	if !without["valid at"].byRange["fact"] || without["valid at"].rows["fact"] > float64(domain.Fanout+1) {
		t.Fatalf("without a policy, the read of what was valid at a moment should find its facts by the range and read no more than the cap: %+v", without["valid at"])
	}
	if with["valid at"].byRange["fact"] || with["valid at"].rows["fact"] < pastFacts {
		t.Fatalf("under a policy, the read of what was valid at a moment was expected to lose the range from its index and read every fact about the entity (%d). It read %v. If the database now lets a range drive an index under a policy, the decision against row-level security should be reopened.",
			pastFacts, with["valid at"].rows["fact"])
	}

	// What was believed at a moment: one version found by the range, and then every version read.
	if !without["known at"].byRange["fact_history"] || without["known at"].rows["fact_history"] != 1 {
		t.Fatalf("without a policy, the read of what was believed at a moment should find one version by the range: %+v", without["known at"])
	}
	if with["known at"].byRange["fact_history"] || with["known at"].rows["fact_history"] < versions {
		t.Fatalf("under a policy, the read of what was believed at a moment was expected to read every version of the fact (%d). It read %v. If the database now lets a range drive an index under a policy, the decision against row-level security should be reopened.",
			versions, with["known at"].rows["fact_history"])
	}
}

type rowSecurityPlanNode struct {
	NodeType   string  `json:"Node Type"`
	Index      string  `json:"Index Name"`
	Relation   string  `json:"Relation Name"`
	IndexCond  string  `json:"Index Cond"`
	ActualRows float64 `json:"Actual Rows"`
	Loops      float64 `json:"Actual Loops"`
	Removed    float64 `json:"Rows Removed by Filter"`
	Plans      []rowSecurityPlanNode
}
