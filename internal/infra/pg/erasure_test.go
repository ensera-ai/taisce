package pg_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/taisce/taisce/internal/domain"
	"github.com/taisce/taisce/internal/extract"
	"github.com/taisce/taisce/internal/formation"
	"github.com/taisce/taisce/internal/infra/pg"
	"github.com/taisce/taisce/internal/recall"
)

// The stub derives its quotes from the message, so every fact it produces is genuinely citable and
// every projection an erasure has to remove is genuinely produced.
type erasureModel struct{}

func (erasureModel) Propose(_ context.Context, message domain.Message, _ domain.Ontology) ([]extract.Proposal, error) {
	var out []extract.Proposal
	for _, p := range []struct{ phrase, predicate, subject, object string }{
		{"Marta works at Ensera", "works_at", "Marta", "Ensera"},
		{"Tomas works at Ensera", "works_at", "Tomas", "Ensera"},
	} {
		if i := strings.Index(message.Content, p.phrase); i >= 0 {
			out = append(out, extract.Proposal{
				Subject: p.subject, Predicate: p.predicate, Object: p.object,
				Statement: p.phrase + ".", Confidence: 0.9,
				Quote: message.Content[i : i+len(p.phrase)],
			})
		}
	}
	if strings.Contains(message.Content, "cycles to work") {
		// Refused by the vocabulary, so the erasure has a rejected_claim to remove as well — the
		// projection that is easiest to forget, because it holds what was NOT stored as a fact.
		out = append(out, extract.Proposal{
			Subject: "Marta", Predicate: "cycles_to", Object: "work",
			Statement: "Marta cycles to work.", Quote: "cycles to work",
		})
	}
	return out, nil
}

func formed(t *testing.T, pool *pgxpool.Pool, schema pg.Schema, turns ...domain.Turn) {
	t.Helper()
	ctx := context.Background()
	observations := pg.NewObservationStore(pool)
	vocabulary, err := pg.LoadOntology(ctx, pool, schema)
	if err != nil {
		t.Fatalf("vocabulary: %v", err)
	}
	worker := formation.NewWorker(pool, observations,
		formation.NewFormer(observations, pg.NewFactStore(pool), extract.New(erasureModel{}, vocabulary)))

	for _, turn := range turns {
		if _, err := observations.Append(ctx, schema, turn); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	if _, err := worker.Drain(ctx, schema, "p1"); err != nil {
		t.Fatalf("drain: %v", err)
	}
}

func subjectTurn(subject, content string) domain.Turn {
	return domain.Turn{
		Scope:         "p1",
		DataSubjectID: subject,
		OccurredAt:    time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC),
		Messages:      []domain.Message{{Ordinal: 0, Role: domain.RoleUser, Content: content}},
	}
}

// ── #4's first claim ──────────────────────────────────────────────────────────────────────────
//
// Everything derived from a subject's observations goes, and the residual is counted against the
// same predicate in the same transaction.
//
// The count is what is being sold. "Deleted" is a claim; a number of rows still matching the
// predicate that selected the deletions is a measurement, and it is the one a data protection
// officer is actually asking for.
func TestEverythingDerivedFromASubjectIsErasedWithAResidualOfZero(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "er_zero")
	formed(t, pool, schema,
		subjectTurn("subject-1", "Marta works at Ensera and cycles to work."))

	// Before: every projection kind has rows, so the erasure has something to prove.
	kinds := declaredKinds(t, pool, schema)
	if len(kinds) == 0 {
		t.Fatal("no projection kinds are declared, so this test asserts nothing")
	}
	for _, kind := range kinds {
		if n := countKind(t, pool, schema, kind); n == 0 {
			t.Fatalf("nothing of kind %q was written, so erasing it proves nothing", kind)
		}
	}

	// And the facts are recallable, so what disappears afterwards is something that was there.
	recaller := recall.New(pg.NewRecallStore(pool, schema), 50)
	before, err := recaller.Recall(ctx, []string{"p1"}, "Where does Marta work?")
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(before.Facts) == 0 {
		t.Fatal("nothing was recallable before the erasure")
	}

	receipt, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", "subject-1", "subject request")
	if err != nil {
		t.Fatalf("erase: %v", err)
	}
	if !receipt.Clean() {
		t.Fatalf("the erasure left a residual: %+v", receipt.Residual)
	}
	// A residual of zero across an empty map would also be "clean", so the receipt has to cover
	// every declared kind.
	for _, kind := range kinds {
		if _, covered := receipt.Residual[kind]; !covered {
			t.Fatalf("the receipt says nothing about %q, which is how a residual stops meaning what it says", kind)
		}
		if n := countKind(t, pool, schema, kind); n != 0 {
			t.Fatalf("%d rows of kind %q survived an erasure that reported none", n, kind)
		}
	}

	// The observation itself, its messages, and its registrations are gone with it.
	var observations, messages, registrations, evidence int
	if err := pool.QueryRow(ctx, schema.SQL(`
		SELECT (SELECT count(*) FROM {schema}.observation),
		       (SELECT count(*) FROM {schema}.turn_message),
		       (SELECT count(*) FROM {schema}.projection_dependency),
		       (SELECT count(*) FROM {schema}.fact_evidence)`)).
		Scan(&observations, &messages, &registrations, &evidence); err != nil {
		t.Fatalf("count: %v", err)
	}
	if observations != 0 || messages != 0 || registrations != 0 || evidence != 0 {
		t.Fatalf("survived: %d observations, %d messages, %d registrations, %d evidence rows",
			observations, messages, registrations, evidence)
	}

	// And nothing is recallable, which is the property a person asking to be forgotten actually
	// cares about.
	after, err := recaller.Recall(ctx, []string{"p1"}, "Where does Marta work?")
	if err != nil {
		t.Fatalf("recall after erasure: %v", err)
	}
	if len(after.Facts) != 0 || len(after.Anchors) != 0 {
		t.Fatalf("the erased subject is still recallable: %+v", after)
	}
}

// ── #4's second claim ─────────────────────────────────────────────────────────────────────────
//
// A projection kind that has not been declared cannot be registered at all.
//
// This is the test that has to fail when a later milestone adds a projection and forgets erasure.
// It does not check a list in the eraser — the eraser has no list — it checks that the database
// refuses the registration, so the failure lands on the person adding the projection, on the day
// they add it, rather than on an erasure months later that quietly misses it.
func TestAProjectionKindThatIsNotDeclaredCannotBeRegistered(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "er_declare")
	formed(t, pool, schema, subjectTurn("subject-1", "Marta works at Ensera."))

	var observation string
	if err := pool.QueryRow(ctx, schema.SQL(
		`SELECT observation_id::text FROM {schema}.observation LIMIT 1`)).Scan(&observation); err != nil {
		t.Fatalf("read observation: %v", err)
	}

	// The shape a future milestone's projection would take: an embedding, registered the way every
	// other projection registers, by somebody who has not touched the eraser.
	if _, err := pool.Exec(ctx, schema.SQL(`
		INSERT INTO {schema}.projection_dependency
		    (source_observation_id, scope, projection_kind, projection_id, data_subject_id)
		VALUES ($1, 'p1', 'embedding', gen_random_uuid()::text, 'subject-1')`), observation); err == nil {
		t.Fatal("an undeclared projection kind was registered; erasure would never see it")
	}

	// Declaring it is all it takes, and then the registration is accepted — so the constraint is a
	// requirement to declare rather than a wall around the four kinds that exist today.
	if _, err := pool.Exec(ctx, schema.SQL(`
		INSERT INTO {schema}.projection_kind (kind, projection_table, id_column, description)
		VALUES ('embedding', 'chunk', 'chunk_id', 'A vector derived from a chunk.')`)); err != nil {
		t.Fatalf("declare: %v", err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`
		INSERT INTO {schema}.projection_dependency
		    (source_observation_id, scope, projection_kind, projection_id, data_subject_id)
		VALUES ($1, 'p1', 'embedding', gen_random_uuid()::text, 'subject-1')`), observation); err != nil {
		t.Fatalf("a declared kind must be registrable: %v", err)
	}

	// And the eraser covers it without having been changed, because it iterates the declarations.
	receipt, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", "subject-1", "subject request")
	if err != nil {
		t.Fatalf("erase: %v", err)
	}
	if _, covered := receipt.Residual["embedding"]; !covered {
		t.Fatalf("a kind declared after the eraser was written is not in its receipt: %+v", receipt.Residual)
	}
}

// Every declaration names a table and column that exist.
//
// A typo in a declaration is a kind that registers happily and fails at erasure time — which is the
// worst moment to discover it, because an erasure is something a customer is waiting on and a legal
// clock is running against.
func TestEveryDeclaredProjectionKindNamesATableThatExists(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "er_declared")

	rows, err := pool.Query(ctx, schema.SQL(`
		SELECT k.kind, k.projection_table, k.id_column,
		       (SELECT count(*) FROM information_schema.columns c
		         WHERE c.table_schema = $1 AND c.table_name = k.projection_table
		           AND c.column_name = k.id_column),
		       (SELECT count(*) FROM information_schema.columns c
		         WHERE c.table_schema = $1 AND c.table_name = k.projection_table
		           AND c.column_name = 'scope')
		  FROM {schema}.projection_kind k`), schema.String())
	if err != nil {
		t.Fatalf("read declarations: %v", err)
	}
	defer rows.Close()

	declared := 0
	for rows.Next() {
		var kind, table, column string
		var idColumn, scopeColumn int
		if err := rows.Scan(&kind, &table, &column, &idColumn, &scopeColumn); err != nil {
			t.Fatalf("scan: %v", err)
		}
		declared++
		if idColumn == 0 {
			t.Errorf("%s declares %s.%s, which does not exist", kind, table, column)
		}
		// Erasure is per project, so every projection table has to be filterable by one. A table
		// without it would be erased across every project in the tenant at once.
		if scopeColumn == 0 {
			t.Errorf("%s declares table %s, which has no scope column", kind, table)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if declared == 0 {
		t.Fatal("nothing is declared, so this test asserts nothing")
	}
}

// ── #4's third claim, which the schema forced ─────────────────────────────────────────────────
//
// A projection somebody else also produced is kept, and keeping it is not a residual.
//
// An entity is the case. `Ensera` is registered by every subject who mentioned it, and deleting it
// because one of them asked to be forgotten would take a node out of everybody else's graph — an
// erasure that damages the people who did not ask for one.
func TestAProjectionSharedWithAnotherSubjectIsKeptAndIsNotAResidual(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "er_shared")
	formed(t, pool, schema,
		subjectTurn("subject-1", "Marta works at Ensera."),
		subjectTurn("subject-2", "Tomas works at Ensera."))

	receipt, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", "subject-1", "subject request")
	if err != nil {
		t.Fatalf("erase: %v", err)
	}
	if !receipt.Clean() {
		t.Fatalf("keeping a shared projection was counted as a residual: %+v", receipt.Residual)
	}

	// Ensera survives, because subject-2 still names it. Marta does not, because only subject-1 did.
	var ensera, marta int
	if err := pool.QueryRow(ctx, schema.SQL(`
		SELECT (SELECT count(*) FROM {schema}.entity WHERE normalized_name = 'ensera'),
		       (SELECT count(*) FROM {schema}.entity WHERE normalized_name = 'marta')`)).
		Scan(&ensera, &marta); err != nil {
		t.Fatalf("count: %v", err)
	}
	if ensera != 1 {
		t.Fatalf("a shared entity was removed for one subject's erasure, damaging the other's graph")
	}
	if marta != 0 {
		t.Fatalf("an entity only the erased subject named survived")
	}

	// And the other subject still recalls what they said.
	other, err := recall.New(pg.NewRecallStore(pool, schema), 50).
		Recall(ctx, []string{"p1"}, "Where does Tomas work?")
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(other.Facts) != 1 {
		t.Fatalf("erasing one subject changed what another can recall: %+v", other.Facts)
	}
}

// The receipt is a row, with its residual, findable by asking.
//
// An erasure with no completion time is one in flight; one with a non-zero residual is an erasure
// that did not do what it claimed. Both have to be answerable by a query rather than by grepping a
// log, because a log line is not something a customer can be shown.
func TestTheReceiptIsARowCarryingItsResidual(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "er_receipt")
	formed(t, pool, schema, subjectTurn("subject-1", "Marta works at Ensera."))

	eraser := pg.NewEraser(pool)
	if _, err := eraser.Erase(ctx, schema, "p1", "subject-1", "subject request"); err != nil {
		t.Fatalf("erase: %v", err)
	}

	receipts, err := eraser.Erasures(ctx, schema, "p1")
	if err != nil {
		t.Fatalf("read receipts: %v", err)
	}
	if len(receipts) != 1 {
		t.Fatalf("expected one receipt, got %d", len(receipts))
	}
	got := receipts[0]
	if got.DataSubjectID != "subject-1" || got.Reason != "subject request" {
		t.Fatalf("the receipt does not say what was asked: %+v", got)
	}
	if got.CompletedAt.IsZero() {
		t.Fatal("the receipt has no completion time, so it reads as an erasure still in flight")
	}
	if len(got.Residual) == 0 {
		t.Fatal("the receipt carries no residual, which is the part that is the product")
	}
	if !got.Clean() {
		t.Fatalf("residual: %+v", got.Residual)
	}
}

// An erasure with no subject is refused. Without it the predicate matches every row whose
// data_subject_id is NULL — every projection that was never about a person.
func TestAnErasureWithNoSubjectIsRefused(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "er_nosubject")
	formed(t, pool, schema, subjectTurn("subject-1", "Marta works at Ensera."))

	if _, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", "", "oops"); err == nil {
		t.Fatal("an erasure with no data subject was accepted")
	}
	var facts int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.fact`)).Scan(&facts); err != nil {
		t.Fatalf("count: %v", err)
	}
	if facts == 0 {
		t.Fatal("a refused erasure deleted something")
	}
}

func declaredKinds(t *testing.T, pool *pgxpool.Pool, schema pg.Schema) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		schema.SQL(`SELECT kind FROM {schema}.projection_kind ORDER BY kind`))
	if err != nil {
		t.Fatalf("read kinds: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var kind string
		if err := rows.Scan(&kind); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, kind)
	}
	return out
}

// countKind counts the rows of a kind that are registered in this scope, by joining the declaration
// to the table it names — so the count follows the declarations rather than a list in the test.
func countKind(t *testing.T, pool *pgxpool.Pool, schema pg.Schema, kind string) int {
	t.Helper()
	var table, column string
	if err := pool.QueryRow(context.Background(), schema.SQL(
		`SELECT projection_table, id_column FROM {schema}.projection_kind WHERE kind = $1`), kind).
		Scan(&table, &column); err != nil {
		t.Fatalf("read declaration for %q: %v", kind, err)
	}
	var n int
	if err := pool.QueryRow(context.Background(), schema.SQL(
		`SELECT count(*) FROM {schema}.`+table+` WHERE scope = 'p1'`)).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", kind, err)
	}
	return n
}
