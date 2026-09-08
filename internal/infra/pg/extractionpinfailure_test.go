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

func TestExtractionPinFailuresRefuseWritesWithoutPartialState(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "extraction_pin_failure")
	obs, facts := pg.NewObservationStore(pool), pg.NewFactStore(pool)
	source, err := obs.Append(ctx, schema, domain.Turn{Scope: "p1", DataSubjectID: "owner", Messages: []domain.Message{{Role: domain.RoleUser, Content: "Atlas works at Ensera"}}})
	if err != nil {
		t.Fatal(err)
	}
	claim := domain.Claim{Subject: "Atlas", Object: "Ensera", Predicate: "works_at", Statement: "Atlas works at Ensera", Quote: "Atlas works at Ensera", ByteEnd: len("Atlas works at Ensera"), Cardinality: domain.CardinalityMany}
	if _, err := pool.Exec(ctx, schema.SQL(`CREATE FUNCTION {schema}.refuse_pin() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected pin refusal'; END $$;
        CREATE TRIGGER refuse_pin BEFORE INSERT ON {schema}.source_extraction FOR EACH ROW EXECUTE FUNCTION {schema}.refuse_pin()`)); err != nil {
		t.Fatal(err)
	}
	if _, err := facts.Assert(ctx, schema, "p1", source.ID, domain.RoleUser, "owner", claim); err == nil {
		t.Fatal("fact escaped failed pin")
	}
	if err := facts.Reject(ctx, schema, "p1", source.ID, "owner", domain.RejectedClaim{Reason: domain.ReasonUnmappedRelation, Quote: "text"}); err == nil {
		t.Fatal("diagnostic escaped failed pin")
	}
	for _, table := range []string{"source_extraction", "fact", "entity", "rejected_claim"} {
		var n int
		if err := pool.QueryRow(ctx, schema.SQL("SELECT count(*) FROM {schema}."+table)).Scan(&n); err != nil || n != 0 {
			t.Fatal("partial formation after failed pin", table, err)
		}
	}
	if _, err := pool.Exec(ctx, schema.SQL(`DROP TRIGGER refuse_pin ON {schema}.source_extraction`)); err != nil {
		t.Fatal(err)
	}
	if _, err := facts.Assert(ctx, schema, "p1", source.ID, domain.RoleUser, "owner", claim); err != nil {
		t.Fatal(err)
	}
	if _, err := facts.AssertVersioned(ctx, schema, "p1", source.ID, domain.RoleUser, "owner", claim, "extract/v2:"+strings.Repeat("a", 64)); !errors.Is(err, pg.ErrExtractionChanged) {
		t.Fatal("direct versioned write bypassed pin", err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`ALTER TABLE {schema}.source_extraction RENAME TO unavailable_extraction`)); err != nil {
		t.Fatal(err)
	}
	if err := facts.PinExtraction(ctx, schema, "p1", source.ID, pg.ExtractorVersion); err == nil {
		t.Fatal("unavailable pin store accepted write")
	}
}
