// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
)

// Expired memory stops answering when the policy says it does, not when the sweep reaches it.
// The fact carries the deadline of the turns behind it, and every surface that reads
// a fact refuses one that has passed it.
func TestAnExpiredTurnStopsAnsweringBeforeItIsSwept(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "fact_expiry")
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.project SET retention = interval '30 days' WHERE scope='p1'`)); err != nil {
		t.Fatal(err)
	}
	observations := pg.NewObservationStore(pool)
	facts := pg.NewFactStore(pool)
	stored, err := observations.Append(ctx, schema, domain.Turn{Scope: "p1", DataSubjectID: "subject-1", OccurredAt: time.Now(),
		Messages: []domain.Message{{Ordinal: 0, Role: domain.RoleUser, Content: "I work at Ensera."}}})
	if err != nil {
		t.Fatal(err)
	}
	claim := domain.Claim{Subject: "Marta", Predicate: "works_at", Object: "Ensera",
		Statement: "Marta works at Ensera.", Quote: "work at Ensera", ByteStart: 2, ByteEnd: 16,
		Confidence: 0.9, Cardinality: domain.CardinalityMany}
	factID, err := facts.Assert(ctx, schema, "p1", stored.ID, domain.RoleUser, "subject-1", claim)
	if err != nil {
		t.Fatal(err)
	}

	// The fact took the turn's deadline, which the project's policy stamped when the turn arrived.
	var factDeadline, turnDeadline *time.Time
	if err := pool.QueryRow(ctx, schema.SQL(
		`SELECT (SELECT retention_until FROM {schema}.fact WHERE scope='p1' AND fact_id=$1::uuid),
		        (SELECT retention_until FROM {schema}.observation WHERE observation_id=$2)`), factID, stored.ID).
		Scan(&factDeadline, &turnDeadline); err != nil {
		t.Fatal(err)
	}
	if factDeadline == nil || turnDeadline == nil || !factDeadline.Equal(*turnDeadline) {
		t.Fatalf("the fact did not take the turn's deadline: fact=%v turn=%v", factDeadline, turnDeadline)
	}

	var subjectEntity string
	if err := pool.QueryRow(ctx, schema.SQL(
		`SELECT subject_entity_id::text FROM {schema}.fact WHERE scope='p1' AND fact_id=$1::uuid`), factID).Scan(&subjectEntity); err != nil {
		t.Fatal(err)
	}
	recalls := pg.NewRecallStore(pool, schema)
	records := pg.NewRecordStore(pool, schema)
	citations := pg.NewCitationStore(pool, schema)
	answers := func() (int, int, bool) {
		t.Helper()
		walked, err := recalls.FactsAbout(ctx, []string{"p1"}, []string{subjectEntity}, 10, []string{string(domain.RoleUser)}, domain.AsOf{}, 1)
		if err != nil {
			t.Fatal(err)
		}
		page, err := records.List(ctx, "p1", "", nil, 10)
		if err != nil {
			t.Fatal(err)
		}
		_, err = citations.Resolve(ctx, "p1", factID, nil, 10)
		if err != nil && !errors.Is(err, pg.ErrCitationNotFound) {
			t.Fatal(err)
		}
		return len(walked), len(page.Records), err == nil
	}
	if walked, listed, cited := answers(); walked != 1 || listed != 1 || !cited {
		t.Fatalf("before the deadline: walked=%d listed=%d cited=%v", walked, listed, cited)
	}

	// The deadline passes. Nothing has swept yet.
	if _, err := pool.Exec(ctx, schema.SQL(
		`UPDATE {schema}.fact SET retention_until = now() - interval '1 minute' WHERE scope='p1';
		 UPDATE {schema}.observation SET retention_until = now() - interval '1 minute' WHERE scope='p1'`)); err != nil {
		t.Fatal(err)
	}
	if walked, listed, cited := answers(); walked != 0 || listed != 0 || cited {
		t.Fatalf("after the deadline: walked=%d listed=%d cited=%v", walked, listed, cited)
	}
	var surviving int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.fact WHERE scope='p1'`)).Scan(&surviving); err != nil {
		t.Fatal(err)
	}
	if surviving != 1 {
		t.Fatalf("the test is not about deletion: %d facts", surviving)
	}
	// History reads the fact first, so an expired one is not found rather than returning intervals
	// for a record nothing else will admit to.
	if history, err := records.History(ctx, "p1", factID, nil, 10); !errors.Is(err, pg.ErrRecordNotFound) {
		t.Fatalf("an expired fact's history still answers: %d previous (%v)", len(history.Previous), err)
	}
}

// A fact's identity includes the turn it came from, so the same words in two turns are two facts,
// each keeping the deadline of its own turn. One expiring leaves the other answering.
func TestTwoTurnsSayingTheSameThingExpireSeparately(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "fact_expiry_two")
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.project SET retention = interval '1 day' WHERE scope='p1'`)); err != nil {
		t.Fatal(err)
	}
	observations := pg.NewObservationStore(pool)
	facts := pg.NewFactStore(pool)
	claim := domain.Claim{Subject: "Marta", Predicate: "works_at", Object: "Ensera",
		Statement: "Marta works at Ensera.", Quote: "work at Ensera", ByteStart: 2, ByteEnd: 16,
		Confidence: 0.9, Cardinality: domain.CardinalityMany}
	say := func() string {
		t.Helper()
		stored, err := observations.Append(ctx, schema, domain.Turn{Scope: "p1", DataSubjectID: "subject-1", OccurredAt: time.Now(),
			Messages: []domain.Message{{Ordinal: 0, Role: domain.RoleUser, Content: "I work at Ensera."}}})
		if err != nil {
			t.Fatal(err)
		}
		id, err := facts.Assert(ctx, schema, "p1", stored.ID, domain.RoleUser, "subject-1", claim)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	first, second := say(), say()
	if first == second {
		t.Fatal("two turns produced one fact; this test is about the identity that includes the turn")
	}
	var deadlines int
	if err := pool.QueryRow(ctx, schema.SQL(
		`SELECT count(*) FROM {schema}.fact WHERE scope='p1' AND retention_until IS NOT NULL`)).Scan(&deadlines); err != nil {
		t.Fatal(err)
	}
	if deadlines != 2 {
		t.Fatalf("both facts should carry their turn's deadline, got %d", deadlines)
	}

	// One turn expires. The other's fact still answers.
	if _, err := pool.Exec(ctx, schema.SQL(
		`UPDATE {schema}.fact SET retention_until = now() - interval '1 minute' WHERE scope='p1' AND fact_id=$1::uuid`), first); err != nil {
		t.Fatal(err)
	}
	records := pg.NewRecordStore(pool, schema)
	page, err := records.List(ctx, "p1", "", nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Records) != 1 || page.Records[0].ID != second {
		t.Fatalf("expiring one fact took the other with it: %+v", page.Records)
	}
}
