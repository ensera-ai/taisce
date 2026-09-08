// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
)

// Corrections author new evidence; an old saved citation must never silently resolve to new words.
func TestCorrectionPreservesAttributionAndOneKnowledgeBoundary(t *testing.T) {
	for _, subject := range []string{"Atlas", "I"} {
		t.Run(subject, func(t *testing.T) {
			ctx := context.Background()
			pool := testPool(t)
			schema := tenant(t, pool, "correct_"+strings.ToLower(subject))
			march := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
			id := statedAt(t, ctx, pg.NewObservationStore(pool), pg.NewFactStore(pool), schema, march, subject+" lives in Dublin", domain.Claim{Subject: subject, Predicate: "lives_in", Object: "Dublin", Statement: subject + " lives in Dublin", Cardinality: domain.CardinalityOne, ValidFrom: march})
			actor := uuid.NewString()
			request := pg.RecordCorrection{RecordMutation: recordMutation(t, pool, schema, id), Object: "Oslo", Statement: subject + " lives in Oslo", ValidFrom: march}
			old, err := pg.NewCitationStore(pool, schema).Resolve(ctx, "p1", id, nil, 8)
			if err != nil {
				t.Fatal(err)
			}
			out, err := pg.NewRecordStore(pool, schema).Correct(ctx, "p1", actor, []pg.RecordCorrection{request})
			if err != nil || len(out.Records) != 1 {
				t.Fatalf("correct: %+v %v", out, err)
			}
			replacement := out.Records[0]
			if replacement.OriginalID != id || replacement.ID == id || replacement.Version == "" {
				t.Fatal("correction lost independent identity")
			}
			current, err := pg.NewCitationStore(pool, schema).Resolve(ctx, "p1", id, nil, 8)
			if err != nil {
				t.Fatal(err)
			}
			fresh, err := pg.NewCitationStore(pool, schema).Resolve(ctx, "p1", replacement.ID, nil, 8)
			if err != nil {
				t.Fatal(err)
			}
			if current.Retraction == nil || current.Retraction.ReplacementFactID == nil || *current.Retraction.ReplacementFactID != replacement.ID || *current.Retraction.ReplacementObservationID != replacement.SourceObservationID || current.Statement != old.Statement || current.Evidence[0].Quote != old.Evidence[0].Quote {
				t.Fatal("old citation was rewritten or replacement link lost")
			}
			if !current.Known.Until.Equal(*fresh.Known.From) || fresh.SubjectID == nil || *fresh.SubjectID != *old.SubjectID || fresh.Predicate != old.Predicate || fresh.Evidence[0].Quote != request.Statement || fresh.Evidence[0].ExtractorVersion != pg.CuratedExtractorVersion || fresh.Evidence[0].AuthoredBy == nil || *fresh.Evidence[0].AuthoredBy != actor {
				t.Fatalf("correction boundary or authorship lost: %+v", fresh)
			}
			recall := pg.NewRecallStore(pool, schema)
			past, err := recall.FactsAbout(ctx, []string{"p1"}, []string{*old.SubjectID}, 10, []string{"user"}, domain.AsOf{Known: *old.Known.From}, 1)
			if err != nil || len(past) != 1 || past[0].ID != id {
				t.Fatalf("past knowledge: %+v %v", past, err)
			}
			boundary, err := recall.FactsAbout(ctx, []string{"p1"}, []string{*old.SubjectID}, 10, []string{"user"}, domain.AsOf{Known: *fresh.Known.From}, 1)
			if err != nil || len(boundary) != 1 || boundary[0].ID != replacement.ID {
				t.Fatalf("correction knowledge: %+v %v", boundary, err)
			}
			exported, err := pg.NewExporter(pool, schema).Export(ctx, schema, "p1", "subject-1")
			if err != nil || len(exported.Sections["curated_claim"]) != 1 {
				t.Fatalf("authored source export: %v", err)
			}
			erased, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", "subject-1", "requested")
			if err != nil || erased.Deleted["curated_claim"] != 1 || erased.Residual["curated_claim"] != 0 {
				t.Fatalf("authored source erasure: %+v %v", erased, err)
			}
		})
	}
}

func TestCuratedReplayRecoversTheSavedIdentityAndHonorsLaterRetraction(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "correct_recovery")
	facts := pg.NewFactStore(pool)
	id := statedAt(t, ctx, pg.NewObservationStore(pool), facts, schema, time.Now().UTC(), "I work at Ensera", domain.Claim{Subject: "I", Predicate: "works_at", Object: "Ensera", Statement: "I work at Ensera", Cardinality: domain.CardinalityMany})
	out, err := pg.NewRecordStore(pool, schema).Correct(ctx, "p1", uuid.NewString(), []pg.RecordCorrection{{RecordMutation: recordMutation(t, pool, schema, id), Object: "Atlas", Statement: "I work at Atlas"}})
	if err != nil {
		t.Fatal(err)
	}
	replacement := out.Records[0]
	originalKnown := recordedAt(t, ctx, pool, schema, replacement.ID)
	if _, err := pool.Exec(ctx, schema.SQL(`DELETE FROM {schema}.fact WHERE fact_id=$1`), replacement.ID); err != nil {
		t.Fatal(err)
	}
	found, err := facts.ReplayCurated(ctx, schema, "p1", replacement.SourceObservationID)
	if err != nil || !found {
		t.Fatalf("curated recovery: %v", err)
	}
	citation, err := pg.NewCitationStore(pool, schema).Resolve(ctx, "p1", replacement.ID, nil, 8)
	if err != nil || citation.Statement != "I work at Atlas" || citation.Evidence[0].ExtractorVersion != pg.CuratedExtractorVersion {
		t.Fatalf("recovered claim: %+v %v", citation, err)
	}
	if !citation.Known.From.Equal(originalKnown) {
		t.Fatal("recovery changed when the authored correction was known")
	}
	if _, err := pg.NewRecordStore(pool, schema).Retract(ctx, "p1", uuid.NewString(), []pg.RecordMutation{recordMutation(t, pool, schema, replacement.ID)}); err != nil {
		t.Fatal(err)
	}
	if found, err := facts.ReplayCurated(ctx, schema, "p1", replacement.SourceObservationID); !found || !errors.Is(err, pg.ErrRetractedClaim) {
		t.Fatalf("curation bypassed later withdrawal: %v", err)
	}
}

func TestCorrectionBatchRollsBackSourcesVersionsAndAuditOnFailure(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "correct_rollback")
	store := pg.NewRecordStore(pool, schema)
	actor := uuid.NewString()
	var requests []pg.RecordCorrection
	for _, name := range []string{"Atlas", "Marta"} {
		id := statedAt(t, ctx, pg.NewObservationStore(pool), pg.NewFactStore(pool), schema, time.Now().UTC(), name+" works at Ensera", domain.Claim{Subject: name, Predicate: "works_at", Object: "Ensera", Statement: name + " works at Ensera", Cardinality: domain.CardinalityMany})
		requests = append(requests, pg.RecordCorrection{RecordMutation: recordMutation(t, pool, schema, id), Object: "Orbit", Statement: name + " works at Orbit"})
	}
	if _, err := pool.Exec(ctx, schema.SQL(`ALTER TABLE {schema}.audit_entry ADD CONSTRAINT fail_correction_audit CHECK(operation<>'record.correct')`)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Correct(ctx, "p1", actor, requests); err == nil {
		t.Fatal("audit failure committed correction")
	}
	for _, r := range requests {
		if recordMutation(t, pool, schema, r.ID) != r.RecordMutation {
			t.Fatal("rollback changed target version")
		}
	}
	var observations, curated, withdrawals, audit int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT (SELECT count(*) FROM {schema}.observation),(SELECT count(*) FROM {schema}.curated_claim),(SELECT count(*) FROM {schema}.record_retraction),(SELECT count(*) FROM {schema}.audit_entry)`)).Scan(&observations, &curated, &withdrawals, &audit); err != nil || observations != 2 || curated != 0 || withdrawals != 0 || audit != 0 {
		t.Fatalf("partial correction: %d %d %d %d %v", observations, curated, withdrawals, audit, err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`ALTER TABLE {schema}.audit_entry DROP CONSTRAINT fail_correction_audit`)); err != nil {
		t.Fatal(err)
	}
	out, err := store.Correct(ctx, "p1", actor, requests)
	if err != nil || len(out.Records) != 2 {
		t.Fatalf("batch correction: %+v %v", out, err)
	}
	for i, r := range out.Records {
		if r.OriginalID != requests[i].ID {
			t.Fatal("batch reordered")
		}
	}
	if _, err := store.Correct(ctx, "p1", actor, requests); !errors.Is(err, pg.ErrRecordConflict) {
		t.Fatalf("stale correction replay accepted: %v", err)
	}
}

func TestReplacementExpiryRemovesLinksWithoutResurrectingTheOriginal(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "correct_expiry")
	facts := pg.NewFactStore(pool)
	id := statedAt(t, ctx, pg.NewObservationStore(pool), facts, schema, time.Now().UTC(), "Atlas works at Ensera", domain.Claim{Subject: "Atlas", Predicate: "works_at", Object: "Ensera", Statement: "Atlas works at Ensera", Cardinality: domain.CardinalityMany})
	before, err := pg.NewCitationStore(pool, schema).Resolve(ctx, "p1", id, nil, 8)
	if err != nil {
		t.Fatal(err)
	}
	out, err := pg.NewRecordStore(pool, schema).Correct(ctx, "p1", uuid.NewString(), []pg.RecordCorrection{{RecordMutation: recordMutation(t, pool, schema, id), Object: "Orbit", Statement: "Atlas works at Orbit"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.observation SET retention_until=clock_timestamp()-interval '1 second' WHERE observation_id=$1`), out.Records[0].SourceObservationID); err != nil {
		t.Fatal(err)
	}
	swept, err := pg.NewRetentionStore(pool, schema).Sweep(ctx, "p1", 10)
	if err != nil || swept.Observations != 1 {
		t.Fatalf("expiry: %+v %v", swept, err)
	}
	old, err := pg.NewCitationStore(pool, schema).Resolve(ctx, "p1", id, nil, 8)
	if err != nil || old.Retraction == nil || old.Retraction.ReplacementFactID != nil || old.Retraction.ReplacementObservationID != nil {
		t.Fatalf("replacement left a link: %+v %v", old, err)
	}
	if _, err := facts.Assert(ctx, schema, "p1", before.Evidence[0].ObservationID, domain.RoleUser, "subject-1", domain.Claim{Subject: "Atlas", Predicate: "works_at", Object: "Ensera", Statement: "Atlas works at Ensera", Cardinality: domain.CardinalityMany}); !errors.Is(err, pg.ErrRetractedClaim) {
		t.Fatalf("replacement expiry undid original withdrawal: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.curated_claim`)).Scan(&n); err != nil || n != 0 {
		t.Fatalf("authored source residue: %d %v", n, err)
	}
}

func TestConcurrentCorrectionsCreateExactlyOneReplacement(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "correct_race")
	id := statedAt(t, ctx, pg.NewObservationStore(pool), pg.NewFactStore(pool), schema, time.Now().UTC(), "Atlas works at Ensera", domain.Claim{Subject: "Atlas", Predicate: "works_at", Object: "Ensera", Statement: "Atlas works at Ensera", Cardinality: domain.CardinalityMany})
	request := pg.RecordCorrection{RecordMutation: recordMutation(t, pool, schema, id), Object: "Orbit", Statement: "Atlas works at Orbit"}
	actor := uuid.NewString()
	start := make(chan struct{})
	errs := make(chan error, 12)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := pg.NewRecordStore(pool, schema).Correct(ctx, "p1", actor, []pg.RecordCorrection{request})
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	success := 0
	for err := range errs {
		if err == nil {
			success++
		} else if !errors.Is(err, pg.ErrRecordConflict) {
			t.Fatal(err)
		}
	}
	var n int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.curated_claim`)).Scan(&n); err != nil || n != 1 || success != 1 {
		t.Fatalf("competing edits committed: %d %d %v", n, success, err)
	}
}
