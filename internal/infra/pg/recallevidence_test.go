package pg_test

import (
	"context"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
)

// A fact several spans support is recalled once, citing the earliest of them in the log.
// Joining every evidence row returned it once per span, charged each copy to the budget and counted
// each against the row limit.
func TestAFactWithSeveralSpansIsRecalledOnce(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "recall_evidence_once")
	obs, facts := pg.NewObservationStore(pool), pg.NewFactStore(pool)
	claim := domain.Claim{Subject: "Atlas", Predicate: "works_at", Object: "Ensera", Statement: "Atlas works at Ensera", Cardinality: domain.CardinalityMany}
	now := time.Now().UTC()
	id := statedAt(t, ctx, obs, facts, schema, now, claim.Statement, claim)
	var first string
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT source_observation_id::text FROM {schema}.fact_evidence WHERE fact_id=$1::uuid`), id).Scan(&first); err != nil {
		t.Fatal(err)
	}
	stored, err := obs.Append(ctx, schema, domain.Turn{Scope: "p1", DataSubjectID: "subject-1", OccurredAt: now,
		Messages: []domain.Message{{Ordinal: 0, Role: domain.RoleUser, Content: "Atlas works at Ensera, still.", OccurredAt: now}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`
		INSERT INTO {schema}.fact_evidence (fact_id, source_observation_id, source_ordinal, quote, byte_start, byte_end, extractor_version, scope)
		SELECT fact_id, $2::uuid, 0, 'Atlas works at Ensera', 0, 21, extractor_version, scope
		  FROM {schema}.fact_evidence WHERE fact_id = $1::uuid`), id, stored.ID); err != nil {
		t.Fatal(err)
	}
	var atlas string
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT entity_id::text FROM {schema}.entity WHERE normalized_name='atlas'`)).Scan(&atlas); err != nil {
		t.Fatal(err)
	}
	got, err := pg.NewRecallStore(pool, schema).FactsAboutForSubject(ctx, []string{"p1"}, []string{atlas}, 50, []string{"user"}, domain.AsOf{}, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	copies := 0
	for _, f := range got {
		if f.ID == id {
			copies++
			if f.Evidence.SourceObservationID != first {
				t.Fatalf("the fact cites %s, not the earliest span %s", f.Evidence.SourceObservationID, first)
			}
		}
	}
	if copies != 1 {
		t.Fatalf("a fact with two spans came back %d times", copies)
	}
}
