package pg_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ensera-ai/taisce/internal/compaction"
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

// ── Everything derived from a subject's observations goes ─────────────────────────────────────
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

	// A segment is the projection that holds the subject's words as prose; one over every turn they
	// have said so far is registered to each of them.
	segments := pg.NewSegmentStore(pool, schema)
	subjectTurns, err := segments.Formed(ctx, "p1", "subject-1")
	if err != nil || len(subjectTurns) == 0 {
		t.Fatalf("the subject's formed turns are the material a segment covers, got %d %v", len(subjectTurns), err)
	}
	if _, err := segments.Write(ctx, "p1", "subject-1", compaction.Plan{Level: 1, From: subjectTurns[0].Offset, To: subjectTurns[len(subjectTurns)-1].Offset, Turns: len(subjectTurns)},
		compaction.Summary{Text: "Marta works at Ensera, cycles to work and has lived in Dublin and Oslo."}); err != nil {
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

	// A report about one of the subject's records. It is not derived from the turn the way the
	// others are — somebody wrote it — which is exactly why it has to be here: its registration is
	// copied from the record's own, and a copy that stopped happening would be invisible otherwise.
	var subjectRecord string
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT fact_id::text FROM {schema}.fact
        WHERE scope='p1' AND data_subject_id='subject-1' ORDER BY fact_id LIMIT 1`)).Scan(&subjectRecord); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.NewFeedbackStore(pool, schema, pg.NewRecordStore(pool, schema)).Record(ctx, "p1",
		uuid.NewString(), pg.FeedbackRequest{RecordID: subjectRecord, Note: "this record is about me"}); err != nil {
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

// ── An undeclared projection kind cannot be registered ────────────────────────────────────────
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

// ── A projection shared with another subject is kept ──────────────────────────────────────────
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
	// And the quotes behind those facts. Without them a person is handed a claim about themselves
	// with the words it was taken from removed, which is the part they can actually check.
	if len(export.Sections["fact_evidence"]) == 0 {
		t.Fatal("the export holds no evidence, so its facts arrive with their quotes stripped")
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

// ── Retention ─────────────────────────────────────────────────────────────────────────────────

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

// ── Sharing saves an identity and must not save text ──────────────────────────────────────────

// The rule that keeps a shared entity is right for an identity and wrong for anything holding several
// people's words. Before this, one rule covered every kind — and the kind it would have been wrong for
// is the one this test is about.
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

// ── A document is erased by its source observations ───────────────────────────────────────────
//
// A document observed without a data subject is erased by its source observations, with the same
// receipt as a subject: everything registered only to those sources goes, a projection another
// source also registered stays, the residual is counted against the same predicate, and the person
// who also spoke is untouched — their turns, and so their row, survive.

// projectTurn is a turn attributed to nobody: a third-party document has no principal.
func projectTurn(content string) domain.Turn {
	return domain.Turn{
		Scope:      "p1",
		OccurredAt: time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC),
		Messages:   []domain.Message{{Ordinal: 0, Role: domain.RoleUser, Content: content}},
	}
}

func TestADocumentObservedWithoutASubjectIsErasedByItsSourcesWithAResidualOfZero(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "er_source")
	formed(t, pool, schema,
		projectTurn("Ensera is a company in Dublin."),
		projectTurn("Ensera builds memory for agents."),
		subjectTurn("subject-1", "Marta works at Ensera."))

	var sources []string
	rows, err := pool.Query(ctx, schema.SQL(`SELECT observation_id::text FROM {schema}.observation WHERE scope='p1' AND data_subject_id IS NULL ORDER BY log_offset`))
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		sources = append(sources, id)
	}
	rows.Close()
	if len(sources) != 2 {
		t.Fatalf("the document is two observations attributed to nobody, found %d", len(sources))
	}

	eraser := pg.NewEraser(pool)
	receipt, err := eraser.EraseSources(ctx, schema, "p1", sources, "takedown")
	if err != nil {
		t.Fatalf("erase by source: %v", err)
	}
	if !receipt.Clean() {
		t.Fatalf("the erasure by source left a residual: %+v", receipt.Residual)
	}
	if receipt.Deleted["observation"] != 2 {
		t.Fatalf("both source observations must go, deleted %+v", receipt.Deleted)
	}
	if _, counted := receipt.Deleted["data_subject"]; counted {
		t.Fatal("an erasure by source has no person to remove, and must not claim one")
	}
	if len(receipt.SourceObservationIDs) != 2 || receipt.DataSubjectID != "" {
		t.Fatalf("the receipt names the sources and no subject: %+v", receipt)
	}

	// Ensera survives, because Marta's turn still names it; Dublin does not, because only the
	// document did. And Marta is still here, turn and words.
	var ensera, dublin, marta, messages int
	if err := pool.QueryRow(ctx, schema.SQL(`
		SELECT (SELECT count(*) FROM {schema}.entity WHERE normalized_name = 'ensera'),
		       (SELECT count(*) FROM {schema}.entity WHERE normalized_name = 'dublin'),
		       (SELECT count(*) FROM {schema}.observation WHERE scope='p1' AND data_subject_id='subject-1'),
		       (SELECT count(*) FROM {schema}.turn_message m JOIN {schema}.observation o ON o.observation_id=m.observation_id WHERE o.data_subject_id='subject-1')`)).
		Scan(&ensera, &dublin, &marta, &messages); err != nil {
		t.Fatal(err)
	}
	if ensera != 1 {
		t.Fatal("a projection the person's turn also registered was removed with the document")
	}
	if dublin != 0 {
		t.Fatal("an entity only the document named survived its erasure")
	}
	if marta != 1 || messages == 0 {
		t.Fatalf("erasing a document touched the person: their observations %d, messages %d", marta, messages)
	}

	// The receipt is a row carrying what was asked, and it was the sources.
	erasures, err := eraser.Erasures(ctx, schema, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if len(erasures) != 1 || len(erasures[0].SourceObservationIDs) != 2 || erasures[0].DataSubjectID != "" {
		t.Fatalf("the ledger of erasures holds the selector as it was asked: %+v", erasures)
	}

	// A second erasure of the same sources is a retry, and reports that there was nothing to do.
	again, err := eraser.EraseSources(ctx, schema, "p1", sources, "takedown, again")
	if err != nil {
		t.Fatalf("a repeated erasure by source must be answered, not refused: %v", err)
	}
	if again.Deleted["observation"] != 0 || !again.Clean() {
		t.Fatalf("a repeat found something the first left: %+v", again)
	}
}

// An observation id from another project matches nothing: the predicate is scoped before it is
// keyed, so the caller's credential reaches its own project and no other, and the receipt says so
// by deleting nothing rather than by reaching across.
func TestAnErasureBySourceCannotReachAnotherProjectAndRefusesWhatIsNotAnId(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "er_source_scope")
	observations := pg.NewObservationStore(pool)
	elsewhere := projectTurn("Ensera is a company in Dublin.")
	elsewhere.Scope = "p2"
	other, err := observations.Append(ctx, schema, elsewhere)
	if err != nil {
		t.Fatal(err)
	}
	formed(t, pool, schema, projectTurn("Ensera builds memory for agents."))

	eraser := pg.NewEraser(pool)
	receipt, err := eraser.EraseSources(ctx, schema, "p1", []string{other.ID}, "reaching")
	if err != nil {
		t.Fatalf("erase: %v", err)
	}
	if receipt.Deleted["observation"] != 0 {
		t.Fatalf("an erasure under one project deleted another's observation: %+v", receipt.Deleted)
	}
	var kept int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.observation WHERE scope='p2'`)).Scan(&kept); err != nil {
		t.Fatal(err)
	}
	if kept != 1 {
		t.Fatal("the other project's observation is gone")
	}

	for name, sources := range map[string][]string{"nothing": nil, "not an id": {"the-manifest"}} {
		if _, err := eraser.EraseSources(ctx, schema, "p1", sources, name); err == nil {
			t.Fatalf("an erasure by source naming %s was accepted", name)
		}
	}
	var mine int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.observation WHERE scope='p1'`)).Scan(&mine); err != nil {
		t.Fatal(err)
	}
	if mine != 1 {
		t.Fatal("a refused erasure deleted something")
	}
}

// Every quote names its own turn, so the erasure walk's evidence guard catches nothing.
//
// The finding this answers assumed a fact could survive an erasure on another turn's support, with
// that turn's quote removed silently. It cannot: a fact's identity hashes the source observation
// and the ordinal, so the same words in two turns are two facts. This pins that, and pins the
// guard's silence — if the identity rule ever changes, the reasoning behind the guard changes with
// it, and this says so.
func TestEvidenceNamesOnlyItsOwnTurnSoTheErasureGuardCatchesNothing(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "er_guard")
	observations := pg.NewObservationStore(pool)
	facts := pg.NewFactStore(pool)
	claim := domain.Claim{Subject: "Marta", Predicate: "works_at", Object: "Ensera",
		Statement: "Marta works at Ensera.", Quote: "works at Ensera", ByteStart: 6, ByteEnd: 21,
		Confidence: 0.9, Cardinality: domain.CardinalityMany}
	said := map[string]string{}
	for i := 0; i < 2; i++ {
		stored, err := observations.Append(ctx, schema, domain.Turn{Scope: "p1", DataSubjectID: "subject-1", OccurredAt: time.Now(),
			Messages: []domain.Message{{Ordinal: 0, Role: domain.RoleUser, Content: "Marta works at Ensera."}}})
		if err != nil {
			t.Fatal(err)
		}
		factID, err := facts.Assert(ctx, schema, "p1", stored.ID, domain.RoleUser, "subject-1", claim)
		if err != nil {
			t.Fatal(err)
		}
		said[factID] = stored.ID
	}
	if len(said) != 2 {
		t.Fatalf("the same words in two turns must be two facts, got %d", len(said))
	}
	// Each fact's evidence names that fact's own turn, and only it.
	for factID, source := range said {
		var sources []string
		rows, err := pool.Query(ctx, schema.SQL(
			`SELECT DISTINCT source_observation_id::text FROM {schema}.fact_evidence WHERE scope='p1' AND fact_id=$1::uuid`), factID)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			sources = append(sources, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if len(sources) != 1 || sources[0] != source {
			t.Fatalf("fact %s cites %v, not its own turn %s", factID, sources, source)
		}
	}

	receipt, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", "subject-1", "the guard")
	if err != nil {
		t.Fatal(err)
	}
	if escaped, reported := receipt.Deleted["fact_evidence_orphaned"]; reported {
		t.Fatalf("the guard caught %d quotes, so something deleted a fact around the cascade", escaped)
	}
	if receipt.Residual["fact"] != 0 {
		t.Fatalf("a residual of %d facts", receipt.Residual["fact"])
	}
	var quotes int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.fact_evidence WHERE scope='p1'`)).Scan(&quotes); err != nil {
		t.Fatal(err)
	}
	if quotes != 0 {
		t.Fatalf("%d quotes survived the erasure", quotes)
	}
}

// An export can be taken by source, the way an erasure can: a document observed
// project-wide has no person, so a subject was no way to reach it and neither was an export.
func TestAnExportCanBeTakenByTheTurnsItIsFor(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "ex_by_source")
	observations := pg.NewObservationStore(pool)
	facts := pg.NewFactStore(pool)
	// A registered subject, because data_subject holds the people somebody registered: this is the
	// row a source selection has to reach through the turns it names.
	registered, err := pg.NewSubjectStore(pool, schema).Register(ctx, "p1", uuid.NewString(),
		pg.SubjectRegistration{IdempotencyKey: uuid.NewString(), SubjectFields: pg.SubjectFields{ExternalReference: "marta@example.test", Label: "Marta"}})
	if err != nil {
		t.Fatal(err)
	}
	subject := registered.ID
	say := func(content, object string) string {
		t.Helper()
		stored, err := observations.Append(ctx, schema, domain.Turn{Scope: "p1", DataSubjectID: subject, OccurredAt: time.Now(),
			Messages: []domain.Message{{Ordinal: 0, Role: domain.RoleUser, Content: content}}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := facts.Assert(ctx, schema, "p1", stored.ID, domain.RoleUser, subject, domain.Claim{
			Subject: "Marta", Predicate: "works_at", Object: object, Statement: "Marta works at " + object + ".",
			Quote: content[:len(content)-1], ByteStart: 0, ByteEnd: len(content) - 1,
			Confidence: 0.9, Cardinality: domain.CardinalityMany}); err != nil {
			t.Fatal(err)
		}
		return stored.ID
	}
	first := say("Marta works at Ensera.", "Ensera")
	say("Marta works at Taqneen.", "Taqneen")

	exporter := pg.NewExporter(pool, schema)
	whole, err := exporter.Export(ctx, schema, "p1", subject)
	if err != nil {
		t.Fatal(err)
	}
	if whole.Rows()["data_subject"] != 1 {
		t.Fatalf("the registered subject is missing from their own export: %v", whole.Rows())
	}
	if whole.Rows()["fact"] != 2 {
		t.Fatalf("the subject holds %d facts, not the two asserted", whole.Rows()["fact"])
	}

	one, err := exporter.ExportSources(ctx, schema, "p1", []string{first})
	if err != nil {
		t.Fatal(err)
	}
	if one.DataSubjectID != "" || len(one.SourceObservationIDs) != 1 || one.SourceObservationIDs[0] != first {
		t.Fatalf("the export does not say what it was asked: %+v", one)
	}
	if one.Rows()["fact"] != 1 || one.Rows()["message"] != 1 {
		t.Fatalf("an export of one turn returned %v", one.Rows())
	}
	// The subject rows the selected turn registered come with it, because a source selection still
	// covers the person those turns were about.
	if len(one.Sections["data_subject"]) != 1 {
		t.Fatalf("the subject behind the selected turn is missing: %v", one.Rows())
	}
	if _, err := exporter.ExportSources(ctx, schema, "p1", nil); err == nil {
		t.Fatal("an export by source with no ids was accepted")
	}
	if _, err := exporter.ExportSources(ctx, schema, "p1", []string{"not-a-uuid"}); err == nil {
		t.Fatal("an export by source accepted something that is not an observation id")
	}
}

// An export is one answer at one instant, so it is bounded rather than paged: past the ceiling it
// is refused, and the caller takes those turns by source.
func TestAnExportBeyondItsCeilingIsRefusedRatherThanTruncated(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "ex_ceiling")
	observations := pg.NewObservationStore(pool)
	if _, err := observations.Append(ctx, schema, domain.Turn{Scope: "p1", DataSubjectID: "subject-1", OccurredAt: time.Now(),
		Messages: []domain.Message{{Ordinal: 0, Role: domain.RoleUser, Content: "Marta works at Ensera."}}}); err != nil {
		t.Fatal(err)
	}
	was := pg.MaxExportRows
	pg.MaxExportRows = 1
	t.Cleanup(func() { pg.MaxExportRows = was })

	if _, err := pg.NewExporter(pool, schema).Export(ctx, schema, "p1", "subject-1"); !errors.Is(err, pg.ErrExportTooLarge) {
		t.Fatalf("an export past its ceiling was not refused: %v", err)
	}
	// And the refusal is about size, not about the person: one turn's worth is still exportable.
	pg.MaxExportRows = was
	if _, err := pg.NewExporter(pool, schema).Export(ctx, schema, "p1", "subject-1"); err != nil {
		t.Fatalf("the same export below the ceiling failed: %v", err)
	}
}
