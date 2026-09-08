// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
)

func TestConcurrentRejectedReplayRetainsOneDiagnosticAndRecoversItsIdentity(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "rejected_replay")
	obs, facts := pg.NewObservationStore(pool), pg.NewFactStore(pool)
	source, err := obs.Append(ctx, schema, turnOf(domain.Message{Role: domain.RoleUser, Content: "I work at Ensera"}))
	if err != nil {
		t.Fatal(err)
	}
	rejected := domain.RejectedClaim{Predicate: "employed_by", Statement: "I work at Ensera", Quote: "I work at Ensera", Reason: domain.ReasonUnmappedRelation}
	if err := facts.Reject(ctx, schema, "p1", source.ID, "subject-1", rejected); err != nil {
		t.Fatal(err)
	}
	var id string
	var recorded time.Time
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT rejected_claim_id::text,recorded_at FROM {schema}.rejected_claim`)).Scan(&id, &recorded); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errs := make(chan error, 16)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- facts.Reject(ctx, schema, "p1", strings.ToUpper(source.ID), "subject-1", rejected)
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var n, registered int
	var retained time.Time
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*),min(recorded_at),(SELECT count(*) FROM {schema}.projection_dependency WHERE projection_kind='rejected_claim') FROM {schema}.rejected_claim`)).Scan(&n, &retained, &registered); err != nil || n != 1 || registered != 1 || !retained.Equal(recorded) {
		t.Fatalf("replay duplicated or restamped diagnostics: %d %d %v", n, registered, err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`DELETE FROM {schema}.rejected_claim WHERE rejected_claim_id=$1`), id); err != nil {
		t.Fatal(err)
	}
	if err := facts.Reject(ctx, schema, "p1", source.ID, "subject-1", rejected); err != nil {
		t.Fatal(err)
	}
	var recovered string
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT rejected_claim_id::text FROM {schema}.rejected_claim`)).Scan(&recovered); err != nil || recovered != id {
		t.Fatalf("recovery changed identity: %v", err)
	}
	independent, err := obs.Append(ctx, schema, turnOf(domain.Message{Role: domain.RoleUser, Content: "I work at Ensera"}))
	if err != nil {
		t.Fatal(err)
	}
	if err := facts.Reject(ctx, schema, "p1", independent.ID, "subject-1", rejected); err != nil {
		t.Fatal(err)
	}
	rejected.Statement = "Another rejected interpretation"
	if err := facts.Reject(ctx, schema, "p1", source.ID, "subject-1", rejected); err != nil {
		t.Fatal(err)
	}
	exported, err := pg.NewExporter(pool, schema).Export(ctx, schema, "p1", "subject-1")
	if err != nil || len(exported.Sections["rejected_claim"]) != 3 {
		t.Fatalf("distinct diagnostics merged or lost from export: %v", err)
	}
	erased, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", "subject-1", "requested")
	if err != nil || erased.Deleted["rejected_claim"] != 3 || erased.Residual["rejected_claim"] != 0 {
		t.Fatalf("rejection erasure: %+v %v", erased, err)
	}
}

func TestRejectedReplayRefusesForeignSourcesAndRollsBackRegistrationFailure(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "rejected_replay_refusal")
	source, err := pg.NewObservationStore(pool).Append(ctx, schema, turnOf(domain.Message{Role: domain.RoleUser, Content: "I work at Ensera"}))
	if err != nil {
		t.Fatal(err)
	}
	facts := pg.NewFactStore(pool)
	rejected := domain.RejectedClaim{Predicate: "employed_by", Statement: "I work at Ensera", Quote: "I work at Ensera", Reason: domain.ReasonUnmappedRelation}
	if err := facts.Reject(ctx, schema, "p1", "invalid-source", "subject-1", rejected); err == nil {
		t.Fatal("invalid source accepted")
	}
	if err := facts.Reject(ctx, schema, "p2", source.ID, "subject-1", rejected); err == nil {
		t.Fatal("foreign source accepted")
	}
	if _, err := pool.Exec(ctx, schema.SQL(`ALTER TABLE {schema}.projection_dependency ADD CONSTRAINT fail_rejected_registration CHECK(projection_kind<>'rejected_claim')`)); err != nil {
		t.Fatal(err)
	}
	if err := facts.Reject(ctx, schema, "p1", source.ID, "subject-1", rejected); err == nil {
		t.Fatal("unregistered rejection committed")
	}
	var n int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.rejected_claim`)).Scan(&n); err != nil || n != 0 {
		t.Fatalf("failed diagnostic left text: %d %v", n, err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`ALTER TABLE {schema}.projection_dependency DROP CONSTRAINT fail_rejected_registration`)); err != nil {
		t.Fatal(err)
	}
	if err := facts.Reject(ctx, schema, "p1", source.ID, "subject-1", rejected); err != nil {
		t.Fatal(err)
	}
}
