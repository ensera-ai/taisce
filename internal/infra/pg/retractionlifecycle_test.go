// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/report"
	"github.com/google/uuid"
)

// An independent source can assert overlapping valid time after knowledge of the old claim ends.
// This exercises the bitemporal exclusion constraint while both fact rows remain retained.
func TestRetractionAllowsIndependentKnowledgeWithoutChangingRetainedEvidence(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "retract_independent")
	obs, facts := pg.NewObservationStore(pool), pg.NewFactStore(pool)
	at := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	claim := domain.Claim{Subject: "Atlas", Predicate: "lives_in", Object: "Dublin", Statement: "Atlas lives in Dublin", Cardinality: domain.CardinalityOne, ValidFrom: at}
	first := statedAt(t, ctx, obs, facts, schema, at, claim.Statement, claim)
	if _, err := pg.NewRecordStore(pool, schema).Retract(ctx, "p1", uuid.NewString(), []pg.RecordMutation{recordMutation(t, pool, schema, first)}); err != nil {
		t.Fatal(err)
	}
	second := statedAt(t, ctx, obs, facts, schema, at, claim.Statement, claim)
	if first == second {
		t.Fatal("independent source reused a withdrawn identity")
	}
	var retained, open int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*),count(*) FILTER(WHERE upper_inf(known) AND upper_inf(valid)) FROM {schema}.fact`)).Scan(&retained, &open); err != nil || retained != 2 || open != 1 {
		t.Fatalf("retained knowledge: %d %d %v", retained, open, err)
	}
	old, err := pg.NewCitationStore(pool, schema).Resolve(ctx, "p1", first, nil, 8)
	if err != nil || old.Status != "retracted" || old.Valid.Until != nil || len(old.Evidence) != 1 {
		t.Fatalf("independent assertion changed withdrawal: %+v %v", old, err)
	}
	version := recordMutation(t, pool, schema, second)
	claim.Object, claim.Statement, claim.ValidFrom = "Oslo", "Atlas lives in Oslo", at.AddDate(0, 1, 0)
	statedAt(t, ctx, obs, facts, schema, claim.ValidFrom, claim.Statement, claim)
	if recordMutation(t, pool, schema, second).ExpectedVersion == version.ExpectedVersion {
		t.Fatal("supersession did not advance the editor version")
	}
	if _, err := pg.NewRecordStore(pool, schema).Retract(ctx, "p1", uuid.NewString(), []pg.RecordMutation{version}); !errors.Is(err, pg.ErrRecordConflict) {
		t.Fatalf("stale editor overwrote supersession: %v", err)
	}
}

func TestRetentionRemovesSourceOwnedRetractionInstructions(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "retract_retention")
	id := statedAt(t, ctx, pg.NewObservationStore(pool), pg.NewFactStore(pool), schema, time.Now().UTC(), "Atlas works at Ensera", domain.Claim{Subject: "Atlas", Predicate: "works_at", Object: "Ensera", Statement: "Atlas works at Ensera", Cardinality: domain.CardinalityMany})
	if _, err := pg.NewRecordStore(pool, schema).Retract(ctx, "p1", uuid.NewString(), []pg.RecordMutation{recordMutation(t, pool, schema, id)}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.observation SET retention_until=clock_timestamp()-interval '1 second'`)); err != nil {
		t.Fatal(err)
	}
	swept, err := pg.NewRetentionStore(pool, schema).Sweep(ctx, "p1", 10)
	if err != nil || swept.Observations != 1 {
		t.Fatalf("expiry: %+v %v", swept, err)
	}
	var n int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.record_retraction`)).Scan(&n); err != nil || n != 0 {
		t.Fatalf("expired source left withdrawal metadata: %d %v", n, err)
	}
}

func TestRetractionInvalidatesPublishedAndInFlightReports(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "retract_reports")
	id := statedAt(t, ctx, pg.NewObservationStore(pool), pg.NewFactStore(pool), schema, time.Now().UTC(), "Atlas works at Ensera", domain.Claim{Subject: "Atlas", Predicate: "works_at", Object: "Ensera", Statement: "Atlas works at Ensera", Cardinality: domain.CardinalityMany})
	material := projectReportMaterial(t, pool, schema)
	store := pg.NewCommunityStore(pool, schema)
	community := reportCommunity(t, pool, schema, "p1")
	content, input := report.Report{Title: "work", Summary: "Ensera"}, report.Context{Facts: material}
	if err := store.Write(ctx, "p1", community, content, input); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.NewRecordStore(pool, schema).Retract(ctx, "p1", uuid.NewString(), []pg.RecordMutation{recordMutation(t, pool, schema, id)}); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.Report(ctx, "p1", community); err != nil || found {
		t.Fatalf("withdrawal retained a published report: %v", err)
	}
	if err := store.Write(ctx, "p1", community, content, input); !errors.Is(err, pg.ErrReportSourceUnavailable) {
		t.Fatalf("in-flight report restored withdrawn knowledge: %v", err)
	}
}

func TestSourceReplayRacingRetractionLeavesNoActiveDuplicate(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "retract_replay_race")
	obs, facts := pg.NewObservationStore(pool), pg.NewFactStore(pool)
	text := "Atlas works at Ensera"
	source, err := obs.Append(ctx, schema, turnOf(domain.Message{Role: domain.RoleUser, Content: text}))
	if err != nil {
		t.Fatal(err)
	}
	claim := domain.Claim{Subject: "Atlas", Predicate: "works_at", Object: "Ensera", Statement: text, Quote: text, ByteEnd: len(text), Cardinality: domain.CardinalityMany}
	id, err := facts.Assert(ctx, schema, "p1", source.ID, domain.RoleUser, "subject-1", claim)
	if err != nil {
		t.Fatal(err)
	}
	request := recordMutation(t, pool, schema, id)
	start := make(chan struct{})
	errs := make(chan error, 16)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := facts.Assert(ctx, schema, "p1", source.ID, domain.RoleUser, "subject-1", claim)
			errs <- err
		}()
	}
	close(start)
	if _, err := pg.NewRecordStore(pool, schema).Retract(ctx, "p1", uuid.NewString(), []pg.RecordMutation{request}); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil && !errors.Is(err, pg.ErrRetractedClaim) {
			t.Fatal(err)
		}
	}
	var retained, active int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*),count(*) FILTER(WHERE upper_inf(known)) FROM {schema}.fact`)).Scan(&retained, &active); err != nil || retained != 1 || active != 0 {
		t.Fatalf("racing replay restored withdrawn knowledge: %d %d %v", retained, active, err)
	}
}
