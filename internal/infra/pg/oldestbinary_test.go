// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
)

// TestABinaryTwoReleasesOldIsNotReadyAndThePreviousOneIs holds the readiness check to both of its
// sides at once.
//
// A process of the previous release, still serving after a migration, must stay ready: that is
// every upgrade, and a release's schema is checked to be one its predecessor can serve on. A
// process older than that was never checked against this schema and must stand down when the
// migration commits, instead of failing requests until the roll reaches it. And a process newer
// than the schema must still wait for it, as before.
func TestABinaryTwoReleasesOldIsNotReadyAndThePreviousOneIs(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "oldest_binary")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	this, err := migrate.BuiltFor()
	if err != nil {
		t.Fatal(err)
	}
	previous, err := migrate.OldestServed()
	if err != nil {
		t.Fatal(err)
	}
	// Equal is the ordinary state straight after a release: nothing has been migrated since, so the
	// previous release was built for this schema. Only a previous release newer than this tree is a
	// contradiction.
	if previous > this {
		t.Fatalf("the previous release was built for version %d and this tree for %d; one of them is wrong", previous, this)
	}
	// Whoever migrated wrote the number down.
	var recorded int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT oldest_binary FROM {schema}.schema_serves`)).Scan(&recorded); err != nil || recorded != previous {
		t.Fatalf("the schema records %d as the oldest binary it serves, not the previous release's %d (%v)", recorded, previous, err)
	}

	if err := pg.CheckSchemaBuiltFor(ctx, pool, schema, this); err != nil {
		t.Fatalf("this release is not ready on its own schema: %v", err)
	}
	if err := pg.CheckSchemaBuiltFor(ctx, pool, schema, previous); err != nil {
		t.Fatalf("the previous release is not ready on this schema, so an ordinary upgrade would have nothing serving: %v", err)
	}
	if err := pg.CheckSchemaBuiltFor(ctx, pool, schema, previous-1); !errors.Is(err, pg.ErrBinaryBehind) {
		t.Fatalf("a binary built for a schema older than the previous release's is ready: %v", err)
	}
	if err := pg.CheckSchemaBuiltFor(ctx, pool, schema, this+1); !errors.Is(err, pg.ErrSchemaBehind) {
		t.Fatalf("a binary newer than the schema does not wait for it: %v", err)
	}

	// The side an upgrade depends on: a binary older than the schema, and no older than the number,
	// stays ready. Straight after a release no real binary is in that position — the previous
	// release's schema is this one — so the test writes the number itself. Otherwise this would be
	// asserted only on commits that carry a migration the last release did not.
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.schema_serves SET oldest_binary = $1`), this-1); err != nil {
		t.Fatal(err)
	}
	if err := pg.CheckSchemaBuiltFor(ctx, pool, schema, this-1); err != nil {
		t.Fatalf("a binary one version older than the schema, at the number the schema serves, is not ready: %v", err)
	}
	if err := pg.CheckSchemaBuiltFor(ctx, pool, schema, this-2); !errors.Is(err, pg.ErrBinaryBehind) {
		t.Fatalf("a binary below the number the schema serves is ready: %v", err)
	}

	// The number only rises. A namespace a newer release has migrated, reached again by this
	// runner, keeps what the newer one wrote. One above this tree and not this tree's own version,
	// which straight after a release is what this runner would write anyway. A newer release would
	// have added its migrations too; the number is all this runner's write depends on.
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.schema_serves SET oldest_binary = $1`), this+1); err != nil {
		t.Fatal(err)
	}
	if _, err := migrate.Apply(ctx, pool, schema); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT oldest_binary FROM {schema}.schema_serves`)).Scan(&recorded); err != nil || recorded != this+1 {
		t.Fatalf("migrating again lowered the oldest binary served from %d to %d (%v)", this+1, recorded, err)
	}
	if err := pg.CheckSchemaBuiltFor(ctx, pool, schema, this); !errors.Is(err, pg.ErrBinaryBehind) {
		t.Fatalf("after a newer release migrated, a binary older than the number it wrote is ready: %v", err)
	}
}
