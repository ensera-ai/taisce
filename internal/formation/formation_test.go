package formation_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
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
	if err := migrate.ProvisionMemorySchema(ctx, pool, name); err != nil {
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
			Subject: "user", Predicate: "works_at", Polarity: domain.PolarityAsserted, Tense: domain.TensePresent, Object: "Ensera",
			Statement: "The user works at Ensera.", Confidence: 0.9,
			Quote: message.Content[i : i+len("work at Ensera")],
		})
	}
	if i := strings.Index(message.Content, "love Dublin"); i >= 0 {
		out = append(out, extract.Proposal{
			Subject: "user", Predicate: "prefers", Polarity: domain.PolarityAsserted, Tense: domain.TensePresent, Object: "Dublin",
			Statement: "The user likes Dublin.", Confidence: 0.6,
			Quote: message.Content[i : i+len("love Dublin")],
		})
	}
	for _, city := range []string{"Dublin", "Amman"} {
		if i := strings.Index(message.Content, "live in "+city); i >= 0 {
			out = append(out, extract.Proposal{
				Subject: "user", Predicate: "lives_in", Polarity: domain.PolarityAsserted, Tense: domain.TensePresent, Object: city,
				Statement: "The user lives in " + city + ".", Confidence: 0.9,
				Quote: message.Content[i : i+len("live in "+city)],
			})
		}
	}
	if strings.Contains(message.Content, "commutes by bicycle") {
		out = append(out, extract.Proposal{
			// Not in the vocabulary. Refused before the span is considered.
			Subject: "user", Predicate: "commutes_by", Polarity: domain.PolarityAsserted, Tense: domain.TensePresent, Object: "bicycle",
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

// workerWith builds a worker over a chosen model and policy. The existing `former` helper fixes both,
// which is right for the tests it serves and wrong for these: what these are about is what happens when
// the model refuses, and how many times.
func workerWith(t *testing.T, pool *pgxpool.Pool, schema pg.Schema,
	model extract.Model, policy formation.Policy) *formation.Worker {
	t.Helper()
	vocabulary, err := pg.LoadOntology(context.Background(), pool, schema)
	if err != nil {
		t.Fatalf("load vocabulary: %v", err)
	}
	observations := pg.NewObservationStore(pool)
	f := formation.NewFormer(observations, pg.NewFactStore(pool), extract.New(model, vocabulary))
	return formation.NewWorkerWithPolicy(pool, observations, f, policy)
}

func turnOf(msgs ...domain.Message) domain.Turn {
	return domain.Turn{
		Scope:         "p1",
		DataSubjectID: "subject-1",
		OccurredAt:    time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC),
		Messages:      msgs,
	}
}

// ── A turn is formed and its facts cite their own messages ────────────────────────────────────
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

// ── The role is the message's, never the turn's ───────────────────────────────────────────────
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

// ── Stored is not formed, and the watermark cannot run past a gap ─────────────────────────────
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

// ── One worker per scope ──────────────────────────────────────────────────────────────────────
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

// ── What happens to a turn that will not form ─────────────────────────────────────────────────

// failingModel refuses every message, the way a provider that will never accept this turn does.
type failingModel struct{ calls int }

func (m *failingModel) Propose(context.Context, domain.Message, domain.Ontology) ([]extract.Proposal, error) {
	m.calls++
	return nil, fmt.Errorf("the provider refused this message")
}

// impatient is the default policy with the waiting taken out, so a test can drive a turn to
// exhaustion without spending ten minutes doing it. The bounds being configurable is the reason this
// test can exist at all.
func impatient() formation.Policy {
	p := formation.DefaultPolicy()
	p.MaxAttempts = 3
	p.RetryAfter = time.Nanosecond
	p.TurnBudget = 30 * time.Second
	return p
}

// A turn that fails every time is parked, and parking is legible without reading a log.
func TestATurnThatWillNotFormIsParkedWithItsReasonKept(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "fm_park")
	observations := pg.NewObservationStore(pool)

	stored, err := observations.Append(ctx, schema, turnOf(
		domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "I work at Ensera."},
	))
	if err != nil {
		t.Fatalf("append: %v", err)
	}

	model := &failingModel{}
	worker := workerWith(t, pool, schema, model, impatient())

	// Each drain makes one attempt and moves on; the turn is offered again on the next drain.
	for i := 0; i < impatient().MaxAttempts; i++ {
		if _, err := worker.Drain(ctx, schema, "p1"); err != nil {
			t.Fatalf("drain %d: %v", i, err)
		}
	}

	parked, err := observations.Parked(ctx, schema, "p1")
	if err != nil {
		t.Fatalf("list parked: %v", err)
	}
	if len(parked) != 1 {
		t.Fatalf("got %d parked turns after %d failed attempts, wanted 1", len(parked), impatient().MaxAttempts)
	}
	if parked[0].ObservationID != stored.ID {
		t.Fatalf("parked the wrong turn: %s", parked[0].ObservationID)
	}
	if parked[0].Attempts != impatient().MaxAttempts {
		t.Fatalf("parked after %d attempts, wanted %d", parked[0].Attempts, impatient().MaxAttempts)
	}
	// The reason is the deliverable. A parked turn whose failure can only be found in a log is a
	// turn nobody will diagnose.
	if !strings.Contains(parked[0].Reason, "the provider refused") {
		t.Fatalf("the parked turn does not say why: %q", parked[0].Reason)
	}
}

// A parked turn does not hold the scope. This is the decision the issue said was the customer-facing
// one: the watermark advances past it so one poisonous turn cannot freeze a project's memory.
func TestAParkedTurnDoesNotFreezeTheScopeBehindIt(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "fm_unfreeze")
	observations := pg.NewObservationStore(pool)

	poison, err := observations.Append(ctx, schema, turnOf(
		domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "This one will never form."},
	))
	if err != nil {
		t.Fatalf("append poison: %v", err)
	}

	// Exhaust it against a model that always fails.
	failing := workerWith(t, pool, schema, &failingModel{}, impatient())
	for i := 0; i < impatient().MaxAttempts; i++ {
		if _, err := failing.Drain(ctx, schema, "p1"); err != nil {
			t.Fatalf("drain: %v", err)
		}
	}

	// A turn written after it forms normally.
	good, err := observations.Append(ctx, schema, turnOf(
		domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "I work at Ensera."},
	))
	if err != nil {
		t.Fatalf("append good: %v", err)
	}
	working := workerWith(t, pool, schema, scriptedModel{}, impatient())
	if _, err := working.Drain(ctx, schema, "p1"); err != nil {
		t.Fatalf("drain good: %v", err)
	}

	fresh, err := observations.Freshness(ctx, schema, "p1")
	if err != nil {
		t.Fatalf("freshness: %v", err)
	}
	if !fresh.HasFormed || fresh.Formed != good.LogOffset {
		t.Fatalf("the watermark is %v/%v and the good turn is at %d — a parked turn froze the scope",
			fresh.Formed, fresh.HasFormed, good.LogOffset)
	}
	// And the exception to that number is reported rather than hidden, which is the whole condition
	// on which advancing past a parked turn is acceptable.
	if fresh.Parked != 1 {
		t.Fatalf("freshness reports %d parked turns, so the watermark's exception is invisible", fresh.Parked)
	}
	_ = poison
}

// Parking is a decision, not a deletion. A decision that cannot be reversed is a deletion with extra
// steps, so unparking returns the turn to the backlog and takes the watermark back down with it.
func TestUnparkingReturnsTheTurnAndCorrectsTheWatermark(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "fm_unpark")
	observations := pg.NewObservationStore(pool)

	stored, err := observations.Append(ctx, schema, turnOf(
		domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "I work at Ensera."},
	))
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	failing := workerWith(t, pool, schema, &failingModel{}, impatient())
	for i := 0; i < impatient().MaxAttempts; i++ {
		if _, err := failing.Drain(ctx, schema, "p1"); err != nil {
			t.Fatalf("drain: %v", err)
		}
	}

	if err := observations.Unpark(ctx, schema, stored.ID); err != nil {
		t.Fatalf("unpark: %v", err)
	}
	fresh, err := observations.Freshness(ctx, schema, "p1")
	if err != nil {
		t.Fatalf("freshness: %v", err)
	}
	if fresh.HasFormed {
		t.Fatalf("the watermark still claims offset %d is formed after the turn came back", fresh.Formed)
	}
	if fresh.Parked != 0 {
		t.Fatalf("%d turns are still parked", fresh.Parked)
	}

	// And it forms, which is what makes unparking worth having.
	working := workerWith(t, pool, schema, scriptedModel{}, impatient())
	if _, err := working.Drain(ctx, schema, "p1"); err != nil {
		t.Fatalf("drain after unpark: %v", err)
	}
	fresh, err = observations.Freshness(ctx, schema, "p1")
	if err != nil {
		t.Fatalf("freshness: %v", err)
	}
	if !fresh.HasFormed || fresh.Formed != stored.LogOffset {
		t.Fatal("an unparked turn did not form")
	}
}

// A shutdown is not a bad turn. Counting a cancelled attempt would spend a turn's budget on every
// restart, and a handful of restarts would park a backlog nothing was ever wrong with.
func TestACancelledDrainDoesNotSpendTheTurnsAttempts(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "fm_cancel")
	observations := pg.NewObservationStore(pool)

	if _, err := observations.Append(ctx, schema, turnOf(
		domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "I work at Ensera."},
	)); err != nil {
		t.Fatalf("append: %v", err)
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	worker := workerWith(t, pool, schema, &failingModel{}, impatient())
	if _, err := worker.Drain(cancelled, schema, "p1"); err == nil {
		t.Fatal("a cancelled drain reported success")
	}

	parked, err := observations.Parked(ctx, schema, "p1")
	if err != nil {
		t.Fatalf("list parked: %v", err)
	}
	if len(parked) != 0 {
		t.Fatalf("a cancelled drain parked %d turns", len(parked))
	}
}

// ── The driver ────────────────────────────────────────────────────────────────────────────────

// The driver finds scopes rather than being told them, which is the only way a project created after
// it started gets formed.
func TestTheDriverFindsScopesItWasNeverToldAbout(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "fm_driver")
	observations := pg.NewObservationStore(pool)

	for _, scope := range []string{"p1", "p2"} {
		turn := turnOf(domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "I work at Ensera."})
		turn.Scope = scope
		if err := migrate.ProvisionScope(ctx, pool, schema.String(), scope); err != nil {
			t.Fatalf("provision %s: %v", scope, err)
		}
		if _, err := observations.Append(ctx, schema, turn); err != nil {
			t.Fatalf("append to %s: %v", scope, err)
		}
	}

	driver := formation.NewDriver(
		workerWith(t, pool, schema, scriptedModel{}, impatient()),
		observations, pg.NewRetentionStore(pool, schema), pg.NewAuditStore(pool, schema), schema, impatient(), quietLogger())

	pass, err := driver.Once(ctx)
	if err != nil {
		t.Fatalf("pass: %v", err)
	}
	if pass.Scopes != 2 {
		t.Fatalf("the driver found %d scopes, wanted the two that have a backlog", pass.Scopes)
	}
	if pass.Formed != 2 {
		t.Fatalf("the driver formed %d turns across two scopes", pass.Formed)
	}

	// A second pass finds nothing, which is what says the first one finished rather than looped.
	again, err := driver.Once(ctx)
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if again.Scopes != 0 {
		t.Fatalf("%d scopes still have a backlog after a completed pass", again.Scopes)
	}
}

// One project's bad turn does not hold up another project's memory. This is the same mistake parking
// exists to fix, one level up.
func TestAScopeThatFailsDoesNotStopTheOthersForming(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "fm_isolate")
	observations := pg.NewObservationStore(pool)

	for _, scope := range []string{"p1", "p2"} {
		if err := migrate.ProvisionScope(ctx, pool, schema.String(), scope); err != nil {
			t.Fatalf("provision %s: %v", scope, err)
		}
	}
	// p1 gets a turn a model will refuse; p2 gets one it will accept. The model fails on the
	// content, so which scope suffers is decided by the fixture rather than by ordering.
	poison := turnOf(domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "refuse me"})
	poison.Scope = "p1"
	if _, err := observations.Append(ctx, schema, poison); err != nil {
		t.Fatalf("append poison: %v", err)
	}
	good := turnOf(domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "I work at Ensera."})
	good.Scope = "p2"
	stored, err := observations.Append(ctx, schema, good)
	if err != nil {
		t.Fatalf("append good: %v", err)
	}

	driver := formation.NewDriver(
		workerWith(t, pool, schema, selective{}, impatient()),
		observations, pg.NewRetentionStore(pool, schema), pg.NewAuditStore(pool, schema), schema, impatient(), quietLogger())

	pass, err := driver.Once(ctx)
	if err != nil {
		t.Fatalf("pass: %v", err)
	}
	if pass.Formed != 1 {
		t.Fatalf("formed %d turns; the healthy scope should have formed regardless of the other", pass.Formed)
	}

	fresh, err := observations.Freshness(ctx, schema, "p2")
	if err != nil {
		t.Fatalf("freshness: %v", err)
	}
	if !fresh.HasFormed || fresh.Formed != stored.LogOffset {
		t.Fatal("a healthy scope did not form because another scope was failing")
	}
}

// selective refuses one fixture and accepts the other, so a test can have two scopes disagree.
type selective struct{}

func (selective) Propose(_ context.Context, m domain.Message, _ domain.Ontology) ([]extract.Proposal, error) {
	if strings.Contains(m.Content, "refuse me") {
		return nil, fmt.Errorf("the provider refused this message")
	}
	return scriptedModel{}.Propose(context.Background(), m, domain.Ontology{})
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

// The driver forms without anybody asking it to.
//
// This is the driver's whole point and the one property none of the other tests cover: every one of them
// calls Drain or Once directly, which proves the mechanism and not that it ever runs. Here a turn is
// appended into a running driver and the assertion is that memory formed on its own.
func TestTheDriverFormsAScopeWithoutAnybodyAskingIt(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "fm_running")
	observations := pg.NewObservationStore(pool)

	policy := impatient()
	policy.Interval = 20 * time.Millisecond
	driver := formation.NewDriver(
		workerWith(t, pool, schema, scriptedModel{}, policy),
		observations, pg.NewRetentionStore(pool, schema), pg.NewAuditStore(pool, schema), schema, policy, quietLogger())

	running, stop := context.WithCancel(ctx)
	returned := make(chan error, 1)
	go func() { returned <- driver.Run(running) }()

	stored, err := observations.Append(ctx, schema, turnOf(
		domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "I work at Ensera."},
	))
	if err != nil {
		stop()
		t.Fatalf("append: %v", err)
	}

	// Polled rather than slept on: what is being asserted is that it happens, and a fixed sleep
	// would either make the test slow or make it flaky, and there is no third outcome.
	formed := false
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		fresh, err := observations.Freshness(ctx, schema, "p1")
		if err == nil && fresh.HasFormed && fresh.Formed >= stored.LogOffset {
			formed = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	stop()

	if !formed {
		t.Fatal("a turn was appended into a running driver and never formed")
	}
	select {
	case err := <-returned:
		if err != nil {
			t.Fatalf("the driver returned an error on shutdown: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the driver did not return when its context ended")
	}
}

// Two drivers over one instance do not form the same turn twice.
//
// This is what makes the chart's several replicas a deployment decision rather than a code change,
// so it is asserted rather than assumed: the second driver finds the scope locked and moves on.
func TestASecondDriverDoesNotFormTheSameTurnTwice(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "fm_replicas")
	observations := pg.NewObservationStore(pool)

	for i := 0; i < 3; i++ {
		if _, err := observations.Append(ctx, schema, turnOf(
			domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "I work at Ensera."},
		)); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	policy := impatient()
	newDriver := func() *formation.Driver {
		return formation.NewDriver(workerWith(t, pool, schema, scriptedModel{}, policy),
			observations, pg.NewRetentionStore(pool, schema), pg.NewAuditStore(pool, schema), schema, policy, quietLogger())
	}

	// Run two passes concurrently. Whichever loses the lock reports the scope busy and forms
	// nothing; between them they must form each turn exactly once.
	type result struct {
		pass formation.Pass
		err  error
	}
	results := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() {
			p, err := newDriver().Once(ctx)
			results <- result{p, err}
		}()
	}
	total := 0
	for i := 0; i < 2; i++ {
		r := <-results
		if r.err != nil {
			t.Fatalf("a concurrent pass failed: %v", r.err)
		}
		total += r.pass.Formed
	}
	if total != 3 {
		t.Fatalf("two drivers formed %d turns between them, wanted the 3 that exist", total)
	}

	var facts int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.fact`)).Scan(&facts); err != nil {
		t.Fatalf("count facts: %v", err)
	}
	if facts != 3 {
		t.Fatalf("%d facts from 3 turns asserting one relation each — a turn was formed twice", facts)
	}
}

// Recording a failure against a turn that formed in the meantime is not an error.
//
// It happens: an attempt fails after the mark, or the subject was erased between the attempt and the
// write. Failing here would turn a recovered run into a stuck one, and an erased subject into a
// driver that cannot make progress.
func TestRecordingAFailureAgainstAFormedOrAbsentTurnIsANoOp(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "fm_noop")
	observations := pg.NewObservationStore(pool)

	stored, err := observations.Append(ctx, schema, turnOf(
		domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "I work at Ensera."},
	))
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if _, err := workerWith(t, pool, schema, scriptedModel{}, impatient()).
		Drain(ctx, schema, "p1"); err != nil {
		t.Fatalf("drain: %v", err)
	}

	attempts, err := observations.RecordFormationFailure(ctx, schema, stored.ID, "too late")
	if err != nil {
		t.Fatalf("recording against a formed turn errored: %v", err)
	}
	if attempts != 0 {
		t.Fatalf("a formed turn accumulated %d attempts", attempts)
	}
	// Parking one is the same: the turn is in memory and giving up on it is contradictory.
	if err := observations.Park(ctx, schema, stored.ID); err != nil {
		t.Fatalf("parking a formed turn errored: %v", err)
	}
	parked, err := observations.Parked(ctx, schema, "p1")
	if err != nil {
		t.Fatalf("list parked: %v", err)
	}
	if len(parked) != 0 {
		t.Fatalf("a formed turn was parked")
	}
	// And an observation that does not exist at all.
	if err := observations.Unpark(ctx, schema, "00000000-0000-0000-0000-000000000000"); err != nil {
		t.Fatalf("unparking an absent turn errored: %v", err)
	}
}

// The driver sweeps retention as well as forming, on its own schedule.
//
// Separate from a formation pass rather than folded into it, because the two answer to different
// clocks: formation runs when there is a backlog, retention runs whether or not anything was written.
// Folded together, a quiet project would never expire anything.
func TestTheDriverSweepsRetention(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "fm_retention")
	if err := migrate.ProvisionScope(ctx, pool, schema.String(), "p1"); err != nil {
		t.Fatalf("provision: %v", err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(
		`UPDATE {schema}.project SET retention = interval '1 microsecond' WHERE scope = 'p1'`)); err != nil {
		t.Fatalf("set retention: %v", err)
	}

	observations := pg.NewObservationStore(pool)
	if _, err := observations.Append(ctx, schema, turnOf(
		domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "I work at Ensera."},
	)); err != nil {
		t.Fatalf("append: %v", err)
	}
	// This asserts the driver's sweep, not a race against a one-microsecond policy deadline.
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.observation SET retention_until=now()-interval '1 hour'`)); err != nil {
		t.Fatal(err)
	}
	var diagnostics bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&diagnostics, nil))

	driver := formation.NewDriver(
		workerWith(t, pool, schema, scriptedModel{}, impatient()),
		observations, pg.NewRetentionStore(pool, schema), pg.NewAuditStore(pool, schema), schema, impatient(), logger)

	if err := driver.SweepRetention(ctx); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.observation`)).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("%d expired observations survived the driver's sweep; diagnostics: %s", n, diagnostics.String())
	}
	if strings.Contains(diagnostics.String(), "level=ERROR") {
		t.Fatalf("sweep logged a failure: %s", diagnostics.String())
	}

	// What it deleted is a receipt, written in the transaction that deleted it, and an entry on the
	// ledger under the system principal: nobody asked for this, the project's policy did.
	var receipts, sweptTurns int
	var deleted string
	if err := pool.QueryRow(ctx, schema.SQL(
		`SELECT count(*), coalesce(max(observations),0), coalesce(max(deleted::text),'')
		   FROM {schema}.retention_sweep WHERE scope='p1'`)).Scan(&receipts, &sweptTurns, &deleted); err != nil {
		t.Fatal(err)
	}
	if receipts != 1 || sweptTurns != 1 || !strings.Contains(deleted, "observation") {
		t.Fatalf("the sweep left receipts=%d observations=%d deleted=%s", receipts, sweptTurns, deleted)
	}
	var entries int
	if err := pool.QueryRow(ctx, schema.SQL(
		`SELECT count(*) FROM {schema}.audit_entry
		  WHERE operation='retention.sweep' AND principal_kind='system' AND project='p1' AND magnitude=1`)).Scan(&entries); err != nil {
		t.Fatal(err)
	}
	if entries != 1 {
		t.Fatalf("the ledger recorded %d sweeps", entries)
	}
	// A sweep that finds nothing writes nothing: a receipt per quiet tick would bury the ones that
	// say something.
	if err := driver.SweepRetention(ctx); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.retention_sweep WHERE scope='p1'`)).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if receipts != 1 {
		t.Fatalf("a sweep that deleted nothing left %d receipts", receipts)
	}
}

// Stored deadlines determine whether a project has due work, including after its current policy
// becomes indefinite. A future deadline does not schedule an early sweep.
func TestOnlyProjectsWithDueStoredDeadlinesAreSwept(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "fm_noretention")
	observations := pg.NewObservationStore(pool)

	for _, scope := range []string{"kept", "expiring"} {
		if err := migrate.ProvisionScope(ctx, pool, schema.String(), scope); err != nil {
			t.Fatalf("provision %s: %v", scope, err)
		}
	}
	if _, err := pool.Exec(ctx, schema.SQL(
		`UPDATE {schema}.project SET retention = interval '1 microsecond' WHERE scope = 'expiring'`)); err != nil {
		t.Fatalf("set retention: %v", err)
	}

	for _, scope := range []string{"kept", "expiring"} {
		turn := turnOf(domain.Message{Role: domain.RoleUser, Content: "A retained source"})
		turn.Scope = scope
		if _, err := observations.Append(ctx, schema, turn); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.observation SET retention_until=CASE WHEN scope='expiring' THEN now()-interval '1 hour' ELSE now()+interval '1 hour' END; UPDATE {schema}.project SET retention=NULL`)); err != nil {
		t.Fatal(err)
	}

	scopes, err := observations.ScopesWithRetention(ctx, schema)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(scopes) != 1 || scopes[0] != "expiring" {
		t.Fatalf("scopes with due deadlines: %v", scopes)
	}
}

// A driver pass keeps going when one scope fails, and reports that it did.
//
// The same reasoning as parking a turn, one level up: one project's failure holding up every other
// project's memory is the mistake the whole design refuses.
func TestAPassReportsWhatItCouldNotDo(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "fm_passreport")
	observations := pg.NewObservationStore(pool)

	for _, scope := range []string{"p1", "p2"} {
		if err := migrate.ProvisionScope(ctx, pool, schema.String(), scope); err != nil {
			t.Fatalf("provision %s: %v", scope, err)
		}
		turn := turnOf(domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "refuse me"})
		turn.Scope = scope
		if _, err := observations.Append(ctx, schema, turn); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	driver := formation.NewDriver(
		workerWith(t, pool, schema, selective{}, impatient()),
		observations, pg.NewRetentionStore(pool, schema), pg.NewAuditStore(pool, schema),
		schema, impatient(), quietLogger())

	pass, err := driver.Once(ctx)
	if err != nil {
		t.Fatalf("pass: %v", err)
	}
	if pass.Scopes != 2 {
		t.Fatalf("the pass saw %d scopes", pass.Scopes)
	}
	if pass.Formed != 0 {
		t.Fatalf("%d turns formed from a model that refuses everything", pass.Formed)
	}
	// Both scopes had a turn the model refuses. Neither is an error the pass reports — a refused
	// turn is counted against the turn and retried, which is parking's job rather than the pass's.
	if pass.Errored != 0 {
		t.Fatalf("a refused turn was reported as a pass error: %+v", pass)
	}
}

// A cancelled pass stops rather than working through the remaining scopes.
func TestACancelledPassStops(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "fm_cancelpass")
	observations := pg.NewObservationStore(pool)
	if _, err := observations.Append(ctx, schema, turnOf(
		domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "I work at Ensera."},
	)); err != nil {
		t.Fatalf("append: %v", err)
	}

	driver := formation.NewDriver(
		workerWith(t, pool, schema, scriptedModel{}, impatient()),
		observations, pg.NewRetentionStore(pool, schema), pg.NewAuditStore(pool, schema),
		schema, impatient(), quietLogger())

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := driver.Once(cancelled); err == nil {
		t.Fatal("a cancelled pass reported success")
	}
	if err := driver.SweepRetention(cancelled); err == nil {
		t.Fatal("a cancelled retention sweep reported success")
	}
}

// A message that gives one single-cardinality relation two current values forms rather than parks:
// the first value is the fact, the second is a refusal with its own reason, and the watermark moves.
// Measured on a news corpus before this, such turns were retried six times and parked with nothing
// stored.
func TestATurnWithTwoCurrentValuesFormsWithOneFactAndOneRefusal(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "fm_conflict")
	worker, observations := former(t, pool, schema)
	stored, err := observations.Append(ctx, schema, turnOf(
		domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "I live in Dublin. I live in Amman."},
	))
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	reports, err := worker.Drain(ctx, schema, "p1")
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if len(reports) != 1 || reports[0].FactsAsserted != 1 || reports[0].ClaimsRejected != 1 {
		t.Fatalf("expected one fact and one refusal, got %+v", reports)
	}
	var formed bool
	var reason string
	if err := pool.QueryRow(ctx, schema.SQL(
		`SELECT o.formed_at IS NOT NULL, r.reason FROM {schema}.observation o
		 JOIN {schema}.rejected_claim r ON r.source_observation_id = o.observation_id
		 WHERE o.observation_id = $1`), stored.ID).Scan(&formed, &reason); err != nil {
		t.Fatalf("read outcome: %v", err)
	}
	if !formed || reason != domain.ReasonConflictingValue {
		t.Fatalf("formed=%v reason=%q; the turn should be formed and the second value refused as conflicting", formed, reason)
	}
}

// A refusal by role is a row, not only a number: an operator reading what a document was refused
// sees the assistant's first-person claims beside the other reasons.
func TestAClaimRefusedByRoleIsRecordedWithItsReason(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "fm_rolerow")
	worker, observations := former(t, pool, schema)
	stored, err := observations.Append(ctx, schema, turnOf(
		domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "Hello."},
		domain.Message{Ordinal: 1, Role: domain.RoleAssistant, Content: "So you work at Ensera, then."},
	))
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	reports, err := worker.Drain(ctx, schema, "p1")
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if len(reports) != 1 || reports[0].ClaimsRefusedByRole != 1 || reports[0].FactsAsserted != 0 {
		t.Fatalf("expected one claim refused by role and no fact, got %+v", reports)
	}
	var reason string
	var ordinal int
	if err := pool.QueryRow(ctx, schema.SQL(
		`SELECT reason, source_ordinal FROM {schema}.rejected_claim WHERE source_observation_id = $1`), stored.ID).
		Scan(&reason, &ordinal); err != nil {
		t.Fatalf("read the refusal row: %v", err)
	}
	if reason != domain.ReasonNotSpokenByPrincipal || ordinal != 1 {
		t.Fatalf("expected the assistant's claim recorded as not spoken by the principal, got %q at ordinal %d", reason, ordinal)
	}
}

// ── A worker that dies strands nothing ────────────────────────────────────────────────────────
//
// A worker that dies strands nothing.
//
// The scope lock is a session lock, so the guarantee that another worker can take over is a
// property of the connection ending rather than of any code here — which is exactly why it needs a
// test: nothing in this package would fail if the lock were taken in a way that outlived the
// process, and the failure would first appear as a project whose formation stopped for good.
//
// The turn is left mid-flight the way a killed pod leaves it: claimed, attempted, and not formed.
func TestAWorkerThatDiesStrandsNeitherTheScopeNorTheTurn(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "fm_crash")
	worker, observations := former(t, pool, schema)

	if _, err := observations.Append(ctx, schema, turnOf(
		domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "I work at Ensera."},
	)); err != nil {
		t.Fatalf("append: %v", err)
	}

	// A worker on its own connection, holding the scope and having failed one attempt at the turn:
	// the state a pod killed between claiming a turn and forming it leaves behind.
	dying, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	key := schema.String() + "/p1"
	var taken bool
	if err := dying.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtext($1)::bigint)`, key).Scan(&taken); err != nil || !taken {
		t.Fatalf("take the scope: %v %v", taken, err)
	}
	var pid int
	if err := dying.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatalf("read the backend pid: %v", err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(
		`UPDATE {schema}.observation SET formation_attempts = 1, formation_failed_at = now() - interval '1 hour' WHERE scope = 'p1'`)); err != nil {
		t.Fatalf("leave the turn attempted: %v", err)
	}
	if _, err := worker.Drain(ctx, schema, "p1"); !errors.Is(err, formation.ErrScopeBusy) {
		t.Fatalf("the scope must be busy while the worker holds it; got %v", err)
	}

	// The process dies. Not a clean shutdown: the backend is terminated, which is what the
	// scheduler's kill leaves behind.
	if _, err := pool.Exec(ctx, `SELECT pg_terminate_backend($1)`, pid); err != nil {
		t.Fatalf("terminate the worker's backend: %v", err)
	}
	dying.Hijack().Close(ctx)

	// Another worker takes the scope and finishes the turn. No operator, no restart of anything
	// else, and no waiting for a lock that nobody holds.
	deadline := time.Now().Add(10 * time.Second)
	var reports []formation.Report
	for {
		reports, err = worker.Drain(ctx, schema, "p1")
		if err == nil || !errors.Is(err, formation.ErrScopeBusy) || time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("a worker could not take a scope whose holder died: %v", err)
	}
	if len(reports) != 1 {
		t.Fatalf("the turn the dead worker left was not formed: %d report(s)", len(reports))
	}

	var unformed int
	if err := pool.QueryRow(ctx, schema.SQL(
		`SELECT count(*) FROM {schema}.observation WHERE scope='p1' AND formed_at IS NULL`)).Scan(&unformed); err != nil {
		t.Fatal(err)
	}
	if unformed != 0 {
		t.Fatalf("%d turn(s) left unformed after the worker died", unformed)
	}
}
