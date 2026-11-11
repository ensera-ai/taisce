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

// peopleIn says whether the people in a test's project share one world. Through the store, as an
// operator would, so a test that sets it is also a test that it can be set.
func peopleIn(t *testing.T, ctx context.Context, pool *pgxpool.Pool, schema pg.Schema, statements pg.Statements) {
	t.Helper()
	if err := pg.NewProjectStore(pool, schema).SetStatements(ctx, "p1", statements); err != nil {
		t.Fatalf("say that p1's statements are %s: %v", statements, err)
	}
}

func accountOf(t *testing.T, ctx context.Context, pool *pgxpool.Pool, schema pg.Schema, factID string) string {
	t.Helper()
	var account string
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT account FROM {schema}.fact WHERE fact_id = $1`), factID).Scan(&account); err != nil {
		t.Fatalf("read the fact's account: %v", err)
	}
	return account
}

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

// Where people do not share a world, one person's statement does not end another person's.
//
// A named entity is one node per project, and a single-valued relation holds one current value. Both
// were keyed without the person who spoke, so when two end users of one application each mentioned
// somebody by the same name, the second person's statement closed the first person's fact. Nothing
// was refused, so nothing was counted; nothing was erased, so there was no receipt; and the citation
// on the fact that did the closing was genuine. A recall scoped to the first person then answered as
// though they had never said it.
//
// In a project whose statements are separate, each data subject has an account of their own, and a
// statement closes only what the same account held. This is also what a project is when nobody has
// said: the test does not set it.
func TestWherePeopleDoNotShareAWorldOnePersonsStatementDoesNotEndAnothers(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "wp_separate_worlds")
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
	if a, b := accountOf(t, ctx, pool, schema, alice), accountOf(t, ctx, pool, schema, bob); a != "alice" || b != "bob" {
		t.Fatalf("the facts are filed under %q and %q; each belongs to the person who said it", a, b)
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

	// A statement nobody is named as having made is an account of its own, not everybody's: it
	// neither ends a person's statement nor is ended by one.
	nobody := must(saidBy(t, ctx, pool, schema, "", "Sarah", "Lagos", time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)))
	for who, id := range map[string]string{"Alice's": aliceAgain, "Bob's": bob, "the unattributed": nobody} {
		if until := validUntil(t, ctx, pool, schema, id); until != nil {
			t.Fatalf("%s current statement has an end at %v after an unattributed one was recorded", who, until)
		}
	}
}

// Where people share a world, a later statement ends an earlier one, whoever made either.
//
// A team's workspace is one world. "Atlas is in Dublin", then from a colleague, "Atlas moved to
// Oslo": the second is news about the thing the first described, and a memory that kept both as
// current would answer a question about where Atlas is with a stale value beside the true one.
// This is the rule every project had before a project could say which it was, and a project that
// says `shared` keeps it exactly.
func TestWherePeopleShareAWorldALaterStatementEndsAnEarlierOneWhoeverMadeEither(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "wp_shared_world")
	peopleIn(t, ctx, pool, schema, pg.StatementsShared)
	jan := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	june := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)

	alice, err := saidBy(t, ctx, pool, schema, "alice", "Atlas", "Dublin", jan)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := saidBy(t, ctx, pool, schema, "bob", "Atlas", "Oslo", june)
	if err != nil {
		t.Fatal(err)
	}
	if a, b := accountOf(t, ctx, pool, schema, alice), accountOf(t, ctx, pool, schema, bob); a != "" || b != "" {
		t.Fatalf("the facts are filed under %q and %q; in a shared world there is one account", a, b)
	}
	if until := validUntil(t, ctx, pool, schema, alice); until == nil || !until.Equal(june) {
		t.Fatalf("the earlier statement ends at %v; a colleague's later one starts at %v, and it must close it", until, june)
	}
	if until := validUntil(t, ctx, pool, schema, bob); until != nil {
		t.Fatalf("the later statement has an end at %v", until)
	}
	// Whoever made them: two people at one instant is still two current values for one thing.
	if _, err := saidBy(t, ctx, pool, schema, "carol", "Atlas", "Lisbon", june); !errors.Is(err, pg.ErrConflictingValue) {
		t.Fatalf("in a shared world a second current value from a third person at the same instant was not refused: %v", err)
	}
}

// A rebuild keeps to the project's rule.
//
// Re-deriving a source's facts gives each new fact an end where the next statement in its slot
// begins. With the slot keyed without the account, rebuilding Alice's source in a project of
// separate worlds would have ended her fact at Bob's — writing back, on the path that exists to
// repair things, the loss the ordinary path does not make. And in a shared world the rebuilt fact
// must still be bounded at the colleague's later statement, or a rebuild would resurrect a value
// that had been replaced.
func TestARebuildKeepsToTheProjectsRule(t *testing.T) {
	for _, c := range []struct {
		name       string
		statements pg.Statements
		bounded    bool
	}{
		{"generation_separate", pg.StatementsSeparate, false},
		{"generation_shared", pg.StatementsShared, true},
	} {
		ctx := context.Background()
		pool := testPool(t)
		schema := tenant(t, pool, c.name)
		peopleIn(t, ctx, pool, schema, c.statements)
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
			t.Fatalf("%s: rebuild the first person's source: %v", c.statements, err)
		}
		var fresh string
		if err := pool.QueryRow(ctx, schema.SQL(
			`SELECT fact_id::text FROM {schema}.fact_generation_record WHERE generation_id=$1 AND disposition='admitted'`), g.ID).Scan(&fresh); err != nil {
			t.Fatalf("%s: the rebuild admitted nothing: %v", c.statements, err)
		}
		until := validUntil(t, ctx, pool, schema, fresh)
		switch {
		case !c.bounded && until != nil:
			t.Errorf("separate worlds: the rebuilt fact ends at %v, which is where another person's statement about the same name begins", until)
		case c.bounded && (until == nil || !until.Equal(june)):
			t.Errorf("shared world: the rebuilt fact ends at %v; a colleague's later statement begins at %v and must bound it", until, june)
		}
	}
}

// Whether a project's people share a world can be said until it holds a fact, and not after.
//
// The account is written on every fact, from this setting. A project that switched after forming
// facts would hold some filed under the shared account and some under people's own, and a person's
// new statement would no longer close what they said before the switch. So the substrate refuses
// it — by a trigger, which is what makes an UPDATE written by hand during an incident be refused as
// surely as the command is.
func TestAProjectsStatementsAreFixedOnceItHoldsAFact(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "wp_statements_fixed")
	projects := pg.NewProjectStore(pool, schema)

	// Before any fact: said, and said again.
	for _, statements := range []pg.Statements{pg.StatementsShared, pg.StatementsSeparate, pg.StatementsShared} {
		if err := projects.SetStatements(ctx, "p1", statements); err != nil {
			t.Fatalf("an empty project could not be said to be %s: %v", statements, err)
		}
	}
	listed, err := projects.List(ctx)
	if err != nil || len(listed) != 1 || listed[0].Statements != "shared" {
		t.Fatalf("the listing does not say what the project is: %+v %v", listed, err)
	}
	if err := projects.SetStatements(ctx, "p1", "everybody"); !errors.Is(err, pg.ErrInvalidStatements) {
		t.Fatalf("a value that is neither shared nor separate was not refused as such: %v", err)
	}
	if err := projects.SetStatements(ctx, "no_such_project", pg.StatementsShared); !errors.Is(err, pg.ErrNoSuchProject) {
		t.Fatalf("a project that does not exist was not reported as such: %v", err)
	}

	fact, err := saidBy(t, ctx, pool, schema, "alice", "Atlas", "Dublin", time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}

	// After one: refused by name, and saying what it already is remains harmless.
	if err := projects.SetStatements(ctx, "p1", pg.StatementsSeparate); !errors.Is(err, pg.ErrStatementsFixed) {
		t.Fatalf("a project holding a fact was switched, or refused for another reason: %v", err)
	}
	if err := projects.SetStatements(ctx, "p1", pg.StatementsShared); err != nil {
		t.Fatalf("saying what the project already is was refused: %v", err)
	}
	// By hand, around the store: the same refusal, from the same place.
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.project SET statements='separate' WHERE scope='p1'`)); err == nil ||
		!strings.Contains(err.Error(), "already holds facts") {
		t.Fatalf("an UPDATE written by hand switched a project that holds facts: %v", err)
	}
	// And a fact cannot be moved to another account, which would let an UPDATE make two current
	// values coexist where an INSERT would have been refused.
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.fact SET account='bob' WHERE fact_id=$1`), fact); err == nil ||
		!strings.Contains(err.Error(), "fixed when it is written") {
		t.Fatalf("a fact was moved to another account: %v", err)
	}
	if got := accountOf(t, ctx, pool, schema, fact); got != "" {
		t.Fatalf("the fact is filed under %q after the refused changes", got)
	}
}
