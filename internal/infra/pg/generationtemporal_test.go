// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
)

func TestGenerationCannotStepAroundAHumanCorrectionByChangingTheObject(t *testing.T) {
	ctx := context.Background()
	f := generationSource(t, "generation_correction")
	result, err := pg.NewRecordStore(f.pool, f.schema).Correct(ctx, "p1", uuid.NewString(), []pg.RecordCorrection{{RecordMutation: recordMutation(t, f.pool, f.schema, f.original), Object: "Orbit", Statement: "I work at Orbit"}})
	if err != nil {
		t.Fatal(err)
	}
	citations := pg.NewCitationStore(f.pool, f.schema)
	human, err := citations.Resolve(ctx, "p1", result.Records[0].ID, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	f.snapshot, err = f.facts.ReadGenerationSource(ctx, f.schema, "p1", f.snapshot.SourceID)
	if err != nil {
		t.Fatal(err)
	}
	f.claim.Object = "Different"
	f.claim.Statement = "I work at Different"
	g, err := f.facts.ApplyGeneration(ctx, f.schema, f.snapshot, uuid.NewString(), f.version, uuid.NewString(), pg.GenerationPlan{Claims: []domain.Claim{f.claim}})
	if err != nil || g.Asserted != 0 || g.Retracted != 1 {
		t.Fatal("generation bypassed human correction", g, err)
	}
	after, err := citations.Resolve(ctx, "p1", human.ID, nil, 10)
	if err != nil || after.Version != human.Version || after.Statement != human.Statement || after.Known.Until != nil {
		t.Fatal("human replacement changed", err)
	}
	replay, found, err := f.facts.ReplayGeneration(ctx, f.schema, "p1", f.snapshot.SourceID)
	if err != nil || !found || replay.Retracted != 1 {
		t.Fatal("generation replay lost protected correction", err)
	}
}

func TestGenerationBoundsAnEarlierFactAtALaterHumanAssertionAndKeepsItsCause(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "generation_future")
	obs, facts := pg.NewObservationStore(pool), pg.NewFactStore(pool)
	jan := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	june := jan.AddDate(0, 5, 0)
	claim := domain.Claim{Subject: "I", Object: "Dublin", Predicate: "lives_in", Statement: "I live in Dublin", Quote: "I live in Dublin", ByteEnd: len("I live in Dublin"), Cardinality: domain.CardinalityOne, ValidFrom: jan}
	id := statedAt(t, ctx, obs, facts, schema, jan, claim.Quote, claim)
	human, err := pg.NewRecordStore(pool, schema).AssertRecords(ctx, "p1", uuid.NewString(), []pg.RecordAssertion{{IdempotencyKey: uuid.NewString(), DataSubjectID: "subject-1", Subject: "I", Predicate: "lives_in", Object: "Oslo", Statement: "I live in Oslo", ValidFrom: june}})
	if err != nil {
		t.Fatal(err)
	}
	citations := pg.NewCitationStore(pool, schema)
	before, err := citations.Resolve(ctx, "p1", human.Records[0].ID, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	var source string
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT source_observation_id::text FROM {schema}.fact_receipt WHERE fact_id=$1`), id).Scan(&source); err != nil {
		t.Fatal(err)
	}
	snapshot, err := facts.ReadGenerationSource(ctx, schema, "p1", source)
	if err != nil {
		t.Fatal(err)
	}
	g, err := facts.ApplyGeneration(ctx, schema, snapshot, uuid.NewString(), "extract/v2:"+strings.Repeat("a", 64), uuid.NewString(), pg.GenerationPlan{Claims: []domain.Claim{claim}})
	if err != nil {
		t.Fatal(err)
	}
	var fresh string
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT fact_id::text FROM {schema}.fact_generation_record WHERE generation_id=$1 AND disposition='admitted'`), g.ID).Scan(&fresh); err != nil {
		t.Fatal(err)
	}
	newCitation, err := citations.Resolve(ctx, "p1", fresh, nil, 10)
	if err != nil || newCitation.Valid.Until == nil || !newCitation.Valid.Until.Equal(june) || newCitation.SupersededBy == nil || *newCitation.SupersededBy != before.ID || newCitation.SupersessionSource == nil || *newCitation.SupersessionSource != human.Records[0].SourceObservationID {
		t.Fatal("generation lost the future boundary or its source", err)
	}
	after, err := citations.Resolve(ctx, "p1", before.ID, nil, 10)
	if err != nil || after.Version != before.Version {
		t.Fatal("generation rewrote a later human assertion", err)
	}
	if _, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", "subject-1", "generation test"); err != nil {
		t.Fatal(err)
	}
	if _, err := citations.Resolve(ctx, "p1", fresh, nil, 10); !errors.Is(err, pg.ErrCitationNotFound) {
		t.Fatal("generation retained a validity boundary after its cause was erased", err)
	}
}

func TestGenerationExportAndErasureIncludeLineageWithoutChangingOtherSubjects(t *testing.T) {
	ctx := context.Background()
	f := generationSource(t, "generation_governance")
	g, err := f.facts.ApplyGeneration(ctx, f.schema, f.snapshot, uuid.NewString(), f.version, uuid.NewString(), pg.GenerationPlan{Claims: []domain.Claim{f.claim}})
	if err != nil {
		t.Fatal(err)
	}
	citation, err := pg.NewCitationStore(f.pool, f.schema).Resolve(ctx, "p1", f.original, nil, 10)
	if err != nil || citation.Status != "reinterpreted" || citation.ReinterpretedBy == nil || citation.ReinterpretedBy.ID != g.ID {
		t.Fatal("old citation cannot explain its retirement", err)
	}
	exported, err := pg.NewExporter(f.pool, f.schema).Export(ctx, f.schema, "p1", "subject-1")
	if err != nil || len(exported.Sections["fact_generation"]) != 1 || len(exported.Sections["fact_generation_record"]) != 2 {
		t.Fatal("lineage not exported", err)
	}
	foreign, err := pg.NewExporter(f.pool, f.schema).Export(ctx, f.schema, "p1", "other")
	if err != nil || len(foreign.Sections["fact_generation"]) != 0 || len(foreign.Sections["fact_generation_record"]) != 0 {
		t.Fatal("lineage crossed subject boundary", err)
	}
	erased, err := pg.NewEraser(f.pool).Erase(ctx, f.schema, "p1", "subject-1", "requested")
	if err != nil || erased.Deleted["fact_generation"] != 1 || erased.Deleted["fact_generation_record"] != 2 || erased.Residual["fact_generation"] != 0 {
		t.Fatal("lineage was not counted and erased", err)
	}
	if _, found, err := f.facts.ReplayGeneration(ctx, f.schema, "p1", f.snapshot.SourceID); err != nil || found {
		t.Fatal("erased generation replayed", err)
	}
}

func TestGenerationRequestsAreBoundedAndPublishedResultsCanBeLookedUp(t *testing.T) {
	ctx := context.Background()
	f := generationSource(t, "generation_requests")
	actor, key := uuid.NewString(), uuid.NewString()
	validVersion := "extract/v2:" + strings.Repeat("d", 64)

	if _, err := f.facts.ReadGenerationSource(ctx, f.schema, "p1", "not-a-uuid"); !errors.Is(err, pg.ErrInvalidGeneration) {
		t.Fatalf("invalid source was accepted: %v", err)
	}
	if _, _, err := f.facts.FindGeneration(ctx, f.schema, "bad scope", f.snapshot.SourceID, key, validVersion); !errors.Is(err, pg.ErrInvalidGeneration) {
		t.Fatalf("invalid scope was accepted: %v", err)
	}
	if _, err := f.facts.ApplyGeneration(ctx, f.schema, f.snapshot, "not-a-uuid", validVersion, actor, pg.GenerationPlan{}); !errors.Is(err, pg.ErrInvalidGeneration) {
		t.Fatalf("invalid operation key was accepted: %v", err)
	}
	if _, err := f.facts.ApplyGeneration(ctx, f.schema, f.snapshot, key, pg.ExtractorVersion, actor, pg.GenerationPlan{}); !errors.Is(err, pg.ErrInvalidExtractionVersion) {
		t.Fatalf("invalid extraction version was accepted: %v", err)
	}
	claims := make([]domain.Claim, pg.MaxGenerationClaims+1)
	if _, err := f.facts.ApplyGeneration(ctx, f.schema, f.snapshot, key, validVersion, actor, pg.GenerationPlan{Claims: claims}); !errors.Is(err, pg.ErrGenerationLimit) {
		t.Fatalf("oversized generation was accepted: %v", err)
	}
	if _, err := f.facts.ApplyGeneration(ctx, f.schema, f.snapshot, uuid.NewString(), validVersion, actor, pg.GenerationPlan{Rejected: []domain.RejectedClaim{{SourceOrdinal: 99}}}); !errors.Is(err, pg.ErrInvalidReceipt) {
		t.Fatalf("rejection from an unknown message was accepted: %v", err)
	}
	if _, err := f.facts.ApplyGeneration(ctx, f.schema, f.snapshot, uuid.NewString(), validVersion, actor, pg.GenerationPlan{Rejected: []domain.RejectedClaim{{SourceOrdinal: 0, Statement: strings.Repeat("x", pg.MaxGenerationBytes)}}}); !errors.Is(err, pg.ErrGenerationLimit) {
		t.Fatalf("oversized rejection payload was accepted: %v", err)
	}
	if _, err := f.facts.ApplyGeneration(ctx, f.schema, f.snapshot, key, validVersion, "not-a-uuid", pg.GenerationPlan{}); !errors.Is(err, pg.ErrInvalidGeneration) {
		t.Fatalf("invalid principal was accepted: %v", err)
	}

	result, err := f.facts.ApplyGeneration(ctx, f.schema, f.snapshot, key, validVersion, actor, pg.GenerationPlan{Claims: []domain.Claim{f.claim}})
	if err != nil {
		t.Fatal(err)
	}
	lookedUp, found, err := f.facts.FindGeneration(ctx, f.schema, "p1", f.snapshot.SourceID, key, validVersion)
	if err != nil || !found || lookedUp.ID != result.ID || !lookedUp.Replayed {
		t.Fatalf("published generation lookup failed: %+v found=%v err=%v", lookedUp, found, err)
	}
	if _, _, err := f.facts.FindGeneration(ctx, f.schema, "p1", f.snapshot.SourceID, key, "extract/v2:"+strings.Repeat("e", 64)); !errors.Is(err, pg.ErrGenerationConflict) {
		t.Fatalf("version conflict was accepted: %v", err)
	}
	if _, found, err := f.facts.ReplayGeneration(ctx, f.schema, "p1", uuid.NewString()); err != nil || found {
		t.Fatalf("missing generation replay changed state: found=%v err=%v", found, err)
	}
	if err := f.facts.FinishGeneration(ctx, f.schema, "p1", uuid.NewString()); !errors.Is(err, pg.ErrGenerationSource) {
		t.Fatalf("missing source was marked finished: %v", err)
	}
}

func TestGenerationSourceRejectsAuthoritativeAndOversizedInput(t *testing.T) {
	ctx := context.Background()
	p := testPool(t)
	schema := tenant(t, p, "generation_source_bounds")
	authored, err := pg.NewRecordStore(p, schema).AssertRecords(ctx, "p1", uuid.NewString(), []pg.RecordAssertion{{
		IdempotencyKey: uuid.NewString(), DataSubjectID: "subject-1", Subject: "I", Predicate: "works_at", Object: "Ensera", Statement: "I work at Ensera",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pg.NewFactStore(p).ReadGenerationSource(ctx, schema, "p1", authored.Records[0].SourceObservationID); !errors.Is(err, pg.ErrGenerationSource) {
		t.Fatalf("authoritative source was accepted: %v", err)
	}

	observations := pg.NewObservationStore(p)
	turn, err := observations.Append(ctx, schema, domain.Turn{
		Scope: "p1", DataSubjectID: "subject-1", OccurredAt: time.Now().UTC(),
		Messages: []domain.Message{{Ordinal: 0, Role: domain.RoleUser, Content: "small"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Exec(ctx, schema.SQL(`UPDATE {schema}.turn_message SET content=repeat('x',$2) WHERE observation_id=$1::uuid`), turn.ID, pg.MaxGenerationBytes+1); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.NewFactStore(p).ReadGenerationSource(ctx, schema, "p1", turn.ID); !errors.Is(err, pg.ErrGenerationLimit) {
		t.Fatalf("oversized source was accepted: %v", err)
	}
}

func TestGenerationMessageTimeUsesSourceAndIngestedFallbacks(t *testing.T) {
	sourceTime := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	ingestedTime := sourceTime.Add(time.Hour)
	snapshot := pg.GenerationSnapshot{OccurredAt: sourceTime, IngestedAt: ingestedTime}
	if got := snapshot.MessageTime(domain.Message{OccurredAt: sourceTime.Add(time.Minute)}); !got.Equal(sourceTime.Add(time.Minute)) {
		t.Fatalf("message timestamp was ignored: %v", got)
	}
	if got := snapshot.MessageTime(domain.Message{}); !got.Equal(sourceTime) {
		t.Fatalf("source timestamp was ignored: %v", got)
	}
	snapshot.OccurredAt = time.Time{}
	if got := snapshot.MessageTime(domain.Message{}); !got.Equal(ingestedTime) {
		t.Fatalf("ingested fallback was ignored: %v", got)
	}
}

func TestGenerationValidatesClaimsAndPreservesMessageRolePolicy(t *testing.T) {
	ctx := context.Background()
	f := generationSource(t, "generation_claim_validation")
	bad := f.claim
	bad.Quote = "not in the message"
	bad.ByteEnd = len(bad.Quote)
	if _, err := f.facts.ApplyGeneration(ctx, f.schema, f.snapshot, uuid.NewString(), f.version, uuid.NewString(), pg.GenerationPlan{Claims: []domain.Claim{bad}}); !errors.Is(err, pg.ErrInvalidReceipt) {
		t.Fatalf("unverifiable claim was accepted: %v", err)
	}
	bad.SourceOrdinal = 99
	if _, err := f.facts.ApplyGeneration(ctx, f.schema, f.snapshot, uuid.NewString(), f.version, uuid.NewString(), pg.GenerationPlan{Claims: []domain.Claim{bad}}); !errors.Is(err, pg.ErrInvalidReceipt) {
		t.Fatalf("claim from an unknown message was accepted: %v", err)
	}
	duplicate, err := f.facts.ApplyGeneration(ctx, f.schema, f.snapshot, uuid.NewString(), f.version, uuid.NewString(), pg.GenerationPlan{Claims: []domain.Claim{f.claim, f.claim}})
	if err != nil || duplicate.Asserted != 1 {
		t.Fatalf("duplicate claim was not coalesced: %+v %v", duplicate, err)
	}

	observations := pg.NewObservationStore(f.pool)
	assistant, err := observations.Append(ctx, f.schema, domain.Turn{
		Scope: "p1", DataSubjectID: "subject-1", OccurredAt: time.Now().UTC(),
		Messages: []domain.Message{{Ordinal: 0, Role: domain.RoleAssistant, Content: f.claim.Quote}},
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := f.facts.ReadGenerationSource(ctx, f.schema, "p1", assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.facts.ApplyGeneration(ctx, f.schema, snapshot, uuid.NewString(), f.version, uuid.NewString(), pg.GenerationPlan{Claims: []domain.Claim{f.claim}})
	if err != nil || result.RefusedRole != 1 || result.Asserted != 0 {
		t.Fatalf("assistant claim was promoted: %+v %v", result, err)
	}
}

func TestGenerationRecordsUnboundRelativeSpeakerAsRejected(t *testing.T) {
	ctx := context.Background()
	p := testPool(t)
	schema := tenant(t, p, "generation_unbound_speaker")
	observations := pg.NewObservationStore(p)
	stored, err := observations.Append(ctx, schema, domain.Turn{
		Scope: "p1", OccurredAt: time.Now().UTC(),
		Messages: []domain.Message{{Ordinal: 0, Role: domain.RoleUser, Content: "I work at Ensera"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	facts := pg.NewFactStore(p)
	snapshot, err := facts.ReadGenerationSource(ctx, schema, "p1", stored.ID)
	if err != nil {
		t.Fatal(err)
	}
	claim := domain.Claim{
		Subject: "I", Predicate: "works_at", Object: "Ensera", Statement: "I work at Ensera",
		Quote: "I work at Ensera", ByteEnd: len("I work at Ensera"), Cardinality: domain.CardinalityMany,
	}
	result, err := facts.ApplyGeneration(ctx, schema, snapshot, uuid.NewString(), "extract/v2:"+strings.Repeat("f", 64), uuid.NewString(), pg.GenerationPlan{Claims: []domain.Claim{claim}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Asserted != 0 || result.Rejected != 1 {
		t.Fatalf("unbound speaker was promoted: %+v", result)
	}
	var reason string
	if err := p.QueryRow(ctx, schema.SQL(`SELECT reason FROM {schema}.rejected_claim LIMIT 1`)).Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if reason != string(domain.ReasonUnresolvableSubject) {
		t.Fatalf("unexpected rejection reason: %q", reason)
	}
	replay, found, err := facts.ReplayGeneration(ctx, schema, "p1", stored.ID)
	if err != nil || !found || !replay.Replayed || replay.Asserted != 0 || replay.Rejected != result.Rejected {
		t.Fatalf("empty generation did not replay its receipt: %+v found=%v err=%v", replay, found, err)
	}
}

func TestGenerationRefusesMoreThanBoundedPriorFacts(t *testing.T) {
	ctx := context.Background()
	p := testPool(t)
	schema := tenant(t, p, "generation_prior_bound")
	observations, facts := pg.NewObservationStore(p), pg.NewFactStore(p)
	stored, err := observations.Append(ctx, schema, domain.Turn{
		Scope: "p1", DataSubjectID: "subject-1", OccurredAt: time.Now().UTC(),
		Messages: []domain.Message{{Ordinal: 0, Role: domain.RoleUser, Content: "I work at Ensera"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < pg.MaxGenerationClaims+1; i++ {
		claim := domain.Claim{
			Subject: "I", Predicate: "works_at", Object: fmt.Sprintf("Company-%d", i),
			Statement: fmt.Sprintf("I work at Company-%d", i), Quote: "I work at Ensera",
			ByteEnd: len("I work at Ensera"), Cardinality: domain.CardinalityMany,
		}
		if _, err := facts.Assert(ctx, schema, "p1", stored.ID, domain.RoleUser, "subject-1", claim); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := facts.ReadGenerationSource(ctx, schema, "p1", stored.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := facts.ApplyGeneration(ctx, schema, snapshot, uuid.NewString(), "extract/v2:"+strings.Repeat("a", 64), uuid.NewString(), pg.GenerationPlan{}); !errors.Is(err, pg.ErrGenerationLimit) {
		t.Fatalf("generation accepted an unbounded prior source: %v", err)
	}
	if _, err := p.Exec(ctx, schema.SQL(`DELETE FROM {schema}.fact`)); err != nil {
		t.Fatal(err)
	}
	snapshot, err = facts.ReadGenerationSource(ctx, schema, "p1", stored.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := facts.ApplyGeneration(ctx, schema, snapshot, uuid.NewString(), "extract/v2:"+strings.Repeat("a", 64), uuid.NewString(), pg.GenerationPlan{}); !errors.Is(err, pg.ErrGenerationLimit) {
		t.Fatalf("generation restored an unbounded missing projection set: %v", err)
	}
}
