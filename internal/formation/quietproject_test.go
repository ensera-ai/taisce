// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package formation_test

import (
	"context"
	"testing"

	"github.com/ensera-ai/taisce/internal/formation"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/report"
)

// A project with nothing left to form still owes the reports an erasure or a correction deleted.
// The driver is fed by backlogs, so such a project was never offered again and the hole stayed.
// With the projects that owe a report named, the next pass writes them.
func TestTheDriverWritesTheReportsAQuietProjectOwes(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "form_quiet")
	subjectsFixture(t, pool, schema, "subject-1")
	communities := pg.NewCommunityStore(pool, schema)
	policy := formation.DefaultPolicy()
	policy.SegmentsPerPass = 0
	driver := func() *formation.Driver {
		return formation.NewDriver(workerWith(t, pool, schema, scriptedModel{}, policy), pg.NewObservationStore(pool),
			pg.NewRetentionStore(pool, schema), pg.NewAuditStore(pool, schema), schema, policy, nil).
			WithPasses(formation.NewSubjects(communities, report.New(&scriptedWriter{})), nil).
			WithOwedScopes(communities)
	}
	// The first pass partitions the project and writes its reports.
	if _, err := driver().Once(ctx); err != nil {
		t.Fatal(err)
	}
	written := func() int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.community_report WHERE scope='p1'`)).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := written()
	if before == 0 {
		t.Fatal("the fixture wrote no report to lose")
	}
	// What an erasure or a correction does to a report whose facts changed.
	if _, err := pool.Exec(ctx, schema.SQL(`DELETE FROM {schema}.community_report WHERE scope='p1'`)); err != nil {
		t.Fatal(err)
	}
	var backlog int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.observation WHERE scope='p1' AND formed_at IS NULL AND parked_at IS NULL`)).Scan(&backlog); err != nil {
		t.Fatal(err)
	}
	if backlog != 0 {
		t.Fatalf("the project must have nothing to form for this to be the case under test, got %d", backlog)
	}
	pass, err := driver().Once(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if pass.Derived != 1 {
		t.Fatalf("the project owing reports was not given a derived pass: %+v", pass)
	}
	if after := written(); after != before {
		t.Fatalf("the reports were not written back: %d, want %d", after, before)
	}
}
