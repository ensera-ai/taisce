// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
)

func TestEntityInventoryBoundsPreviewsAndKeepsCursorAfterDeletion(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "entity_inspection_bounds")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	store := pg.NewRecordStore(pool, schema)
	ids := []string{"10000000-0000-0000-0000-000000000001", "20000000-0000-0000-0000-000000000002", "30000000-0000-0000-0000-000000000003"}
	name := strings.Repeat("界", 600)
	for _, id := range ids {
		if _, err := pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.entity(entity_id,scope,canonical_name,normalized_name) VALUES($1::uuid,'p1',$2,$3)`), id, name, id); err != nil {
			t.Fatal(err)
		}
	}
	page, err := store.ListEntities(ctx, "p1", nil, 1)
	if err != nil || page.Next == nil || len(page.Entities) != 1 || page.Entities[0].NameBytes != 1800 || utf8.RuneCountInString(page.Entities[0].NamePreview) != 512 || !page.Entities[0].NameTruncated {
		t.Fatalf("preview %+v %v", page, err)
	}
	identity, err := store.InspectEntity(ctx, "p1", ids[0])
	if err != nil || identity.Name != name {
		t.Fatalf("exact name changed %+v %v", identity, err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`DELETE FROM {schema}.entity WHERE entity_id=$1`), ids[0]); err != nil {
		t.Fatal(err)
	}
	page, err = store.ListEntities(ctx, "p1", page.Next, 100)
	if err != nil || len(page.Entities) != 2 || page.Entities[0].ID != ids[1] || page.Next != nil {
		t.Fatalf("cursor changed %+v %v", page, err)
	}
	if _, err := store.InspectEntity(ctx, "p2", ids[1]); !errors.Is(err, pg.ErrEntityNotFound) {
		t.Fatalf("foreign identity %v", err)
	}
	for _, scope := range []string{"", "bad scope"} {
		if _, err := store.ListEntities(ctx, scope, nil, 1); !errors.Is(err, pg.ErrInvalidEntityPage) {
			t.Fatal(err)
		}
		if _, err := store.InspectEntity(ctx, scope, ids[1]); !errors.Is(err, pg.ErrInvalidEntityPage) {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"bad", uuid.Nil.String()} {
		if _, err := store.ListEntities(ctx, "p1", &pg.EntityCursor{ID: id}, 1); !errors.Is(err, pg.ErrInvalidEntityPage) {
			t.Fatal(err)
		}
		if _, err := store.InspectEntity(ctx, "p1", id); !errors.Is(err, pg.ErrInvalidEntityPage) {
			t.Fatal(err)
		}
	}
	for _, limit := range []int{0, 101} {
		if _, err := store.ListEntities(ctx, "p1", nil, limit); !errors.Is(err, pg.ErrInvalidEntityPage) {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.entity SET aliases=array_fill('oversized'::text,ARRAY[65]) WHERE entity_id=$1`), ids[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := store.InspectEntity(ctx, "p1", ids[1]); !errors.Is(err, pg.ErrInvalidEntityIdentity) {
		t.Fatalf("oversized alias set accepted %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := store.ListEntities(canceled, "p1", nil, 1); err == nil {
		t.Fatal("cancellation ignored")
	}
	if _, err := store.InspectEntity(canceled, "p1", ids[2]); err == nil {
		t.Fatal("cancellation ignored")
	}
}

func TestEntityInventoryUsesProjectIdentityIndex(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "entity_inspection_plan")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	if _, err := pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.entity(entity_id,scope,canonical_name,normalized_name)
 SELECT gen_random_uuid(),CASE WHEN i%20=0 THEN 'p1' ELSE 'p2' END,'entity '||i,'entity '||i FROM generate_series(1,10000) i;
 ANALYZE {schema}.entity`)); err != nil {
		t.Fatal(err)
	}
	rows, err := pool.Query(ctx, schema.SQL(`EXPLAIN (FORMAT TEXT) SELECT e.entity_id::text,identity_kind,left(canonical_name,512),octet_length(canonical_name),char_length(canonical_name)>512,cardinality(aliases),first_seen_at
 FROM {schema}.entity e WHERE scope='p1' AND e.entity_id>'80000000-0000-0000-0000-000000000000'::uuid ORDER BY e.entity_id LIMIT 21`))
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
	if !strings.Contains(plan, "Index Scan using entity_scope_id_uniq") || !strings.Contains(plan, "Index Cond:") || strings.Contains(plan, "Sort") {
		t.Fatalf("project identity index missing: %s", plan)
	}
}

func TestEntityInspectionKeepsSharedIdentityAndOnlySurvivingVariants(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "entity_inspection_sharing")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	for _, v := range []struct{ owner, name string }{{"base", "Ensera"}, {"departing", "ENSERA"}} {
		if _, err := storeEntityName(ctx, pool, schema, v.owner, v.name); err != nil {
			t.Fatal(err)
		}
	}
	var id string
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT entity_id::text FROM {schema}.entity WHERE scope='p1' AND normalized_name='ensera'`)).Scan(&id); err != nil {
		t.Fatal(err)
	}
	store := pg.NewRecordStore(pool, schema)
	before, err := store.InspectEntity(ctx, "p1", id)
	if err != nil || len(before.Aliases) != 1 || before.Aliases[0] != "ENSERA" {
		t.Fatalf("supported alias missing %+v %v", before, err)
	}
	if _, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", "departing", "test"); err != nil {
		t.Fatal(err)
	}
	after, err := store.InspectEntity(ctx, "p1", id)
	if err != nil || after.ID != before.ID || after.Name != before.Name || len(after.Aliases) != 0 {
		t.Fatalf("shared identity/alias erasure %+v %v", after, err)
	}
}
