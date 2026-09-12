// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package migrate

import (
	"context"
	"testing"
)

func TestCanonicalAnchorUpgradeRefusesDistinctNormalizedAliases(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	all, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	var prior []Script
	for _, script := range all {
		if script.Version < 51 {
			prior = append(prior, script)
		}
	}
	schema, _ := NewSchema("canonical_anchor_upgrade")
	if _, err := pool.Exec(ctx, "DROP SCHEMA IF EXISTS canonical_anchor_upgrade CASCADE; CREATE SCHEMA canonical_anchor_upgrade"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DROP SCHEMA canonical_anchor_upgrade CASCADE") })
	if _, err := apply(ctx, pool, schema, prior); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.project(scope,label) VALUES('p1','fixture');
INSERT INTO {schema}.entity(entity_id,scope,canonical_name,normalized_name,aliases,normalized_aliases)
VALUES(gen_random_uuid(),'p1','Ensera','ensera',ARRAY['Ensera AI'],ARRAY['ensera ai'])`)); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(ctx, pool, schema); err == nil {
		t.Fatal("distinct normalized alias was silently discarded")
	}
	var version int
	var column, index bool
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT
 (SELECT max(version) FROM {schema}.schema_migration),
 EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=$1 AND table_name='entity' AND column_name='normalized_aliases'),
 to_regclass($1||'.entity_aliases_idx') IS NOT NULL`), schema.String()).Scan(&version, &column, &index); err != nil {
		t.Fatal(err)
	}
	if version != 50 || !column || !index {
		t.Fatalf("failed migration changed schema: version=%d column=%v index=%v", version, column, index)
	}
}

func TestCanonicalAnchorUpgradePreservesRawSpellingVariants(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	all, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	var prior []Script
	for _, script := range all {
		if script.Version < 51 {
			prior = append(prior, script)
		}
	}
	schema, _ := NewSchema("canonical_anchor_variants")
	if _, err := pool.Exec(ctx, "DROP SCHEMA IF EXISTS canonical_anchor_variants CASCADE; CREATE SCHEMA canonical_anchor_variants"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DROP SCHEMA canonical_anchor_variants CASCADE") })
	if _, err := apply(ctx, pool, schema, prior); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.project(scope,label) VALUES('p1','fixture');
INSERT INTO {schema}.entity(entity_id,scope,canonical_name,normalized_name,aliases,normalized_aliases)
VALUES(gen_random_uuid(),'p1','Ensera','ensera',ARRAY[' ENSERA '],ARRAY['ensera'])`)); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(ctx, pool, schema); err != nil {
		t.Fatal(err)
	}
	var aliases []string
	var column, index bool
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT aliases,
 EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=$1 AND table_name='entity' AND column_name='normalized_aliases'),
 to_regclass($1||'.entity_aliases_idx') IS NOT NULL FROM {schema}.entity WHERE scope='p1'`), schema.String()).Scan(&aliases, &column, &index); err != nil {
		t.Fatal(err)
	}
	if len(aliases) != 1 || aliases[0] != " ENSERA " || column || index {
		t.Fatalf("raw variant or redundant index changed: aliases=%q column=%v index=%v", aliases, column, index)
	}
}
