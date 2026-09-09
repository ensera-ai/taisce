package pg_test

import (
	"context"
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
	"github.com/ensera-ai/taisce/internal/report"
	"github.com/google/uuid"
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
			out = append(out, extract.Proposal{Polarity: domain.PolarityAsserted, Tense: domain.TensePresent,
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
			Subject: "Marta", Predicate: "cycles_to", Polarity: domain.PolarityAsserted, Tense: domain.TensePresent, Object: "work",
			Statement: "Marta cycles to work.", Quote: "cycles to work",
		})
	}
	return out, nil
}

// described runs the subject pass over a scope after it has formed, so a test asserting that every
// declared projection kind is produced actually produces the one that describes a subject.
//
// A stub writer, because what a model does with good material is measured against a live one — what
// matters here is that prose exists, is registered to the observations behind it, and goes when they
// do.
func described(t *testing.T, pool *pgxpool.Pool, schema pg.Schema) {
	t.Helper()
	pass, err := formation.NewSubjects(pg.NewCommunityStore(pool, schema),
		report.New(fixedReport{})).Run(context.Background(), "p1", 50)
	if err != nil {
		t.Fatalf("form subjects: %v", err)
	}
	if pass.Written == 0 {
		t.Fatalf("no subject was described, so a test asserting erasure covers reports would pass "+
			"for the wrong reason: %+v", pass)
	}
}

type fixedReport struct{}

func (fixedReport) Write(_ context.Context, c report.Context) (report.Report, error) {
	return report.Report{
		Title:            "A subject",
		Summary:          "Written from what several people said.",
		Importance:       4,
		ImportanceReason: "There was material for it.",
	}, nil
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
	// Temporal history is a declared projection too; create a real supersession before testing
	// that every kind is erased. Both turns belong to the subject selected below.
	for i, city := range []string{"Dublin", "Oslo"} {
		at := time.Date(2026, time.Month(1+i), 1, 0, 0, 0, 0, time.UTC)
		statedAt(t, ctx, pg.NewObservationStore(pool), pg.NewFactStore(pool), schema, at,
			"Marta lives in "+city, domain.Claim{Subject: "Marta", Predicate: "lives_in", Cardinality: domain.CardinalityOne, Object: city, Statement: "Marta lives in " + city, ValidFrom: at})
	}

	if _, err := pg.NewArtifactStore(pool, schema).Put(ctx, "p1", uuid.NewString(), pg.ArtifactPut{ID: uuid.NewString(), DataSubjectID: "subject-1", Kind: "state", Content: []byte("opaque session state")}); err != nil {
		t.Fatal(err)
	}

	described(t, pool, schema)
	embeddings := pg.NewMessageEmbeddingStore(pool, schema)
	embeddingActor := uuid.NewString()
	embeddingModel := pg.EmbeddingModel{Name: "erasure-fixture", Revision: "v1", EndpointHash: strings.Repeat("a", 64), Dimensions: 3}
	embeddingGeneration, err := embeddings.Start(ctx, "p1", uuid.NewString(), embeddingActor, embeddingModel)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := embeddings.BuildPage(ctx, "p1", embeddingGeneration.ID, embeddingActor, embeddingModel, 64, func(_ context.Context, texts []string) ([][]float32, error) {
		vectors := make([][]float32, len(texts))
		for i := range vectors {
			vectors[i] = []float32{1, 0, 0}
		}
		return vectors, nil
	}); err != nil {
		t.Fatal(err)
	}
	entityEmbeddings := pg.NewEntityEmbeddingStore(pool, schema)
	entityGeneration, err := entityEmbeddings.Start(ctx, "p1", uuid.NewString(), embeddingActor, embeddingModel)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entityEmbeddings.BuildPage(ctx, "p1", entityGeneration.ID, embeddingActor, embeddingModel, 64, func(_ context.Context, texts []string) ([][]float32, error) {
		vectors := make([][]float32, len(texts))
		for i := range vectors {
			vectors[i] = []float32{1, 0, 0}
		}
		return vectors, nil
	}); err != nil {
		t.Fatal(err)
	}
	reportEmbeddings := pg.NewReportEmbeddingStore(pool, schema)
	reportGeneration, err := reportEmbeddings.Start(ctx, "p1", uuid.NewString(), embeddingActor, embeddingModel)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reportEmbeddings.BuildPage(ctx, "p1", reportGeneration.ID, embeddingActor, embeddingModel, 64, func(_ context.Context, texts []string) ([][]float32, error) {
		vectors := make([][]float32, len(texts))
		for i := range vectors {
			vectors[i] = []float32{1, 0, 0}
		}
		return vectors, nil
	}); err != nil {
		t.Fatal(err)
	}

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
	recaller := recall.NewWithBudget(pg.NewRecallStore(pool, schema), recall.Budget{Characters: 100000, MaxRows: 50})
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

	// A catalog row alone cannot make an unchecked relationship registrable. The extension
	// migration must also bind its target to a real row in the same project.
	if _, err := pool.Exec(ctx, schema.SQL(`
		INSERT INTO {schema}.projection_kind (kind, projection_table, id_column, description)
		VALUES ('embedding', 'chunk', 'chunk_id', 'A vector derived from a chunk.')`)); err != nil {
		t.Fatalf("declare: %v", err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.projection_dependency
        (source_observation_id,scope,projection_kind,projection_id)
        VALUES($1,'p1','embedding',gen_random_uuid()::text)`), observation); err == nil {
		t.Fatal("catalog declaration bypassed target integrity")
	}
	extendProjectionFixture(t, pool, schema, "embedding", "chunk", "chunk_id")
	if _, err := pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.projection_dependency
        (source_observation_id,scope,projection_kind,projection_id,data_subject_id)
        SELECT $1,'p1','embedding',chunk_id::text,'subject-1' FROM {schema}.chunk WHERE source_observation_id=$1`), observation); err != nil {
		t.Fatal(err)
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
	other, err := recall.NewWithBudget(pg.NewRecallStore(pool, schema), recall.Budget{Characters: 100000, MaxRows: 50}).
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

// An export of a subject who was never here is refused only for having no subject, never for having
// nothing — and one with no subject at all is refused before it reads anything.
func TestAnExportWithoutASubjectIsRefusedBeforeItReadsAnything(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "er_export_nosubject")

	if _, err := pg.NewExporter(pool, schema).Export(ctx, schema, "p1", ""); err == nil {
		t.Fatal("an export with no subject was produced; that is a copy of the project")
	}
}

// The export reads at one instant, so its sections cannot disagree with each other.
//
// Assembled across several reads, an export could show a fact whose evidence had been erased between
// them — internally inconsistent, and the inconsistency would look like a defect in the memory rather
// than in the export.
func TestAnExportIsInternallyConsistent(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "er_export_snapshot")
	obs := pg.NewObservationStore(pool)
	facts := pg.NewFactStore(pool)

	stored, err := obs.Append(ctx, schema, turnOf(
		domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "I work at Ensera."},
	))
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if _, err := facts.Assert(ctx, schema, "p1", stored.ID, domain.RoleUser, "subject-1", domain.Claim{
		Subject: "the user", Predicate: "works_at", Cardinality: domain.CardinalityMany, Object: "Ensera",
		Statement: "The user works at Ensera.", Quote: "I work at Ensera", ByteStart: 0, ByteEnd: 16,
	}); err != nil {
		t.Fatalf("assert: %v", err)
	}

	export, err := pg.NewExporter(pool, schema).Export(ctx, schema, "p1", "subject-1")
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(export.Sections["fact"]) == 0 {
		t.Fatal("the export holds no facts")
	}
	if len(export.Sections["message"]) == 0 {
		t.Fatal("the export holds no messages, so a person cannot see what it derived from")
	}
	// Every declared kind has a section, whether or not it holds anything — an absent section says
	// "we did not look".
	for _, kind := range []string{"fact", "entity", "chunk", "rejected_claim"} {
		if _, present := export.Sections[kind]; !present {
			t.Fatalf("no section for declared kind %q", kind)
		}
	}
	if export.Rows()["fact"] != len(export.Sections["fact"]) {
		t.Fatal("the row count does not match the rows")
	}
}

// ── #55: retention ────────────────────────────────────────────────────────────────────────────

// An expired turn takes its projections with it, and the sweep reports what went.
//
// The observation is what expires, not the fact: the log is authoritative and everything else derives
// from it, so a fact outliving the message that produced it would have a citation resolving against
// nothing — which is exactly the thing this product does not offer.
func TestAnExpiredTurnTakesItsProjectionsWithIt(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "er_retention")
	if err := migrate.ProvisionScope(ctx, pool, schema.String(), "p1"); err != nil {
		t.Fatalf("provision: %v", err)
	}
	// An interval so short that the turn is expired the moment it is written.
	if _, err := pool.Exec(ctx, schema.SQL(
		`UPDATE {schema}.project SET retention = interval '1 microsecond' WHERE scope = 'p1'`)); err != nil {
		t.Fatalf("set retention: %v", err)
	}

	obs := pg.NewObservationStore(pool)
	facts := pg.NewFactStore(pool)
	stored, err := obs.Append(ctx, schema, turnOf(
		domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "I work at Ensera."},
	))
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if _, err := facts.Assert(ctx, schema, "p1", stored.ID, domain.RoleUser, "subject-1", domain.Claim{
		Subject: "the user", Predicate: "works_at", Cardinality: domain.CardinalityMany, Object: "Ensera",
		Statement: "The user works at Ensera.", Quote: "I work at Ensera", ByteStart: 0, ByteEnd: 16,
	}); err != nil {
		t.Fatalf("assert: %v", err)
	}

	// The expiry was stamped at write time, from the project's policy.
	var stamped bool
	if err := pool.QueryRow(ctx, schema.SQL(
		`SELECT retention_until IS NOT NULL FROM {schema}.observation WHERE observation_id = $1`),
		stored.ID).Scan(&stamped); err != nil {
		t.Fatalf("read stamp: %v", err)
	}
	if !stamped {
		t.Fatal("a turn written into a project with a retention policy has no expiry")
	}

	sweep, err := pg.NewRetentionStore(pool, schema).Sweep(ctx, "p1", 100)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if sweep.Observations != 1 {
		t.Fatalf("the sweep expired %d turns, wanted 1", sweep.Observations)
	}
	if sweep.Deleted["fact"] == 0 {
		t.Fatal("the turn expired and its facts did not, so a citation now resolves against nothing")
	}

	// And nothing derived from it survives.
	for _, table := range []string{"observation", "turn_message", "fact", "fact_evidence",
		"projection_dependency"} {
		var n int
		if err := pool.QueryRow(ctx, schema.SQL(
			`SELECT count(*) FROM {schema}.`+table)).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if n != 0 {
			t.Fatalf("%d rows survived in %s", n, table)
		}
	}
}

// A project with no retention policy keeps everything, and a sweep over it finds nothing.
//
// NULL is a policy — keep — rather than an absence of one, and the failure being prevented is a sweep
// that treats "unset" as "expire immediately".
func TestAProjectWithNoPolicyKeepsEverything(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "er_noretention")
	if err := migrate.ProvisionScope(ctx, pool, schema.String(), "p1"); err != nil {
		t.Fatalf("provision: %v", err)
	}

	obs := pg.NewObservationStore(pool)
	if _, err := obs.Append(ctx, schema, turnOf(
		domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "I work at Ensera."},
	)); err != nil {
		t.Fatalf("append: %v", err)
	}

	sweep, err := pg.NewRetentionStore(pool, schema).Sweep(ctx, "p1", 100)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if !sweep.Empty() {
		t.Fatalf("a project with no policy expired %d turns", sweep.Observations)
	}
	var n int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.observation`)).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("%d observations survived a sweep that should have done nothing", n)
	}
}

// Shortening a policy does not reach back. The turn keeps the expiry it was written under.
//
// The same reasoning as memory surfaces only growing: a configuration change that retroactively
// destroys memory is a change discovered by whoever made it. Removing what is already held is an
// erasure with a receipt.
func TestShorteningAPolicyDoesNotExpireWhatIsAlreadyHeld(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "er_shorten")
	if err := migrate.ProvisionScope(ctx, pool, schema.String(), "p1"); err != nil {
		t.Fatalf("provision: %v", err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(
		`UPDATE {schema}.project SET retention = interval '10 years' WHERE scope = 'p1'`)); err != nil {
		t.Fatalf("set retention: %v", err)
	}

	obs := pg.NewObservationStore(pool)
	if _, err := obs.Append(ctx, schema, turnOf(
		domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "I work at Ensera."},
	)); err != nil {
		t.Fatalf("append: %v", err)
	}

	// The operator shortens the policy to something that would have expired it long ago.
	if _, err := pool.Exec(ctx, schema.SQL(
		`UPDATE {schema}.project SET retention = interval '1 microsecond' WHERE scope = 'p1'`)); err != nil {
		t.Fatalf("shorten: %v", err)
	}

	sweep, err := pg.NewRetentionStore(pool, schema).Sweep(ctx, "p1", 100)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if !sweep.Empty() {
		t.Fatal("shortening a policy retroactively deleted memory that was within policy when written")
	}
}

// A sweep takes at most what it was asked for, so a large expiry does not become one enormous
// transaction that holds locks across every project on the instance.
func TestASweepIsBounded(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "er_bounded")
	if err := migrate.ProvisionScope(ctx, pool, schema.String(), "p1"); err != nil {
		t.Fatalf("provision: %v", err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(
		`UPDATE {schema}.project SET retention = interval '1 microsecond' WHERE scope = 'p1'`)); err != nil {
		t.Fatalf("set retention: %v", err)
	}

	obs := pg.NewObservationStore(pool)
	for i := 0; i < 5; i++ {
		if _, err := obs.Append(ctx, schema, turnOf(
			domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "I work at Ensera."},
		)); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	retention := pg.NewRetentionStore(pool, schema)
	first, err := retention.Sweep(ctx, "p1", 2)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if first.Observations != 2 {
		t.Fatalf("a sweep limited to 2 took %d", first.Observations)
	}

	// The rest go on subsequent passes rather than being forgotten.
	total := first.Observations
	for i := 0; i < 5 && total < 5; i++ {
		next, err := retention.Sweep(ctx, "p1", 2)
		if err != nil {
			t.Fatalf("sweep: %v", err)
		}
		if next.Empty() {
			break
		}
		total += next.Observations
	}
	if total != 5 {
		t.Fatalf("%d of 5 expired turns were removed across repeated sweeps", total)
	}

	// A sweep with no limit given picks a default rather than taking everything or nothing.
	if _, err := retention.Sweep(ctx, "p1", 0); err != nil {
		t.Fatalf("a sweep with no limit failed: %v", err)
	}
	// And a project that does not exist is not an error — it has nothing to expire.
	empty, err := retention.Sweep(ctx, "never-existed", 10)
	if err != nil {
		t.Fatalf("sweeping an unknown project errored: %v", err)
	}
	if !empty.Empty() {
		t.Fatal("an unknown project expired something")
	}
}

// The ledger refuses an entry it cannot count, and the refusal reaches the caller rather than being
// swallowed by the store.
func TestTheAuditStoreRefusesAnUncountableEntry(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "er_auditrefuse")
	audit := pg.NewAuditStore(pool, schema)

	if err := audit.Append(ctx, domain.AuditEntry{
		Operation: "exfiltrate", Principal: "p",
		PrincipalKind: domain.PrincipalSystem, Outcome: domain.OutcomeAllowed,
	}); err == nil {
		t.Fatal("the store accepted an operation the ledger does not record")
	}
	if err := audit.Append(ctx, domain.AuditEntry{
		Operation: domain.AuditRecall, Principal: "",
		PrincipalKind: domain.PrincipalSystem, Outcome: domain.OutcomeAllowed,
	}); err == nil {
		t.Fatal("the store accepted an unattributed operation")
	}

	// A valid one goes in, and comes back.
	if err := audit.Append(ctx, domain.AuditEntry{
		Operation: domain.AuditRecall, Principal: "cred-1", Project: "p1",
		PrincipalKind: domain.PrincipalCredential, Outcome: domain.OutcomeAllowed, Magnitude: 3,
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	entries, err := audit.Recent(ctx, 0)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(entries) != 1 || entries[0].Magnitude != 3 {
		t.Fatalf("the ledger read back %+v", entries)
	}
}

// ── D57: sharing saves an identity and must not save text ─────────────────────────────────────

// The rule that keeps a shared entity is right for an identity and wrong for anything holding several
// people's words. Before this, one rule covered every kind — and the kind it would have been wrong for
// is the one D57 is about.
//
// Built with a projection kind declared for the test rather than with a real report, because the
// report table does not exist yet and the mechanism does: what has to hold is that a kind saying
// sharing does not save it is erased even when somebody else registered it, in the same sweep, counted
// in the same receipt.
func TestAKindThatAggregatesTextIsNotSavedByBeingShared(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "er_shared_text")
	formed(t, pool, schema,
		subjectTurn("subject-1", "Marta works at Ensera."),
		subjectTurn("subject-2", "Tomas works at Ensera."))

	// A kind that holds text drawn from both subjects, declared the way a report would be.
	if _, err := pool.Exec(ctx, schema.SQL(`
		CREATE TABLE {schema}.digest (
		    digest_id uuid PRIMARY KEY,
		    scope     text NOT NULL,
		    body      text NOT NULL, UNIQUE(scope,digest_id))`)); err != nil {
		t.Fatalf("create the table: %v", err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`
		INSERT INTO {schema}.projection_kind
		    (kind, projection_table, id_column, description, survives_sharing)
		VALUES ('digest', 'digest', 'digest_id', 'Prose written from what several people said.', false)`)); err != nil {
		t.Fatalf("declare the kind: %v", err)
	}

	extendProjectionFixture(t, pool, schema, "digest", "digest", "digest_id")
	var digest string
	if err := pool.QueryRow(ctx, schema.SQL(`
		INSERT INTO {schema}.digest (digest_id, scope, body)
		VALUES (gen_random_uuid(), 'p1', 'Marta and Tomas both work at Ensera.')
		RETURNING digest_id::text`)).Scan(&digest); err != nil {
		t.Fatalf("write the digest: %v", err)
	}
	// Registered to both subjects' observations, which is what a report over a community would be.
	rows, err := pool.Query(ctx, schema.SQL(
		`SELECT observation_id::text, data_subject_id FROM {schema}.observation WHERE scope = 'p1'`))
	if err != nil {
		t.Fatalf("read observations: %v", err)
	}
	type registration struct{ observation, subject string }
	var registrations []registration
	for rows.Next() {
		var r registration
		if err := rows.Scan(&r.observation, &r.subject); err != nil {
			t.Fatalf("scan: %v", err)
		}
		registrations = append(registrations, r)
	}
	rows.Close()
	if len(registrations) != 2 {
		t.Fatalf("expected two observations, got %d", len(registrations))
	}
	for _, r := range registrations {
		if _, err := pool.Exec(ctx, schema.SQL(`
			INSERT INTO {schema}.projection_dependency
			    (source_observation_id, scope, projection_kind, projection_id, data_subject_id)
			VALUES ($1, 'p1', 'digest', $2, $3)`), r.observation, digest, r.subject); err != nil {
			t.Fatalf("register: %v", err)
		}
	}

	receipt, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", "subject-1", "subject request")
	if err != nil {
		t.Fatalf("erase: %v", err)
	}
	if !receipt.Clean() {
		t.Fatalf("the erasure left a residual: %+v", receipt.Residual)
	}

	var digests, entities int
	if err := pool.QueryRow(ctx, schema.SQL(`
		SELECT (SELECT count(*) FROM {schema}.digest),
		       (SELECT count(*) FROM {schema}.entity WHERE normalized_name = 'ensera')`)).
		Scan(&digests, &entities); err != nil {
		t.Fatalf("count: %v", err)
	}
	if digests != 0 {
		t.Fatal("prose containing the departing subject's words was kept because somebody else " +
			"also contributed to it, and the receipt reported a clean erasure")
	}
	// And the identity is still shared, so this did not simply delete more of everything.
	if entities != 1 {
		t.Fatal("a shared entity was removed too, which would take a node out of the other " +
			"subject's graph")
	}
	if receipt.Deleted["digest"] != 1 {
		t.Fatalf("the receipt says %d digests were deleted", receipt.Deleted["digest"])
	}
}

// The default is the safe one. A kind added by somebody who did not read the migration deletes too
// much rather than leaking — over-deletion is visible to the person who lost data, and a leak is
// visible to nobody.
func TestAKindThatDoesNotSayIsTreatedAsHoldingText(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "er_default_share")

	if _, err := pool.Exec(ctx, schema.SQL(`
		INSERT INTO {schema}.projection_kind (kind, projection_table, id_column, description)
		VALUES ('unstated', 'chunk', 'chunk_id', 'A kind whose author did not answer the question.')`)); err != nil {
		t.Fatalf("declare: %v", err)
	}
	var survives bool
	if err := pool.QueryRow(ctx, schema.SQL(
		`SELECT survives_sharing FROM {schema}.projection_kind WHERE kind = 'unstated'`)).
		Scan(&survives); err != nil {
		t.Fatalf("read: %v", err)
	}
	if survives {
		t.Fatal("a kind that said nothing about sharing acquired the setting that leaks")
	}

	// And the four that were declared before the question existed keep what they did.
	for _, kind := range []string{"chunk", "fact", "entity", "rejected_claim"} {
		if err := pool.QueryRow(ctx, schema.SQL(
			`SELECT survives_sharing FROM {schema}.projection_kind WHERE kind = $1`), kind).
			Scan(&survives); err != nil {
			t.Fatalf("read %s: %v", kind, err)
		}
		if !survives {
			t.Fatalf("%q changed behaviour, and this migration is not the place to do that", kind)
		}
	}
}

// A test-only extension migration: the generic eraser still learns kinds from the catalog,
// while every new target gets the same integrity contract as the built-in projections.
func extendProjectionFixture(t *testing.T, pool *pgxpool.Pool, schema pg.Schema, kind, table, id string) {
	t.Helper()
	// All identifiers are closed fixture constants, never application input.
	if kind != "embedding" && kind != "digest" {
		t.Fatal("unknown fixture kind")
	}
	_, err := pool.Exec(context.Background(), schema.SQL(`ALTER TABLE {schema}.projection_dependency
        ADD COLUMN extension_ref uuid GENERATED ALWAYS AS (CASE WHEN projection_kind='`+kind+`' THEN projection_id::uuid END) STORED,
        DROP CONSTRAINT dependency_one_target_chk,
        ADD CONSTRAINT dependency_one_target_chk CHECK(num_nonnulls(entity_ref,fact_ref,chunk_ref,rejected_ref,report_ref,extension_ref)=1),
        ADD CONSTRAINT dependency_extension_fk FOREIGN KEY(scope,extension_ref) REFERENCES {schema}.`+table+`(scope,`+id+`) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED`))
	if err != nil {
		t.Fatal(err)
	}
}
