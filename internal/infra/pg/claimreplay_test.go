// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
)

func TestConcurrentSourceClaimReplayPreservesIdentityVersionAndEvidence(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "source_claim_replay")
	obs, facts := pg.NewObservationStore(pool), pg.NewFactStore(pool)
	text := "Atlas works at Ensera"
	source, err := obs.Append(ctx, schema, turnOf(domain.Message{Role: domain.RoleUser, Content: text}))
	if err != nil {
		t.Fatal(err)
	}
	claim := domain.Claim{Subject: "Atlas", Predicate: "works_at", Object: "Ensera", Cardinality: domain.CardinalityMany, Statement: text, Quote: text, ByteEnd: len(text)}
	first, err := facts.Assert(ctx, schema, "p1", source.ID, domain.RoleUser, "subject-1", claim)
	if err != nil {
		t.Fatal(err)
	}
	version := recordMutation(t, pool, schema, first).ExpectedVersion
	known := recordedAt(t, ctx, pool, schema, first)
	var wg sync.WaitGroup
	ids := make(chan string, 16)
	errs := make(chan error, 16)
	claim.Subject = "  atlas  "
	claim.Object = "ENSERA"
	claim.Statement = "a replayed paraphrase"
	claim.ValidFrom = time.Now().UTC()
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := facts.Assert(ctx, schema, "p1", source.ID, domain.RoleUser, "subject-1", claim)
			ids <- id
			errs <- err
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for id := range ids {
		if id != first {
			t.Fatal("replay changed fact identity")
		}
	}
	if recordMutation(t, pool, schema, first).ExpectedVersion != version || !recordedAt(t, ctx, pool, schema, first).Equal(known) {
		t.Fatal("replay changed retained knowledge")
	}
	var count, evidence int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*),(SELECT count(*) FROM {schema}.fact_evidence) FROM {schema}.fact`)).Scan(&count, &evidence); err != nil || count != 1 || evidence != 1 {
		t.Fatalf("replay duplicated state: %d %d %v", count, evidence, err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`DELETE FROM {schema}.fact WHERE fact_id=$1`), first); err != nil {
		t.Fatal(err)
	}
	restored, err := facts.Assert(ctx, schema, "p1", source.ID, domain.RoleUser, "subject-1", claim)
	if err != nil || restored != first {
		t.Fatalf("recovery changed identity: %s %v", restored, err)
	}
	other, err := obs.Append(ctx, schema, turnOf(domain.Message{Role: domain.RoleUser, Content: text}))
	if err != nil {
		t.Fatal(err)
	}
	independent, err := facts.Assert(ctx, schema, "p1", other.ID, domain.RoleUser, "subject-1", claim)
	if err != nil || independent == first {
		t.Fatalf("independent observation merged: %s %v", independent, err)
	}
}
