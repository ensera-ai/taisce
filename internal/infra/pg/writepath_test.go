package pg_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
)

// A database is required. These tests assert behaviour that lives in DDL and in transactions —
// constraints, upsert races, offset contiguity — and a fake would assert only that the fake was
// written to match the assertion.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TAISCE_TEST_DSN")
	if dsn == "" {
		t.Skip("TAISCE_TEST_DSN is not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// Each test gets its own tenant, so tests cannot see each other's data — which is also the property
// the schema boundary is for.
func tenant(t *testing.T, pool *pgxpool.Pool, name string) pg.Schema {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `DROP SCHEMA IF EXISTS `+name+` CASCADE`); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if err := migrate.ProvisionTenant(ctx, pool, name); err != nil {
		t.Fatalf("provision tenant: %v", err)
	}
	if err := migrate.ProvisionScope(ctx, pool, name, "p1"); err != nil {
		t.Fatalf("provision scope: %v", err)
	}
	schema, err := pg.NewSchema(name)
	if err != nil {
		t.Fatalf("schema: %v", err)
	}
	return schema
}

func turnOf(msgs ...domain.Message) domain.Turn {
	return domain.Turn{
		Scope:         "p1",
		DataSubjectID: "subject-1",
		OccurredAt:    time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC),
		Messages:      msgs,
	}
}

// ── #2's first claim ──────────────────────────────────────────────────────────────────────────
//
// A two-message turn where the assistant's sentence produces no user fact. This is the role policy,
// and it is the assertion the whole write path exists to make possible.
func TestAnAssistantsSentenceDoesNotBecomeTheUsersFact(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "wp_role")
	obs := pg.NewObservationStore(pool)
	facts := pg.NewFactStore(pool)

	stored, err := obs.Append(ctx, schema, turnOf(
		domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "I moved to Dublin last month."},
		domain.Message{Ordinal: 1, Role: domain.RoleAssistant, Content: "You must love the Dublin food scene."},
	))
	if err != nil {
		t.Fatalf("append: %v", err)
	}

	// The user's own claim is accepted.
	if _, err := facts.Assert(ctx, schema, "p1", stored.ID, domain.RoleUser, "subject-1", domain.Claim{
		Subject: "user", Predicate: "lives_in", Cardinality: domain.CardinalityOne, Object: "Dublin",
		Statement: "The user lives in Dublin.", Confidence: 0.9,
		Quote: "I moved to Dublin", ByteStart: 0, ByteEnd: 17, SourceOrdinal: 0,
	}); err != nil {
		t.Fatalf("the user's own claim must be accepted: %v", err)
	}

	// The assistant's inference about the same person is not.
	_, err = facts.Assert(ctx, schema, "p1", stored.ID, domain.RoleAssistant, "subject-1", domain.Claim{
		Subject: "user", Predicate: "prefers", Cardinality: domain.CardinalityMany, Object: "Dublin food scene",
		Statement: "The user likes the Dublin food scene.", Confidence: 0.8,
		Quote: "You must love the Dublin food scene.", ByteStart: 0, ByteEnd: 36, SourceOrdinal: 1,
	})
	if !errors.Is(err, pg.ErrNotSpokenByPrincipal) {
		t.Fatalf("an assistant's sentence must not become the user's fact; got %v", err)
	}

	var n int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.fact`)).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected exactly the user's fact, found %d", n)
	}

	// And the refusal did not half-write: no orphan entity from the rejected claim.
	var likes int
	if err := pool.QueryRow(ctx,
		schema.SQL(`SELECT count(*) FROM {schema}.entity WHERE normalized_name = 'dublin food scene'`)).Scan(&likes); err != nil {
		t.Fatalf("count entities: %v", err)
	}
	if likes != 0 {
		t.Fatalf("a refused claim left %d entities behind", likes)
	}
}

// ── #2's second claim ─────────────────────────────────────────────────────────────────────────
//
// One entity when the same name arrives under two types. With the type in the identity key this is
// two nodes holding two halves of what is known, and both rows look correct on their own.
func TestTheSameNameUnderTwoTypesIsOneEntity(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "wp_entity")
	obs := pg.NewObservationStore(pool)
	facts := pg.NewFactStore(pool)

	stored, err := obs.Append(ctx, schema, turnOf(
		domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "The Dublin Office is on Grand Canal. dublin office is part of Ensera."},
	))
	if err != nil {
		t.Fatalf("append: %v", err)
	}

	for _, c := range []domain.Claim{
		{Subject: "Dublin Office", Predicate: "located_in", Cardinality: domain.CardinalityOne, Object: "Grand Canal",
			Statement: "The Dublin Office is on Grand Canal.", SubjectType: "place",
			Quote: "The Dublin Office is on Grand Canal", ByteStart: 0, ByteEnd: 35},
		{Subject: "dublin office", Predicate: "part_of", Cardinality: domain.CardinalityMany, Object: "Ensera",
			Statement: "The Dublin office is part of Ensera.", SubjectType: "thing",
			Quote: "dublin office is part of Ensera", ByteStart: 37, ByteEnd: 68},
	} {
		if _, err := facts.Assert(ctx, schema, "p1", stored.ID, domain.RoleUser, "", c); err != nil {
			t.Fatalf("assert %q: %v", c.Predicate, err)
		}
	}

	var n int
	var canonical, kind string
	if err := pool.QueryRow(ctx, schema.SQL(
		`SELECT count(*), min(canonical_name), min(entity_type) FROM {schema}.entity
		  WHERE normalized_name = 'dublin office'`)).Scan(&n, &canonical, &kind); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("the same name under two types must be one entity, found %d", n)
	}
	// First seen wins for both, so a later mention does not rewrite what a person reads.
	if canonical != "Dublin Office" || kind != "place" {
		t.Fatalf("canonical name and type should be as first seen, got %q / %q", canonical, kind)
	}
	// Both facts still point at it.
	var facts2 int
	if err := pool.QueryRow(ctx, schema.SQL(
		`SELECT count(*) FROM {schema}.fact f
		   JOIN {schema}.entity e ON e.entity_id = f.subject_entity_id
		  WHERE e.normalized_name = 'dublin office'`)).Scan(&facts2); err != nil {
		t.Fatalf("count facts: %v", err)
	}
	if facts2 != 2 {
		t.Fatalf("both facts should hang off the one entity, found %d", facts2)
	}
}

// ── #2's third claim ──────────────────────────────────────────────────────────────────────────
//
// Evidence spans index into ONE MESSAGE, not the turn. This is what makes a citation resolvable:
// taking the span from the message content must give back the quote.
func TestAnEvidenceSpanResolvesAgainstItsOwnMessage(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "wp_evidence")
	obs := pg.NewObservationStore(pool)
	facts := pg.NewFactStore(pool)

	// The second message is the one with the claim, so a span mistakenly taken against the whole
	// turn would land in the first message's text and look plausible.
	stored, err := obs.Append(ctx, schema, turnOf(
		domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "Some earlier chatter about nothing at all."},
		domain.Message{Ordinal: 1, Role: domain.RoleUser, Content: "My sister Marta works at Ensera."},
	))
	if err != nil {
		t.Fatalf("append: %v", err)
	}

	content := "My sister Marta works at Ensera."
	quote := "Marta works at Ensera"
	start, end := 10, 31
	if content[start:end] != quote {
		t.Fatalf("test fixture is wrong: %q != %q", content[start:end], quote)
	}

	if _, err := facts.Assert(ctx, schema, "p1", stored.ID, domain.RoleUser, "", domain.Claim{
		Subject: "Marta", Predicate: "works_at", Cardinality: domain.CardinalityMany, Object: "Ensera",
		Statement: "Marta works at Ensera.", Quote: quote,
		ByteStart: start, ByteEnd: end, SourceOrdinal: 1,
	}); err != nil {
		t.Fatalf("assert: %v", err)
	}

	// Resolve the citation the way a caller would: find the chunk for the message, take the span.
	var resolved, storedQuote string
	if err := pool.QueryRow(ctx, schema.SQL(`
		SELECT substring(c.text FROM e.byte_start + 1 FOR e.byte_end - e.byte_start), e.quote
		  FROM {schema}.fact_evidence e
		  JOIN {schema}.chunk c
		    ON c.source_observation_id = e.source_observation_id
		   AND c.source_message_ordinal = e.source_ordinal
		 LIMIT 1`)).Scan(&resolved, &storedQuote); err != nil {
		t.Fatalf("resolve citation: %v", err)
	}
	if resolved != storedQuote {
		t.Fatalf("the span resolved to %q but the quote is %q — evidence is indexing the wrong string",
			resolved, storedQuote)
	}
}

// ── #2's fourth claim ─────────────────────────────────────────────────────────────────────────
//
// Everything written registers, so erasure can prove residual zero without knowing what the write
// path produced. A projection that does not register is invisible to erasure, and invisible is
// exactly how a residual count stops meaning what it says.
func TestEverythingWrittenRegistersForErasure(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "wp_register")
	obs := pg.NewObservationStore(pool)
	facts := pg.NewFactStore(pool)

	stored, err := obs.Append(ctx, schema, turnOf(
		domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "Marta works at Ensera."},
	))
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if _, err := facts.Assert(ctx, schema, "p1", stored.ID, domain.RoleUser, "subject-1", domain.Claim{
		Subject: "Marta", Predicate: "works_at", Cardinality: domain.CardinalityMany, Object: "Ensera",
		Statement: "Marta works at Ensera.", Quote: "Marta works at Ensera",
		ByteStart: 0, ByteEnd: 21,
	}); err != nil {
		t.Fatalf("assert: %v", err)
	}

	// Every row of every projected kind must have a registration. Written as a comparison of counts
	// per kind so a new kind that forgets to register fails here rather than in an erasure test
	// somebody runs later.
	for _, k := range []struct{ kind, table, idCol string }{
		{pg.ProjectionChunk, "chunk", "chunk_id"},
		{pg.ProjectionFact, "fact", "fact_id"},
		{pg.ProjectionEntity, "entity", "entity_id"},
	} {
		var rows, registered int
		if err := pool.QueryRow(ctx, schema.SQL(
			`SELECT (SELECT count(*) FROM {schema}.`+k.table+`),
			        (SELECT count(*) FROM {schema}.projection_dependency WHERE projection_kind = $1)`),
			k.kind).Scan(&rows, &registered); err != nil {
			t.Fatalf("count %s: %v", k.kind, err)
		}
		if rows == 0 {
			t.Fatalf("%s wrote nothing, so this test asserts nothing", k.kind)
		}
		if registered != rows {
			t.Fatalf("%s: %d rows but %d registrations — the difference is invisible to erasure",
				k.kind, rows, registered)
		}
	}
}

// ── #2's fifth claim ──────────────────────────────────────────────────────────────────────────
//
// The caller's order is authoritative and groups survive. Messages arriving out of order round-trip
// in order, with their atomic units intact — because a compaction that cuts through a tool-call
// group produces a message list the model rejects outright.
func TestATurnRoundTripsInOrderWithItsGroupsIntact(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "wp_order")
	obs := pg.NewObservationStore(pool)

	// Deliberately shuffled, and group 1 is an assistant tool-call with two results answering it.
	stored, err := obs.Append(ctx, schema, turnOf(
		domain.Message{Ordinal: 3, Role: domain.RoleAssistant, Content: "It is 12C in Dublin and 9C in Oslo.", GroupOrdinal: 2},
		domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "What is the weather?", GroupOrdinal: 0},
		domain.Message{Ordinal: 2, Role: domain.RoleTool, Content: "Oslo: 9C", GroupOrdinal: 1},
		domain.Message{Ordinal: 1, Role: domain.RoleAssistant, Content: "calling get_weather", GroupOrdinal: 1},
	))
	if err != nil {
		t.Fatalf("append: %v", err)
	}

	got, err := obs.Messages(ctx, schema, stored.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(got))
	}
	for i, m := range got {
		if m.Ordinal != i {
			t.Fatalf("message %d came back at ordinal %d — the caller's order was not preserved", i, m.Ordinal)
		}
	}
	if got[0].Content != "What is the weather?" || got[3].Role != domain.RoleAssistant {
		t.Fatalf("round trip changed the turn: %+v", got)
	}
	// The atomic unit: the tool call and its result share a group, so nothing can summarise one
	// without the other.
	if got[1].GroupOrdinal != got[2].GroupOrdinal {
		t.Fatalf("a tool call and its result must share a group; got %d and %d",
			got[1].GroupOrdinal, got[2].GroupOrdinal)
	}
	if got[0].GroupOrdinal == got[1].GroupOrdinal {
		t.Fatalf("the user turn and the tool-call unit must be separable")
	}
}

// The offset is contiguous per scope, which is the whole reason the watermark can mean "everything
// below this is present". A gap stalls it permanently.
func TestTheLogOffsetIsContiguousWithinAScope(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "wp_offset")
	if err := migrate.ProvisionScope(ctx, pool, "wp_offset", "p2"); err != nil {
		t.Fatalf("provision p2: %v", err)
	}
	obs := pg.NewObservationStore(pool)

	for i := 0; i < 5; i++ {
		for _, scope := range []string{"p1", "p2"} {
			turn := turnOf(domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "message"})
			turn.Scope = scope
			stored, err := obs.Append(ctx, schema, turn)
			if err != nil {
				t.Fatalf("append %s #%d: %v", scope, i, err)
			}
			// Each scope counts from zero on its own, so a busy project cannot make a quiet one's
			// watermark look stale.
			if stored.LogOffset != int64(i) {
				t.Fatalf("%s append %d got offset %d", scope, i, stored.LogOffset)
			}
		}
	}

	var gaps int
	if err := pool.QueryRow(ctx, schema.SQL(`
		SELECT count(*) FROM (
			SELECT log_offset - row_number() OVER (PARTITION BY scope ORDER BY log_offset) AS drift
			  FROM {schema}.observation
		) d WHERE drift <> -1`)).Scan(&gaps); err != nil {
		t.Fatalf("check contiguity: %v", err)
	}
	if gaps != 0 {
		t.Fatalf("%d offsets are not contiguous within their scope", gaps)
	}
}

// A refused turn writes nothing at all — including no offset, since a claimed-and-abandoned offset
// is exactly the permanent gap the watermark cannot survive.
func TestARefusedTurnLeavesNothingBehind(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "wp_refuse")
	obs := pg.NewObservationStore(pool)

	if _, err := obs.Append(ctx, schema, turnOf(
		domain.Message{Ordinal: 0, Role: "developer", Content: "hello"},
	)); err == nil {
		t.Fatal("a turn with an unknown role must be refused")
	}

	var observations, watermarks int
	if err := pool.QueryRow(ctx, schema.SQL(
		`SELECT (SELECT count(*) FROM {schema}.observation),
		        (SELECT count(*) FROM {schema}.watermark)`)).Scan(&observations, &watermarks); err != nil {
		t.Fatalf("count: %v", err)
	}
	if observations != 0 || watermarks != 0 {
		t.Fatalf("a refused turn left %d observations and %d watermark rows", observations, watermarks)
	}
}

// ── #5's claim, on the write path ─────────────────────────────────────────────────────────────
//
// An unmapped relation is refused, and the refusal leaves nothing behind.
//
// This matters beyond the constraint itself: `Assert` resolves both entity ends BEFORE it writes
// the fact, so a predicate rejected at the last statement would otherwise leave two entities and
// two projection registrations for a fact that does not exist. Erasure would then count residual
// against rows nothing points at.
func TestAnUnmappedRelationIsRefusedAndLeavesNothingBehind(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "wp_ontology")
	obs := pg.NewObservationStore(pool)
	facts := pg.NewFactStore(pool)

	stored, err := obs.Append(ctx, schema, turnOf(
		domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "Marta is employed by Ensera."},
	))
	if err != nil {
		t.Fatalf("append: %v", err)
	}

	// A synonym of an admitted relation, which is the realistic failure: not a typo, but a second
	// spelling that no traversal asking for `works_at` would ever match.
	if _, err := facts.Assert(ctx, schema, "p1", stored.ID, domain.RoleUser, "", domain.Claim{
		Subject: "Marta", Predicate: "employed_by", Cardinality: domain.CardinalityMany, Object: "Ensera",
		Statement: "Marta is employed by Ensera.", Quote: "Marta is employed by Ensera",
		ByteStart: 0, ByteEnd: 27,
	}); err == nil {
		t.Fatal("a relation outside the vocabulary was asserted")
	}

	var entities, facts2, registrations int
	if err := pool.QueryRow(ctx, schema.SQL(`
		SELECT (SELECT count(*) FROM {schema}.entity),
		       (SELECT count(*) FROM {schema}.fact),
		       (SELECT count(*) FROM {schema}.projection_dependency
		         WHERE projection_kind IN ('entity', 'fact'))`)).Scan(&entities, &facts2, &registrations); err != nil {
		t.Fatalf("count: %v", err)
	}
	if entities != 0 || facts2 != 0 || registrations != 0 {
		t.Fatalf("a refused relation left %d entities, %d facts and %d registrations behind",
			entities, facts2, registrations)
	}
}

// ── #42's claim, on the write path ────────────────────────────────────────────────────────────
//
// What extraction refused is kept, with the model's own wording, and it registers for erasure.
//
// The registration is the part that matters. A rejected claim holds a verbatim quote from somebody's
// message, so a table of "things we did not store" that is invisible to erasure is the worst version
// of this feature — it would survive a deletion the residual count reported as complete.
func TestWhatExtractionRefusedIsKeptAndIsErasable(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "wp_rejected")
	obs := pg.NewObservationStore(pool)
	facts := pg.NewFactStore(pool)

	stored, err := obs.Append(ctx, schema, turnOf(
		domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "I am employed by Ensera and I love it here."},
	))
	if err != nil {
		t.Fatalf("append: %v", err)
	}

	for _, r := range []domain.RejectedClaim{
		{Predicate: "employed_by", Statement: "The user is employed by Ensera.",
			Quote: "I am employed by Ensera", Reason: domain.ReasonUnmappedRelation},
		{Predicate: "prefers", Statement: "The user likes working at Ensera.",
			Quote: "I really enjoy working here", Reason: domain.ReasonUnlocatableQuote},
	} {
		if err := facts.Reject(ctx, schema, "p1", stored.ID, "subject-1", r); err != nil {
			t.Fatalf("reject %q: %v", r.Reason, err)
		}
	}

	// The words survive, unnormalised. The near-duplicate relations that would justify adding a
	// predicate are visible only in exactly what was proposed.
	var predicate, quote string
	if err := pool.QueryRow(ctx, schema.SQL(
		`SELECT predicate, quote FROM {schema}.rejected_claim WHERE reason = 'unmapped_relation'`)).
		Scan(&predicate, &quote); err != nil {
		t.Fatalf("read rejection: %v", err)
	}
	if predicate != "employed_by" || quote != "I am employed by Ensera" {
		t.Fatalf("the rejection lost what the model said: %q / %q", predicate, quote)
	}

	// The refused relation is in a column with no foreign key, which is the whole point: this table
	// exists to hold the relations the vocabulary will not admit.
	var admitted int
	if err := pool.QueryRow(ctx, schema.SQL(
		`SELECT count(*) FROM {schema}.predicate WHERE predicate = 'employed_by'`)).Scan(&admitted); err != nil {
		t.Fatalf("check vocabulary: %v", err)
	}
	if admitted != 0 {
		t.Fatal("the fixture is wrong: employed_by is in the vocabulary, so nothing was refused")
	}

	// And every row registered, so erasure reaches them without knowing this table exists.
	var rows, registered int
	if err := pool.QueryRow(ctx, schema.SQL(
		`SELECT (SELECT count(*) FROM {schema}.rejected_claim),
		        (SELECT count(*) FROM {schema}.projection_dependency WHERE projection_kind = $1)`),
		pg.ProjectionRejectedClaim).Scan(&rows, &registered); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 2 || registered != rows {
		t.Fatalf("%d rejections but %d registrations — the difference is invisible to erasure", rows, registered)
	}
}

// The vocabulary an extractor is given is the one the database enforces.
//
// A Go list beside the table would be a second definition of a closed set, and the one that drifts
// is always the one no constraint enforces — at which point the extractor is told a vocabulary the
// database will refuse, and the failure surfaces as a foreign key violation on a claim that looked
// fine.
func TestTheVocabularyIsReadFromTheTableThatEnforcesIt(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "wp_vocab")

	vocabulary, err := pg.LoadOntology(ctx, pool, schema)
	if err != nil {
		t.Fatalf("load vocabulary: %v", err)
	}

	var rows int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.predicate`)).Scan(&rows); err != nil {
		t.Fatalf("count: %v", err)
	}
	if vocabulary.Len() != rows {
		t.Fatalf("the vocabulary has %d relations, the table has %d", vocabulary.Len(), rows)
	}

	// Every relation carries the two things declaring it was for: what its object is, and how many
	// of it may be true at once. A zero-valued cardinality reaching supersession would silently mean
	// "many" and stop a contradiction from ever being closed.
	for _, p := range vocabulary.All() {
		if p.Cardinality != domain.CardinalityOne && p.Cardinality != domain.CardinalityMany {
			t.Fatalf("relation %q has cardinality %q", p.Name, p.Cardinality)
		}
		if p.ObjectKind == "" || p.SemanticType == "" || p.Description == "" {
			t.Fatalf("relation %q is incompletely declared: %+v", p.Name, p)
		}
	}

	// A relation the table admits resolves; one it refuses does not.
	if _, ok := vocabulary.Lookup("works_at"); !ok {
		t.Fatal("works_at is in the table and must resolve")
	}
	if _, ok := vocabulary.Lookup("employed_by"); ok {
		t.Fatal("employed_by is not in the table and must not resolve")
	}

	// Stable order, so two extractions of one message are given the same prompt. A prompt whose
	// vocabulary arrives in a different order is a different prompt, and extraction quality cannot
	// be compared across runs that were not asked the same question.
	again, err := pg.LoadOntology(ctx, pool, schema)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	for i, p := range vocabulary.All() {
		if again.All()[i].Name != p.Name {
			t.Fatalf("the vocabulary came back in a different order at %d: %q then %q",
				i, p.Name, again.All()[i].Name)
		}
	}
}

// ── #46's claim ───────────────────────────────────────────────────────────────────────────────
//
// Evidence names the message it indexes, and the citation is resolved by that name.
//
// The fixture is built so a join that ignores the ordinal returns the WRONG WORDS rather than the
// same ones by luck: two messages share a prefix and diverge inside the span. That is the realistic
// shape — a person restating themselves with one detail changed — and it is the case where taking a
// byte offset against the wrong message of the same turn produces a plausible fragment instead of an
// error.
func TestACitationResolvesAgainstTheMessageItsEvidenceNames(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "wp_ordinal")
	obs := pg.NewObservationStore(pool)
	facts := pg.NewFactStore(pool)

	first := "Marta works at Ensera in Dublin."
	second := "Marta works at Ensera in Cork."
	stored, err := obs.Append(ctx, schema, turnOf(
		domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: first},
		domain.Message{Ordinal: 1, Role: domain.RoleUser, Content: second},
	))
	if err != nil {
		t.Fatalf("append: %v", err)
	}

	quote, start, end := "Ensera in Cork", 15, 29
	if second[start:end] != quote {
		t.Fatalf("fixture is wrong: %q", second[start:end])
	}
	// The same offsets in the other message give different words, which is what makes this test
	// able to fail.
	if first[start:end] == quote {
		t.Fatal("fixture is wrong: both messages give the same text at these offsets, so an ordinal-blind join would pass")
	}

	if _, err := facts.Assert(ctx, schema, "p1", stored.ID, domain.RoleUser, "", domain.Claim{
		Subject: "Marta", Predicate: "works_at", Cardinality: domain.CardinalityMany, Object: "Ensera",
		Statement: "Marta works at Ensera in Cork.", Quote: quote,
		ByteStart: start, ByteEnd: end, SourceOrdinal: 1,
	}); err != nil {
		t.Fatalf("assert: %v", err)
	}

	var resolved, storedQuote string
	var ordinal int
	if err := pool.QueryRow(ctx, schema.SQL(`
		SELECT e.source_ordinal, e.quote,
		       substring(c.text FROM e.byte_start + 1 FOR e.byte_end - e.byte_start)
		  FROM {schema}.fact_evidence e
		  JOIN {schema}.chunk c
		    ON c.source_observation_id = e.source_observation_id
		   AND c.source_message_ordinal = e.source_ordinal`)).Scan(&ordinal, &storedQuote, &resolved); err != nil {
		t.Fatalf("resolve citation: %v", err)
	}
	if ordinal != 1 {
		t.Fatalf("evidence names message %d; the claim came from message 1", ordinal)
	}
	if resolved != storedQuote {
		t.Fatalf("the span resolved to %q but the quote is %q", resolved, storedQuote)
	}
}

// ── #74 ───────────────────────────────────────────────────────────────────────────────────────
//
// The reasons a claim can be refused are closed by the schema, and the closed set is the one the Go
// constants name.
//
// Two definitions of a closed set is two places for it to drift, and the one that drifts is whichever
// is not enforced. Here the constraint is enforced, so a constant the schema has never heard of
// surfaces as a failed insert at the moment a refusal is recorded — which is inside the transaction
// that was forming a turn.
func TestEveryRefusalReasonTheCodeNamesIsOneTheSchemaAdmits(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "wp_reasons")
	obs := pg.NewObservationStore(pool)
	facts := pg.NewFactStore(pool)

	stored, err := obs.Append(ctx, schema, turnOf(
		domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "I work at Ensera."},
	))
	if err != nil {
		t.Fatalf("append: %v", err)
	}

	for _, reason := range []string{
		domain.ReasonUnmappedRelation,
		domain.ReasonUnlocatableQuote,
		domain.ReasonDuplicateClaim,
	} {
		if err := facts.Reject(ctx, schema, "p1", stored.ID, "subject-1", domain.RejectedClaim{
			Predicate: "works_at", Statement: "The user works at Ensera.",
			Quote: "I work at Ensera", Reason: reason,
		}); err != nil {
			t.Fatalf("the schema refused %q, which the code will write: %v", reason, err)
		}
	}

	// And the other direction. Without this the constraint could have been dropped rather than
	// widened, and every test above would still pass.
	if err := facts.Reject(ctx, schema, "p1", stored.ID, "subject-1", domain.RejectedClaim{
		Predicate: "works_at", Statement: "The user works at Ensera.",
		Quote: "I work at Ensera", Reason: "because_i_said_so",
	}); err == nil {
		t.Fatal("a reason no reader knows about was accepted; the set is not closed")
	}
}

// ── #9: a fact that stops being true ──────────────────────────────────────────────────────────
//
// Eleven of the thirty-nine relations hold one value at a time. Asserting a new one closes the old
// one rather than sitting beside it, and the database refuses the overlapping state outright — so
// the invariant survives a write path written by somebody who never read this test.
func TestASingleCardinalityFactSupersedesRatherThanAccumulates(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "wp_supersede")
	obs := pg.NewObservationStore(pool)
	facts := pg.NewFactStore(pool)

	stored, err := obs.Append(ctx, schema, turnOf(
		domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "I live in Dublin. Now I live in Amman."},
	))
	if err != nil {
		t.Fatalf("append: %v", err)
	}

	first := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	second := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)

	assert := func(object string, at time.Time) string {
		t.Helper()
		id, err := facts.Assert(ctx, schema, "p1", stored.ID, domain.RoleUser, "subject-1", domain.Claim{
			Subject: "the user", Predicate: "lives_in", Object: object,
			Statement: "The user lives in " + object + ".", Confidence: 0.9,
			Quote: "I live in " + object, ByteStart: 0, ByteEnd: 1, SourceOrdinal: 0,
			SemanticType: "identity", Cardinality: domain.CardinalityOne, ValidFrom: at,
		})
		if err != nil {
			t.Fatalf("assert %s: %v", object, err)
		}
		return id
	}

	older := assert("Dublin", first)
	newer := assert("Amman", second)

	// The old fact is still there — the history is the product — and it now has an end.
	var upperInf bool
	var upper *time.Time
	if err := pool.QueryRow(ctx, schema.SQL(
		`SELECT upper_inf(valid), upper(valid) FROM {schema}.fact WHERE fact_id = $1`), older).
		Scan(&upperInf, &upper); err != nil {
		t.Fatalf("read the superseded fact: %v", err)
	}
	if upperInf {
		t.Fatal("the earlier fact is still current, so the subject lives in two places")
	}
	// The intervals meet exactly, so a question about any instant has one answer rather than none.
	if upper == nil || !upper.Equal(second) {
		t.Fatalf("the earlier fact ends at %v and the later one starts at %v — a gap or an overlap",
			upper, second)
	}

	var currentObjects []string
	rows, err := pool.Query(ctx, schema.SQL(
		`SELECT e.canonical_name FROM {schema}.fact f
		   JOIN {schema}.entity e ON e.entity_id = f.object_entity_id
		  WHERE f.predicate = 'lives_in' AND upper_inf(f.valid)`))
	if err != nil {
		t.Fatalf("read current facts: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		currentObjects = append(currentObjects, name)
	}
	if len(currentObjects) != 1 || currentObjects[0] != "Amman" {
		t.Fatalf("current lives_in facts are %v, wanted exactly Amman", currentObjects)
	}
	_ = newer
}

// A many-cardinality relation accumulates, and must not be superseded.
//
// Getting this backwards would be worse than the defect being fixed: working at two organisations is
// ordinary, and closing the first would delete a true fact every time a second arrived.
func TestAManyCardinalityFactAccumulatesRatherThanSuperseding(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "wp_accumulate")
	obs := pg.NewObservationStore(pool)
	facts := pg.NewFactStore(pool)

	stored, err := obs.Append(ctx, schema, turnOf(
		domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "I work at Ensera and at Acme."},
	))
	if err != nil {
		t.Fatalf("append: %v", err)
	}

	for _, org := range []string{"Ensera", "Acme"} {
		if _, err := facts.Assert(ctx, schema, "p1", stored.ID, domain.RoleUser, "subject-1", domain.Claim{
			Subject: "the user", Predicate: "works_at", Object: org,
			Statement: "The user works at " + org + ".", Confidence: 0.9,
			Quote: "I work at", ByteStart: 0, ByteEnd: 1, SourceOrdinal: 0,
			SemanticType: "identity", Cardinality: domain.CardinalityMany,
			ValidFrom: time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC),
		}); err != nil {
			t.Fatalf("assert %s: %v", org, err)
		}
	}

	var current int
	if err := pool.QueryRow(ctx, schema.SQL(
		`SELECT count(*) FROM {schema}.fact WHERE predicate = 'works_at' AND upper_inf(valid)`)).
		Scan(&current); err != nil {
		t.Fatalf("count: %v", err)
	}
	if current != 2 {
		t.Fatalf("%d current works_at facts; a many-cardinality relation was superseded", current)
	}
}

// The invariant is in the substrate, so a write that goes around the application cannot break it.
//
// This is the test that says the rule MOVED. It writes the overlapping state directly, with no Go in
// the path at all, and the database refuses it.
func TestTheDatabaseRefusesTwoOverlappingSingleCardinalityFacts(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "wp_excl")
	obs := pg.NewObservationStore(pool)
	facts := pg.NewFactStore(pool)

	stored, err := obs.Append(ctx, schema, turnOf(
		domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "I live in Dublin."},
	))
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	factID, err := facts.Assert(ctx, schema, "p1", stored.ID, domain.RoleUser, "subject-1", domain.Claim{
		Subject: "the user", Predicate: "lives_in", Object: "Dublin",
		Statement: "The user lives in Dublin.", Confidence: 0.9,
		Quote: "I live in Dublin", ByteStart: 0, ByteEnd: 16, SourceOrdinal: 0,
		SemanticType: "identity", Cardinality: domain.CardinalityOne,
		ValidFrom: time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("assert: %v", err)
	}

	var subject string
	if err := pool.QueryRow(ctx, schema.SQL(
		`SELECT subject_entity_id::text FROM {schema}.fact WHERE fact_id = $1`), factID).
		Scan(&subject); err != nil {
		t.Fatalf("read subject: %v", err)
	}

	// A second open interval for the same subject and relation, inserted directly.
	_, err = pool.Exec(ctx, schema.SQL(
		`INSERT INTO {schema}.fact
		     (fact_id, scope, subject_entity_id, predicate, statement, valid, confidence, cardinality)
		 VALUES (gen_random_uuid(), 'p1', $1::uuid, 'lives_in', 'A second current residence.',
		         tstzrange(now(), NULL), 0.9, 'one')`), subject)
	if err == nil {
		t.Fatal("the database stored two overlapping current facts for a single-cardinality relation")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "fact_single_cardinality_excl") {
		t.Fatalf("refused for the wrong reason, which may not survive a schema change: %v", err)
	}
}

// The copy of cardinality cannot disagree with the vocabulary, because a composite foreign key makes
// the disagreeing row unstorable. Without this the denormalisation would be a second definition of a
// closed set, which is what this schema refuses everywhere else.
func TestAFactCannotClaimACardinalityTheVocabularyDoesNotGiveIt(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "wp_cardfk")

	_, err := pool.Exec(ctx, schema.SQL(
		`INSERT INTO {schema}.fact
		     (fact_id, scope, predicate, statement, valid, confidence, cardinality)
		 VALUES (gen_random_uuid(), 'p1', 'lives_in', 'A relation with the wrong cardinality.',
		         tstzrange(now(), NULL), 0.9, 'many')`))
	if err == nil {
		t.Fatal("a fact claimed a cardinality its predicate does not have")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "fact_cardinality_matches_vocabulary") {
		t.Fatalf("refused for the wrong reason: %v", err)
	}
}

// The invariant holds under concurrency with no application lock in the path.
//
// This is the test that says the rule moved. Several workers assert conflicting single-cardinality
// facts for one subject at the same time, through the ordinary write path, with nothing coordinating
// them. Some transactions fail — that is the constraint doing its work — and what must be true
// afterwards is that the subject has exactly one current value.
//
// The old answer was a lock per slot on the write path. It would also produce this outcome, and it
// would produce it only for as long as every future write path remembered to take it. Here there is
// nothing to remember.
func TestConcurrentWritersCannotProduceTwoCurrentFacts(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "wp_race")
	obs := pg.NewObservationStore(pool)
	facts := pg.NewFactStore(pool)

	stored, err := obs.Append(ctx, schema, turnOf(
		domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "I live somewhere."},
	))
	if err != nil {
		t.Fatalf("append: %v", err)
	}

	const workers = 8
	// One instant for all of them, so every interval starts at the same point and every pair
	// genuinely overlaps. Staggering the times would let them supersede one another in sequence and
	// the race would never happen.
	at := time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC)

	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, results[i] = facts.Assert(ctx, schema, "p1", stored.ID, domain.RoleUser, "subject-1",
				domain.Claim{
					Subject: "the user", Predicate: "lives_in", Object: fmt.Sprintf("City%d", i),
					Statement: "The user lives somewhere.", Confidence: 0.9,
					Quote: "I live somewhere", ByteStart: 0, ByteEnd: 16, SourceOrdinal: 0,
					SemanticType: "identity", Cardinality: domain.CardinalityOne, ValidFrom: at,
				})
		}(i)
	}
	close(start)
	wg.Wait()

	succeeded := 0
	for _, err := range results {
		if err == nil {
			succeeded++
		}
	}
	if succeeded == 0 {
		t.Fatalf("every writer failed, so the constraint is refusing more than the overlap: %v", results)
	}

	// The assertion that matters. Whatever happened between the writers, the subject holds one
	// current residence.
	var current int
	if err := pool.QueryRow(ctx, schema.SQL(
		`SELECT count(*) FROM {schema}.fact
		  WHERE predicate = 'lives_in' AND upper_inf(valid) AND subject_entity_id IS NOT NULL`)).
		Scan(&current); err != nil {
		t.Fatalf("count current: %v", err)
	}
	if current != 1 {
		t.Fatalf("%d current lives_in facts after %d concurrent writers (%d succeeded); "+
			"the subject lives in more than one place", current, workers, succeeded)
	}
	t.Logf("%d of %d writers committed; the rest were refused by the constraint", succeeded, workers)
}

// A claim that does not say its cardinality is refused before it reaches the database.
//
// The foreign key would refuse it too, as a constraint violation naming a column — correct, and
// useless to whoever has to work out what they did wrong. Cardinality decides whether an assertion
// supersedes an earlier fact or sits beside it, so a claim without one is a claim whose meaning
// nobody has decided.
func TestAClaimWithNoCardinalityIsRefusedWithAReason(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "wp_nocard")
	obs := pg.NewObservationStore(pool)
	facts := pg.NewFactStore(pool)

	stored, err := obs.Append(ctx, schema, turnOf(
		domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "I live in Dublin."},
	))
	if err != nil {
		t.Fatalf("append: %v", err)
	}

	for name, cardinality := range map[string]domain.Cardinality{
		"absent":  "",
		"invalid": "sometimes",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := facts.Assert(ctx, schema, "p1", stored.ID, domain.RoleUser, "", domain.Claim{
				Subject: "the user", Predicate: "lives_in", Cardinality: cardinality, Object: "Dublin",
				Statement: "The user lives in Dublin.", Quote: "I live in Dublin",
				ByteStart: 0, ByteEnd: 16,
			})
			if err == nil {
				t.Fatal("a claim with no usable cardinality was stored")
			}
			if !strings.Contains(err.Error(), "cardinality") {
				t.Fatalf("the refusal does not name what was wrong: %v", err)
			}
			// And it did not reach the database, so nothing partial was written.
			var facts int
			if err := pool.QueryRow(ctx, schema.SQL(
				`SELECT count(*) FROM {schema}.fact`)).Scan(&facts); err != nil {
				t.Fatalf("count: %v", err)
			}
			if facts != 0 {
				t.Fatalf("%d facts were written by a refused assertion", facts)
			}
		})
	}
}

// Superseding leaves the earlier fact's evidence intact.
//
// The old fact is history rather than a mistake, and history without its evidence is an assertion
// nobody can check — which is the one thing this product does not ship.
func TestASupersededFactKeepsItsEvidence(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "wp_supersede_ev")
	obs := pg.NewObservationStore(pool)
	facts := pg.NewFactStore(pool)

	stored, err := obs.Append(ctx, schema, turnOf(
		domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "I live in Dublin. Now I live in Amman."},
	))
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	older, err := facts.Assert(ctx, schema, "p1", stored.ID, domain.RoleUser, "subject-1", domain.Claim{
		Subject: "the user", Predicate: "lives_in", Cardinality: domain.CardinalityOne, Object: "Dublin",
		Statement: "The user lives in Dublin.", Quote: "I live in Dublin", ByteStart: 0, ByteEnd: 16,
		SourceOrdinal: 0, ValidFrom: time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("assert first: %v", err)
	}
	if _, err := facts.Assert(ctx, schema, "p1", stored.ID, domain.RoleUser, "subject-1", domain.Claim{
		Subject: "the user", Predicate: "lives_in", Cardinality: domain.CardinalityOne, Object: "Amman",
		Statement: "The user lives in Amman.", Quote: "I live in Amman", ByteStart: 22, ByteEnd: 37,
		SourceOrdinal: 0, ValidFrom: time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("assert second: %v", err)
	}

	var quote string
	if err := pool.QueryRow(ctx, schema.SQL(
		`SELECT quote FROM {schema}.fact_evidence WHERE fact_id = $1`), older).Scan(&quote); err != nil {
		t.Fatalf("the superseded fact lost its evidence: %v", err)
	}
	if quote != "I live in Dublin" {
		t.Fatalf("the evidence changed under supersession: %q", quote)
	}
}

// A fact with a blank end is stored with that end unresolved, not refused.
//
// The claim is still worth keeping: its quote is real and its other end is a real entity. What it is
// not is a node in the graph, so traversal skips it — which is different from discarding the fact and
// its evidence because one string was empty.
func TestABlankEndIsUnresolvedRatherThanRefused(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "wp_blankend")
	obs := pg.NewObservationStore(pool)
	facts := pg.NewFactStore(pool)

	stored, err := obs.Append(ctx, schema, turnOf(
		domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "I work at Ensera."},
	))
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	id, err := facts.Assert(ctx, schema, "p1", stored.ID, domain.RoleUser, "", domain.Claim{
		Subject: "the user", Predicate: "works_at", Cardinality: domain.CardinalityMany,
		// Blank, and blank after normalisation, which is the case that matters: whitespace is not
		// an entity either.
		Object: "   ", Statement: "The user works somewhere.", Quote: "I work at",
		ByteStart: 0, ByteEnd: 9, SourceOrdinal: 0,
	})
	if err != nil {
		t.Fatalf("a claim with one blank end was refused: %v", err)
	}

	var objectResolved bool
	if err := pool.QueryRow(ctx, schema.SQL(
		`SELECT object_entity_id IS NOT NULL FROM {schema}.fact WHERE fact_id = $1`), id).
		Scan(&objectResolved); err != nil {
		t.Fatalf("read fact: %v", err)
	}
	if objectResolved {
		t.Fatal("whitespace resolved to an entity, which puts a node named nothing in the graph")
	}
}

// The fourth refusal reason is one the schema admits, and the set is still closed.
//
// Widening a check constraint and dropping it look identical from Go, so both directions are
// asserted: every reason the code names must store, and a reason nobody has heard of must not.
func TestAClaimTheMessageDidNotAssertIsStorableAsARefusal(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "wp_notasserted")
	obs := pg.NewObservationStore(pool)
	facts := pg.NewFactStore(pool)

	stored, err := obs.Append(ctx, schema, turnOf(
		domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "I do not live in London."},
	))
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := facts.Reject(ctx, schema, "p1", stored.ID, "subject-1", domain.RejectedClaim{
		Predicate: "lives_in", Statement: "The user lives in London.",
		Quote: "I do not live in London", Reason: domain.ReasonNotAsserted,
	}); err != nil {
		t.Fatalf("the schema refused a reason the code writes: %v", err)
	}

	// The words survive, which is what makes a refusal auditable: somebody asking why a relation is
	// missing from memory can read exactly what was proposed and why it was declined.
	var quote, reason string
	if err := pool.QueryRow(ctx, schema.SQL(
		`SELECT quote, reason FROM {schema}.rejected_claim`)).Scan(&quote, &reason); err != nil {
		t.Fatalf("read: %v", err)
	}
	if quote != "I do not live in London" || reason != domain.ReasonNotAsserted {
		t.Fatalf("the refusal lost what was proposed: %q / %q", quote, reason)
	}
}
