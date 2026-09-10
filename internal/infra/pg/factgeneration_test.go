// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type generationFixture struct {
	pool     *pgxpool.Pool
	schema   pg.Schema
	facts    *pg.FactStore
	snapshot pg.GenerationSnapshot
	original string
	claim    domain.Claim
	version  string
}

func generationSource(t *testing.T, name string) generationFixture {
	t.Helper()
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, name)
	facts := pg.NewFactStore(pool)
	text := "I work at Ensera"
	claim := domain.Claim{Subject: "I", Predicate: "works_at", Object: "Ensera", Statement: text, Quote: text, ByteEnd: len(text), Cardinality: domain.CardinalityMany}
	id := statedAt(t, ctx, pg.NewObservationStore(pool), facts, schema, time.Now().Add(-time.Hour), text, claim)
	var source string
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT source_observation_id::text FROM {schema}.fact_receipt WHERE fact_id=$1`), id).Scan(&source); err != nil {
		t.Fatal(err)
	}
	snapshot, err := facts.ReadGenerationSource(ctx, schema, "p1", source)
	if err != nil {
		t.Fatal(err)
	}
	return generationFixture{pool, schema, facts, snapshot, id, claim, "extract/v2:" + strings.Repeat("b", 64)}
}

func TestGenerationCutoverPreservesOldCitationsAndPublishesNewIdentities(t *testing.T) {
	ctx := context.Background()
	f := generationSource(t, "generation_citations")
	key, actor := uuid.NewString(), uuid.NewString()
	citations := pg.NewCitationStore(f.pool, f.schema)
	before, err := citations.Resolve(ctx, "p1", f.original, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	f.claim.Statement = "The speaker works at Ensera"
	result, err := f.facts.ApplyGeneration(ctx, f.schema, f.snapshot, key, f.version, actor, pg.GenerationPlan{Claims: []domain.Claim{f.claim}})
	if err != nil || result.Asserted != 1 || result.Retired != 1 || result.Replayed {
		t.Fatalf("generation: %+v %v", result, err)
	}
	old, err := citations.Resolve(ctx, "p1", f.original, nil, 10)
	if err != nil || old.Statement != before.Statement || old.Known.Until == nil || !old.Known.Until.Equal(result.AppliedAt) || old.Evidence[0].ExtractorVersion != pg.ExtractorVersion {
		t.Fatal("old citation was changed or lost", err)
	}
	var fresh string
	if err := f.pool.QueryRow(ctx, f.schema.SQL(`SELECT fact_id::text FROM {schema}.fact_generation_record WHERE generation_id=$1 AND disposition='admitted'`), result.ID).Scan(&fresh); err != nil {
		t.Fatal(err)
	}
	if fresh == f.original {
		t.Fatal("new interpretation overwrote the old citation ID")
	}
	current, err := citations.Resolve(ctx, "p1", fresh, nil, 10)
	if err != nil || current.Statement != f.claim.Statement || current.Evidence[0].ExtractorVersion != f.version || !current.Known.From.Equal(result.AppliedAt) {
		t.Fatal("new provenance is not truthful", err)
	}
	got, err := pg.NewRecallStore(f.pool, f.schema).FactsAbout(ctx, []string{"p1"}, []string{*current.SubjectID}, 10, []string{"user"}, domain.AsOf{}, 1)
	if err != nil || len(got) != 1 || got[0].ID != fresh {
		t.Fatal("current recall includes old interpretation", err)
	}
	historical, err := pg.NewRecallStore(f.pool, f.schema).FactsAbout(ctx, []string{"p1"}, []string{*current.SubjectID}, 10, []string{"user"}, domain.AsOf{Known: *before.Known.From}, 1)
	if err != nil || len(historical) != 1 || historical[0].ID != f.original {
		t.Fatal("historical recall changed", err)
	}
	retry, err := f.facts.ApplyGeneration(ctx, f.schema, f.snapshot, key, f.version, actor, pg.GenerationPlan{Claims: []domain.Claim{f.claim}})
	if err != nil || retry.ID != result.ID || !retry.Replayed {
		t.Fatal("published operation did not replay", err)
	}
	if _, err := f.facts.AssertVersioned(ctx, f.schema, "p1", f.snapshot.SourceID, domain.RoleUser, "subject-1", f.claim, f.version); !errors.Is(err, pg.ErrExtractionChanged) {
		t.Fatal("old in-flight formation appended to sealed generation", err)
	}
	if _, err := f.pool.Exec(ctx, f.schema.SQL(`DELETE FROM {schema}.fact`)); err != nil {
		t.Fatal(err)
	}
	page, err := f.facts.RecoverFacts(ctx, f.schema, "p1", f.snapshot.SourceID, "", actor, 100)
	if err != nil || page.Restored != 2 {
		t.Fatal("generation fact recovery failed", err)
	}
	if _, err := pg.NewRecordStore(f.pool, f.schema).Retract(ctx, "p1", actor, []pg.RecordMutation{recordMutation(t, f.pool, f.schema, fresh)}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, f.schema.SQL(`DELETE FROM {schema}.fact WHERE fact_id=$1`), fresh); err != nil {
		t.Fatal(err)
	}
	replay, found, err := f.facts.ReplayGeneration(ctx, f.schema, "p1", f.snapshot.SourceID)
	if err != nil || !found || replay.Retracted != 1 || replay.Asserted != 0 {
		t.Fatal("replay reactivated a human withdrawal", err)
	}
}

func TestGenerationRefusesChangedSourcesAndRollsBackPublicationFailure(t *testing.T) {
	ctx := context.Background()
	f := generationSource(t, "generation_atomic")
	if _, err := f.pool.Exec(ctx, f.schema.SQL(`CREATE FUNCTION {schema}.refuse_generation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected publication failure'; END $$;
        CREATE TRIGGER refuse_generation BEFORE INSERT ON {schema}.fact_generation FOR EACH ROW EXECUTE FUNCTION {schema}.refuse_generation()`)); err != nil {
		t.Fatal(err)
	}
	before := recoverySnapshot(t, f.pool, f.schema, "fact")
	if _, err := f.facts.ApplyGeneration(ctx, f.schema, f.snapshot, uuid.NewString(), f.version, uuid.NewString(), pg.GenerationPlan{Claims: []domain.Claim{f.claim}}); err == nil {
		t.Fatal("publication failure committed")
	}
	if recoverySnapshot(t, f.pool, f.schema, "fact") != before {
		t.Fatal("failed publication changed old memory")
	}
	for _, table := range []string{"fact_generation", "fact_generation_record"} {
		var n int
		if err := f.pool.QueryRow(ctx, f.schema.SQL("SELECT count(*) FROM {schema}."+table)).Scan(&n); err != nil || n != 0 {
			t.Fatal("partial generation", err)
		}
	}
	if _, err := pg.NewRecordStore(f.pool, f.schema).Retract(ctx, "p1", uuid.NewString(), []pg.RecordMutation{recordMutation(t, f.pool, f.schema, f.original)}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.facts.ApplyGeneration(ctx, f.schema, f.snapshot, uuid.NewString(), f.version, uuid.NewString(), pg.GenerationPlan{Claims: []domain.Claim{f.claim}}); !errors.Is(err, pg.ErrGenerationConflict) {
		t.Fatal("concurrent human edit did not invalidate preparation", err)
	}
}

func TestGenerationAdvancesLineageForASecondInterpretation(t *testing.T) {
	ctx := context.Background()
	f := generationSource(t, "generation_lineage")
	first, err := f.facts.ApplyGeneration(ctx, f.schema, f.snapshot, uuid.NewString(), f.version, uuid.NewString(), pg.GenerationPlan{Claims: []domain.Claim{f.claim}})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := f.facts.ReadGenerationSource(ctx, f.schema, "p1", f.snapshot.SourceID)
	if err != nil {
		t.Fatal(err)
	}
	claim := f.claim
	claim.Statement = "The speaker works at Ensera"
	second, err := f.facts.ApplyGeneration(ctx, f.schema, snapshot, uuid.NewString(), "extract/v2:"+strings.Repeat("c", 64), uuid.NewString(), pg.GenerationPlan{Claims: []domain.Claim{claim}})
	if err != nil || second.Retired != 1 || second.Asserted != 1 || second.ID == first.ID {
		t.Fatalf("second generation did not retire and replace the first: %+v %v", second, err)
	}
	var previous string
	if err := f.pool.QueryRow(ctx, f.schema.SQL(`SELECT previous_generation_id::text FROM {schema}.fact_generation WHERE generation_id=$1`), second.ID).Scan(&previous); err != nil {
		t.Fatal(err)
	}
	if previous != first.ID {
		t.Fatalf("generation lineage was not retained: previous=%s first=%s", previous, first.ID)
	}
}

// A generation whose plan carries two current values for one single-cardinality relation admits the
// first and refuses the second, instead of aborting the whole generation on the constraint.
func TestGenerationRefusesASecondCurrentValueAndKeepsTheRest(t *testing.T) {
	ctx := context.Background()
	f := generationSource(t, "generation_conflict")
	first := f.claim
	first.Predicate, first.Object, first.Cardinality = "lives_in", "Dublin", domain.CardinalityOne
	first.Statement = "The speaker lives in Dublin"
	second := first
	second.Object, second.Statement = "Amman", "The speaker lives in Amman"
	result, err := f.facts.ApplyGeneration(ctx, f.schema, f.snapshot, uuid.NewString(), f.version, uuid.NewString(),
		pg.GenerationPlan{Claims: []domain.Claim{first, second}})
	if err != nil {
		t.Fatalf("generation: %v", err)
	}
	if result.Asserted != 1 || result.Rejected != 1 {
		t.Fatalf("expected one admitted and one refused, got %+v", result)
	}
	var reason string
	if err := f.pool.QueryRow(ctx, f.schema.SQL(
		`SELECT reason FROM {schema}.rejected_claim WHERE scope='p1' AND predicate='lives_in'`)).Scan(&reason); err != nil {
		t.Fatalf("read refusal: %v", err)
	}
	if reason != domain.ReasonConflictingValue {
		t.Fatalf("refused for the wrong reason: %q", reason)
	}
}
