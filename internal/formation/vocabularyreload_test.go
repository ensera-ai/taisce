package formation_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/extract"
	"github.com/ensera-ai/taisce/internal/formation"
	"github.com/ensera-ai/taisce/internal/infra/pg"
)

// ── The vocabulary a turn is extracted under is the one the database holds ─────────────────────
//
// A relation is added to the vocabulary while the worker is running. That is the one operation an
// operator has for fitting an instance to its own domain, and it is only an operation if extraction
// then admits the relation: a worker holding the set it read at startup refuses the turn with the
// same refusal the operator was trying to remove, and nothing in the refusal says the process is
// behind.
//
// This is written against the worker rather than the extractor because the extractor is doing the
// right thing with the vocabulary it was handed. What is under test is which vocabulary a turn is
// handed, which is a property of the thing that runs for longer than the vocabulary stays still.

// reloadingWorker is a worker whose former reads the vocabulary at the top of every turn, built the
// way the serving process builds it.
func reloadingWorker(t *testing.T, pool *pgxpool.Pool, schema pg.Schema,
	model extract.Model) (*formation.Worker, *pg.ObservationStore) {
	t.Helper()
	vocabulary, err := pg.LoadVocabulary(context.Background(), pool, schema)
	if err != nil {
		t.Fatalf("load vocabulary: %v", err)
	}
	observations := pg.NewObservationStore(pool)
	former := formation.NewFormer(observations, pg.NewFactStore(pool),
		extract.NewWith(model, vocabulary)).
		ReadingVocabularyFrom(pg.NewVocabularyReader(pool, schema))
	return formation.NewWorker(pool, observations, former), observations
}

// commutesBy is the relation this file adds: not in the seeded set, and named by the scripted model
// so that the only thing standing between the proposal and a fact is whether the vocabulary admits
// it.
var commutesBy = pg.NewPredicate{
	Predicate:    "commutes_by",
	SemanticType: "state",
	Cardinality:  "many",
	ObjectKind:   "thing",
	Description:  "Something the subject commutes by.",
}

const commutesByTurn = "I commutes by bicycle."

// A relation added over the management surface is admitted by the next turn, with the same worker
// still running.
//
// Before the vocabulary is read per turn this test fails on its second half: the relation is in the
// table, the foreign key would admit a fact carrying it, and the turn is still refused as
// `unmapped_relation` because the running worker is extracting against the list it loaded.
func TestARelationAddedToTheVocabularyIsAdmittedByTheNextTurnWithoutRestartingTheWorker(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "fm_vocab_reload")
	worker, observations := reloadingWorker(t, pool, schema, scriptedModel{})

	// Before the relation exists, the refusal is the correct answer: the vocabulary is closed, and
	// a relation nobody declared has no cardinality, so supersession could not be decided for it.
	if _, err := observations.Append(ctx, schema, turnOf(
		domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: commutesByTurn},
	)); err != nil {
		t.Fatalf("append the turn before the relation exists: %v", err)
	}
	before, err := worker.Drain(ctx, schema, "p1")
	if err != nil {
		t.Fatalf("drain before: %v", err)
	}
	if len(before) != 1 || before[0].ClaimsRejected != 1 || before[0].FactsAsserted != 0 {
		t.Fatalf("expected the relation refused while it is undeclared, got %+v", before)
	}

	// Added the way an operator adds it, over the store the management surface holds, rather than
	// with an INSERT of this test's own: what is under test is the operation they actually have.
	if _, err := pg.NewVocabularyStore(pool, schema).Add(ctx, commutesBy); err != nil {
		t.Fatalf("extend the vocabulary: %v", err)
	}

	// The same worker, never restarted, never rebuilt. If this half passes only after a restart,
	// the operation is one an operator cannot use without a deployment action.
	if _, err := observations.Append(ctx, schema, turnOf(
		domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: commutesByTurn},
	)); err != nil {
		t.Fatalf("append the turn after the relation exists: %v", err)
	}
	after, err := worker.Drain(ctx, schema, "p1")
	if err != nil {
		t.Fatalf("drain after: %v", err)
	}
	if len(after) != 1 || after[0].FactsAsserted != 1 || after[0].ClaimsRejected != 0 {
		t.Fatalf("a relation the vocabulary now admits was still refused by the running worker: %+v", after)
	}

	var predicate string
	if err := pool.QueryRow(ctx, schema.SQL(
		`SELECT predicate FROM {schema}.fact`)).Scan(&predicate); err != nil {
		t.Fatalf("read the fact: %v", err)
	}
	if predicate != commutesBy.Predicate {
		t.Fatalf("the fact carries %q rather than the relation that was added", predicate)
	}
}

// identifiedScriptedModel is the scripted model with a provenance identity, so that the version
// pinned to a turn is the vocabulary-bearing one rather than the legacy label an unidentified
// proposer gets.
type identifiedScriptedModel struct{ scriptedModel }

func (identifiedScriptedModel) ExtractionIdentity() string { return "scripted/vocabulary-reload" }

// The identity pinned to a turn names the vocabulary that turn was extracted under.
//
// This is what makes reading the vocabulary per turn safe rather than merely convenient. The
// identity is the record of what produced a fact, the vocabulary is folded into it, and the two
// would be a lie about each other if the set could move between the moment the identity is taken
// and the moment the claims are proposed. Here the vocabulary changes between two turns and the two
// turns are stamped differently — so a reader can tell which vocabulary each was formed under, and a
// rebuild can find the earlier ones.
func TestTheExtractionIdentityPinnedToATurnNamesTheVocabularyThatTurnWasExtractedUnder(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "fm_vocab_identity")
	worker, observations := reloadingWorker(t, pool, schema, identifiedScriptedModel{})

	first, err := observations.Append(ctx, schema, turnOf(
		domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "I work at Ensera."},
	))
	if err != nil {
		t.Fatalf("append the first turn: %v", err)
	}
	if _, err := worker.Drain(ctx, schema, "p1"); err != nil {
		t.Fatalf("drain the first turn: %v", err)
	}

	if _, err := pg.NewVocabularyStore(pool, schema).Add(ctx, commutesBy); err != nil {
		t.Fatalf("extend the vocabulary: %v", err)
	}

	second, err := observations.Append(ctx, schema, turnOf(
		domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "I work at Ensera."},
	))
	if err != nil {
		t.Fatalf("append the second turn: %v", err)
	}
	if _, err := worker.Drain(ctx, schema, "p1"); err != nil {
		t.Fatalf("drain the second turn: %v", err)
	}

	version := func(source string) string {
		t.Helper()
		var v string
		if err := pool.QueryRow(ctx, schema.SQL(
			`SELECT extractor_version FROM {schema}.source_extraction
			  WHERE scope='p1' AND source_observation_id=$1::uuid`), source).Scan(&v); err != nil {
			t.Fatalf("read the pinned version for %s: %v", source, err)
		}
		return v
	}
	one, two := version(first.ID), version(second.ID)
	if one == two {
		t.Fatalf("both turns are stamped %q, so nothing records that the vocabulary changed between them", one)
	}
}

// refusingVocabularies is the vocabulary unreadable: the tenant's tables cannot be reached for this
// turn.
type refusingVocabularies struct{}

var errVocabularyUnreadable = errors.New("the vocabulary could not be read")

func (refusingVocabularies) Vocabulary(context.Context) (domain.Vocabulary, error) {
	return domain.Vocabulary{}, errVocabularyUnreadable
}

// A turn whose vocabulary cannot be read is refused rather than formed under the previous one.
//
// The tempting failure is to carry on with the set from last time, which would keep the backlog
// moving. It would also stamp those facts with an identity naming a vocabulary this process cannot
// currently show anybody — and a fact formed under an unverifiable vocabulary is not recoverable,
// whereas a turn that waits is. So the turn fails and is retried.
func TestATurnWhoseVocabularyCannotBeReadIsRefusedRatherThanFormedUnderAStaleOne(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "fm_vocab_unreadable")

	vocabulary, err := pg.LoadVocabulary(ctx, pool, schema)
	if err != nil {
		t.Fatalf("load vocabulary: %v", err)
	}
	observations := pg.NewObservationStore(pool)
	former := formation.NewFormer(observations, pg.NewFactStore(pool),
		extract.NewWith(scriptedModel{}, vocabulary)).
		ReadingVocabularyFrom(refusingVocabularies{})

	stored, err := observations.Append(ctx, schema, turnOf(
		domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "I work at Ensera."},
	))
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	observation, found, err := observations.NextUnformed(ctx, schema, "p1", 0)
	if err != nil || !found || observation.ID != stored.ID {
		t.Fatalf("expected the stored turn to be offered: found=%v err=%v", found, err)
	}
	if _, err := former.Form(ctx, schema, observation); !errors.Is(err, errVocabularyUnreadable) {
		t.Fatalf("expected the turn refused with the read failure, got %v", err)
	}

	var facts int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.fact`)).Scan(&facts); err != nil {
		t.Fatalf("count facts: %v", err)
	}
	if facts != 0 {
		t.Fatalf("expected no fact formed under a vocabulary that could not be read, got %d", facts)
	}
}

// failsOnceModel refuses its first call and answers every later one, which is the ordinary shape of
// a turn that fails an attempt and would have succeeded on the next.
type failsOnceModel struct {
	scriptedModel
	calls int
}

// Identified, because the vocabulary only enters the extraction identity for a proposer that can
// say what it is — an unidentified one is stamped with a fixed label that carries no vocabulary. So
// this consequence is one a real deployment meets and a stub cannot show.
func (m *failsOnceModel) ExtractionIdentity() string { return "scripted/fails-once" }

func (m *failsOnceModel) Propose(ctx context.Context, message domain.Message,
	vocabulary domain.Ontology) ([]extract.Proposal, error) {
	m.calls++
	if m.calls == 1 {
		return nil, errors.New("the endpoint did not answer")
	}
	return m.scriptedModel.Propose(ctx, message, vocabulary)
}

// What reading the vocabulary per turn costs, stated and held by a test.
//
// A turn is stamped with its extractor identity before the first model call, and later writes for
// that turn must match it: one turn is never half one interpretation and half another. The
// vocabulary is part of that identity. So a turn that has already been attempted, and whose
// vocabulary then changes, cannot complete — it is refused on every further attempt and parks, and
// the way to form it is a rebuild.
//
// That is not new behaviour; it is what a worker restarted after a vocabulary change already did.
// What is new is that it no longer needs a restart to happen, which makes it an operator-facing
// consequence of adding a relation rather than of a deployment action — so it is written down in
// the operator guide, and held here.
func TestATurnAlreadyAttemptedUnderTheEarlierVocabularyParksRatherThanMixingTwo(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "fm_vocab_midflight")

	vocabulary, err := pg.LoadVocabulary(ctx, pool, schema)
	if err != nil {
		t.Fatalf("load vocabulary: %v", err)
	}
	observations := pg.NewObservationStore(pool)
	former := formation.NewFormer(observations, pg.NewFactStore(pool),
		extract.NewWith(&failsOnceModel{}, vocabulary)).
		ReadingVocabularyFrom(pg.NewVocabularyReader(pool, schema))

	if _, err := observations.Append(ctx, schema, turnOf(
		domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "I work at Ensera."},
	)); err != nil {
		t.Fatalf("append: %v", err)
	}
	observation, found, err := observations.NextUnformed(ctx, schema, "p1", 0)
	if err != nil || !found {
		t.Fatalf("expected the turn offered: found=%v err=%v", found, err)
	}

	// The first attempt pins the identity and then fails at the model, which is the state this is
	// about: a turn with an identity already recorded and no facts yet.
	if _, err := former.Form(ctx, schema, observation); err == nil {
		t.Fatal("expected the first attempt to fail at the model")
	}

	if _, err := pg.NewVocabularyStore(pool, schema).Add(ctx, commutesBy); err != nil {
		t.Fatalf("extend the vocabulary: %v", err)
	}

	// The model would answer now. The substrate refuses anyway, because completing this turn would
	// mean one turn interpreted under two vocabularies.
	if _, err := former.Form(ctx, schema, observation); !errors.Is(err, pg.ErrExtractionChanged) {
		t.Fatalf("expected the turn refused as a changed extraction, got %v", err)
	}
}
