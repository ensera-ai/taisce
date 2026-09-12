// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func storeEntityName(ctx context.Context, pool *pgxpool.Pool, schema pg.Schema, owner, name string) (string, error) {
	text := "Atlas works at " + name
	stored, err := pg.NewObservationStore(pool).Append(ctx, schema, domain.Turn{Scope: "p1", DataSubjectID: owner, OccurredAt: time.Now().UTC(), Messages: []domain.Message{{Role: domain.RoleUser, Content: text}}})
	if err != nil {
		return "", err
	}
	_, err = pg.NewFactStore(pool).Assert(ctx, schema, "p1", stored.ID, domain.RoleUser, owner, domain.Claim{Subject: "Atlas", Predicate: "works_at", Object: name, Statement: text, Cardinality: domain.CardinalityMany, Quote: text, ByteEnd: len(text)})
	return stored.ID, err
}

func requireEntityAliases(t *testing.T, pool *pgxpool.Pool, schema pg.Schema, want []string) {
	t.Helper()
	var got []string
	if err := pool.QueryRow(context.Background(), schema.SQL(`SELECT aliases FROM {schema}.entity WHERE scope='p1' AND normalized_name='ensera'`)).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("aliases=%q want=%q", got, want)
	}
}

func TestEntityNameOwnershipSurvivesSharingAndErasesOnlyDepartingVariants(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "entity_name_owners")
	for _, source := range []struct{ owner, name string }{{"base", "Ensera"}, {"alice", "ENSERA"}, {"bob", "ensera"}, {"carol", "ENSERA"}} {
		if _, err := storeEntityName(ctx, pool, schema, source.owner, source.name); err != nil {
			t.Fatal(err)
		}
	}
	requireEntityAliases(t, pool, schema, []string{"ENSERA", "ensera"})
	exported, err := pg.NewExporter(pool, schema).Export(ctx, schema, "p1", "alice")
	if err != nil || len(exported.Sections["entity_name_receipt"]) != 2 {
		t.Fatalf("source names not exported: %+v %v", exported.Sections, err)
	}
	for _, owner := range []string{"alice", "carol"} {
		result, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", owner, "requested")
		if err != nil || !result.Clean() || result.Deleted["entity_name_receipt"] != 2 {
			t.Fatalf("name erasure: %+v %v", result, err)
		}
		if owner == "alice" {
			requireEntityAliases(t, pool, schema, []string{"ENSERA", "ensera"})
		}
	}
	requireEntityAliases(t, pool, schema, []string{"ensera"})
	if _, err := pool.Exec(ctx, schema.SQL(`DELETE FROM {schema}.fact; DELETE FROM {schema}.entity`)); err != nil {
		t.Fatal(err)
	}
	page, err := pg.NewFactStore(pool).RecoverFacts(ctx, schema, "p1", "", "", uuid.NewString(), 100)
	if err != nil || page.Restored != 2 {
		t.Fatalf("name recovery: %+v %v", page, err)
	}
	requireEntityAliases(t, pool, schema, []string{"ensera"})
}

func TestEntityNameCacheRepairIsReportedAndExpiryRemovesVariants(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "entity_name_expiry")
	if _, err := storeEntityName(ctx, pool, schema, "base", "Ensera"); err != nil {
		t.Fatal(err)
	}
	source, err := storeEntityName(ctx, pool, schema, "expired", "ENSERA")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.entity SET aliases='{}'`)); err != nil {
		t.Fatal(err)
	}
	page, err := pg.NewFactStore(pool).RecoverFacts(ctx, schema, "p1", "", "", uuid.NewString(), 100)
	if err != nil || page.Repaired != 1 {
		t.Fatalf("cache repair not counted: %+v %v", page, err)
	}
	requireEntityAliases(t, pool, schema, []string{"ENSERA"})
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.observation SET retention_until=now()-interval '1 hour' WHERE observation_id=$1`), source); err != nil {
		t.Fatal(err)
	}
	sweep, err := pg.NewRetentionStore(pool, schema).Sweep(ctx, "p1", 10)
	if err != nil || sweep.Deleted["entity_name_receipt"] != 2 {
		t.Fatalf("name expiry: %+v %v", sweep, err)
	}
	requireEntityAliases(t, pool, schema, []string{})
}
