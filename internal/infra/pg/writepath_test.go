package pg_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/taisce/taisce/internal/domain"
	"github.com/taisce/taisce/internal/infra/pg"
	"github.com/taisce/taisce/internal/migrate"
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
		Subject: "user", Predicate: "lives_in", Object: "Dublin",
		Statement: "The user lives in Dublin.", Confidence: 0.9,
		Quote: "I moved to Dublin", ByteStart: 0, ByteEnd: 17, SourceOrdinal: 0,
	}); err != nil {
		t.Fatalf("the user's own claim must be accepted: %v", err)
	}

	// The assistant's inference about the same person is not.
	_, err = facts.Assert(ctx, schema, "p1", stored.ID, domain.RoleAssistant, "subject-1", domain.Claim{
		Subject: "user", Predicate: "likes", Object: "Dublin food scene",
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
		domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "The Dublin Office is on Grand Canal. dublin office has 40 desks."},
	))
	if err != nil {
		t.Fatalf("append: %v", err)
	}

	for _, c := range []domain.Claim{
		{Subject: "Dublin Office", Predicate: "located_on", Object: "Grand Canal",
			Statement: "The Dublin Office is on Grand Canal.", SubjectType: "place",
			Quote: "The Dublin Office is on Grand Canal", ByteStart: 0, ByteEnd: 35},
		{Subject: "dublin office", Predicate: "has_desks", Object: "40",
			Statement: "The Dublin office has 40 desks.", SubjectType: "thing",
			Quote: "dublin office has 40 desks", ByteStart: 37, ByteEnd: 63},
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
		Subject: "Marta", Predicate: "works_at", Object: "Ensera",
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
		   AND c.source_message_ordinal = 1
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
		Subject: "Marta", Predicate: "works_at", Object: "Ensera",
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
