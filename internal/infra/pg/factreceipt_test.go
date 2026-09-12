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
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func recoverySnapshot(t *testing.T, pool *pgxpool.Pool, schema pg.Schema, table string) string {
	t.Helper()
	var out string
	// Recovery reconnects missing successor FKs, which legitimately advances optimistic versions.
	err := pool.QueryRow(context.Background(), schema.SQL("SELECT coalesce(jsonb_agg(to_jsonb(t)-'version' ORDER BY to_jsonb(t)::text),'[]'::jsonb)::text FROM {schema}."+table+" t WHERE scope='p1'")).Scan(&out)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestReceiptRecoveryPreservesCitationsTemporalHistoryAndHumanEdits(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "receipt_lifecycle")
	obs, facts := pg.NewObservationStore(pool), pg.NewFactStore(pool)
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var ids []string
	for i, place := range []string{"Dublin", "Oslo"} {
		text := "I live in " + place
		ids = append(ids, statedAt(t, ctx, obs, facts, schema, start.AddDate(0, i, 0), text, domain.Claim{Subject: "I", Predicate: "lives_in", Object: place, Statement: text, Cardinality: domain.CardinalityOne, ValidFrom: start.AddDate(0, i, 0)}))
	}
	original := statedAt(t, ctx, obs, facts, schema, start, "Atlas works at Ensera", domain.Claim{Subject: "Atlas", Predicate: "works_at", Object: "Ensera", Statement: "Atlas works at Ensera", Cardinality: domain.CardinalityMany})
	corrected, err := pg.NewRecordStore(pool, schema).Correct(ctx, "p1", uuid.NewString(), []pg.RecordCorrection{{RecordMutation: recordMutation(t, pool, schema, original), Object: "Orbit", Statement: "Atlas works at Orbit"}})
	if err != nil {
		t.Fatal(err)
	}
	replacement := corrected.Records[0].ID
	if _, err := pg.NewRecordStore(pool, schema).Retract(ctx, "p1", uuid.NewString(), []pg.RecordMutation{recordMutation(t, pool, schema, replacement)}); err != nil {
		t.Fatal(err)
	}
	ids = append(ids, original, replacement)
	beforeFacts := recoverySnapshot(t, pool, schema, "fact")
	beforeHistory := recoverySnapshot(t, pool, schema, "fact_history")
	beforeEvidence := recoverySnapshot(t, pool, schema, "fact_evidence")
	if _, err := pool.Exec(ctx, schema.SQL(`DELETE FROM {schema}.fact WHERE scope='p1'; DELETE FROM {schema}.entity WHERE scope='p1'`)); err != nil {
		t.Fatal(err)
	}
	restored := 0
	after := ""
	for i := 0; i < 5; i++ {
		page, err := facts.RecoverFacts(ctx, schema, "p1", "", after, uuid.NewString(), 1)
		if err != nil {
			t.Fatal(err)
		}
		restored += page.Restored
		if page.Examined != 1 {
			t.Fatalf("unbounded or skipped page: %+v", page)
		}
		if page.Next == "" {
			break
		}
		after = page.Next
	}
	if restored != 4 {
		t.Fatalf("restored %d", restored)
	}
	if got := recoverySnapshot(t, pool, schema, "fact"); got != beforeFacts {
		t.Fatal("recovery changed recorded fact state")
	}
	if got := recoverySnapshot(t, pool, schema, "fact_history"); got != beforeHistory {
		t.Fatal("recovery changed recorded history")
	}
	if got := recoverySnapshot(t, pool, schema, "fact_evidence"); got != beforeEvidence {
		t.Fatal("recovery changed exact evidence or version stamps")
	}
	for _, id := range ids {
		citation, err := pg.NewCitationStore(pool, schema).Resolve(ctx, "p1", id, nil, 8)
		if err != nil || len(citation.Evidence) != 1 {
			t.Fatalf("citation unresolved: %v", err)
		}
		if (id == original || id == replacement) && (citation.Known.Until == nil || citation.Retraction == nil) {
			t.Fatal("withdrawn record became current")
		}
	}
	replay, err := facts.RecoverFacts(ctx, schema, "p1", "", "", uuid.NewString(), 100)
	if err != nil || replay.Restored != 0 || replay.Examined != 4 {
		t.Fatalf("replay: %+v %v", replay, err)
	}
	exported, err := pg.NewExporter(pool, schema).Export(ctx, schema, "p1", "subject-1")
	if err != nil || len(exported.Sections["fact_receipt"]) != 4 || len(exported.Sections["fact_receipt_history"]) != 1 {
		t.Fatalf("missing receipt export: %v", err)
	}
	erased, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", "subject-1", "requested")
	if err != nil || erased.Deleted["fact_receipt"] != 4 || erased.Deleted["fact_receipt_history"] != 1 || erased.Residual["fact_receipt"] != 0 {
		t.Fatalf("receipt erasure: %+v %v", erased, err)
	}
	page, err := facts.RecoverFacts(ctx, schema, "p1", "", "", uuid.NewString(), 100)
	if err != nil || page.Examined != 0 {
		t.Fatalf("erased source recovered: %+v %v", page, err)
	}
}

func TestReceiptRecoveryPagesAreAtomicScopedAndSafeToRetry(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "receipt_pages")
	facts := pg.NewFactStore(pool)
	first := statedAt(t, ctx, pg.NewObservationStore(pool), facts, schema, time.Now(), "Atlas works at Ensera", domain.Claim{Subject: "Atlas", Predicate: "works_at", Object: "Ensera", Statement: "Atlas works at Ensera", Cardinality: domain.CardinalityMany})
	var source string
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT source_observation_id::text FROM {schema}.fact_receipt WHERE fact_id=$1`), first).Scan(&source); err != nil {
		t.Fatal(err)
	}
	for _, args := range []struct {
		source, after, actor string
		limit                int
	}{{"", "", uuid.NewString(), 0}, {"", "", uuid.NewString(), 101}, {"bad", "", uuid.NewString(), 1}, {"", "bad", uuid.NewString(), 1}, {"", "", "bad", 1}, {"", "", "", 1}} {
		if _, err := facts.RecoverFacts(ctx, schema, "p1", args.source, args.after, args.actor, args.limit); !errors.Is(err, pg.ErrInvalidRecovery) {
			t.Fatalf("invalid input: %v", err)
		}
	}
	if _, err := pool.Exec(ctx, schema.SQL(`DELETE FROM {schema}.fact; DELETE FROM {schema}.entity; ALTER TABLE {schema}.audit_entry ADD CONSTRAINT refuse_recovery CHECK(operation<>'formation.recover')`)); err != nil {
		t.Fatal(err)
	}
	if _, err := facts.RecoverFacts(ctx, schema, "p1", "", "", uuid.NewString(), 100); err == nil {
		t.Fatal("failed audit committed recovery")
	}
	if recoverySnapshot(t, pool, schema, "fact") != "[]" || recoverySnapshot(t, pool, schema, "entity") != "[]" {
		t.Fatal("failed page partially restored")
	}
	if _, err := pool.Exec(ctx, schema.SQL(`ALTER TABLE {schema}.audit_entry DROP CONSTRAINT refuse_recovery`)); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"p2", "p3"} {
		page, err := facts.RecoverFacts(ctx, schema, scope, source, "", uuid.NewString(), 1)
		if err != nil || page.Examined != 0 {
			t.Fatalf("foreign recovery: %+v %v", page, err)
		}
	}
	// Transcript tampering is detected before entities, facts, or audit commit.
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.turn_message SET content='changed' WHERE observation_id=$1`), source); err != nil {
		t.Fatal(err)
	}
	if _, err := facts.RecoverFacts(ctx, schema, "p1", source, "", uuid.NewString(), 1); !errors.Is(err, pg.ErrInvalidReceipt) {
		t.Fatalf("changed source accepted: %v", err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.turn_message SET content='Atlas works at Ensera' WHERE observation_id=$1`), source); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan pg.RecoveryPage, 16)
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, e := facts.RecoverFacts(ctx, schema, "p1", source, "", uuid.NewString(), 1)
			results <- p
			errs <- e
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	n := 0
	for p := range results {
		n += p.Restored
	}
	if n != 1 {
		t.Fatalf("concurrent retry restored %d times", n)
	}
	// A current record wins over its recovery copy, including its optimistic version.
	version := recordMutation(t, pool, schema, first).ExpectedVersion
	if _, err := facts.RecoverFacts(ctx, schema, "p1", source, "", uuid.NewString(), 1); err != nil {
		t.Fatal(err)
	}
	if recordMutation(t, pool, schema, first).ExpectedVersion != version {
		t.Fatal("recovery restamped a retained record")
	}
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.observation SET retention_until=clock_timestamp()-interval '1 second'`)); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.NewRetentionStore(pool, schema).Sweep(ctx, "p1", 10); err != nil {
		t.Fatal(err)
	}
	p, err := facts.RecoverFacts(ctx, schema, "p1", source, "", uuid.NewString(), 1)
	if err != nil || p.Examined != 0 {
		t.Fatalf("expired receipt survived: %+v %v", p, err)
	}
}
