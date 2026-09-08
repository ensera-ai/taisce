// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
)

func TestDirectAnchorCallersCannotBypassParameterLimits(t *testing.T) {
	store := pg.NewRecallStore(nil, pg.Schema("unused"))
	for _, terms := range [][]string{make([]string, domain.MaxRecallTerms+1), {strings.Repeat("x", domain.MaxRecallTermBytes+1)}} {
		if _, err := store.Anchors(context.Background(), []string{"p1"}, terms); !errors.Is(err, domain.ErrRecallTerms) {
			t.Fatalf("parameter refusal: %v", err)
		}
	}
}

func TestAnchorMatchOverflowRefusesInsteadOfReturningAnArbitrarySlice(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "anchor_overflow")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	if _, err := pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.entity(entity_id,scope,canonical_name,normalized_name,normalized_aliases)
SELECT gen_random_uuid(),'p1','name '||n,'name '||n,ARRAY['common'] FROM generate_series(1,$1::int) n`), domain.MaxRecallMatches); err != nil {
		t.Fatal(err)
	}
	store := pg.NewRecallStore(pool, schema)
	anchors, err := store.Anchors(ctx, []string{"p1"}, []string{"common", "common"})
	if err != nil || len(anchors) != domain.MaxRecallMatches {
		t.Fatalf("exact boundary or duplicate terms: %d %v", len(anchors), err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.entity(entity_id,scope,canonical_name,normalized_name,normalized_aliases)
VALUES(gen_random_uuid(),'p1','overflow','overflow',ARRAY['common'])`)); err != nil {
		t.Fatal(err)
	}
	anchors, err = store.Anchors(ctx, []string{"p1"}, []string{"common"})
	if !errors.Is(err, domain.ErrRecallMatches) || anchors != nil {
		t.Fatalf("partial anchors returned: %d %v", len(anchors), err)
	}
}

func TestCanonicalAliasOverlapAndSubjectAttributionPreserveExactMatches(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "anchor_attribution")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	if _, err := pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.project(scope,label) VALUES('p2','p2')`)); err != nil {
		t.Fatal(err)
	}
	obs := pg.NewObservationStore(pool)
	for _, tc := range []struct{ project, subject, name string }{{"p1", "a", "alpha"}, {"p1", "b", "beta"}, {"p2", "a", "foreign"}} {
		turn := turnOf(domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: tc.name})
		turn.Scope = tc.project
		turn.DataSubjectID = tc.subject
		source, err := obs.Append(ctx, schema, turn)
		if err != nil {
			t.Fatal(err)
		}
		var id string
		if err := pool.QueryRow(ctx, schema.SQL(`INSERT INTO {schema}.entity(entity_id,scope,canonical_name,normalized_name,normalized_aliases)
VALUES(gen_random_uuid(),$1,$2,$2,ARRAY[$2,'shared alias']) RETURNING entity_id::text`), tc.project, tc.name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.projection_dependency(source_observation_id,scope,projection_kind,projection_id,data_subject_id)
VALUES($1,$2,'entity',$3,$4)`), source.ID, tc.project, id, tc.subject); err != nil {
			t.Fatal(err)
		}
	}
	store := pg.NewRecallStore(pool, schema)
	anchors, err := store.AnchorsForSubject(ctx, []string{"p1"}, []string{"alpha", "alpha", "shared alias"}, "a")
	if err != nil || len(anchors) != 2 {
		t.Fatalf("one canonical and one alias match expected: %v %v", anchors, err)
	}
	for _, a := range anchors {
		if a.Entity.CanonicalName != "alpha" || a.Entity.Scope != "p1" {
			t.Fatalf("foreign attribution: %+v", a)
		}
	}
	if anchors[0].Matched != "shared alias" || anchors[1].Matched != "alpha" {
		t.Fatalf("specificity order: %+v", anchors)
	}
	if _, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", "a", "test"); err != nil {
		t.Fatal(err)
	}
	anchors, err = store.Anchors(ctx, []string{"p1"}, []string{"shared alias"})
	if err != nil || len(anchors) != 1 || anchors[0].Entity.CanonicalName != "beta" {
		t.Fatalf("erased/foreign alias leaked: %+v %v", anchors, err)
	}
}
