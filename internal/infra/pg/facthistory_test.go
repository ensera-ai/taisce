// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
)

func TestEarlierKnowledgeRetainsItsOriginalValidity(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "history_views")
	obs, facts := pg.NewObservationStore(pool), pg.NewFactStore(pool)
	march := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	june := march.AddDate(0, 3, 0)
	august := march.AddDate(0, 5, 0)
	april := march.AddDate(0, 1, 0)
	first := statedAt(t, ctx, obs, facts, schema, march, "I live in Dublin", domain.Claim{Subject: "the user", Predicate: "lives_in", Cardinality: domain.CardinalityOne, Object: "Dublin", Statement: "The user lives in Dublin.", ValidFrom: march})
	known := recordedAt(t, ctx, pool, schema, first)
	second := statedAt(t, ctx, obs, facts, schema, june, "I live in Amman", domain.Claim{Subject: "the user", Predicate: "lives_in", Cardinality: domain.CardinalityOne, Object: "Amman", Statement: "The user lives in Amman.", ValidFrom: june})
	secondKnown := recordedAt(t, ctx, pool, schema, second)
	var person string
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT subject_entity_id::text FROM {schema}.fact WHERE fact_id=$1`), first).Scan(&person); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, object, id string
		at               domain.AsOf
		closed           bool
	}{
		{"August at first knowledge", "Dublin", first, domain.AsOf{Valid: august, Known: known}, false},
		{"April at first knowledge", "Dublin", first, domain.AsOf{Valid: april, Known: known}, false},
		{"First knowledge alone", "Dublin", first, domain.AsOf{Known: known}, false},
		{"April under current knowledge", "Dublin", first, domain.AsOf{Valid: april}, true},
		{"August under current knowledge", "Amman", second, domain.AsOf{Valid: august}, false},
		{"Exact transition knowledge", "Amman", second, domain.AsOf{Valid: august, Known: secondKnown}, false},
		{"Current", "Amman", second, domain.AsOf{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := pg.NewRecallStore(pool, schema).FactsAbout(ctx, []string{"p1"}, []string{person}, 10, []string{"user"}, tc.at, 1)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0].Object != tc.object || got[0].ID != tc.id || (got[0].ValidUntil != nil) != tc.closed {
				t.Fatalf("wrong knowledge view: %+v", got)
			}
			if tc.closed && !got[0].ValidUntil.Equal(june) {
				t.Fatalf("wrong end: %v", got[0].ValidUntil)
			}
		})
	}
	exported, err := pg.NewExporter(pool, schema).Export(ctx, schema, "p1", "subject-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(exported.Sections["fact_history"]) == 0 {
		t.Fatal("export omitted history")
	}
	if _, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", "subject-1", "requested"); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"fact", "fact_history", "fact_evidence", "projection_dependency"} {
		var n int
		if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.`+table)).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s residual=%d err=%v", table, n, err)
		}
	}
}

func TestFailedSupersessionRollsBackHistoryAndKnownTime(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "history_rollback")
	obs, facts := pg.NewObservationStore(pool), pg.NewFactStore(pool)
	march := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	first := statedAt(t, ctx, obs, facts, schema, march, "I live in Dublin", domain.Claim{Subject: "Atlas", Predicate: "lives_in", Cardinality: domain.CardinalityOne, Object: "Dublin", Statement: "Dublin resident", ValidFrom: march})
	known := recordedAt(t, ctx, pool, schema, first)
	source, err := obs.Append(ctx, schema, turnOf(domain.Message{Role: domain.RoleUser, Content: "Oslo office"}))
	if err != nil {
		t.Fatal(err)
	}
	// A missing message fails at commit, after the history and successor have been staged.
	_, err = facts.Assert(ctx, schema, "p1", source.ID, domain.RoleUser, "subject-1", domain.Claim{Subject: "Atlas", Predicate: "lives_in", Cardinality: domain.CardinalityOne, Object: "Oslo", Statement: "Oslo resident", ValidFrom: march.AddDate(0, 1, 0), SourceOrdinal: 9, Quote: "Oslo", ByteEnd: 4})
	if err == nil {
		t.Fatal("missing message accepted")
	}
	var history int
	var open bool
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT upper_inf(valid),(SELECT count(*) FROM {schema}.fact_history) FROM {schema}.fact WHERE fact_id=$1`), first).Scan(&open, &history); err != nil {
		t.Fatal(err)
	}
	if !open || history != 0 || !recordedAt(t, ctx, pool, schema, first).Equal(known) {
		t.Fatal("failed write changed prior knowledge")
	}
}

func TestErasingOnlyTheSupersessionCauseInvalidatesDerivedValidity(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "history_causal_erasure")
	obs, facts := pg.NewObservationStore(pool), pg.NewFactStore(pool)
	march := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	first := statedAt(t, ctx, obs, facts, schema, march, "Atlas is in Dublin", domain.Claim{Subject: "Atlas", Predicate: "located_in", Cardinality: domain.CardinalityOne, Object: "Dublin", Statement: "Atlas is in Dublin", ValidFrom: march})
	turn := turnOf(domain.Message{Role: domain.RoleUser, Content: "Atlas moved to Oslo"})
	turn.DataSubjectID = "subject-2"
	source, err := obs.Append(ctx, schema, turn)
	if err != nil {
		t.Fatal(err)
	}
	_, err = facts.Assert(ctx, schema, "p1", source.ID, domain.RoleUser, "subject-2", domain.Claim{Subject: "Atlas", Predicate: "located_in", Cardinality: domain.CardinalityOne, Object: "Oslo", Statement: "Atlas is in Oslo", Quote: "Atlas moved to Oslo", ByteEnd: len("Atlas moved to Oslo"), ValidFrom: march.AddDate(0, 1, 0)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", "subject-2", "requested"); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.NewCitationStore(pool, schema).Resolve(ctx, "p1", first, nil, 8); !errors.Is(err, pg.ErrCitationNotFound) {
		t.Fatalf("a date derived from erased source survived: %v", err)
	}
	var remaining int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.observation WHERE data_subject_id='subject-1'`)).Scan(&remaining); err != nil || remaining != 1 {
		t.Fatalf("retained source lost: %d %v", remaining, err)
	}
}

func TestHistoricalLookupUsesFactRangeIndex(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "history_plan")
	id := statedAt(t, ctx, pg.NewObservationStore(pool), pg.NewFactStore(pool), schema, time.Now().UTC(), "I live in Dublin", domain.Claim{Subject: "user", Predicate: "lives_in", Cardinality: domain.CardinalityOne, Object: "Dublin", Statement: "Dublin resident"})
	// A controlled plan fixture with many disjoint versions of one retained fact.
	if _, err := pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.fact_history(history_id,scope,fact_id,valid,known,source_observation_id)
 SELECT gen_random_uuid(),'p1',$1,'[2020-01-01,)'::tstzrange,tstzrange('2020-01-01'::timestamptz+n*interval '1 day','2020-01-02'::timestamptz+n*interval '1 day'),e.source_observation_id
 FROM generate_series(0,1999) n CROSS JOIN {schema}.fact_evidence e WHERE e.fact_id=$1`), id); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`ANALYZE {schema}.fact_history`)); err != nil {
		t.Fatal(err)
	}
	rows, err := pool.Query(ctx, schema.SQL(`EXPLAIN (ANALYZE,BUFFERS) SELECT valid FROM {schema}.fact_history WHERE scope='p1' AND fact_id=$1 AND known @> '2021-01-01'::timestamptz`), id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, line)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	plan := strings.Join(lines, "\n")
	t.Log(plan)
	if !strings.Contains(plan, "Index") || strings.Contains(plan, "Seq Scan") {
		t.Fatal(fmt.Sprintf("history lookup scans all versions: %s", plan))
	}
}

func TestConcurrentSupersessionCommitsOneCoherentKnowledgeTransition(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "history_race")
	obs, facts := pg.NewObservationStore(pool), pg.NewFactStore(pool)
	march := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	first := statedAt(t, ctx, obs, facts, schema, march, "I live in Dublin", domain.Claim{Subject: "user", Predicate: "lives_in", Cardinality: domain.CardinalityOne, Object: "Dublin", Statement: "Dublin resident", ValidFrom: march})
	known := recordedAt(t, ctx, pool, schema, first)
	source, err := obs.Append(ctx, schema, turnOf(domain.Message{Role: domain.RoleUser, Content: "I live in Oslo"}))
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	result := make(chan error, 12)
	identities := make(chan string, 12)
	var workers sync.WaitGroup
	for i := 0; i < 12; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			id, err := facts.Assert(ctx, schema, "p1", source.ID, domain.RoleUser, "subject-1", domain.Claim{Subject: "user", Predicate: "lives_in", Cardinality: domain.CardinalityOne, Object: "Oslo", Statement: "Oslo resident", Quote: "I live in Oslo", ByteEnd: 14, ValidFrom: march.AddDate(0, 1, 0)})
			result <- err
			identities <- id
		}()
	}
	close(start)
	workers.Wait()
	close(result)
	close(identities)
	for err := range result {
		if err != nil {
			t.Fatalf("source replay failed: %v", err)
		}
	}
	var successor string
	for id := range identities {
		if id == "" || (successor != "" && successor != id) {
			t.Fatal("source replay returned different successors")
		}
		successor = id
	}
	var snapshots, factsCount int
	var aligned bool
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT (SELECT count(*) FROM {schema}.fact_history),(SELECT count(*) FROM {schema}.fact),
 upper(h.known)=lower(f.known) AND lower(f.known)=lower(successor.known) AND lower(h.known)=$2
 FROM {schema}.fact_history h JOIN {schema}.fact f USING(fact_id) JOIN {schema}.fact successor ON successor.fact_id=f.superseded_by WHERE f.fact_id=$1`), first, known).Scan(&snapshots, &factsCount, &aligned); err != nil {
		t.Fatal(err)
	}
	if snapshots != 1 || factsCount != 2 || !aligned {
		t.Fatalf("incoherent transition: %d snapshots %d facts aligned=%v", snapshots, factsCount, aligned)
	}
}

func TestRetentionRemovesTemporalStateDerivedFromExpiredCause(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "history_retention")
	obs, facts := pg.NewObservationStore(pool), pg.NewFactStore(pool)
	march := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	first := statedAt(t, ctx, obs, facts, schema, march, "I live in Dublin", domain.Claim{Subject: "user", Predicate: "lives_in", Cardinality: domain.CardinalityOne, Object: "Dublin", Statement: "Dublin resident", ValidFrom: march})
	second := statedAt(t, ctx, obs, facts, schema, march.AddDate(0, 1, 0), "I live in Oslo", domain.Claim{Subject: "user", Predicate: "lives_in", Cardinality: domain.CardinalityOne, Object: "Oslo", Statement: "Oslo resident", ValidFrom: march.AddDate(0, 1, 0)})
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.observation SET retention_until=now()-interval '1 second' WHERE observation_id=(SELECT source_observation_id FROM {schema}.fact_evidence WHERE fact_id=$1)`), second); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.NewRetentionStore(pool, schema).Sweep(ctx, "p1", 10); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.NewCitationStore(pool, schema).Resolve(ctx, "p1", first, nil, 8); !errors.Is(err, pg.ErrCitationNotFound) {
		t.Fatalf("expired causal metadata survived: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.fact_history`)).Scan(&n); err != nil || n != 0 {
		t.Fatalf("history residual=%d: %v", n, err)
	}
}

func TestCitationReportsRecordedSuccessorAndCause(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "history_citation")
	obs, facts := pg.NewObservationStore(pool), pg.NewFactStore(pool)
	march := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	first := statedAt(t, ctx, obs, facts, schema, march, "I live in Dublin", domain.Claim{Subject: "user", Predicate: "lives_in", Cardinality: domain.CardinalityOne, Object: "Dublin", Statement: "Dublin resident", ValidFrom: march})
	second := statedAt(t, ctx, obs, facts, schema, march.AddDate(0, 1, 0), "I live in Oslo", domain.Claim{Subject: "user", Predicate: "lives_in", Cardinality: domain.CardinalityOne, Object: "Oslo", Statement: "Oslo resident", ValidFrom: march.AddDate(0, 1, 0)})
	var source string
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT source_observation_id::text FROM {schema}.fact_evidence WHERE fact_id=$1`), second).Scan(&source); err != nil {
		t.Fatal(err)
	}
	resolver := pg.NewCitationStore(pool, schema)
	citation, err := resolver.Resolve(ctx, "p1", first, nil, 8)
	if err != nil {
		t.Fatal(err)
	}
	if citation.SupersededBy == nil || *citation.SupersededBy != second || citation.SupersessionSource == nil || *citation.SupersessionSource != source {
		t.Fatalf("missing or guessed lineage: %+v", citation)
	}
}

func TestSupersessionRacingCauseErasureCannotRetainItsClosingDate(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "history_erase_race")
	obs, facts := pg.NewObservationStore(pool), pg.NewFactStore(pool)
	march := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	first := statedAt(t, ctx, obs, facts, schema, march, "Atlas is in Dublin", domain.Claim{Subject: "Atlas", Predicate: "located_in", Cardinality: domain.CardinalityOne, Object: "Dublin", Statement: "Atlas is in Dublin", ValidFrom: march})
	turn := turnOf(domain.Message{Role: domain.RoleUser, Content: "Atlas is in Oslo"})
	turn.DataSubjectID = "subject-2"
	source, err := obs.Append(ctx, schema, turn)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	assertDone := make(chan error, 1)
	eraseDone := make(chan error, 1)
	go func() {
		<-start
		_, err := facts.Assert(ctx, schema, "p1", source.ID, domain.RoleUser, "subject-2", domain.Claim{Subject: "Atlas", Predicate: "located_in", Cardinality: domain.CardinalityOne, Object: "Oslo", Statement: "Atlas is in Oslo", Quote: "Atlas is in Oslo", ByteEnd: len("Atlas is in Oslo"), ValidFrom: march.AddDate(0, 1, 0)})
		assertDone <- err
	}()
	go func() {
		<-start
		_, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", "subject-2", "requested")
		eraseDone <- err
	}()
	close(start)
	<-assertDone
	if err := <-eraseDone; err != nil {
		if _, retry := pg.NewEraser(pool).Erase(ctx, schema, "p1", "subject-2", "requested"); retry != nil {
			t.Fatalf("erasure did not complete: %v, retry %v", err, retry)
		}
	}
	// The original survives only if the correction failed before committing; then it must stay open.
	var bad int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT
 (SELECT count(*) FROM {schema}.fact WHERE fact_id=$1 AND NOT upper_inf(valid))+
 (SELECT count(*) FROM {schema}.fact_history)+
 (SELECT count(*) FROM {schema}.observation WHERE data_subject_id='subject-2')`), first).Scan(&bad); err != nil || bad != 0 {
		t.Fatalf("erased cause resurfaced: %d %v", bad, err)
	}
}
