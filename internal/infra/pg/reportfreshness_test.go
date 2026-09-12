// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/report"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Read actual material and revisions together, as the report worker does before calling a model.
func projectReportMaterial(t *testing.T, pool *pgxpool.Pool, schema pg.Schema) []report.Fact {
	t.Helper()
	var ids []string
	if err := pool.QueryRow(context.Background(), schema.SQL(`SELECT coalesce(array_agg(entity_id::text),'{}') FROM {schema}.entity WHERE scope='p1'`)).Scan(&ids); err != nil {
		t.Fatal(err)
	}
	_, facts, err := pg.NewCommunityStore(pool, schema).Material(context.Background(), "p1", ids)
	if err != nil {
		t.Fatal(err)
	}
	return facts
}

func TestSupersessionInvalidatesReportsAndRefusesTheirInFlightSnapshots(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "report_supersession")
	obs, facts := pg.NewObservationStore(pool), pg.NewFactStore(pool)
	march := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	claim := domain.Claim{Subject: "Atlas", Predicate: "lives_in", Cardinality: domain.CardinalityOne, Object: "Dublin", Statement: "Atlas lives in Dublin", ValidFrom: march}
	first := statedAt(t, ctx, obs, facts, schema, march, claim.Statement, claim)
	material := projectReportMaterial(t, pool, schema)
	if len(material) != 1 || material[0].SourceRevision < 1 {
		t.Fatalf("missing revision: %+v", material)
	}
	store := pg.NewCommunityStore(pool, schema)
	childID, parentID := reportCommunity(t, pool, schema, "p1"), reportCommunity(t, pool, schema, "p1")
	content := report.Report{Title: "home", Summary: "Dublin"}
	if err := store.Write(ctx, "p1", childID, content, report.Context{Facts: material}, "report/test"); err != nil {
		t.Fatal(err)
	}
	child, found, err := store.Report(ctx, "p1", childID)
	if err != nil || !found || child.SourceRevisions[material[0].SourceObservationID] != material[0].SourceRevision {
		t.Fatalf("child revision: %+v %v", child, err)
	}
	if err := store.Write(ctx, "p1", parentID, content, report.Context{Children: []report.Report{child}}, "report/test"); err != nil {
		t.Fatal(err)
	}

	claim.Object, claim.Statement, claim.ValidFrom = "Oslo", "Atlas lives in Oslo", march.AddDate(0, 1, 0)
	statedAt(t, ctx, obs, facts, schema, claim.ValidFrom, claim.Statement, claim)
	// The source is still retained and citable; existence alone cannot validate a model snapshot.
	if _, err := pg.NewCitationStore(pool, schema).Resolve(ctx, "p1", first, nil, 8); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{childID, parentID} {
		if _, found, err := store.Report(ctx, "p1", id); err != nil || found {
			t.Fatalf("stale persisted report survived: %v", err)
		}
	}
	for _, stale := range []report.Context{{Facts: material}, {Children: []report.Report{child}}} {
		if err := store.Write(ctx, "p1", childID, content, stale, "report/test"); !errors.Is(err, pg.ErrReportSourceUnavailable) {
			t.Fatalf("stale context published: %v", err)
		}
	}
	current := projectReportMaterial(t, pool, schema)
	if len(current) != 1 || current[0].Object != "Oslo" {
		t.Fatalf("current material: %+v", current)
	}
	if err := store.Write(ctx, "p1", childID, report.Report{Title: "home", Summary: "Oslo"}, report.Context{Facts: current}, "report/test"); err != nil {
		t.Fatal(err)
	}
}

func TestReportsRefuseMissingAndConflictingSourceRevisions(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "report_revision_refusal")
	store, sources := reportSources(t, pool, schema)
	original := sources["alice"]
	missing, changed := original, original
	missing.SourceRevision = 0
	changed.SourceRevision++
	for _, c := range []report.Context{
		{Facts: []report.Fact{missing}},
		{Facts: []report.Fact{changed}},
		{Facts: []report.Fact{original, changed}},
		{Children: []report.Report{{SourceObservationIDs: []string{original.SourceObservationID}}}},
		{Children: []report.Report{{SourceObservationIDs: []string{original.SourceObservationID}, SourceRevisions: map[string]int64{"wrong": original.SourceRevision}}}},
	} {
		if err := store.Write(ctx, "p1", reportCommunity(t, pool, schema, "p1"), report.Report{Title: "stale"}, c, "report/test"); !errors.Is(err, pg.ErrReportSourceUnavailable) {
			t.Fatalf("bad revision accepted: %v", err)
		}
	}
	var count int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.community_report`)).Scan(&count); err != nil || count != 0 {
		t.Fatalf("refusal left a report: %d %v", count, err)
	}
}

func TestReportInvalidationRollsBackWithTheFactChange(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "report_revision_rollback")
	store, f := reportSources(t, pool, schema)
	source := f["alice"]
	id := reportCommunity(t, pool, schema, "p1")
	if err := store.Write(ctx, "p1", id, report.Report{Title: "original", Summary: "retained"}, report.Context{Facts: []report.Fact{source}}, "report/test"); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, schema.SQL(`UPDATE {schema}.fact SET statement='temporary' WHERE fact_id IN (SELECT fact_id FROM {schema}.fact_evidence WHERE source_observation_id=$1)`), source.SourceObservationID); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := tx.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.community_report`)).Scan(&n); err != nil || n != 0 {
		t.Fatalf("staged invalidation missing: %d %v", n, err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.Report(ctx, "p1", id); err != nil || !found {
		t.Fatalf("rollback removed report: %v", err)
	}
	var revision int64
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT fact_revision FROM {schema}.observation WHERE observation_id=$1`), source.SourceObservationID).Scan(&revision); err != nil || revision != source.SourceRevision {
		t.Fatalf("rollback advanced revision: %d %v", revision, err)
	}
	// Removing evidence is also a source change, even when the semantic fact row remains retained.
	if _, err := pool.Exec(ctx, schema.SQL(`DELETE FROM {schema}.fact_evidence WHERE source_observation_id=$1`), source.SourceObservationID); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.Report(ctx, "p1", id); err != nil || found {
		t.Fatalf("report retained removed evidence: %v", err)
	}
}

func TestReportPublicationRacingFactChangesCannotLeaveAStaleReport(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "report_revision_race")
	store, _ := reportSources(t, pool, schema)
	for attempt := 0; attempt < 16; attempt++ {
		material := projectReportMaterial(t, pool, schema)
		id := reportCommunity(t, pool, schema, "p1")
		start := make(chan struct{})
		var writeErr, updateErr error
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			writeErr = store.Write(ctx, "p1", id, report.Report{Title: "snapshot", Summary: "generated"}, report.Context{Facts: material}, "report/test")
		}()
		go func() {
			defer wg.Done()
			<-start
			_, updateErr = pool.Exec(ctx, schema.SQL(`UPDATE {schema}.fact SET statement=$2 WHERE fact_id IN (SELECT fact_id FROM {schema}.fact_evidence WHERE source_observation_id=$1)`), material[0].SourceObservationID, fmt.Sprintf("revision %d", attempt))
		}()
		close(start)
		wg.Wait()
		if updateErr != nil {
			t.Fatal(updateErr)
		}
		if writeErr != nil && !errors.Is(writeErr, pg.ErrReportSourceUnavailable) {
			t.Fatal(writeErr)
		}
		if _, found, err := store.Report(ctx, "p1", id); err != nil || found {
			t.Fatalf("racing stale report survived: %v", err)
		}
	}
}

func TestTheDatabaseRefusesAReportRegistrationForAnotherSourceRevision(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "report_revision_constraint")
	store, f := reportSources(t, pool, schema)
	source := f["alice"]
	id := reportCommunity(t, pool, schema, "p1")
	if err := store.Write(ctx, "p1", id, report.Report{Title: "current", Summary: "verified"}, report.Context{Facts: []report.Fact{source}}, "report/test"); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		`UPDATE {schema}.projection_dependency SET report_source_revision=NULL WHERE projection_kind='community_report'`,
		`UPDATE {schema}.projection_dependency SET report_source_revision=report_source_revision+1 WHERE projection_kind='community_report'`,
		`UPDATE {schema}.observation SET fact_revision=fact_revision+1 WHERE observation_id=$1`,
	} {
		args := []any{}
		if sql[len(sql)-2:] == "$1" {
			args = append(args, source.SourceObservationID)
		}
		if _, err := pool.Exec(ctx, schema.SQL(sql), args...); err == nil {
			t.Fatal("database admitted an unverifiable report revision")
		}
	}
	if _, found, err := store.Report(ctx, "p1", id); err != nil || !found {
		t.Fatalf("refused drift damaged the report: %v", err)
	}
}
