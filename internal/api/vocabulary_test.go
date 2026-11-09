// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/api"
	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/jackc/pgx/v5/pgxpool"
)

// addRelation sends one declaration and returns the status and the body, so a test can assert both
// what was refused and what the refusal said.
func addRelation(t *testing.T, m *managed, body map[string]any, want int) []byte {
	t.Helper()
	status, raw := rawAgainst(t, m.manage.URL, http.MethodPost, api.ManagementPrefix+"/vocabulary/add", body, m.operator)
	if status != want {
		t.Fatalf("/vocabulary/add returned %d, want %d: %s", status, want, raw)
	}
	return raw
}

// declaration is a complete one, which each refusal test then spoils in exactly one way — so what
// the test varies is the one thing it is about.
func declaration(predicate string) map[string]any {
	return map[string]any{
		"project": "p1", "predicate": predicate, "semantic_type": "identity",
		"cardinality": "many", "object_kind": "organisation",
		"description": "An organisation the subject is contracted to.",
	}
}

// A relation added to the vocabulary is one a fact may then reference, and one the extractor is
// told about.
//
// This is the property the whole operation exists for, and the foreign key is what makes it a
// property rather than a hope: the same insert is attempted before the relation is admitted and
// after it, and only the second one is allowed. The vocabulary is then read back through the loader
// the extractor itself uses, because a relation the database admits and the model is never told
// about would be refused as unmapped on every message and look like a bad model.
func TestARelationAddedToTheVocabularyIsOneAFactMayThenReferenceAndTheExtractorIsTold(t *testing.T) {
	m := newManaged(t, "api_vocabulary_usable")
	ctx := context.Background()
	const relation = "contracted_to"
	insert := m.schema.SQL(`INSERT INTO {schema}.fact(fact_id,scope,predicate,statement,cardinality,valid,source_role)
        VALUES (gen_random_uuid(),'p1',$1,'vocabulary fixture','many','[2026-01-01,)'::tstzrange,'user')`)
	if _, err := m.pool.Exec(ctx, insert, relation); err == nil {
		t.Fatal("a fact naming a relation the vocabulary does not admit was stored; the foreign key is what closes the set")
	}

	var added struct {
		Project   string `json:"project"`
		Predicate struct {
			Predicate    string `json:"predicate"`
			SemanticType string `json:"semantic_type"`
			Cardinality  string `json:"cardinality"`
			ObjectKind   string `json:"object_kind"`
			Description  string `json:"description"`
			Event        bool   `json:"event"`
		} `json:"predicate"`
	}
	decodeJSON(t, addRelation(t, m, declaration(relation), http.StatusCreated), &added)
	if added.Predicate.Predicate != relation || added.Predicate.Cardinality != "many" || added.Predicate.ObjectKind != "organisation" {
		t.Fatalf("the surface reported %+v", added.Predicate)
	}
	// Not one of the five, and false until somebody judges the relation an event: the conservative
	// value, because admitting a past tense writes a fact from a relation that may have ended.
	if added.Predicate.Event {
		t.Fatal("a relation arrived marked as an event, which no declaration asked for")
	}
	if _, err := m.pool.Exec(ctx, insert, relation); err != nil {
		t.Fatalf("a fact could not name a relation the vocabulary now admits: %v", err)
	}

	vocabulary, err := pg.LoadVocabulary(ctx, m.pool, m.schema)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := vocabulary.Ontology.Lookup(relation); !ok {
		t.Fatal("the extractor's vocabulary does not hold a relation the database admits")
	}

	var listed struct {
		Predicates []struct {
			Predicate string `json:"predicate"`
		} `json:"predicates"`
	}
	decodeJSON(t, m.manageDo(t, "/vocabulary/list", map[string]any{"project": "p1"}, m.operator, http.StatusOK, nil), &listed)
	if !holdsRelation(listed.Predicates, relation) {
		t.Fatal("the listing does not hold the relation that was just added")
	}
	if len(listed.Predicates) != vocabulary.Ontology.Len() {
		t.Fatalf("the listing has %d relations and the extractor is told %d", len(listed.Predicates), vocabulary.Ontology.Len())
	}
}

func holdsRelation(listed []struct {
	Predicate string `json:"predicate"`
}, name string) bool {
	for _, p := range listed {
		if p.Predicate == name {
			return true
		}
	}
	return false
}

// A name outside the spelling the vocabulary keeps is refused by the constraint that keeps it, and
// the refusal names that constraint and nothing the caller sent.
//
// Two properties in one test because they are one answer: an operator has to be told which of the
// five declarations to fix, and a refusal that quoted the declaration back would put a caller's
// words into a response and a log where this surface deliberately never puts them.
func TestARelationNameOutsideTheVocabularysSpellingIsRefusedByTheConstraintThatNamesIt(t *testing.T) {
	m := newManaged(t, "api_vocabulary_name")
	for _, name := range []string{"Contracted To", "contracted_to_", "_contracted", "a", "contracted-to", "contracted to"} {
		raw := addRelation(t, m, declaration(name), http.StatusBadRequest)
		if !strings.Contains(string(raw), "predicate_name_chk") {
			t.Fatalf("%q was refused without naming the constraint that refused it: %s", name, raw)
		}
	}
	// A relation with no name at all is a declaration that was not made, so it is the column's
	// NOT NULL that refuses it rather than the spelling.
	if raw := addRelation(t, m, declaration(""), http.StatusBadRequest); !strings.Contains(string(raw), "predicate") {
		t.Fatalf("a relation with no name was refused without naming what was missing: %s", raw)
	}
	// Nothing the caller sent appears in the answer. Checked on a name distinctive enough that its
	// presence could only be an echo.
	if raw := addRelation(t, m, declaration("Contracted Qux"), http.StatusBadRequest); strings.Contains(string(raw), "Qux") {
		t.Fatalf("the refusal quoted what was sent: %s", raw)
	}
}

// Each of the three closed axes refuses a value outside it, and says which axis it was.
//
// One test over all three, because the property is about the arrangement rather than any one axis:
// none of them is checked in Go, so each is either enforced by its own CHECK or not at all.
func TestEachClosedAxisOfTheVocabularyRefusesAValueOutsideIt(t *testing.T) {
	m := newManaged(t, "api_vocabulary_axes")
	for field, cases := range map[string]struct {
		constraint string
		values     []string
	}{
		"semantic_type": {"predicate_semantic_type_chk", []string{"commercial", "Identity", "identity "}},
		"cardinality":   {"predicate_cardinality_chk", []string{"two", "One", "unbounded"}},
		"object_kind":   {"predicate_object_kind_chk", []string{"contract", "Person", "organization"}},
	} {
		for _, value := range cases.values {
			body := declaration("axis_" + field)
			body[field] = value
			raw := addRelation(t, m, body, http.StatusBadRequest)
			if !strings.Contains(string(raw), cases.constraint) {
				t.Fatalf("%s=%q was refused without naming %s: %s", field, value, cases.constraint, raw)
			}
		}
	}
}

// A declaration left out is refused by the column that required it, and the refusal names that
// column — so the answer to "which of the five did I miss" is in the refusal.
//
// Required in the substrate rather than here: each column is NOT NULL, and a declaration that was
// not made is sent as NULL. Cardinality is the reason none of the five may be defaulted — it
// decides whether a later fact closes this one, and nothing can work that out afterwards.
func TestADeclarationLeftOutOfARelationIsRefusedByTheColumnThatRequiredIt(t *testing.T) {
	m := newManaged(t, "api_vocabulary_required")
	for _, field := range []string{"semantic_type", "cardinality", "object_kind", "description"} {
		body := declaration("missing_" + field)
		body[field] = ""
		raw := addRelation(t, m, body, http.StatusBadRequest)
		if !strings.Contains(string(raw), field) {
			t.Fatalf("a relation declared without %s was refused without naming it: %s", field, raw)
		}
	}
	// Whitespace is not a declaration either: a relation whose description is blank is one nobody
	// can tell from its nearest neighbour a year later, which is what the description is for.
	body := declaration("missing_blank_description")
	body["description"] = "   "
	if raw := addRelation(t, m, body, http.StatusBadRequest); !strings.Contains(string(raw), "description") {
		t.Fatalf("a blank description was not refused as a missing one: %s", raw)
	}
}

// A relation the vocabulary already admits is a conflict and not a second row.
//
// Its own status and its own code, because nothing about the declaration is wrong: the relation is
// there, and the caller's next move is to use it. The count is asserted afterwards, since the
// failure this prevents is a second spelling of one relation rather than a confusing message.
func TestARelationTheVocabularyAlreadyAdmitsIsAConflictRatherThanASecondRow(t *testing.T) {
	m := newManaged(t, "api_vocabulary_duplicate")
	before := relationCount(t, m)
	addRelation(t, m, declaration("contracted_to"), http.StatusCreated)
	raw := addRelation(t, m, declaration("contracted_to"), http.StatusConflict)
	if !strings.Contains(string(raw), "predicate_conflict") {
		t.Fatalf("a duplicate was refused under another code: %s", raw)
	}
	// A seeded relation is the same answer: the vocabulary admits it, whoever put it there.
	addRelation(t, m, declaration("works_at"), http.StatusConflict)
	if after := relationCount(t, m); after != before+1 {
		t.Fatalf("the vocabulary holds %d relations, want %d: a duplicate was stored", after, before+1)
	}
}

func relationCount(t *testing.T, m *managed) int {
	t.Helper()
	var n int
	if err := m.pool.QueryRow(context.Background(), m.schema.SQL(`SELECT count(*) FROM {schema}.predicate`)).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Extending the vocabulary is on the ledger with the operator as its principal, and so is an
// attempt that was refused.
//
// The refused row is the half that matters most: a relation attempted against a project that does
// not exist is what a typo produces, and without a row nobody would ever find out it was tried.
func TestExtendingTheVocabularyIsOnTheLedgerWithTheOperatorAsPrincipal(t *testing.T) {
	m := newManaged(t, "api_vocabulary_ledger")
	ctx := context.Background()
	grant, err := credential.NewStore(m.pool, string(migrate.ControlSchema)).ResolveOperator(ctx, m.operator)
	if err != nil {
		t.Fatal(err)
	}
	addRelation(t, m, declaration("contracted_to"), http.StatusCreated)
	m.manageDo(t, "/vocabulary/list", map[string]any{"project": "p1"}, m.operator, http.StatusOK, nil)
	addRelation(t, m, declaration("Contracted To"), http.StatusBadRequest)
	no := declaration("declared_elsewhere")
	no["project"] = "no_such_project"
	addRelation(t, m, no, http.StatusNotFound)

	for _, want := range []struct {
		operation, project, outcome string
		rows                        int
	}{
		{"vocabulary.add", "p1", "allowed", 1},
		{"vocabulary.list", "p1", "allowed", 1},
		{"vocabulary.add", "p1", "refused", 1},
		{"vocabulary.add", "no_such_project", "refused", 1},
	} {
		var rows int
		if err := m.pool.QueryRow(ctx, m.schema.SQL(
			`SELECT count(*) FROM {schema}.audit_entry
              WHERE operation=$1 AND project=$2 AND outcome=$3
                AND principal=$4 AND principal_kind='operator'`),
			want.operation, want.project, want.outcome, grant.CredentialID).Scan(&rows); err != nil {
			t.Fatal(err)
		}
		if rows != want.rows {
			t.Fatalf("the ledger holds %d rows for %s/%s/%s, want %d",
				rows, want.operation, want.project, want.outcome, want.rows)
		}
	}
}

// A credential that is not an operator's is refused at the vocabulary door, before any declaration
// is read.
//
// The project credential is the one that matters: it is held by application code, which is where
// text written by somebody else arrives, and the vocabulary is what bounds every fact that text can
// become. A revoked operator is a stranger too.
func TestACredentialThatIsNotAnOperatorsIsRefusedAtTheVocabularyDoor(t *testing.T) {
	m := newManaged(t, "api_vocabulary_door")
	ctx := context.Background()
	for _, path := range []string{"/vocabulary/add", "/vocabulary/list"} {
		for _, stranger := range []string{"", "tsk_notatoken", m.token} {
			if status, raw := rawAgainst(t, m.manage.URL, http.MethodPost, api.ManagementPrefix+path,
				declaration("contracted_to"), stranger); status != http.StatusUnauthorized {
				t.Fatalf("%s with token %q returned %d: %s", path, stranger, status, raw)
			}
		}
	}
	if n := relationCount(t, m); n == 0 {
		t.Fatal("the vocabulary is empty, so the refusals above proved nothing")
	}
	credentials := credential.NewStore(m.pool, string(migrate.ControlSchema))
	grant, err := credentials.ResolveOperator(ctx, m.operator)
	if err != nil {
		t.Fatal(err)
	}
	if err := credentials.Revoke(ctx, grant.CredentialID); err != nil {
		t.Fatal(err)
	}
	addRelation(t, m, declaration("contracted_to"), http.StatusUnauthorized)
}

// The vocabulary is the instance's: every project in it is admitted the same relations, and no
// other instance is admitted any of them.
//
// This is the boundary as it actually is, and it is not the one the operation's `project` field
// might suggest. `predicate` is one table per namespace keyed by the relation name alone, and
// `fact.predicate` references it from every project in that namespace — so the project an operation
// names is what the ledger records, not a partition of the vocabulary. The second tenant is where
// the real boundary is, and it is a schema.
func TestTheVocabularyIsSharedByEveryProjectInAnInstanceAndByNoOther(t *testing.T) {
	m := newManaged(t, "api_vocabulary_scope")
	ctx := context.Background()
	if err := migrate.ProvisionScope(ctx, m.pool, m.schema.String(), "p2"); err != nil {
		t.Fatal(err)
	}
	addRelation(t, m, declaration("contracted_to"), http.StatusCreated)

	var listed struct {
		Project    string `json:"project"`
		Predicates []struct {
			Predicate string `json:"predicate"`
		} `json:"predicates"`
	}
	decodeJSON(t, m.manageDo(t, "/vocabulary/list", map[string]any{"project": "p2"}, m.operator, http.StatusOK, nil), &listed)
	if listed.Project != "p2" || !holdsRelation(listed.Predicates, "contracted_to") {
		t.Fatalf("a relation added under one project is not admitted under another in the same instance: %+v", listed)
	}

	elsewhere := newManaged(t, "api_vocabulary_elsewhere")
	decodeJSON(t, elsewhere.manageDo(t, "/vocabulary/list", map[string]any{"project": "p1"}, elsewhere.operator, http.StatusOK, nil), &listed)
	if holdsRelation(listed.Predicates, "contracted_to") {
		t.Fatal("a relation added to one instance's vocabulary reached another instance's")
	}
}

// The memory identity reads the vocabulary and cannot change it, which is what keeps the new write
// authority to the one surface that was given it.
//
// The identity that serves memory is the one handling text written by somebody else and output
// produced by a model, so it is the statement most likely to be shaped by hostile input. A write
// here would let such a statement widen what the next statement is allowed to store, which is the
// one escalation this table's privileges exist to prevent.
func TestTheMemoryIdentityReadsTheVocabularyAndCannotChangeIt(t *testing.T) {
	m := newManaged(t, "api_vocabulary_privileges")
	ctx := context.Background()
	config := m.pool.Config()
	config.ConnConfig.User = migrate.DataRole
	config.ConnConfig.Password = "taisce-test-data"
	memory, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer memory.Close()
	var n int
	if err := memory.QueryRow(ctx, m.schema.SQL(`SELECT count(*) FROM {schema}.predicate`)).Scan(&n); err != nil || n == 0 {
		t.Fatalf("the memory identity must be able to read the vocabulary it is told: %d %v", n, err)
	}
	for _, statement := range []string{
		`INSERT INTO {schema}.predicate(predicate,semantic_type,cardinality,object_kind,description)
            VALUES('forged','identity','many','thing','forged')`,
		`UPDATE {schema}.predicate SET description='forged' WHERE predicate='works_at'`,
		`UPDATE {schema}.predicate SET cardinality='many' WHERE predicate='has_name'`,
		`DELETE FROM {schema}.predicate WHERE predicate='works_at'`,
		`TRUNCATE {schema}.predicate`,
	} {
		if _, err := memory.Exec(ctx, m.schema.SQL(statement)); err == nil {
			t.Fatalf("the memory identity changed the vocabulary through %s", statement)
		}
	}
}

// A vocabulary operation whose table cannot be read is an internal error, and a body that is not
// the operation's is refused before anything is read.
//
// Both are the management surface's standing rules, asserted here for these two routes rather than
// assumed: an empty listing would read as "this instance admits nothing", which is the one answer
// that is never true.
func TestAVocabularyOperationThatCannotReachItsTableSaysSo(t *testing.T) {
	m := newManaged(t, "api_vocabulary_broken")
	ctx := context.Background()
	if _, err := m.pool.Exec(ctx, m.schema.SQL(`ALTER TABLE {schema}.predicate RENAME TO predicate_moved`)); err != nil {
		t.Fatal(err)
	}
	m.manageDo(t, "/vocabulary/list", map[string]any{"project": "p1"}, m.operator, http.StatusInternalServerError, nil)
	addRelation(t, m, declaration("contracted_to"), http.StatusInternalServerError)
	if _, err := m.pool.Exec(ctx, m.schema.SQL(`ALTER TABLE {schema}.predicate_moved RENAME TO predicate`)); err != nil {
		t.Fatal(err)
	}
	// The project an operation is attributed to cannot be resolved: the same answer, because an
	// operation served without being attributable is the one thing this route must not do.
	if _, err := m.pool.Exec(ctx, m.schema.SQL(`ALTER TABLE {schema}.project RENAME TO project_moved`)); err != nil {
		t.Fatal(err)
	}
	m.manageDo(t, "/vocabulary/list", map[string]any{"project": "p1"}, m.operator, http.StatusInternalServerError, nil)
	addRelation(t, m, declaration("contracted_to"), http.StatusInternalServerError)
	if _, err := m.pool.Exec(ctx, m.schema.SQL(`ALTER TABLE {schema}.project_moved RENAME TO project`)); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/vocabulary/add", "/vocabulary/list"} {
		if status, raw := rawAgainst(t, m.manage.URL, http.MethodPost, api.ManagementPrefix+path,
			map[string]any{"nonsense": true}, m.operator); status != http.StatusBadRequest {
			t.Fatalf("%s must refuse an unknown field, got %d: %s", path, status, raw)
		}
	}
	// A project is required on both, because an operation attributed to nothing is one nobody can
	// find afterwards.
	for _, path := range []string{"/vocabulary/add", "/vocabulary/list"} {
		if status, raw := rawAgainst(t, m.manage.URL, http.MethodPost, api.ManagementPrefix+path,
			map[string]any{"project": "  "}, m.operator); status != http.StatusBadRequest {
			t.Fatalf("%s must refuse an unnamed project, got %d: %s", path, status, raw)
		}
	}
}
