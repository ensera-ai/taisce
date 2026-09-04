package recall_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/taisce/taisce/internal/domain"
	"github.com/taisce/taisce/internal/extract"
	"github.com/taisce/taisce/internal/formation"
	"github.com/taisce/taisce/internal/infra/pg"
	"github.com/taisce/taisce/internal/migrate"
	"github.com/taisce/taisce/internal/recall"
)

// ── The windowing, which needs no database ────────────────────────────────────────────────────

func TestAQuestionsWindowsAreTheShapeEntityNamesAreStoredIn(t *testing.T) {
	got := recall.Terms("Where does Marta work? At the Dublin Office, I think.")
	have := map[string]bool{}
	for _, term := range got {
		have[term] = true
	}

	// The multi-word window is the point. Without it, only single words are ever looked up and an
	// entity whose name is two words is unreachable by the name it is stored under.
	for _, want := range []string{"marta", "dublin office", "the dublin office"} {
		if !have[want] {
			t.Fatalf("%q is not among the candidate terms: %v", want, got)
		}
	}
	// Punctuation is dropped at the boundary, so a question mark does not make a word unmatchable.
	if !have["work"] {
		t.Fatalf("trailing punctuation left the term unmatchable: %v", got)
	}
	// Normalised exactly the way entity names are, because any other shape silently fails to match
	// entities that are in fact stored.
	for _, term := range got {
		if term != domain.NormalizeName(term) {
			t.Fatalf("%q is not in the shape entity names are stored in", term)
		}
	}
	// No duplicates: a repeated word would otherwise multiply the candidate list on long questions.
	if len(have) != len(got) {
		t.Fatalf("the candidate list repeats itself: %v", got)
	}
}

func TestAnApostropheOrHyphenInsideAWordSurvives(t *testing.T) {
	got := recall.Terms("Is the co-op still run by O'Brien?")
	have := map[string]bool{}
	for _, term := range got {
		have[term] = true
	}
	for _, want := range []string{"co-op", "o'brien"} {
		if !have[want] {
			t.Fatalf("%q was split apart: %v", want, got)
		}
	}
}

// ── The journey, against a real deployment ────────────────────────────────────────────────────

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

// The model is stubbed and derives its quotes from the message, so it cannot produce a quote the
// message does not contain — the property under test in this file is that a fact comes back with a
// span that resolves, and a stub free to invent one could hide a failure of exactly that.
type scriptedModel struct{}

func (scriptedModel) Propose(_ context.Context, message domain.Message, _ domain.Ontology) ([]extract.Proposal, error) {
	var out []extract.Proposal
	for _, p := range []struct {
		phrase, predicate, subject, object string
	}{
		{"Marta works at Ensera", "works_at", "Marta", "Ensera"},
		{"Ensera is part of the Dublin Office", "part_of", "Ensera", "Dublin Office"},
		{"I live in Cork", "lives_in", "user", "Cork"},
	} {
		if i := strings.Index(message.Content, p.phrase); i >= 0 {
			out = append(out, extract.Proposal{
				Subject: p.subject, Predicate: p.predicate, Object: p.object,
				Statement: p.phrase + ".", Confidence: 0.9,
				Quote: message.Content[i : i+len(p.phrase)],
			})
		}
	}
	return out, nil
}

// world provisions a tenant, appends turns, forms them, and hands back a recaller — the journey a
// caller makes, in the order they make it.
func world(t *testing.T, name string, turns ...domain.Turn) (*pgxpool.Pool, pg.Schema, *recall.Recaller) {
	t.Helper()
	ctx := context.Background()
	pool := testPool(t)
	if _, err := pool.Exec(ctx, `DROP SCHEMA IF EXISTS `+name+` CASCADE`); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if err := migrate.ProvisionTenant(ctx, pool, name); err != nil {
		t.Fatalf("provision tenant: %v", err)
	}
	schema, err := pg.NewSchema(name)
	if err != nil {
		t.Fatalf("schema: %v", err)
	}

	scopes := map[string]bool{}
	observations := pg.NewObservationStore(pool)
	vocabulary, err := pg.LoadOntology(ctx, pool, schema)
	if err != nil {
		t.Fatalf("vocabulary: %v", err)
	}
	worker := formation.NewWorker(pool, observations,
		formation.NewFormer(observations, pg.NewFactStore(pool), extract.New(scriptedModel{}, vocabulary)))

	for _, turn := range turns {
		if !scopes[turn.Scope] {
			if err := migrate.ProvisionScope(ctx, pool, name, turn.Scope); err != nil {
				t.Fatalf("provision scope %s: %v", turn.Scope, err)
			}
			scopes[turn.Scope] = true
		}
		if _, err := observations.Append(ctx, schema, turn); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	for scope := range scopes {
		if _, err := worker.Drain(ctx, schema, scope); err != nil {
			t.Fatalf("drain %s: %v", scope, err)
		}
		// Formed, so a recall now sees what was written. Asserted rather than assumed, because the
		// whole point of the second watermark is that this is a question with an answer.
		fresh, err := observations.Freshness(ctx, schema, scope)
		if err != nil {
			t.Fatalf("freshness %s: %v", scope, err)
		}
		if !fresh.HasFormed || fresh.Formed != fresh.Stored {
			t.Fatalf("%s is stored to %d and formed to %+v", scope, fresh.Stored, fresh)
		}
	}
	return pool, schema, recall.New(pg.NewRecallStore(pool, schema), 50)
}

func turn(scope string, contents ...string) domain.Turn {
	out := domain.Turn{
		Scope:      scope,
		OccurredAt: time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC),
	}
	for i, content := range contents {
		out.Messages = append(out.Messages,
			domain.Message{Ordinal: i, Role: domain.RoleUser, Content: content})
	}
	return out
}

// ── #3's claim ────────────────────────────────────────────────────────────────────────────────
//
// Observe, form, recall — and the fact comes back with its verbatim quote and byte span.
//
// This is the spine's read half in its thinnest form: the question resolves to an entity, the facts
// naming that entity come back, and each one carries the words that produced it. Nothing traverses,
// nothing ranks, nothing fuses.
func TestAQuestionAnchorsToAnEntityAndTheFactsComeBackCited(t *testing.T) {
	ctx := context.Background()
	pool, schema, recaller := world(t, "rc_journey",
		turn("p1",
			"Some chatter that says nothing at all.",
			"Marta works at Ensera, by the way."))

	bundle, err := recaller.Recall(ctx, []string{"p1"}, "Where does Marta work?")
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(bundle.Anchors) != 1 || bundle.Anchors[0].Entity.CanonicalName != "Marta" {
		t.Fatalf("expected to anchor on Marta, got %+v", bundle.Anchors)
	}
	if bundle.Anchors[0].Matched != "marta" {
		t.Fatalf("the anchor does not say what matched it: %+v", bundle.Anchors[0])
	}
	if len(bundle.Facts) != 1 {
		t.Fatalf("expected one fact, got %+v", bundle.Facts)
	}

	fact := bundle.Facts[0]
	if fact.Predicate != "works_at" || fact.Subject != "Marta" || fact.Object != "Ensera" {
		t.Fatalf("the fact is not the one the message asserted: %+v", fact)
	}
	if fact.Evidence.Quote != "Marta works at Ensera" {
		t.Fatalf("the quote is %q", fact.Evidence.Quote)
	}
	// The span, taken from the message the evidence NAMES, gives back the quote. That join is the
	// whole citation: against another message of the same turn it would return a plausible fragment
	// of the wrong sentence.
	var resolved string
	if err := pool.QueryRow(ctx, schema.SQL(`
		SELECT substring(text FROM $2 + 1 FOR $3 - $2)
		  FROM {schema}.chunk
		 WHERE source_observation_id = $1 AND source_message_ordinal = $4`),
		fact.Evidence.SourceObservationID, fact.Evidence.ByteStart, fact.Evidence.ByteEnd,
		fact.Evidence.SourceOrdinal).Scan(&resolved); err != nil {
		t.Fatalf("resolve citation: %v", err)
	}
	if resolved != fact.Evidence.Quote {
		t.Fatalf("the span resolved to %q but the quote is %q", resolved, fact.Evidence.Quote)
	}
	if fact.Evidence.SourceOrdinal != 1 {
		t.Fatalf("the evidence names message %d; the claim is in message 1", fact.Evidence.SourceOrdinal)
	}
}

// A fact is reachable through EITHER end.
//
// `Ensera` is the object of one fact and the subject of another. Both must come back, which is what
// the second arm of the union is for — and an entity that is only ever an object would otherwise be
// an anchor with nothing behind it.
func TestAnEntityIsAnAnchorFromEitherEndOfAFact(t *testing.T) {
	ctx := context.Background()
	_, _, recaller := world(t, "rc_ends",
		turn("p1", "Marta works at Ensera and Ensera is part of the Dublin Office."))

	bundle, err := recaller.Recall(ctx, []string{"p1"}, "Tell me about Ensera.")
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(bundle.Facts) != 2 {
		t.Fatalf("expected both facts naming Ensera, got %+v", bundle.Facts)
	}
	predicates := map[string]bool{}
	for _, f := range bundle.Facts {
		predicates[f.Predicate] = true
		if f.AnchoredOn == "" {
			t.Fatalf("a fact came back without saying which entity reached it: %+v", f)
		}
	}
	if !predicates["works_at"] || !predicates["part_of"] {
		t.Fatalf("only reached through one end: %v", predicates)
	}
}

// ── The soft boundary ─────────────────────────────────────────────────────────────────────────
//
// A project is a permission, so the authorised set is an argument on every read. A principal granted
// two projects recalls across both in one bundle; one granted a single project sees only it.
func TestARecallSeesOnlyTheProjectsItWasAuthorisedFor(t *testing.T) {
	ctx := context.Background()
	_, _, recaller := world(t, "rc_scope",
		turn("p1", "Marta works at Ensera."),
		turn("p2", "I live in Cork, since you ask."))

	only, err := recaller.Recall(ctx, []string{"p1"}, "What about Marta and Cork?")
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	for _, f := range only.Facts {
		if f.Scope != "p1" {
			t.Fatalf("a fact from %q came back for a caller authorised only for p1: %+v", f.Scope, f)
		}
	}
	if len(only.Facts) != 1 {
		t.Fatalf("expected only p1's fact, got %+v", only.Facts)
	}

	both, err := recaller.Recall(ctx, []string{"p1", "p2"}, "What about Marta and Cork?")
	if err != nil {
		t.Fatalf("recall across two projects: %v", err)
	}
	if len(both.Facts) != 2 {
		t.Fatalf("a principal granted both projects should recall across them: %+v", both.Facts)
	}
}

// An empty authorised set returns nothing.
//
// The failure being prevented is a read that WIDENS when its permission list is missing — the one
// mistake a permission list exists to stop, and the one that is invisible in a test suite where
// every caller happens to pass a scope.
func TestAnEmptyAuthorisedSetReturnsNothing(t *testing.T) {
	ctx := context.Background()
	_, _, recaller := world(t, "rc_noscope", turn("p1", "Marta works at Ensera."))

	bundle, err := recaller.Recall(ctx, nil, "Where does Marta work?")
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(bundle.Facts) != 0 || len(bundle.Anchors) != 0 {
		t.Fatalf("a caller with no authorised projects saw %+v", bundle)
	}
}

// A question naming nothing we know about is an empty bundle, not an error. It is most questions,
// early in a scope's life, and a caller made to distinguish it from a failure will end up treating
// failures as ordinary.
func TestAQuestionThatAnchorsToNothingIsAnEmptyBundle(t *testing.T) {
	ctx := context.Background()
	_, _, recaller := world(t, "rc_miss", turn("p1", "Marta works at Ensera."))

	bundle, err := recaller.Recall(ctx, []string{"p1"}, "What is the capital of Peru?")
	if err != nil {
		t.Fatalf("a question about nothing we know must not be an error: %v", err)
	}
	if len(bundle.Anchors) != 0 || len(bundle.Facts) != 0 {
		t.Fatalf("expected an empty bundle, got %+v", bundle)
	}
}

// A truncated bundle says so.
//
// Nothing has ranked the facts, so the limit drops arbitrary members rather than the least useful
// ones. A caller who does not know that will read the cut as a verdict — "these are the important
// ones" — which is the one thing this bundle is not claiming.
func TestATruncatedBundleSaysSo(t *testing.T) {
	ctx := context.Background()
	pool, schema, _ := world(t, "rc_limit",
		turn("p1", "Marta works at Ensera and Ensera is part of the Dublin Office."))

	recaller := recall.New(pg.NewRecallStore(pool, schema), 1)
	bundle, err := recaller.Recall(ctx, []string{"p1"}, "Tell me about Ensera.")
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(bundle.Facts) != 1 {
		t.Fatalf("the limit was not applied: %+v", bundle.Facts)
	}
	if !bundle.Truncated {
		t.Fatal("the bundle was cut and did not say so")
	}
}
