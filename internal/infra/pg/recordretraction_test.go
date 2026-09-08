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
	"github.com/jackc/pgx/v5/pgxpool"
)

func recordMutation(t *testing.T, pool *pgxpool.Pool, schema pg.Schema, id string) pg.RecordMutation {
	t.Helper()
	out := pg.RecordMutation{ID: id}
	if err := pool.QueryRow(context.Background(), schema.SQL(`SELECT version::text FROM {schema}.fact WHERE fact_id=$1`), id).Scan(&out.ExpectedVersion); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRetractionPreservesPastKnowledgeAndSuppressesSourceReextraction(t *testing.T) {
	for _, subject := range []string{"Atlas", "I"} {
		t.Run(subject, func(t *testing.T) {
			ctx := context.Background()
			pool := testPool(t)
			schema := tenant(t, pool, "record_withdrawal_"+strings.ToLower(subject))
			obs, facts := pg.NewObservationStore(pool), pg.NewFactStore(pool)
			march := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
			text := subject + " lives in Dublin"
			claim := domain.Claim{Subject: subject, Predicate: "lives_in", Object: "Dublin", Statement: text, Quote: text, ByteEnd: len(text), Cardinality: domain.CardinalityOne, ValidFrom: march}
			id := statedAt(t, ctx, obs, facts, schema, march, text, claim)
			before, err := pg.NewCitationStore(pool, schema).Resolve(ctx, "p1", id, nil, 8)
			if err != nil {
				t.Fatal(err)
			}
			actor := uuid.NewString()
			result, err := pg.NewRecordStore(pool, schema).Retract(ctx, "p1", actor, []pg.RecordMutation{recordMutation(t, pool, schema, id)})
			if err != nil || len(result.Records) != 1 {
				t.Fatalf("withdraw: %+v %v", result, err)
			}
			current, err := pg.NewCitationStore(pool, schema).Resolve(ctx, "p1", id, nil, 8)
			if err != nil || current.Status != "retracted" || current.Retraction == nil || current.Retraction.PrincipalID != actor || current.Version == before.Version {
				t.Fatalf("missing retained withdrawal: %+v %v", current, err)
			}
			if len(current.Evidence) != 1 || current.Evidence[0].Quote != text || current.Known.Until == nil || current.Valid.Until != nil {
				t.Fatal("withdrawal rewrote evidence or valid time")
			}
			for _, at := range []domain.AsOf{{}, {Known: *before.Known.From}} {
				got, err := pg.NewRecallStore(pool, schema).FactsAbout(ctx, []string{"p1"}, []string{*before.SubjectID}, 10, []string{"user"}, at, 1)
				if err != nil {
					t.Fatal(err)
				}
				want := 0
				if at.HasKnown() {
					want = 1
				}
				if len(got) != want {
					t.Fatalf("wrong knowledge view: %d want %d", len(got), want)
				}
			}
			history, err := pg.NewRecordStore(pool, schema).History(ctx, "p1", id, nil, 20)
			if err != nil || history.Retraction == nil || history.Version != current.Version {
				t.Fatalf("missing history metadata: %+v %v", history, err)
			}
			source := before.Evidence[0].ObservationID
			// Simulate derived-row loss. The authoritative source instruction must survive it.
			if _, err := pool.Exec(ctx, schema.SQL(`DELETE FROM {schema}.fact WHERE fact_id=$1`), id); err != nil {
				t.Fatal(err)
			}
			claim.Statement = "a different paraphrase"
			claim.ValidFrom = march.AddDate(0, 1, 0)
			if _, err := facts.Assert(ctx, schema, "p1", source, domain.RoleUser, "subject-1", claim); !errors.Is(err, pg.ErrRetractedClaim) {
				t.Fatalf("source withdrawal was lost: %v", err)
			}
			// An independent later source is new evidence, not resurrection of the old extraction.
			statedAt(t, ctx, obs, facts, schema, march, text, claim)
			exported, err := pg.NewExporter(pool, schema).Export(ctx, schema, "p1", "subject-1")
			if err != nil || len(exported.Sections["record_retraction"]) != 1 {
				t.Fatalf("instruction missing from export: %v", err)
			}
			erased, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", "subject-1", "requested")
			if err != nil || erased.Deleted["record_retraction"] != 1 || erased.Residual["record_retraction"] != 0 {
				t.Fatalf("instruction erasure: %+v %v", erased, err)
			}
			var count int
			if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.record_retraction`)).Scan(&count); err != nil || count != 0 {
				t.Fatalf("withdrawal residue: %d %v", count, err)
			}
		})
	}
}

func TestRetractionBatchesAreAtomicAndVersionChecked(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "record_batch")
	obs, facts := pg.NewObservationStore(pool), pg.NewFactStore(pool)
	now := time.Now().UTC()
	var requests []pg.RecordMutation
	for _, name := range []string{"Atlas", "Marta"} {
		id := statedAt(t, ctx, obs, facts, schema, now, name+" works at Ensera", domain.Claim{Subject: name, Predicate: "works_at", Object: "Ensera", Statement: name + " works at Ensera", Cardinality: domain.CardinalityMany})
		requests = append(requests, recordMutation(t, pool, schema, id))
	}
	store := pg.NewRecordStore(pool, schema)
	actor := uuid.NewString()
	stale := append([]pg.RecordMutation(nil), requests...)
	stale[1].ExpectedVersion = uuid.NewString()
	if _, err := store.Retract(ctx, "p1", actor, stale); !errors.Is(err, pg.ErrRecordConflict) {
		t.Fatalf("stale batch: %v", err)
	}
	var open, instructions int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FILTER(WHERE upper_inf(known)),(SELECT count(*) FROM {schema}.record_retraction) FROM {schema}.fact`)).Scan(&open, &instructions); err != nil || open != 2 || instructions != 0 {
		t.Fatal("partial batch committed")
	}
	// Even the last audit insert must roll back the instructions and both changed records.
	if _, err := pool.Exec(ctx, schema.SQL(`ALTER TABLE {schema}.audit_entry ADD CONSTRAINT refuse_test_retraction CHECK(operation<>'record.retract')`)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Retract(ctx, "p1", actor, requests); err == nil {
		t.Fatal("missing atomic audit did not refuse mutation")
	}
	if _, err := pool.Exec(ctx, schema.SQL(`ALTER TABLE {schema}.audit_entry DROP CONSTRAINT refuse_test_retraction`)); err != nil {
		t.Fatal(err)
	}
	for _, request := range requests {
		if got := recordMutation(t, pool, schema, request.ID); got != request {
			t.Fatal("failed transaction changed version")
		}
	}
	result, err := store.Retract(ctx, "p1", actor, requests)
	if err != nil || len(result.Records) != 2 {
		t.Fatalf("atomic batch: %+v %v", result, err)
	}
	for i, item := range result.Records {
		if item.ID != requests[i].ID || item.Version == requests[i].ExpectedVersion {
			t.Fatal("batch lost ordering or version")
		}
	}
	if _, err := store.Retract(ctx, "p1", actor, requests); !errors.Is(err, pg.ErrRecordConflict) {
		t.Fatalf("stale replay accepted: %v", err)
	}
	entries, err := pg.NewAuditStore(pool, schema).Recent(ctx, 10)
	if err != nil || len(entries) != 1 || entries[0].Operation != domain.AuditRecordRetract || entries[0].Magnitude != 2 {
		t.Fatalf("mutation audit: %+v %v", entries, err)
	}
}

func TestRetractionSourceBoundsAndProjectRefusalsLeaveMemoryUnchanged(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "record_bounds")
	obs, facts := pg.NewObservationStore(pool), pg.NewFactStore(pool)
	id := statedAt(t, ctx, obs, facts, schema, time.Now().UTC(), strings.Repeat("x", 256), domain.Claim{Subject: "Atlas", Predicate: "works_at", Object: "Ensera", Statement: "Atlas works at Ensera", Cardinality: domain.CardinalityMany})
	request := recordMutation(t, pool, schema, id)
	store := pg.NewRecordStore(pool, schema)
	actor := uuid.NewString()
	for _, scope := range []string{"p2", "p3"} {
		if _, err := store.Retract(ctx, scope, actor, []pg.RecordMutation{request}); !errors.Is(err, pg.ErrRecordNotFound) {
			t.Fatalf("foreign record: %v", err)
		}
	}
	for _, requests := range [][]pg.RecordMutation{nil, {}, {{ID: "bad", ExpectedVersion: request.ExpectedVersion}}, {{ID: id, ExpectedVersion: "bad"}}, {request, request}, make([]pg.RecordMutation, 21)} {
		if _, err := store.Retract(ctx, "p1", actor, requests); !errors.Is(err, pg.ErrInvalidRecordMutation) {
			t.Fatalf("bad mutation accepted: %v", err)
		}
	}
	if _, err := store.Retract(ctx, "invalid scope", actor, []pg.RecordMutation{request}); !errors.Is(err, pg.ErrInvalidRecordMutation) {
		t.Fatal(err)
	}
	if _, err := store.Retract(ctx, "p1", "bad", []pg.RecordMutation{request}); !errors.Is(err, pg.ErrInvalidRecordMutation) {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.fact_evidence(fact_id,source_observation_id,source_ordinal,quote,byte_start,byte_end,extractor_version,scope)
        SELECT e.fact_id,e.source_observation_id,0,'x',n,n+1,'fixture',e.scope FROM {schema}.fact_evidence e CROSS JOIN generate_series(1,128) n WHERE e.fact_id=$1`), id); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Retract(ctx, "p1", actor, []pg.RecordMutation{request}); !errors.Is(err, pg.ErrRecordMutationLimit) {
		t.Fatalf("oversized sources accepted: %v", err)
	}
	if got := recordMutation(t, pool, schema, id); got != request {
		t.Fatal("refusal changed record")
	}
}

func TestConcurrentRetractionsCommitExactlyOneVersionTransition(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "record_retract_race")
	id := statedAt(t, ctx, pg.NewObservationStore(pool), pg.NewFactStore(pool), schema, time.Now().UTC(), "Atlas works at Ensera", domain.Claim{Subject: "Atlas", Predicate: "works_at", Object: "Ensera", Statement: "Atlas works at Ensera", Cardinality: domain.CardinalityMany})
	request := recordMutation(t, pool, schema, id)
	store := pg.NewRecordStore(pool, schema)
	actor := uuid.NewString()
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.Retract(ctx, "p1", actor, []pg.RecordMutation{request})
			errs <- err
		}()
	}
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
	if success != 1 {
		t.Fatalf("successful transitions: %d", success)
	}
	var instructions int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.record_retraction`)).Scan(&instructions); err != nil || instructions != 1 {
		t.Fatalf("instructions: %d %v", instructions, err)
	}
}
