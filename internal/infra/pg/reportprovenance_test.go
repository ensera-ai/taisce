// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"errors"
	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/report"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"sync"
	"testing"
	"time"
)

func reportSources(t *testing.T, pool *pgxpool.Pool, schema pg.Schema) (*pg.CommunityStore, map[string]report.Fact) {
	t.Helper()
	ctx := context.Background()
	obs := pg.NewObservationStore(pool)
	facts := pg.NewFactStore(pool)
	subjects := map[string]string{}
	for _, subject := range []string{"alice", "bob"} {
		content := "I work at Ensera."
		o, err := obs.Append(ctx, schema, domain.Turn{Scope: "p1", DataSubjectID: subject, OccurredAt: time.Now().UTC(), Messages: []domain.Message{{Role: domain.RoleUser, Content: content}}})
		if err != nil {
			t.Fatal(err)
		}
		subjects[o.ID] = subject
		if _, err := facts.Assert(ctx, schema, "p1", o.ID, domain.RoleUser, subject, domain.Claim{Subject: "I", Object: "Ensera", Predicate: "works_at", Cardinality: domain.CardinalityMany, Statement: content, Quote: content, ByteEnd: len(content)}); err != nil {
			t.Fatal(err)
		}
	}
	var ids []string
	rows, err := pool.Query(ctx, schema.SQL(`SELECT entity_id::text FROM {schema}.entity`))
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	store := pg.NewCommunityStore(pool, schema)
	_, material, err := store.Material(ctx, "p1", ids)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]report.Fact{}
	for _, f := range material {
		out[subjects[f.SourceObservationID]] = f
	}
	if len(out) != 2 {
		t.Fatalf("missing source identity: %+v", out)
	}
	return store, out
}

func reportCommunity(t *testing.T, pool *pgxpool.Pool, schema pg.Schema, scope string) string {
	t.Helper()
	id := uuid.NewString()
	if _, err := pool.Exec(context.Background(), schema.SQL(`INSERT INTO {schema}.community(scope,community_id,level) VALUES($1,$2,0)`), scope, id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestReportRegistrationUsesExactSourcesAndConcurrentRetriesLeaveNoGhosts(t *testing.T) {
	pool := testPool(t)
	schema := tenant(t, pool, "wp_report_sources")
	ctx := context.Background()
	store, f := reportSources(t, pool, schema)
	id := reportCommunity(t, pool, schema, "p1")
	r := report.Report{Title: "work", Summary: "one source", SourceObservationIDs: []string{f["bob"].SourceObservationID}}
	c := report.Context{Facts: []report.Fact{f["alice"]}}
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- store.Write(ctx, "p1", id, r, c, "report/test") }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	got, ok, err := store.Report(ctx, "p1", id)
	if err != nil || !ok || len(got.SourceObservationIDs) != 1 || got.SourceObservationIDs[0] != f["alice"].SourceObservationID {
		t.Fatalf("equal quotes or model metadata changed attribution: %+v %v", got, err)
	}
	var count, ghosts int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*),count(*) FILTER(WHERE r.report_id IS NULL)
   FROM {schema}.projection_dependency d LEFT JOIN {schema}.community_report r
   ON r.scope=d.scope AND r.report_id::text=d.projection_id WHERE d.projection_kind='community_report'`)).Scan(&count, &ghosts); err != nil || count != 1 || ghosts != 0 {
		t.Fatalf("registration count=%d ghosts=%d: %v", count, ghosts, err)
	}
	if _, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", "bob", "fixture"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.Report(ctx, "p1", id); err != nil || !ok {
		t.Fatalf("identical words made Bob an owner of Alice's report: %v", err)
	}
}

func TestParentReportsInheritSubstitutedSourceDependencies(t *testing.T) {
	pool := testPool(t)
	schema := tenant(t, pool, "wp_report_parent_sources")
	ctx := context.Background()
	store, f := reportSources(t, pool, schema)
	childID := reportCommunity(t, pool, schema, "p1")
	parentID := reportCommunity(t, pool, schema, "p1")
	r := report.Report{Title: "child", Summary: "Alice"}
	if err := store.Write(ctx, "p1", childID, r, report.Context{Facts: []report.Fact{f["alice"]}}, "report/test"); err != nil {
		t.Fatal(err)
	}
	child, ok, err := store.Report(ctx, "p1", childID)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if err := store.Write(ctx, "p1", parentID, report.Report{Title: "parent", Summary: "both"}, report.Context{Facts: []report.Fact{f["bob"]}, Children: []report.Report{child}, Substituted: 1}, "report/test"); err != nil {
		t.Fatal(err)
	}
	parent, ok, err := store.Report(ctx, "p1", parentID)
	if err != nil || !ok || len(parent.SourceObservationIDs) != 2 {
		t.Fatalf("parent lost child sources: %+v %v", parent, err)
	}
	if _, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", "alice", "fixture"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.Report(ctx, "p1", parentID); err != nil || ok {
		t.Fatalf("parent retained erased child content: %v", err)
	}
}

func TestReportsRefuseMissingForeignOrErasedSourceSnapshotsAtomically(t *testing.T) {
	pool := testPool(t)
	schema := tenant(t, pool, "wp_report_snapshot")
	ctx := context.Background()
	store, f := reportSources(t, pool, schema)
	r := report.Report{Title: "summary", Summary: "content"}
	for _, c := range []report.Context{{}, {Facts: []report.Fact{{Quote: "no source"}}}, {Children: []report.Report{{Title: "child", Summary: "no provenance"}}}} {
		if err := store.Write(ctx, "p1", reportCommunity(t, pool, schema, "p1"), r, c, "report/test"); !errors.Is(err, pg.ErrReportSourceUnavailable) {
			t.Fatalf("missing provenance accepted: %v", err)
		}
	}
	if err := store.Write(ctx, "p2", reportCommunity(t, pool, schema, "p2"), r, report.Context{Facts: []report.Fact{f["alice"]}}, "report/test"); !errors.Is(err, pg.ErrReportSourceUnavailable) {
		t.Fatalf("foreign source accepted: %v", err)
	}
	// Generation has read the material; erasure finishes before persistence begins.
	c := report.Context{Facts: []report.Fact{f["alice"]}}
	if _, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", "alice", "generation race"); err != nil {
		t.Fatal(err)
	}
	if err := store.Write(ctx, "p1", reportCommunity(t, pool, schema, "p1"), r, c, "report/test"); !errors.Is(err, pg.ErrReportSourceUnavailable) {
		t.Fatalf("erased snapshot restored: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.community_report`)).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed registration committed a report: %d %v", count, err)
	}
}
