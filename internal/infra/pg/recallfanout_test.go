// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
)

// crowdEntity gives one entity `extra` further current facts with their evidence, all naming it as
// their subject, so the forward branch of the traversal has more than it may keep.
//
// Inserted directly rather than through the write path because what is under test is the traversal's
// own cut, and asserting several hundred claims through a model-shaped path would measure the
// fixture. The rows are shaped exactly as the write path shapes them: a current valid range, a
// source role, and one evidence row per fact, because evidence is an inner join and a fact without a
// receipt is not returned.
func crowdEntity(t *testing.T, ctx context.Context, pool *pgxpool.Pool, schema pg.Schema,
	subject, object, observation string, extra int) {

	t.Helper()
	if _, err := pool.Exec(ctx, schema.SQL(`
		WITH added AS (
		  INSERT INTO {schema}.fact(fact_id,scope,subject_entity_id,object_entity_id,predicate,
		                            statement,cardinality,valid,source_role)
		  SELECT md5('crowd '||n)::uuid,'p1',$1::uuid,$2::uuid,'works_at','one of many','many',
		         '[2020-01-01,)'::tstzrange,'user'
		    FROM generate_series(1,$4::int) n
		  RETURNING fact_id
		)
		INSERT INTO {schema}.fact_evidence(fact_id,source_observation_id,source_ordinal,quote,
		                                   byte_start,byte_end,extractor_version,scope)
		SELECT fact_id,$3::uuid,0,'one of many',0,11,'test','p1' FROM added`),
		subject, object, observation, extra); err != nil {
		t.Fatal(err)
	}
}

// A walk that hit the fanout cap says so, and a walk that did not does not.
//
// The cap is applied inside the traversal, per direction, per entity, per hop, so the rows that come
// back are a bounded part of a busier neighbourhood and the count alone cannot tell the two apart:
// sixty-four facts is what a hub with four hundred looks like and also what a hub with exactly
// sixty-four looks like. Without this the caller is told the bundle is complete.
func TestAWalkThatHitTheFanoutCapSaysSo(t *testing.T) {
	for _, tc := range []struct {
		name       string
		tenant     string
		facts      int
		wantCapped bool
	}{
		// One short of the cap, counting the fact the claim itself asserted.
		{name: "under the cap", tenant: "recall_fanout_under", facts: domain.Fanout - 1, wantCapped: false},
		// Exactly the cap: every fact fits, and nothing was left behind.
		{name: "exactly the cap", tenant: "recall_fanout_exact", facts: domain.Fanout, wantCapped: false},
		// One past it: the walk kept the cap's worth and left one behind.
		{name: "one past the cap", tenant: "recall_fanout_over", facts: domain.Fanout + 1, wantCapped: true},
		{name: "far past the cap", tenant: "recall_fanout_crowd", facts: domain.Fanout * 4, wantCapped: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			pool := testPool(t)
			schema := tenant(t, pool, tc.tenant)
			occurred := time.Now().UTC()
			statedAt(t, ctx, pg.NewObservationStore(pool), pg.NewFactStore(pool), schema,
				occurred, "Atlas works at Ensera",
				domain.Claim{Subject: "Atlas", Predicate: "works_at", Object: "Ensera",
					Statement: "Atlas works at Ensera", Cardinality: domain.CardinalityMany})
			var hub, other string
			if err := pool.QueryRow(ctx, schema.SQL(
				`SELECT entity_id::text FROM {schema}.entity WHERE normalized_name='atlas'`)).Scan(&hub); err != nil {
				t.Fatal(err)
			}
			if err := pool.QueryRow(ctx, schema.SQL(
				`SELECT entity_id::text FROM {schema}.entity WHERE normalized_name='ensera'`)).Scan(&other); err != nil {
				t.Fatal(err)
			}
			var sourceObservation string
			if err := pool.QueryRow(ctx, schema.SQL(
				`SELECT observation_id::text FROM {schema}.observation WHERE scope='p1' LIMIT 1`)).Scan(&sourceObservation); err != nil {
				t.Fatal(err)
			}
			crowdEntity(t, ctx, pool, schema, hub, other, sourceObservation, tc.facts-1)

			// A row limit far above the cap, so the only cut that can happen here is the traversal's.
			facts, capped, err := pg.NewRecallStore(pool, schema).FactsAbout(ctx, []string{"p1"},
				[]string{hub}, 10*domain.Fanout, []string{"user"}, domain.AsOf{}, 1)
			if err != nil {
				t.Fatal(err)
			}
			if capped != tc.wantCapped {
				t.Fatalf("%d facts on one entity reported capped=%v, want %v", tc.facts, capped, tc.wantCapped)
			}
			if tc.wantCapped && len(facts) != domain.Fanout {
				t.Fatalf("a capped walk returned %d facts, want the cap of %d", len(facts), domain.Fanout)
			}
			if !tc.wantCapped && len(facts) != tc.facts {
				t.Fatalf("an uncapped walk returned %d facts of %d", len(facts), tc.facts)
			}
		})
	}
}
