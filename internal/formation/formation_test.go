package formation_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/extract"
	"github.com/ensera-ai/taisce/internal/formation"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
)

// A database is required. What is asserted here is transactional and lives in DDL — a watermark that
// cannot run past a gap, a lock that is exclusive per scope — and a fake would assert only that the
// fake was written to match the assertion.
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

// The model is stubbed so that this test asserts FORMATION rather than a provider's mood: what is
// under test is that a turn's messages are read in order, extracted with the right speaker, and
// written as one outcome. A live model is exercised by `make test-inference`, where it is the
// subject rather than the fixture.
type scriptedModel struct{}

func (scriptedModel) Propose(_ context.Context, message domain.Message, _ domain.Ontology) ([]extract.Proposal, error) {
	// Proposals are derived from the content so the stub cannot accidentally produce a quote that
	// is not in the message — the property the extractor is responsible for, and one this test must
	// not smuggle past.
	var out []extract.Proposal
	if i := strings.Index(message.Content, "work at Ensera"); i >= 0 {
		out = append(out, extract.Proposal{
			Subject: "user", Predicate: "works_at", Object: "Ensera",
			Statement: "The user works at Ensera.", Confidence: 0.9,
			Quote: message.Content[i : i+len("work at Ensera")],
		})
	}
	if i := strings.Index(message.Content, "love Dublin"); i >= 0 {
		out = append(out, extract.Proposal{
			Subject: "user", Predicate: "prefers", Object: "Dublin",
			Statement: "The user likes Dublin.", Confidence: 0.6,
			Quote: message.Content[i : i+len("love Dublin")],
		})
	}
	if strings.Contains(message.Content, "commutes by bicycle") {
		out = append(out, extract.Proposal{
			// Not in the vocabulary. Refused before the span is considered.
			Subject: "user", Predicate: "commutes_by", Object: "bicycle",
			Statement: "The user commutes by bicycle.", Quote: "commutes by bicycle",
		})
	}
	return out, nil
}

func former(t *testing.T, pool *pgxpool.Pool, schema pg.Schema) (*formation.Worker, *pg.ObservationStore) {
	t.Helper()
	vocabulary, err := pg.LoadOntology(context.Background(), pool, schema)
	if err != nil {
		t.Fatalf("load vocabulary: %v", err)
	}
	observations := pg.NewObservationStore(pool)
	f := formation.NewFormer(observations, pg.NewFactStore(pool), extract.New(scriptedModel{}, vocabulary))
	return formation.NewWorker(pool, observations, f), observations
}

func turnOf(msgs ...domain.Message) domain.Turn {
	return domain.Turn{
		Scope:         "p1",
		DataSubjectID: "subject-1",
		OccurredAt:    time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC),
		Messages:      msgs,
	}
}

// ── #44's first claim ─────────────────────────────────────────────────────────────────────────
//
// A turn is appended, formed, and its facts read back with evidence whose spans resolve against the
// messages they came from. This is the write half of the spine joined to the extractor: before it,
// turns were stored and no fact was ever formed from one.
func TestATurnIsFormedAndItsFactsCiteTheirOwnMessages(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "fm_form")
	worker, observations := former(t, pool, schema)

	stored, err := observations.Append(ctx, schema, turnOf(
		domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "Some chatter with no claim in it."},
		domain.Message{Ordinal: 1, Role: domain.RoleUser, Content: "I work at Ensera and I love Dublin."},
	))
	if err != nil {
		t.Fatalf("append: %v", err)
	}

	// Before forming, the turn is stored and there are no facts. This is the gap the second
	// watermark exists to describe.
	var before int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.fact`)).Scan(&before); err != nil {
		t.Fatalf("count: %v", err)
	}
	if before != 0 {
		t.Fatalf("a stored turn already had %d facts; storing and forming are the same moment", before)
	}

	reports, err := worker.Drain(ctx, schema, "p1")
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if len(reports) != 1 || reports[0].ObservationID != stored.ID {
		t.Fatalf("expected one turn formed, got %+v", reports)
	}
	if reports[0].FactsAsserted != 2 {
		t.Fatalf("expected two facts, got %d (%+v)", reports[0].FactsAsserted, reports[0])
	}

	// Every fact cites the message it came from, and the span taken from that message's own text
	// gives back the stored quote. A span against the turn would land in the first message and look
	// entirely plausible.
	rows, err := pool.Query(ctx, schema.SQL(`
		SELECT f.predicate,
		       e.quote,
		       substring(c.text FROM e.byte_start + 1 FOR e.byte_end - e.byte_start)
		  FROM {schema}.fact f
		  JOIN {schema}.fact_evidence e ON e.fact_id = f.fact_id
		  JOIN {schema}.chunk c
		    ON c.source_observation_id = e.source_observation_id
		   AND c.source_message_ordinal = e.source_ordinal
		 ORDER BY f.predicate`))
	if err != nil {
		t.Fatalf("read facts: %v", err)
	}
	defer rows.Close()

	seen := 0
	for rows.Next() {
		var predicate, quote, resolved string
		if err := rows.Scan(&predicate, &quote, &resolved); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if resolved != quote {
			t.Fatalf("%s: the span resolved to %q but the quote is %q", predicate, resolved, quote)
		}
		seen++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if seen != 2 {
		t.Fatalf("expected two cited facts, found %d", seen)
	}
}

// ── #44's second claim ────────────────────────────────────────────────────────────────────────
//
// The role is the MESSAGE's, never the turn's.
//
// A turn opened by the user does not make an assistant message later in it speak for the principal.
// Passing the turn's role would quietly promote every model sentence in any turn a person started —
// which is the majority of them.
func TestAnAssistantMessageInAUsersTurnFormsNoFactForThePrincipal(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "fm_role")
	worker, observations := former(t, pool, schema)

	if _, err := observations.Append(ctx, schema, turnOf(
		domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "I work at Ensera."},
		// The same claim, in the assistant's voice, later in a turn the user opened.
		domain.Message{Ordinal: 1, Role: domain.RoleAssistant, Content: "So you work at Ensera and love Dublin, then."},
	)); err != nil {
		t.Fatalf("append: %v", err)
	}

	reports, err := worker.Drain(ctx, schema, "p1")
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if len(reports) != 1 {
		t.Fatalf("expected one turn formed, got %+v", reports)
	}
	if reports[0].FactsAsserted != 1 {
		t.Fatalf("expected exactly the user's fact, got %d", reports[0].FactsAsserted)
	}
	// Both of the assistant's claims were refused, and the refusal is counted rather than silent.
	if reports[0].ClaimsRefusedByRole != 2 {
		t.Fatalf("expected two claims refused by role, got %d (%+v)", reports[0].ClaimsRefusedByRole, reports[0])
	}

	var ordinals []int
	rows, err := pool.Query(ctx, schema.SQL(
		`SELECT DISTINCT source_ordinal FROM {schema}.fact_evidence`))
	if err != nil {
		t.Fatalf("read evidence: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var ordinal int
		if err := rows.Scan(&ordinal); err != nil {
			t.Fatalf("scan: %v", err)
		}
		ordinals = append(ordinals, ordinal)
	}
	// Only message 0 produced a fact. Evidence joins every chunk of the turn, so message 1 appearing
	// here would mean the assistant's sentence became something the person is recorded as saying.
	for _, ordinal := range ordinals {
		if ordinal != 0 {
			t.Fatalf("message %d produced a fact, and it is not the principal speaking", ordinal)
		}
	}
}

// ── #44's third claim ─────────────────────────────────────────────────────────────────────────
//
// Stored is not formed, and the formed watermark cannot run past a gap.
//
// The second half is the one that matters. A number that runs ahead of an unformed turn tells a
// caller their scope is current while a turn in the middle of it is missing — and that is the single
// failure a freshness number must not have, because it is acted on rather than read.
func TestTheFormedWatermarkNeverRunsPastAnUnformedTurn(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "fm_watermark")
	worker, observations := former(t, pool, schema)

	var stored []domain.Observation
	for i := 0; i < 3; i++ {
		o, err := observations.Append(ctx, schema, turnOf(
			domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "I work at Ensera."},
		))
		if err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
		stored = append(stored, o)
	}

	fresh, err := observations.Freshness(ctx, schema, "p1")
	if err != nil {
		t.Fatalf("freshness: %v", err)
	}
	if fresh.Stored != 2 {
		t.Fatalf("stored is %d, expected 2", fresh.Stored)
	}
	if fresh.HasFormed {
		t.Fatalf("nothing has formed, yet the formed watermark reports %d", fresh.Formed)
	}

	// Form the MIDDLE turn only, which is what a second worker or a retry would do. The formed
	// watermark must stay absent, because turn 0 is still missing from memory.
	report, err := formationOf(t, pool, schema).Form(ctx, schema, stored[1])
	if err != nil {
		t.Fatalf("form: %v", err)
	}
	if report.FactsAsserted != 1 {
		t.Fatalf("expected one fact, got %d", report.FactsAsserted)
	}
	if err := observations.MarkFormed(ctx, schema, stored[1].ID); err != nil {
		t.Fatalf("mark formed: %v", err)
	}

	fresh, err = observations.Freshness(ctx, schema, "p1")
	if err != nil {
		t.Fatalf("freshness: %v", err)
	}
	if fresh.HasFormed {
		t.Fatalf("the formed watermark reports %d with turn 0 unformed below it", fresh.Formed)
	}

	// Drain the rest. Now everything is formed and the watermark reaches the top.
	if _, err := worker.Drain(ctx, schema, "p1"); err != nil {
		t.Fatalf("drain: %v", err)
	}
	fresh, err = observations.Freshness(ctx, schema, "p1")
	if err != nil {
		t.Fatalf("freshness: %v", err)
	}
	if !fresh.HasFormed || fresh.Formed != 2 {
		t.Fatalf("expected the formed watermark at 2, got %+v", fresh)
	}
}

func formationOf(t *testing.T, pool *pgxpool.Pool, schema pg.Schema) *formation.Former {
	t.Helper()
	vocabulary, err := pg.LoadOntology(context.Background(), pool, schema)
	if err != nil {
		t.Fatalf("load vocabulary: %v", err)
	}
	return formation.NewFormer(pg.NewObservationStore(pool), pg.NewFactStore(pool),
		extract.New(scriptedModel{}, vocabulary))
}

// ── #44's fourth claim ────────────────────────────────────────────────────────────────────────
//
// One worker per scope.
//
// Two workers taking the same turn would extract it twice and assert every fact twice, and the
// duplicates would be indistinguishable afterwards from a person saying the same thing in two
// conversations. Nothing weaker than a lock held across the whole drain prevents it: a lock inside
// the transaction that picks a turn is released before the model call it was meant to protect.
func TestASecondWorkerOnTheSameScopeIsTurnedAway(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "fm_lock")
	worker, observations := former(t, pool, schema)

	if _, err := observations.Append(ctx, schema, turnOf(
		domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "I work at Ensera."},
	)); err != nil {
		t.Fatalf("append: %v", err)
	}

	// Hold the scope from outside, the way another worker's connection would.
	held, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer held.Release()
	key := schema.String() + "/p1"
	var taken bool
	if err := held.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtext($1)::bigint)`, key).Scan(&taken); err != nil {
		t.Fatalf("take lock: %v", err)
	}
	if !taken {
		t.Fatal("the lock was already held, so this test asserts nothing")
	}

	if _, err := worker.Drain(ctx, schema, "p1"); !errors.Is(err, formation.ErrScopeBusy) {
		t.Fatalf("a second worker was let in; got %v", err)
	}

	// Another scope is unaffected: formation for one project has no reason to wait on another's.
	if err := migrate.ProvisionScope(ctx, pool, schema.String(), "p2"); err != nil {
		t.Fatalf("provision p2: %v", err)
	}
	other := turnOf(domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "I work at Ensera."})
	other.Scope = "p2"
	if _, err := observations.Append(ctx, schema, other); err != nil {
		t.Fatalf("append p2: %v", err)
	}
	if _, err := worker.Drain(ctx, schema, "p2"); err != nil {
		t.Fatalf("a busy scope blocked an unrelated one: %v", err)
	}

	// Released, and the first scope can then be drained.
	if _, err := held.Exec(ctx, `SELECT pg_advisory_unlock(hashtext($1)::bigint)`, key); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, err := worker.Drain(ctx, schema, "p1"); err != nil {
		t.Fatalf("drain after release: %v", err)
	}
}

// What the vocabulary refused is written down, so its rate is a query rather than an estimate.
func TestFormationRecordsWhatTheVocabularyRefused(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "fm_rejected")
	worker, observations := former(t, pool, schema)

	if _, err := observations.Append(ctx, schema, turnOf(
		domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "I work at Ensera and commutes by bicycle."},
	)); err != nil {
		t.Fatalf("append: %v", err)
	}
	reports, err := worker.Drain(ctx, schema, "p1")
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if len(reports) != 1 || reports[0].ClaimsRejected != 1 {
		t.Fatalf("expected one rejection, got %+v", reports)
	}

	var predicate, reason string
	if err := pool.QueryRow(ctx, schema.SQL(
		`SELECT predicate, reason FROM {schema}.rejected_claim`)).Scan(&predicate, &reason); err != nil {
		t.Fatalf("read rejection: %v", err)
	}
	if predicate != "commutes_by" || reason != domain.ReasonUnmappedRelation {
		t.Fatalf("the rejection did not record what was refused: %q / %q", predicate, reason)
	}
}
