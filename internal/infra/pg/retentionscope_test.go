// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
)

// TestASweepOfOneProjectLeavesAnotherProjectsRowWithTheSameIdentifier holds the retention sweep
// inside the project it was asked to sweep.
//
// An artifact's identifier is chosen by whoever stores it, and it is unique within a project, not
// across the instance. An application deployed into two projects that names its state by a fixed
// identifier therefore holds two rows with one identifier — and so does anyone who learned an
// identifier in a project they cannot reach and stored something under it in one they can. When
// the copy in one project expires, a sweep that removes "the row with this identifier" removes
// both, and the project that lost its row was never swept and has no record that it was touched.
func TestASweepOfOneProjectLeavesAnotherProjectsRowWithTheSameIdentifier(t *testing.T) {
	ctx := context.Background()
	pool, schema, store := artifactFixture(t, "retention_scope")
	if err := migrate.ProvisionScope(ctx, pool, schema.String(), "p2"); err != nil {
		t.Fatalf("provision the second project: %v", err)
	}
	actor := uuid.NewString()
	request := artifactRequest()
	for _, project := range []string{"p1", "p2"} {
		if _, err := store.Put(ctx, project, actor, request); err != nil {
			t.Fatalf("store the artifact in %s: %v", project, err)
		}
	}
	// The first project's copy has passed its deadline. The second project's has not.
	if _, err := pool.Exec(ctx, schema.SQL(
		`UPDATE {schema}.observation SET retention_until = now() - interval '1 second' WHERE scope = 'p1' AND kind = 'artifact'`)); err != nil {
		t.Fatalf("expire the first project's copy: %v", err)
	}

	sweep, err := pg.NewRetentionStore(pool, schema).Sweep(ctx, "p1", 100)
	if err != nil {
		t.Fatalf("a sweep of the first project failed because another project holds a row with the same identifier: %v", err)
	}
	if sweep.Observations != 1 {
		t.Fatalf("the sweep expired %d turns of the first project, wanted 1", sweep.Observations)
	}
	kept := map[string]int{}
	rows, err := pool.Query(ctx, schema.SQL(`SELECT scope, count(*) FROM {schema}.agent_artifact GROUP BY scope`))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var project string
		var n int
		if err := rows.Scan(&project, &n); err != nil {
			t.Fatal(err)
		}
		kept[project] = n
	}
	if kept["p1"] != 0 {
		t.Errorf("the first project's expired artifact survived its own sweep")
	}
	if kept["p2"] != 1 {
		t.Fatalf("a sweep of the first project removed the second project's artifact, which had not expired and was never swept")
	}
	if _, err := store.Get(ctx, "p2", request.ID, request.DataSubjectID); err != nil {
		t.Fatalf("the second project can no longer read its artifact after the first project's sweep: %v", err)
	}
}
