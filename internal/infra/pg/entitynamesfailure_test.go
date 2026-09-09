// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/infra/pg"
)

func TestEntityNameVariantsAreConcurrentBoundedAndTransactional(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "entity_name_bounds")
	if _, err := storeEntityName(ctx, pool, schema, "base", "Ensera"); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 12)
	for i := 0; i < 12; i++ {
		go func(i int) { _, err := storeEntityName(ctx, pool, schema, fmt.Sprint(i), "ENSERA"); results <- err }(i)
	}
	for i := 0; i < 12; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	requireEntityAliases(t, pool, schema, []string{"ENSERA"})
	// Whitespace variants normalize to the same identity while preserving distinct source spelling.
	for i := 1; i < pg.MaxEntityNameVariants; i++ {
		if _, err := storeEntityName(ctx, pool, schema, "variants", strings.Repeat(" ", i)+"Ensera"); err != nil {
			t.Fatal(err)
		}
	}
	before := recoverySnapshot(t, pool, schema, "entity_name_receipt")
	if _, err := storeEntityName(ctx, pool, schema, "overflow", strings.Repeat(" ", pg.MaxEntityNameVariants)+"Ensera"); !errors.Is(err, pg.ErrEntityNameLimit) {
		t.Fatalf("name bound not enforced: %v", err)
	}
	if recoverySnapshot(t, pool, schema, "entity_name_receipt") != before {
		t.Fatal("refused assertion retained names")
	}
	if _, err := storeEntityName(ctx, pool, schema, "oversized", strings.Repeat("x", 4097)); !errors.Is(err, pg.ErrEntityNameLimit) {
		t.Fatalf("name size not bounded: %v", err)
	}
}

func TestEntityNameReceiptRejectsForeignIdentityAndRollsBackStorageFailure(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "entity_name_failure")
	source, err := storeEntityName(ctx, pool, schema, "base", "Ensera")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.entity_name_receipt(scope,source_observation_id,entity_id,name,normalized_name)
        SELECT 'p2',$1::uuid,entity_id,'ENSERA','ensera' FROM {schema}.entity WHERE normalized_name='ensera'`), source); err == nil {
		t.Fatal("foreign name receipt accepted")
	}
	if _, err := pool.Exec(ctx, schema.SQL(`CREATE FUNCTION {schema}.refuse_names() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'storage interrupted'; END $$;
        CREATE TRIGGER refuse_names BEFORE INSERT ON {schema}.entity_name_receipt FOR EACH ROW EXECUTE FUNCTION {schema}.refuse_names()`)); err != nil {
		t.Fatal(err)
	}
	before := recoverySnapshot(t, pool, schema, "entity")
	if _, err := storeEntityName(ctx, pool, schema, "failed", "ENSERA"); err == nil {
		t.Fatal("storage failure ignored")
	}
	if recoverySnapshot(t, pool, schema, "entity") != before {
		t.Fatal("failed name registration changed entity")
	}
}
