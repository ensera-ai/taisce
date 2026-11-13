package pg_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
)

type walkPlanNode struct {
	NodeType   string  `json:"Node Type"`
	Relation   string  `json:"Relation Name"`
	Index      string  `json:"Index Name"`
	ActualRows float64 `json:"Actual Rows"`
	Removed    float64 `json:"Rows Removed by Filter"`
	Plans      []walkPlanNode
}

// The fanout cap bounds the rows the traversal reads for an entity, not only the rows it returns.
// A hub named by thousands of current facts, as their subject and as their object, among tens of
// thousands of facts about something else, is walked by seeking an index in fact_id order and stopping
// at the cap. Rows read are the rows a scan returned plus the rows its filter threw away: without the
// walk indexes the planner either reads every one of the hub's facts to sort them, or walks the primary
// key in fact_id order discarding other entities' facts until the cap fills — and for a rarely named
// entity that second plan reads the whole table. Either way the cap bounded the answer, not the work.
//
// The bound is the cap plus one row, and the extra row is the price of saying so: each branch asks
// for one more than it may keep, so that reaching the cap is distinguishable from exactly filling it
// and the bundle can report a neighbourhood it had to cut. One row per branch, per entity, per hop.
func TestTheFanoutCapBoundsTheRowsAHubEntityIsReadFor(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "recall_fanout_plan")
	statedAt(t, ctx, pg.NewObservationStore(pool), pg.NewFactStore(pool), schema, time.Now().UTC(), "Atlas works at Ensera",
		domain.Claim{Subject: "Atlas", Predicate: "works_at", Object: "Ensera", Statement: "Atlas works at Ensera", Cardinality: domain.CardinalityMany})
	var hub, other string
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT entity_id::text FROM {schema}.entity WHERE normalized_name='atlas'`)).Scan(&hub); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT entity_id::text FROM {schema}.entity WHERE normalized_name='ensera'`)).Scan(&other); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`
		INSERT INTO {schema}.fact(fact_id,scope,subject_entity_id,object_entity_id,predicate,statement,cardinality,valid,source_role)
		SELECT md5('hub subject '||n)::uuid,'p1',$1::uuid,$2::uuid,'works_at','plan fixture','many','[2020-01-01,)'::tstzrange,'user'
		  FROM generate_series(1,5000) n
		UNION ALL
		SELECT md5('hub object '||n)::uuid,'p1',$2::uuid,$1::uuid,'works_at','plan fixture','many','[2020-01-01,)'::tstzrange,'user'
		  FROM generate_series(1,5000) n
		UNION ALL
		SELECT md5('elsewhere '||n)::uuid,'p1',$2::uuid,NULL,'works_at','plan fixture','many','[2020-01-01,)'::tstzrange,'user'
		  FROM generate_series(1,50000) n`), hub, other); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`ANALYZE {schema}.fact`)); err != nil {
		t.Fatal(err)
	}
	args := []any{[]string{"p1"}, []string{hub}, 50, []string{"user"}, 1, domain.Fanout, nil}
	var raw json.RawMessage
	if err := pool.QueryRow(ctx, "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "+schema.SQL(pg.FactsAboutQueryForTesting), args...).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	t.Logf("plan=%s", raw)
	var plans []struct{ Plan walkPlanNode }
	if err := json.Unmarshal(raw, &plans); err != nil {
		t.Fatal(err)
	}
	read := map[string]float64{}
	var inspect func(walkPlanNode)
	inspect = func(n walkPlanNode) {
		if n.Relation == "fact" {
			if n.NodeType == "Seq Scan" {
				t.Fatalf("the traversal read the fact table by scanning all of it")
			}
			read[n.Index] = max(read[n.Index], n.ActualRows+n.Removed)
		}
		for _, child := range n.Plans {
			inspect(child)
		}
	}
	inspect(plans[0].Plan)
	// Each side of the hop is served by an index that holds the entity's facts in fact_id order:
	// the one that leads with the project (0081), or, while it remains for the previous release,
	// the one that leads with the entity (0065). Either stops at the cap.
	for _, side := range []string{"subject", "object"} {
		rows, sought := 0.0, false
		for _, index := range []string{"fact_" + side + "_project_walk_idx", "fact_" + side + "_walk_idx"} {
			if n, ok := read[index]; ok {
				rows, sought = rows+n, true
			}
		}
		if !sought {
			t.Fatalf("the traversal did not seek a walk index for the %s side; it read the fact table through %v", side, read)
		}
		if rows > float64(domain.Fanout+1) {
			t.Fatalf("the %s side read %v rows for a cap of %d and one row of signal", side, rows, domain.Fanout)
		}
	}
}
