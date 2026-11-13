// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// everyPageAbout walks an entity's pages to the end and returns what they held, failing on a
// record returned twice, out of order, or on a page longer than it was asked for. A page that is
// right one at a time and wrong as a sequence is the defect a cursor exists to prevent.
func everyPageAbout(t *testing.T, ctx context.Context, store *pg.RecordStore, scope, entity, subject string, limit int) []pg.RecordSummary {
	t.Helper()
	var all []pg.RecordSummary
	var cursor *pg.RecordCursor
	last := ""
	for pages := 0; ; pages++ {
		if pages > 1000 {
			t.Fatal("the pages did not end")
		}
		page, err := store.ListAbout(ctx, scope, entity, subject, cursor, limit)
		if err != nil {
			t.Fatalf("page %d: %v", pages, err)
		}
		if len(page.Records) > limit {
			t.Fatalf("page %d held %d records for a limit of %d", pages, len(page.Records), limit)
		}
		for _, record := range page.Records {
			if record.ID <= last {
				t.Fatalf("record %s came after %s: repeated or out of order", record.ID, last)
			}
			last = record.ID
			all = append(all, record)
		}
		if page.Next == nil {
			return all
		}
		cursor = page.Next
	}
}

func idsOf(records []pg.RecordSummary) []string {
	ids := make([]string, 0, len(records))
	for _, record := range records {
		ids = append(ids, record.ID)
	}
	sort.Strings(ids)
	return ids
}

func entityNamed(t *testing.T, ctx context.Context, pool *pgxpool.Pool, schema pg.Schema, normalized string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT entity_id::text FROM {schema}.entity WHERE scope='p1' AND normalized_name=$1`), normalized).Scan(&id); err != nil {
		t.Fatalf("entity %q: %v", normalized, err)
	}
	return id
}

// TestAnEntitysPagesHoldEveryCurrentFactThatNamesItAndNoOtherEntitys is the property the filter
// exists for: paged to the end, an entity's inventory is exactly the facts that hold now and name
// it — as subject, as object, or as both — and nothing else.
//
// The fixture puts beside them everything that must not appear: facts between other entities,
// facts of this entity whose validity closed, facts no longer believed, and one past its retention
// that the sweep has not reached. The expected set is computed from the table by the definition,
// not by the statement under test, and the unfiltered listing is asked to agree about each one.
func TestAnEntitysPagesHoldEveryCurrentFactThatNamesItAndNoOtherEntitys(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "record_entity_page")
	if _, err := pool.Exec(ctx, schema.SQL(`
INSERT INTO {schema}.entity(entity_id,scope,canonical_name,normalized_name)
SELECT md5('entity '||n)::uuid,'p1','entity '||n,'entity '||n FROM generate_series(1,3) n;
-- holds now, entity 1 as subject
INSERT INTO {schema}.fact(fact_id,scope,subject_entity_id,object_entity_id,predicate,statement,cardinality,valid,known,source_role)
SELECT md5('as subject '||n)::uuid,'p1',md5('entity 1')::uuid,md5('entity 2')::uuid,'works_at','entity 1 as subject','many','[2020-01-01,)','[2020-01-01,)','user' FROM generate_series(1,7) n;
-- holds now, entity 1 as object
INSERT INTO {schema}.fact(fact_id,scope,subject_entity_id,object_entity_id,predicate,statement,cardinality,valid,known,source_role)
SELECT md5('as object '||n)::uuid,'p1',md5('entity 2')::uuid,md5('entity 1')::uuid,'works_at','entity 1 as object','many','[2020-01-01,)','[2020-01-01,)','user' FROM generate_series(1,5) n;
-- holds now, entity 1 as both: one record, though both arms find it
INSERT INTO {schema}.fact(fact_id,scope,subject_entity_id,object_entity_id,predicate,statement,cardinality,valid,known,source_role)
VALUES (md5('as both')::uuid,'p1',md5('entity 1')::uuid,md5('entity 1')::uuid,'works_at','entity 1 as both','many','[2020-01-01,)','[2020-01-01,)','user');
-- stopped being true
INSERT INTO {schema}.fact(fact_id,scope,subject_entity_id,object_entity_id,predicate,statement,cardinality,valid,known,source_role)
SELECT md5('validity closed '||n)::uuid,'p1',md5('entity 1')::uuid,md5('entity 2')::uuid,'works_at','no longer true','many','[2020-01-01,2021-01-01)','[2020-01-01,)','user' FROM generate_series(1,4) n;
-- stopped being believed
INSERT INTO {schema}.fact(fact_id,scope,subject_entity_id,object_entity_id,predicate,statement,cardinality,valid,known,source_role)
SELECT md5('knowledge closed '||n)::uuid,'p1',md5('entity 2')::uuid,md5('entity 1')::uuid,'works_at','no longer believed','many','[2020-01-01,)','[2020-01-01,2021-01-01)','user' FROM generate_series(1,2) n;
-- past its retention and not yet swept
INSERT INTO {schema}.fact(fact_id,scope,subject_entity_id,object_entity_id,predicate,statement,cardinality,valid,known,source_role,retention_until)
VALUES (md5('expired')::uuid,'p1',md5('entity 1')::uuid,md5('entity 2')::uuid,'works_at','expired','many','[2020-01-01,)','[2020-01-01,)','user','2021-01-01');
-- other entities' facts
INSERT INTO {schema}.fact(fact_id,scope,subject_entity_id,object_entity_id,predicate,statement,cardinality,valid,known,source_role)
SELECT md5('others '||n)::uuid,'p1',md5('entity 2')::uuid,md5('entity 3')::uuid,'works_at','between two others','many','[2020-01-01,)','[2020-01-01,)','user' FROM generate_series(1,30) n`)); err != nil {
		t.Fatal(err)
	}
	var entity string
	var want []string
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT md5('entity 1')::uuid::text, array_agg(fact_id::text ORDER BY fact_id::text)
  FROM {schema}.fact
 WHERE scope='p1' AND (subject_entity_id=md5('entity 1')::uuid OR object_entity_id=md5('entity 1')::uuid)
   AND upper_inf(valid) AND upper_inf(known) AND (retention_until IS NULL OR retention_until > now())`)).Scan(&entity, &want); err != nil {
		t.Fatal(err)
	}
	if len(want) != 13 {
		t.Fatalf("the fixture holds %d current facts naming the entity, not the 13 it was built with", len(want))
	}
	store := pg.NewRecordStore(pool, schema)
	// Every page size that matters: one that splits both arms mid-way, one that ends exactly on the
	// last record, and one larger than everything.
	for _, limit := range []int{1, 3, 13, pg.MaxRecordPage} {
		got := everyPageAbout(t, ctx, store, "p1", entity, "", limit)
		for _, record := range got {
			subject, object := "", ""
			if record.SubjectID != nil {
				subject = *record.SubjectID
			}
			if record.ObjectID != nil {
				object = *record.ObjectID
			}
			if subject != entity && object != entity {
				t.Fatalf("limit %d: record %s names %s and %s, not the entity asked about", limit, record.ID, subject, object)
			}
			if record.Status != "current" {
				t.Fatalf("limit %d: record %s is %s; the entity's page holds what holds now", limit, record.ID, record.Status)
			}
		}
		if ids := idsOf(got); strings.Join(ids, ",") != strings.Join(want, ",") {
			t.Fatalf("limit %d: the pages held %d records, the entity has %d current facts\n got %v\nwant %v", limit, len(ids), len(want), ids, want)
		}
	}
	// The unfiltered listing describes each of them the same way: the filter narrows, it does not
	// produce a different record.
	whole := map[string]pg.RecordSummary{}
	var cursor *pg.RecordCursor
	for {
		page, err := store.List(ctx, "p1", "", cursor, pg.MaxRecordPage)
		if err != nil {
			t.Fatal(err)
		}
		for _, record := range page.Records {
			whole[record.ID] = record
		}
		if cursor = page.Next; cursor == nil {
			break
		}
	}
	for _, record := range everyPageAbout(t, ctx, store, "p1", entity, "", 5) {
		same, listed := whole[record.ID]
		if !listed || same.Version != record.Version || same.Status != record.Status || same.StatementPreview != record.StatementPreview || same.Predicate != record.Predicate {
			t.Fatalf("record %s is not the record the unfiltered listing holds: %+v against %+v", record.ID, record, same)
		}
	}
	if _, listed := whole[uuid.MustParse(md5Hex(t, ctx, pool, "expired")).String()]; listed {
		t.Fatal("the unfiltered listing returned a fact past its retention; the fixture no longer tests what it says")
	}
}

func md5Hex(t *testing.T, ctx context.Context, pool *pgxpool.Pool, text string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(ctx, `SELECT md5($1)::uuid::text`, text).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// TestAnEntityInAnotherProjectReturnsNothing holds the project boundary on the new filter. An
// entity id is a position in a request, never an authority: a credential for one project that
// names another project's entity gets the page an id nobody has would get, so the answer does not
// say whether the entity exists.
func TestAnEntityInAnotherProjectReturnsNothing(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "record_entity_project")
	if err := migrate.ProvisionScope(ctx, pool, schema.String(), "p2"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`
INSERT INTO {schema}.entity(entity_id,scope,canonical_name,normalized_name)
SELECT md5(p||' entity '||n)::uuid,p,'entity '||n,'entity '||n FROM unnest(ARRAY['p1','p2']) p CROSS JOIN generate_series(1,2) n;
INSERT INTO {schema}.fact(fact_id,scope,subject_entity_id,object_entity_id,predicate,statement,cardinality,valid,known,source_role)
SELECT md5(p||' fact '||n)::uuid,p,md5(p||' entity 1')::uuid,md5(p||' entity 2')::uuid,'works_at','in '||p,'many','[2020-01-01,)','[2020-01-01,)','user'
  FROM unnest(ARRAY['p1','p2']) p CROSS JOIN generate_series(1,6) n`)); err != nil {
		t.Fatal(err)
	}
	foreign := md5Hex(t, ctx, pool, "p2 entity 1")
	store := pg.NewRecordStore(pool, schema)
	// The fixture is real: asked in its own project, the entity has its six facts.
	if own := everyPageAbout(t, ctx, store, "p2", foreign, "", 4); len(own) != 6 {
		t.Fatalf("the entity has %d facts in its own project, not 6", len(own))
	}
	across, err := store.ListAbout(ctx, "p1", foreign, "", nil, pg.MaxRecordPage)
	if err != nil {
		t.Fatal(err)
	}
	unknown, err := store.ListAbout(ctx, "p1", uuid.NewString(), "", nil, pg.MaxRecordPage)
	if err != nil {
		t.Fatal(err)
	}
	if len(across.Records) != 0 || across.Next != nil {
		t.Fatalf("a project was given %d facts of another project's entity", len(across.Records))
	}
	if len(unknown.Records) != 0 || unknown.Next != nil {
		t.Fatal("an entity nobody has returned records")
	}
	// A cursor taken in the entity's own project widens nothing either.
	first, err := store.ListAbout(ctx, "p2", foreign, "", nil, 2)
	if err != nil || first.Next == nil {
		t.Fatalf("no cursor to reuse: %v", err)
	}
	if reused, err := store.ListAbout(ctx, "p1", foreign, "", first.Next, pg.MaxRecordPage); err != nil || len(reused.Records) != 0 {
		t.Fatalf("a cursor from another project returned %d records: %v", len(reused.Records), err)
	}
}

// TestADataSubjectStillNarrowsAnEntitysPage holds the two filters to meaning both. Two people
// each said something about the same organisation; the organisation's page narrowed to one of them
// holds that person's fact and not the other's, by the same rule the subject listing applies — a
// registered source of that subject supports the fact.
func TestADataSubjectStillNarrowsAnEntitysPage(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "record_entity_subject")
	formed(t, pool, schema,
		subjectTurn("subject-1", "Marta works at Ensera."),
		subjectTurn("subject-2", "Tomas works at Ensera."))
	ensera := entityNamed(t, ctx, pool, schema, "ensera")
	store := pg.NewRecordStore(pool, schema)

	both := everyPageAbout(t, ctx, store, "p1", ensera, "", 1)
	if len(both) != 2 {
		t.Fatalf("the organisation's page holds %d facts, not the two that were said about it", len(both))
	}
	for subject, name := range map[string]string{"subject-1": "Marta", "subject-2": "Tomas"} {
		narrowed := everyPageAbout(t, ctx, store, "p1", ensera, subject, 1)
		if len(narrowed) != 1 || !strings.Contains(narrowed[0].StatementPreview, name) {
			t.Fatalf("narrowed to %s the page holds %+v; it should hold the one fact %s's source supports", subject, narrowed, name)
		}
		// And it is the fact the subject listing holds for that person.
		listed, err := store.List(ctx, "p1", subject, nil, pg.MaxRecordPage)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, record := range listed.Records {
			found = found || record.ID == narrowed[0].ID
		}
		if !found {
			t.Fatalf("the entity's page narrowed to %s holds a record the subject listing does not", subject)
		}
	}
	if nobody := everyPageAbout(t, ctx, store, "p1", ensera, "subject-3", pg.MaxRecordPage); len(nobody) != 0 {
		t.Fatalf("a subject who said nothing was given %d facts", len(nobody))
	}
}

// TestAnErasedSourcesFactsLeaveTheEntitysPage holds erasure on the new path. An entity two people
// named survives one person's erasure, and its page is then exactly what the other person's
// sources support: the erased person's fact is gone from it, not hidden behind a filter.
func TestAnErasedSourcesFactsLeaveTheEntitysPage(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "record_entity_erasure")
	formed(t, pool, schema,
		subjectTurn("subject-1", "Marta works at Ensera."),
		subjectTurn("subject-2", "Tomas works at Ensera."))
	ensera := entityNamed(t, ctx, pool, schema, "ensera")
	store := pg.NewRecordStore(pool, schema)
	if before := everyPageAbout(t, ctx, store, "p1", ensera, "", pg.MaxRecordPage); len(before) != 2 {
		t.Fatalf("before the erasure the page holds %d facts, not 2", len(before))
	}
	receipt, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", "subject-1", "subject request")
	if err != nil || !receipt.Clean() {
		t.Fatalf("erase: %v %+v", err, receipt)
	}
	after := everyPageAbout(t, ctx, store, "p1", ensera, "", pg.MaxRecordPage)
	if len(after) != 1 || !strings.Contains(after[0].StatementPreview, "Tomas") || strings.Contains(after[0].StatementPreview, "Marta") {
		t.Fatalf("after one person's erasure the entity's page holds %+v; it should hold the other person's fact alone", after)
	}
	if erased := everyPageAbout(t, ctx, store, "p1", ensera, "subject-1", pg.MaxRecordPage); len(erased) != 0 {
		t.Fatalf("the erased subject still has %d facts on the entity's page", len(erased))
	}
}

// TestABadEntityIDIsRefusedAsAnInvalidRecordPage holds every malformed request on the entity path
// to the listing's one refusal, before anything is read. A second refusal for the new field would
// be a second thing every caller has to handle for the same mistake.
func TestABadEntityIDIsRefusedAsAnInvalidRecordPage(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "record_entity_refusal")
	store := pg.NewRecordStore(pool, schema)
	entity := uuid.NewString()
	for name, request := range map[string]struct {
		scope, entity, subject string
		after                  *pg.RecordCursor
		limit                  int
	}{
		"not an id":                           {"p1", "ensera", "", nil, 20},
		"an id cut short":                     {"p1", entity[:35], "", nil, 20},
		"the id of nothing":                   {"p1", uuid.Nil.String(), "", nil, 20},
		"blank":                               {"p1", "  ", "", nil, 20},
		"an id and a bad cursor":              {"p1", entity, "", &pg.RecordCursor{ID: "bad"}, 20},
		"an id and no limit":                  {"p1", entity, "", nil, 0},
		"an id and a limit past the maximum":  {"p1", entity, "", nil, pg.MaxRecordPage + 1},
		"an id and a blank subject":           {"p1", entity, "  ", nil, 20},
		"an id and a project that is not one": {"not a project", entity, "", nil, 20},
	} {
		page, err := store.ListAbout(ctx, request.scope, request.entity, request.subject, request.after, request.limit)
		if !errors.Is(err, pg.ErrInvalidRecordPage) {
			t.Errorf("%s: %v, not the listing's refusal", name, err)
		}
		if len(page.Records) != 0 || page.Next != nil {
			t.Errorf("%s: a refused request returned records", name)
		}
	}
	// What is not refused: a well-formed id that names nothing is an empty page, as an empty
	// project is.
	if page, err := store.ListAbout(ctx, "p1", entity, "", nil, 20); err != nil || page.Records == nil || len(page.Records) != 0 {
		t.Fatalf("an entity with no facts: %v %+v", err, page)
	}
}
