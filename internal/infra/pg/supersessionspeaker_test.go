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

// saidBy stores one person's sentence about where somebody lives, and asserts the claim it makes.
func saidBy(t *testing.T, ctx context.Context, pool *pgxpool.Pool, schema pg.Schema, person, about, city string, at time.Time) (string, error) {
	t.Helper()
	content := about + " lives in " + city + "."
	stored, err := pg.NewObservationStore(pool).Append(ctx, schema, domain.Turn{
		Scope: "p1", DataSubjectID: person, OccurredAt: at,
		Messages: []domain.Message{{Ordinal: 0, Role: domain.RoleUser, Content: content, OccurredAt: at}},
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	return pg.NewFactStore(pool).Assert(ctx, schema, "p1", stored.ID, domain.RoleUser, person, domain.Claim{
		Subject: about, Predicate: "lives_in", Object: city, Statement: content, Confidence: 0.9,
		Quote: content, ByteStart: 0, ByteEnd: len(content), SourceOrdinal: 0,
		SemanticType: "identity", Cardinality: domain.CardinalityOne, ValidFrom: at,
	})
}

func validUntil(t *testing.T, ctx context.Context, pool *pgxpool.Pool, schema pg.Schema, factID string) *time.Time {
	t.Helper()
	var until *time.Time
	if err := pool.QueryRow(ctx, schema.SQL(
		`SELECT upper(valid) FROM {schema}.fact WHERE fact_id = $1`), factID).Scan(&until); err != nil {
		t.Fatalf("read the fact's validity: %v", err)
	}
	return until
}

// One person's statement does not end another person's.
//
// A named entity is one node per project, and a single-valued relation holds one current value. Both
// were keyed without the person who spoke, so when two people in a project each mentioned somebody
// by the same name, the second person's statement closed the first person's fact. Nothing was
// refused, so nothing was counted; nothing was erased, so there was no receipt; and the citation on
// the fact that did the closing was genuine. A recall scoped to the first person then answered as
// though they had never said it.
//
// Supersession is something a speaker does to their own earlier statement. Two people who say two
// different things about one name hold two current values, which is what was said.
func TestOnePersonsStatementDoesNotEndAnotherPersons(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "wp_speaker_supersession")
	jan := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	june := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	september := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)

	must := func(id string, err error) string {
		t.Helper()
		if err != nil {
			t.Fatalf("assert: %v", err)
		}
		return id
	}
	alice := must(saidBy(t, ctx, pool, schema, "alice", "Sarah", "Paris", jan))
	bob := must(saidBy(t, ctx, pool, schema, "bob", "Sarah", "Tokyo", june))

	// They are talking about one name, so it is one node. That is unchanged, and it is what made
	// the collision possible.
	var nodes int
	if err := pool.QueryRow(ctx, schema.SQL(
		`SELECT count(DISTINCT subject_entity_id) FROM {schema}.fact WHERE fact_id = ANY($1::uuid[])`),
		[]string{alice, bob}).Scan(&nodes); err != nil {
		t.Fatal(err)
	}
	if nodes != 1 {
		t.Fatalf("the two statements are about %d entities; this test is about what happens when they share one", nodes)
	}
	if until := validUntil(t, ctx, pool, schema, alice); until != nil {
		t.Fatalf("Alice's fact was given an end at %v by Bob's statement about somebody with the same name", until)
	}
	if until := validUntil(t, ctx, pool, schema, bob); until != nil {
		t.Fatalf("Bob's fact has an end at %v, and nothing has contradicted it", until)
	}

	// A person still supersedes themself: the rule has moved, it has not gone.
	aliceAgain := must(saidBy(t, ctx, pool, schema, "alice", "Sarah", "Rome", september))
	if until := validUntil(t, ctx, pool, schema, alice); until == nil || !until.Equal(september) {
		t.Fatalf("Alice's earlier statement ends at %v; her later one starts at %v, and the two must meet", until, september)
	}
	if until := validUntil(t, ctx, pool, schema, aliceAgain); until != nil {
		t.Fatalf("Alice's latest statement has an end at %v", until)
	}
	if until := validUntil(t, ctx, pool, schema, bob); until != nil {
		t.Fatalf("Bob's fact was given an end at %v when Alice changed her own mind", until)
	}

	// And the constraint still refuses one person two current values that start together: that is
	// a contradiction inside one account, not two accounts.
	if _, err := saidBy(t, ctx, pool, schema, "alice", "Sarah", "Madrid", september); !errors.Is(err, pg.ErrConflictingValue) {
		t.Fatalf("a second current value from the same person at the same instant was not refused: %v", err)
	}

	// A statement nobody is named as having made is its own account, not everybody's: it neither
	// ends a person's statement nor is ended by one.
	nobody := must(saidBy(t, ctx, pool, schema, "", "Sarah", "Lagos", time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)))
	for who, id := range map[string]string{"Alice's": aliceAgain, "Bob's": bob, "the unattributed": nobody} {
		if until := validUntil(t, ctx, pool, schema, id); until != nil {
			t.Fatalf("%s current statement has an end at %v after an unattributed one was recorded", who, until)
		}
	}
}

// A rebuild does not bound one person's fact at another person's later one.
//
// Re-deriving a source's facts gives each new fact an end where the next statement in its slot
// begins. With the slot keyed without the speaker, rebuilding Alice's source would have ended her
// fact at Bob's — writing back, on the path that exists to repair things, the same loss the ordinary
// path no longer makes.
func TestARebuildDoesNotBoundOnePersonsFactAtAnotherPersons(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "generation_speaker")
	facts := pg.NewFactStore(pool)
	jan := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	june := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)

	alice, err := saidBy(t, ctx, pool, schema, "alice", "Sarah", "Paris", jan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := saidBy(t, ctx, pool, schema, "bob", "Sarah", "Tokyo", june); err != nil {
		t.Fatal(err)
	}
	var source string
	if err := pool.QueryRow(ctx, schema.SQL(
		`SELECT source_observation_id::text FROM {schema}.fact_receipt WHERE fact_id=$1`), alice).Scan(&source); err != nil {
		t.Fatal(err)
	}
	snapshot, err := facts.ReadGenerationSource(ctx, schema, "p1", source)
	if err != nil {
		t.Fatal(err)
	}
	content := "Sarah lives in Paris."
	g, err := facts.ApplyGeneration(ctx, schema, snapshot, uuid.NewString(), "extract/v2:"+strings.Repeat("c", 64), uuid.NewString(),
		pg.GenerationPlan{Claims: []domain.Claim{{
			Subject: "Sarah", Predicate: "lives_in", Object: "Paris", Statement: content,
			Quote: content, ByteEnd: len(content), Cardinality: domain.CardinalityOne, ValidFrom: jan,
		}}})
	if err != nil {
		t.Fatalf("rebuild Alice's source: %v", err)
	}
	var fresh string
	if err := pool.QueryRow(ctx, schema.SQL(
		`SELECT fact_id::text FROM {schema}.fact_generation_record WHERE generation_id=$1 AND disposition='admitted'`), g.ID).Scan(&fresh); err != nil {
		t.Fatalf("the rebuild admitted nothing: %v", err)
	}
	if until := validUntil(t, ctx, pool, schema, fresh); until != nil {
		t.Fatalf("the rebuilt fact ends at %v, which is where Bob's statement about the same name begins", until)
	}
}
