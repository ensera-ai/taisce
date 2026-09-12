package recall_test

import (
	"context"
	"fmt"
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
	"github.com/ensera-ai/taisce/internal/recall"
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
			out = append(out, extract.Proposal{Polarity: domain.PolarityAsserted, Tense: domain.TensePresent,
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
	if err := migrate.ProvisionMemorySchema(ctx, pool, name); err != nil {
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
	return pool, schema, recall.NewWithBudget(pg.NewRecallStore(pool, schema), recall.Budget{Characters: 100000, MaxRows: 50})
}

func turn(scope string, contents ...string) domain.Turn {
	out := domain.Turn{
		Scope:         scope,
		DataSubjectID: "recall-fixture-subject",
		OccurredAt:    time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC),
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

	recaller := recall.NewWithBudget(pg.NewRecallStore(pool, schema), recall.Budget{Characters: 100000, MaxRows: 1})
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

// A bundle cap of zero or less falls back rather than returning nothing.
//
// A cap is a bound on how much comes back, and a caller who passes a nonsense one has made a
// configuration mistake — answering every question with an empty bundle would look exactly like a
// memory that holds nothing, which is the most expensive way to report a typo.
func TestANonsenseBundleCapFallsBackRatherThanReturningNothing(t *testing.T) {
	for _, b := range []recall.Budget{
		{}, {Characters: 0, MaxRows: 0}, {Characters: -1, MaxRows: -100},
	} {
		r := recall.NewWithBudget(stubStore{}, b)
		bundle, err := r.Recall(context.Background(), []string{"p1"}, "anything at all")
		if err != nil {
			t.Fatalf("budget %+v: %v", b, err)
		}
		// The stub returns nothing, so what is asserted is that the call is well-formed rather than
		// short-circuited by a budget of zero.
		if bundle.Truncated {
			t.Fatalf("budget %+v reported a truncated bundle from an empty store", b)
		}
	}
}

// stubStore answers nothing, which is enough to exercise the cap.
type stubStore struct{}

func (stubStore) AnchorsForSubject(context.Context, []string, []string, string) ([]domain.Anchor, error) {
	return nil, nil
}

func (stubStore) FactsAboutForSubject(context.Context, []string, []string, int, []string, domain.AsOf, int, string) ([]domain.CitedFact, error) {
	return nil, nil
}

// ── #81: facts from one sentence are not two sources ──────────────────────────────────────────
//
// One sentence can produce several facts at different granularities — a preference stated broadly and
// the same preference stated as a rule. They are different triples, so nothing collapses them: no key
// to be unique on, and no similarity threshold that separates a restatement from a genuinely
// different claim.
//
// What can be established exactly is that they are not independent. That is the harm — a reader
// seeing two rows and weighing them as two sources, when one person said one thing once.

func coDerivedStore(facts []domain.CitedFact) stubFactStore { return stubFactStore{facts: facts} }

type stubFactStore struct{ facts []domain.CitedFact }

func (s stubFactStore) AnchorsForSubject(context.Context, []string, []string, string) ([]domain.Anchor, error) {
	return []domain.Anchor{{Entity: domain.Entity{ID: "e1", CanonicalName: "Marta"}, Matched: "marta"}}, nil
}

func (s stubFactStore) FactsAboutForSubject(context.Context, []string, []string, int, []string, domain.AsOf, int, string) ([]domain.CitedFact, error) {
	return s.facts, nil
}

func TestFactsFromOneMessageAboutOneSubjectAreMarkedNotIndependent(t *testing.T) {
	facts := []domain.CitedFact{
		{ID: "f1", Subject: "Marta", Predicate: "prefers", Object: "written docs",
			Evidence: domain.Evidence{SourceObservationID: "obs-1", SourceOrdinal: 0}},
		{ID: "f2", Subject: "Marta", Predicate: "requires", Object: "writing things down",
			Evidence: domain.Evidence{SourceObservationID: "obs-1", SourceOrdinal: 0}},
		// Same subject, a different message: genuinely independent.
		{ID: "f3", Subject: "Marta", Predicate: "works_at", Object: "Ensera",
			Evidence: domain.Evidence{SourceObservationID: "obs-2", SourceOrdinal: 0}},
	}
	bundle, err := recall.NewWithBudget(coDerivedStore(facts), recall.Budget{Characters: 100000, MaxRows: 50}).
		Recall(context.Background(), []string{"p1"}, "what about Marta")
	if err != nil {
		t.Fatalf("recall: %v", err)
	}

	by := map[string][]string{}
	for _, f := range bundle.Facts {
		by[f.ID] = f.CoDerivedWith
	}
	if len(by["f1"]) != 1 || by["f1"][0] != "f2" {
		t.Fatalf("f1 co-derived with %v, wanted f2", by["f1"])
	}
	if len(by["f2"]) != 1 || by["f2"][0] != "f1" {
		t.Fatalf("f2 co-derived with %v, wanted f1", by["f2"])
	}
	if len(by["f3"]) != 0 {
		t.Fatalf("a fact from another message was marked co-derived: %v", by["f3"])
	}
}

// Two messages in one turn are two speakers. Facts from different ones are genuinely independent, and
// keying on the turn rather than the message would erase that.
func TestFactsFromDifferentMessagesOfOneTurnAreIndependent(t *testing.T) {
	facts := []domain.CitedFact{
		{ID: "f1", Subject: "Marta", Predicate: "prefers", Object: "docs",
			Evidence: domain.Evidence{SourceObservationID: "obs-1", SourceOrdinal: 0}},
		{ID: "f2", Subject: "Marta", Predicate: "prefers", Object: "docs",
			Evidence: domain.Evidence{SourceObservationID: "obs-1", SourceOrdinal: 1}},
	}
	bundle, err := recall.NewWithBudget(coDerivedStore(facts), recall.Budget{Characters: 100000, MaxRows: 50}).
		Recall(context.Background(), []string{"p1"}, "what about Marta")
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	for _, f := range bundle.Facts {
		if len(f.CoDerivedWith) != 0 {
			t.Fatalf("fact %s from message %d was marked co-derived with %v",
				f.ID, f.Evidence.SourceOrdinal, f.CoDerivedWith)
		}
	}
}

// Different subjects in one sentence are different claims. "Marta manages the Cork team" says
// something about Marta and something about the team, and neither restates the other.
func TestFactsAboutDifferentSubjectsAreIndependent(t *testing.T) {
	facts := []domain.CitedFact{
		{ID: "f1", Subject: "Marta", Predicate: "manages", Object: "Cork team",
			Evidence: domain.Evidence{SourceObservationID: "obs-1", SourceOrdinal: 0}},
		{ID: "f2", Subject: "Cork team", Predicate: "part_of", Object: "Ensera",
			Evidence: domain.Evidence{SourceObservationID: "obs-1", SourceOrdinal: 0}},
	}
	bundle, err := recall.NewWithBudget(coDerivedStore(facts), recall.Budget{Characters: 100000, MaxRows: 50}).
		Recall(context.Background(), []string{"p1"}, "what about Marta")
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	for _, f := range bundle.Facts {
		if len(f.CoDerivedWith) != 0 {
			t.Fatalf("%s was marked co-derived with %v", f.ID, f.CoDerivedWith)
		}
	}
}

// The subject is compared the way entities are resolved, so casing does not make one person two.
func TestCoDerivationComparesSubjectsTheWayEntitiesResolve(t *testing.T) {
	facts := []domain.CitedFact{
		{ID: "f1", Subject: "Marta", Predicate: "prefers", Object: "docs",
			Evidence: domain.Evidence{SourceObservationID: "obs-1", SourceOrdinal: 0}},
		{ID: "f2", Subject: "  MARTA ", Predicate: "requires", Object: "writing",
			Evidence: domain.Evidence{SourceObservationID: "obs-1", SourceOrdinal: 0}},
	}
	bundle, err := recall.NewWithBudget(coDerivedStore(facts), recall.Budget{Characters: 100000, MaxRows: 50}).
		Recall(context.Background(), []string{"p1"}, "what about Marta")
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(bundle.Facts[0].CoDerivedWith) != 1 {
		t.Fatal("casing made one subject into two")
	}
}

// Only what is in the bundle is named. A truncated bundle that pointed at a fact the caller cannot
// see would be a citation resolving to nothing.
func TestCoDerivationNamesOnlyWhatTheCallerCanSee(t *testing.T) {
	var facts []domain.CitedFact
	for i := 0; i < 6; i++ {
		facts = append(facts, domain.CitedFact{
			ID: fmt.Sprintf("f%d", i), Subject: "Marta", Predicate: "prefers",
			Object:   fmt.Sprintf("thing-%d", i),
			Evidence: domain.Evidence{SourceObservationID: "obs-1", SourceOrdinal: 0},
		})
	}
	bundle, err := recall.NewWithBudget(coDerivedStore(facts), recall.Budget{Characters: 100000, MaxRows: 3}).
		Recall(context.Background(), []string{"p1"}, "what about Marta")
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if !bundle.Truncated {
		t.Fatal("the fixture did not truncate")
	}
	visible := map[string]bool{}
	for _, f := range bundle.Facts {
		visible[f.ID] = true
	}
	for _, f := range bundle.Facts {
		for _, id := range f.CoDerivedWith {
			if !visible[id] {
				t.Fatalf("%s points at %s, which was cut from the bundle", f.ID, id)
			}
		}
	}
}

// A fact whose subject or evidence is gone — an erased entity — is independent of nothing rather than
// co-derived with everything, which is what an empty key would make it.
func TestAFactWithNoSubjectIsNotCoDerivedWithEverything(t *testing.T) {
	facts := []domain.CitedFact{
		{ID: "f1", Subject: "", Predicate: "prefers", Object: "docs",
			Evidence: domain.Evidence{SourceObservationID: "obs-1", SourceOrdinal: 0}},
		{ID: "f2", Subject: "", Predicate: "requires", Object: "writing",
			Evidence: domain.Evidence{SourceObservationID: "obs-1", SourceOrdinal: 0}},
	}
	bundle, err := recall.NewWithBudget(coDerivedStore(facts), recall.Budget{Characters: 100000, MaxRows: 50}).
		Recall(context.Background(), []string{"p1"}, "what about Marta")
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	for _, f := range bundle.Facts {
		if len(f.CoDerivedWith) != 0 {
			t.Fatalf("a fact with no subject was grouped: %v", f.CoDerivedWith)
		}
	}
}

// ── #80: a recall says what it had to work with ───────────────────────────────────────────────
//
// Every mechanism that orders candidates says which are BEST and none says whether any is GOOD.
// Entity anchoring makes the worst version impossible — a question naming nothing known reaches
// nothing — and does not prevent a question that names something known and then asks about something
// else, which comes back full and plausible.

// A question that names nothing this memory holds says so, rather than returning an empty bundle that
// looks like having nothing to say.
func TestAQuestionNamingNothingKnownSaysSoRatherThanLookingSilent(t *testing.T) {
	bundle, err := recall.NewWithBudget(emptyAnchorStore{}, recall.Budget{Characters: 100000, MaxRows: 50}).
		Recall(context.Background(), []string{"p1"}, "what is our Kubernetes cluster configuration")
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(bundle.Facts) != 0 {
		t.Fatal("facts came back for a question naming nothing known")
	}
	if bundle.Reach.Terms == 0 {
		t.Fatal("the question offered candidate names and the bundle reports none")
	}
	if bundle.Reach.Anchored != 0 {
		t.Fatalf("%d anchors from a store that matches nothing", bundle.Reach.Anchored)
	}
	if !bundle.Reach.NamedNothingKnown() {
		t.Fatal("the bundle does not distinguish 'never heard of it' from 'nothing to say about it'")
	}
}

// A question that names something known and finds no facts is the OTHER empty answer, and must not
// claim the thing is unknown.
func TestAKnownSubjectWithNoFactsIsNotTheSameAsAnUnknownOne(t *testing.T) {
	bundle, err := recall.NewWithBudget(coDerivedStore(nil), recall.Budget{Characters: 100000, MaxRows: 50}).
		Recall(context.Background(), []string{"p1"}, "what about Marta")
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if bundle.Reach.Anchored == 0 {
		t.Fatal("the fixture anchors on Marta and the bundle says it anchored on nothing")
	}
	if bundle.Reach.NamedNothingKnown() {
		t.Fatal("a known subject with no facts was reported as an unknown one")
	}
}

// Where the facts came from is visible, so a question that named several things and drew everything
// from one is recognisable without anybody choosing a threshold.
func TestTheBundleSaysWhereItsFactsCameFrom(t *testing.T) {
	facts := []domain.CitedFact{
		{ID: "f1", Subject: "Marta", Predicate: "works_at", AnchoredOn: "e1",
			Evidence: domain.Evidence{SourceObservationID: "o1"}},
		{ID: "f2", Subject: "Marta", Predicate: "lives_in", AnchoredOn: "e1",
			Evidence: domain.Evidence{SourceObservationID: "o2"}},
		{ID: "f3", Subject: "Cork team", Predicate: "part_of", AnchoredOn: "e2",
			Evidence: domain.Evidence{SourceObservationID: "o3"}},
	}
	bundle, err := recall.NewWithBudget(coDerivedStore(facts), recall.Budget{Characters: 100000, MaxRows: 50}).
		Recall(context.Background(), []string{"p1"}, "what about Marta")
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if bundle.Reach.FactsPerAnchor["e1"] != 2 || bundle.Reach.FactsPerAnchor["e2"] != 1 {
		t.Fatalf("facts per anchor: %v", bundle.Reach.FactsPerAnchor)
	}
}

// The counts describe what the caller received. Counting rows that were cut would describe something
// else.
func TestTheCountsDescribeWhatTheCallerGot(t *testing.T) {
	var facts []domain.CitedFact
	for i := 0; i < 6; i++ {
		facts = append(facts, domain.CitedFact{
			ID: fmt.Sprintf("f%d", i), Subject: "Marta", Predicate: "prefers", AnchoredOn: "e1",
			Evidence: domain.Evidence{SourceObservationID: fmt.Sprintf("o%d", i)},
		})
	}
	bundle, err := recall.NewWithBudget(coDerivedStore(facts), recall.Budget{Characters: 100000, MaxRows: 2}).
		Recall(context.Background(), []string{"p1"}, "what about Marta")
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if !bundle.Truncated {
		t.Fatal("the fixture did not truncate")
	}
	total := 0
	for _, n := range bundle.Reach.FactsPerAnchor {
		total += n
	}
	if total != len(bundle.Facts) {
		t.Fatalf("the counts total %d and the caller received %d", total, len(bundle.Facts))
	}
}

// emptyAnchorStore is a memory that holds nothing the question names.
type emptyAnchorStore struct{}

func (emptyAnchorStore) AnchorsForSubject(context.Context, []string, []string, string) ([]domain.Anchor, error) {
	return nil, nil
}

func (emptyAnchorStore) FactsAboutForSubject(context.Context, []string, []string, int, []string, domain.AsOf, int, string) ([]domain.CitedFact, error) {
	return nil, nil
}

// ── #61: the bundle is cut in a unit a caller can convert ─────────────────────────────────────

// The cut is by characters, not rows, because a row is not a size: one fact carries a two-word quote
// and another carries a paragraph, so a caller converting rows to anything has to assume the worst
// every time to avoid overrunning once.
func TestTheBundleIsCutByWhatTheCallerCanAfford(t *testing.T) {
	long := strings.Repeat("a paragraph of recorded conversation. ", 20)
	facts := []domain.CitedFact{
		{ID: "f1", Subject: "Marta", Predicate: "works_at", Object: "Ensera", Statement: "short",
			AnchoredOn: "e1", Evidence: domain.Evidence{SourceObservationID: "o1", Quote: "short"}},
		{ID: "f2", Subject: "Marta", Predicate: "prefers", Object: "docs", Statement: long,
			AnchoredOn: "e1", Evidence: domain.Evidence{SourceObservationID: "o2", Quote: long}},
		{ID: "f3", Subject: "Marta", Predicate: "lives_in", Object: "Dublin", Statement: "short",
			AnchoredOn: "e1", Evidence: domain.Evidence{SourceObservationID: "o3", Quote: "short"}},
	}
	// Enough for the first and nowhere near enough for the second.
	r := recall.NewWithBudget(coDerivedStore(facts), recall.Budget{Characters: 100, MaxRows: 50})
	bundle, err := r.Recall(context.Background(), []string{"p1"}, "what about Marta")
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(bundle.Facts) != 1 || bundle.Facts[0].ID != "f1" {
		t.Fatalf("got %d facts, wanted the one that fits", len(bundle.Facts))
	}
	if !bundle.Truncated {
		t.Fatal("the bundle was cut and does not say so")
	}
	if bundle.Characters == 0 || bundle.Characters > 100 {
		t.Fatalf("reported %d characters against a budget of 100", bundle.Characters)
	}
}

// A too-small context allowance returns no facts and explicitly reports the budget cut.
func TestABudgetTooSmallForOneFactReturnsAnExplicitlyTruncatedEmptyBundle(t *testing.T) {
	long := strings.Repeat("x", 5000)
	facts := []domain.CitedFact{
		{ID: "f1", Subject: "Marta", Statement: long, AnchoredOn: "e1",
			Evidence: domain.Evidence{SourceObservationID: "o1", Quote: long}},
		{ID: "f2", Subject: "Marta", Statement: long, AnchoredOn: "e1",
			Evidence: domain.Evidence{SourceObservationID: "o2", Quote: long}},
	}
	bundle, err := recall.NewWithBudget(coDerivedStore(facts), recall.Budget{Characters: 10, MaxRows: 50}).
		Recall(context.Background(), []string{"p1"}, "what about Marta")
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(bundle.Facts) != 0 || bundle.Characters != 0 {
		t.Fatalf("a too-small budget returned %d facts costing %d", len(bundle.Facts), bundle.Characters)
	}
	if !bundle.Truncated {
		t.Fatal("the bundle was cut and does not say so")
	}
}

// A generous budget returns everything and does not claim truncation.
func TestAGenerousBudgetReturnsEverythingAndSaysSo(t *testing.T) {
	facts := []domain.CitedFact{
		{ID: "f1", Subject: "Marta", Statement: "a", AnchoredOn: "e1",
			Evidence: domain.Evidence{SourceObservationID: "o1", Quote: "a"}},
		{ID: "f2", Subject: "Marta", Statement: "b", AnchoredOn: "e1",
			Evidence: domain.Evidence{SourceObservationID: "o2", Quote: "b"}},
	}
	bundle, err := recall.NewWithBudget(coDerivedStore(facts), recall.Budget{Characters: 100000, MaxRows: 50}).
		Recall(context.Background(), []string{"p1"}, "what about Marta")
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(bundle.Facts) != 2 {
		t.Fatalf("got %d of 2 facts", len(bundle.Facts))
	}
	if bundle.Truncated {
		t.Fatal("a bundle that returned everything claims it was cut")
	}
}

// The row bound guards the query rather than the answer: an enormous budget must not become an
// unbounded read.
func TestTheRowBoundGuardsTheQuery(t *testing.T) {
	var facts []domain.CitedFact
	for i := 0; i < 10; i++ {
		facts = append(facts, domain.CitedFact{
			ID: fmt.Sprintf("f%d", i), Subject: "Marta", Statement: "s", AnchoredOn: "e1",
			Evidence: domain.Evidence{SourceObservationID: fmt.Sprintf("o%d", i), Quote: "q"},
		})
	}
	bundle, err := recall.NewWithBudget(coDerivedStore(facts),
		recall.Budget{Characters: 1000000, MaxRows: 3}).
		Recall(context.Background(), []string{"p1"}, "what about Marta")
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(bundle.Facts) != 3 {
		t.Fatalf("the row bound was %d and %d facts came back", 3, len(bundle.Facts))
	}
	if !bundle.Truncated {
		t.Fatal("the row bound cut the result and the bundle does not say so")
	}
}

// ── #60: what a legible citation costs, and what it says when it is missing ───────────────────

// The context is on the wire, so the caller pays for it. A budget that counted only the quote would
// describe a payload smaller than the one that arrives, which is the failure a budget exists to
// prevent — and it would be believed, because it is labelled characters.
func TestTheWordsAroundAQuoteAreChargedToTheBudget(t *testing.T) {
	context160 := strings.Repeat("y", 300)
	bare := domain.CitedFact{ID: "f1", Subject: "Marta", Predicate: "works_at", Object: "Ensera",
		Statement: "Marta works at Ensera.", AnchoredOn: "e1",
		Evidence: domain.Evidence{SourceObservationID: "o1", Quote: "works at Ensera",
			Context: domain.Context{Text: "works at Ensera", Complete: true}}}
	legible := bare
	legible.ID = "f2"
	legible.Evidence.SourceObservationID = "o2"
	legible.Evidence.Context = domain.Context{Text: context160}

	cheap, err := recall.NewWithBudget(coDerivedStore([]domain.CitedFact{bare}),
		recall.Budget{Characters: 100000, MaxRows: 50}).
		Recall(context.Background(), []string{"p1"}, "what about Marta")
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	dear, err := recall.NewWithBudget(coDerivedStore([]domain.CitedFact{legible}),
		recall.Budget{Characters: 100000, MaxRows: 50}).
		Recall(context.Background(), []string{"p1"}, "what about Marta")
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if dear.Characters-cheap.Characters != len(context160)-len("works at Ensera") {
		t.Fatalf("the context was not charged: %d against %d", dear.Characters, cheap.Characters)
	}

	// And a budget that cannot afford the context cuts the bundle rather than the context: a fact
	// half-cited is worse than a fact not returned, because nothing on the wire would say so.
	cut, err := recall.NewWithBudget(coDerivedStore([]domain.CitedFact{bare, legible}),
		recall.Budget{Characters: 120, MaxRows: 50}).
		Recall(context.Background(), []string{"p1"}, "what about Marta")
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(cut.Facts) != 1 || cut.Facts[0].ID != "f1" {
		t.Fatalf("got %d facts, wanted the one whose citation fits", len(cut.Facts))
	}
	if cut.Facts[0].Evidence.Context.Text == "" {
		t.Fatal("a fact was returned with its context stripped to fit, which nothing on the wire says")
	}
}

// A bare citation is announced once for the bundle. Which facts are bare is visible in the facts
// themselves; what a caller cannot see without being told is that anything is bare at all.
func TestABundleSaysWhenACitationLostItsContext(t *testing.T) {
	legible := domain.CitedFact{ID: "f1", Subject: "Marta", Predicate: "works_at", Object: "Ensera",
		Statement: "Marta works at Ensera.", AnchoredOn: "e1",
		Evidence: domain.Evidence{SourceObservationID: "o1", Quote: "works at Ensera",
			Context: domain.Context{Text: "Marta works at Ensera.", Complete: true}}}
	bare := legible
	bare.ID = "f2"
	bare.Evidence.SourceObservationID = "o2"
	bare.Evidence.Context = domain.Context{}

	whole, err := recall.NewWithBudget(coDerivedStore([]domain.CitedFact{legible}),
		recall.Budget{Characters: 100000, MaxRows: 50}).
		Recall(context.Background(), []string{"p1"}, "what about Marta")
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(whole.Degraded) != 0 {
		t.Fatalf("a bundle whose citations are all legible reported %v", whole.Degraded)
	}

	partial, err := recall.NewWithBudget(coDerivedStore([]domain.CitedFact{legible, bare}),
		recall.Budget{Characters: 100000, MaxRows: 50}).
		Recall(context.Background(), []string{"p1"}, "what about Marta")
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(partial.Degraded) != 1 || partial.Degraded[0] != domain.DegradedCitationContext {
		t.Fatalf("a bare citation was not reported: %v", partial.Degraded)
	}
}

// ── #8: the instants a caller names reach the store unchanged ─────────────────────────────────

type recordingStore struct {
	facts []domain.CitedFact
	asked domain.AsOf
	hops  int
}

func (s *recordingStore) AnchorsForSubject(context.Context, []string, []string, string) ([]domain.Anchor, error) {
	return []domain.Anchor{{Entity: domain.Entity{ID: "e1", CanonicalName: "Marta"}, Matched: "marta"}}, nil
}

func (s *recordingStore) FactsAboutForSubject(_ context.Context, _, _ []string, _ int, _ []string,
	at domain.AsOf, hops int, _ string) ([]domain.CitedFact, error) {
	s.hops = hops
	s.asked = at
	return s.facts, nil
}

// The ordinary recall asks about now, and "now" is the zero value rather than a timestamp this
// process computed — the store turns that into the open interval, which is the only version that does
// not depend on two clocks agreeing.
func TestAnOrdinaryRecallNamesNoInstant(t *testing.T) {
	store := &recordingStore{}
	if _, err := recall.NewWithBudget(store, recall.DefaultBudget()).
		Recall(context.Background(), []string{"p1"}, "what about Marta"); err != nil {
		t.Fatalf("recall: %v", err)
	}
	if !store.asked.IsNow() {
		t.Fatalf("the current read named an instant: %+v", store.asked)
	}
}

// And a historical recall is the same read with two instants on it: same anchoring, same scopes, same
// sources, same budget. A past question is not a different product.
func TestAHistoricalRecallPassesBothInstantsThrough(t *testing.T) {
	store := &recordingStore{}
	valid := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	known := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	if _, err := recall.NewWithBudget(store, recall.DefaultBudget()).
		RecallAsOf(context.Background(), []string{"p1"}, "what about Marta",
			recall.PrincipalSources, domain.AsOf{Valid: valid, Known: known}, recall.DirectHop); err != nil {
		t.Fatalf("recall: %v", err)
	}
	if !store.asked.Valid.Equal(valid) || !store.asked.Known.Equal(known) {
		t.Fatalf("the instants did not reach the store: %+v", store.asked)
	}
	if store.asked.IsNow() {
		t.Fatal("a read naming two instants was treated as the current read")
	}
}

// ── #6: how far a question may reach ──────────────────────────────────────────────────────────

// The ordinary recall asks for one hop. A second is paid for by every caller who did not need one,
// and at two hops a bundle contains facts about things the question never named — useful for "who
// does Marta's manager work for" and noise for "where does Marta live". Nothing here can tell which
// question it was given, so the caller says.
func TestAnOrdinaryRecallAsksForOneHop(t *testing.T) {
	store := &recordingStore{}
	if _, err := recall.NewWithBudget(store, recall.DefaultBudget()).
		Recall(context.Background(), []string{"p1"}, "what about Marta"); err != nil {
		t.Fatalf("recall: %v", err)
	}
	if store.hops != recall.DirectHop {
		t.Fatalf("the ordinary recall asked for %d hops", store.hops)
	}
}

// A depth beyond what the traversal does is clamped rather than refused: a caller asking for more
// memory gets the deepest answer the system gives, which is the useful outcome. Refusing would turn
// a request for more into an error on a path that otherwise cannot fail.
func TestADepthBeyondTheBoundIsClampedRatherThanRefused(t *testing.T) {
	for asked, want := range map[int]int{
		-5:                 recall.DirectHop,
		0:                  recall.DirectHop,
		1:                  1,
		2:                  2,
		9:                  recall.MaxHops,
		1 << 20:            recall.MaxHops,
		recall.MaxHops + 1: recall.MaxHops,
	} {
		store := &recordingStore{}
		if _, err := recall.NewWithBudget(store, recall.DefaultBudget()).
			RecallAsOf(context.Background(), []string{"p1"}, "what about Marta",
				recall.PrincipalSources, domain.AsOf{}, asked); err != nil {
			t.Fatalf("recall: %v", err)
		}
		if store.hops != want {
			t.Errorf("asking for %d hops reached the store as %d, wanted %d", asked, store.hops, want)
		}
	}
}
