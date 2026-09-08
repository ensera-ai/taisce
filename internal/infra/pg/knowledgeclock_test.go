// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
)

// Move the retained knowledge stamp ahead of wall time rather than changing the machine's clock.
// The writer must preserve logical transition order when the next wall-clock reading is earlier.
func TestSupersessionAfterClockRollbackPreservesOrderedKnowledge(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "knowledge_clock")
	obs, facts := pg.NewObservationStore(pool), pg.NewFactStore(pool)
	march := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	first := statedAt(t, ctx, obs, facts, schema, march, "Atlas lives in Dublin", domain.Claim{Subject: "Atlas", Predicate: "lives_in", Object: "Dublin", Statement: "Atlas lives in Dublin", Cardinality: domain.CardinalityOne, ValidFrom: march})
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.fact SET known=tstzrange(clock_timestamp()+interval '1 minute',NULL) WHERE fact_id=$1`), first); err != nil {
		t.Fatal(err)
	}
	oldKnown := recordedAt(t, ctx, pool, schema, first)
	second := statedAt(t, ctx, obs, facts, schema, march.AddDate(0, 1, 0), "Atlas lives in Oslo", domain.Claim{Subject: "Atlas", Predicate: "lives_in", Object: "Oslo", Statement: "Atlas lives in Oslo", Cardinality: domain.CardinalityOne, ValidFrom: march.AddDate(0, 1, 0)})
	newKnown := recordedAt(t, ctx, pool, schema, second)
	var aligned bool
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT lower(h.known)=$2 AND upper(h.known)=$3 AND lower(f.known)=$3 FROM {schema}.fact f JOIN {schema}.fact_history h USING(fact_id) WHERE f.fact_id=$1`), first, oldKnown, newKnown).Scan(&aligned); err != nil || !aligned || !newKnown.After(oldKnown) {
		t.Fatalf("clock rollback broke transition: aligned=%v %v", aligned, err)
	}
	citation, err := pg.NewCitationStore(pool, schema).Resolve(ctx, "p1", first, nil, 8)
	if err != nil {
		t.Fatal(err)
	}
	for _, view := range []struct {
		known time.Time
		id    string
	}{{oldKnown, first}, {newKnown, second}} {
		got, err := pg.NewRecallStore(pool, schema).FactsAbout(ctx, []string{"p1"}, []string{*citation.SubjectID}, 10, []string{"user"}, domain.AsOf{Known: view.known}, 1)
		if err != nil || len(got) != 1 || got[0].ID != view.id {
			t.Fatalf("logical knowledge view: %+v %v", got, err)
		}
	}
}
