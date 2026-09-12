// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package formation_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ensera-ai/taisce/internal/community"
	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/formation"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/report"
)

// scriptedWriter stands in for the model. What a model does with good material is measured against a
// live one; what this test is about is the pass — what it partitions, what it asks to have
// written, and what an erasure then does to the answer.
type scriptedWriter struct {
	seen []report.Context
	fail bool
}

func (w *scriptedWriter) Write(_ context.Context, c report.Context) (report.Report, error) {
	w.seen = append(w.seen, c)
	if w.fail {
		return report.Report{}, fmt.Errorf("the provider is down")
	}
	return report.Report{
		Title:            "Subject of " + strings.Join(c.Entities, " and "),
		Summary:          "Written from " + fmt.Sprint(len(c.Facts)) + " relations.",
		Importance:       5,
		ImportanceReason: "It is the material there was most of.",
		Findings:         []report.Finding{{Summary: "a finding", Explanation: "from the material"}},
	}, nil
}

// subjectsFixture writes two densely connected groups that share no entity, which is the shape of a
// person's memory rather than of a document corpus: work and a hobby, connected to nothing between
// them.
func subjectsFixture(t *testing.T, pool *pgxpool.Pool, schema pg.Schema, subject string) {
	t.Helper()
	ctx := context.Background()
	obs := pg.NewObservationStore(pool)
	facts := pg.NewFactStore(pool)

	// The cardinality is the vocabulary's, not the fixture's: a relation holding one value at a time
	// is refused by the foreign key if it is asserted as accumulating, which is the constraint doing
	// its job on a test that got it wrong.
	type relation struct {
		subject, predicate, object string
		cardinality                domain.Cardinality
	}
	groups := map[string][]relation{
		"work": {
			{"Marta", "works_at", "Ensera", domain.CardinalityMany},
			{"Marta", "responsible_for", "the migration", domain.CardinalityMany},
			{"the migration", "depends_on", "Ensera", domain.CardinalityMany},
		},
		"hobby": {
			{"Tomas", "committed_to", "the cycling club", domain.CardinalityMany},
			{"the cycling club", "located_in", "the coast road", domain.CardinalityOne},
			{"Tomas", "knows", "the coast road", domain.CardinalityMany},
		},
	}
	for _, relations := range groups {
		for _, r := range relations {
			content := fmt.Sprintf("%s %s %s.", r.subject,
				strings.ReplaceAll(r.predicate, "_", " "), r.object)
			stored, err := obs.Append(ctx, schema, domain.Turn{
				Scope: "p1", DataSubjectID: subject,
				OccurredAt: time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC),
				Messages: []domain.Message{{Ordinal: 0, Role: domain.RoleUser, Content: content,
					OccurredAt: time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)}},
			})
			if err != nil {
				t.Fatalf("append: %v", err)
			}
			quote := strings.TrimSuffix(content, ".")
			if _, err := facts.Assert(ctx, schema, "p1", stored.ID, domain.RoleUser, subject,
				domain.Claim{
					Subject: r.subject, Predicate: r.predicate, Object: r.object,
					Cardinality: r.cardinality, Statement: content, Quote: quote,
					ByteStart: 0, ByteEnd: len(quote),
				}); err != nil {
				t.Fatalf("assert %s: %v", r.predicate, err)
			}
		}
	}
}

// ── The pass, end to end ──────────────────────────────────────────────────────────────────────

func TestASubjectIsFormedAndDescribedFromWhatWasSaid(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "fm_subjects")
	subjectsFixture(t, pool, schema, "subject-1")

	writer := &scriptedWriter{}
	pass, err := formation.NewSubjects(pg.NewCommunityStore(pool, schema), report.New(writer)).
		Run(ctx, "p1", 50)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	// Two groups sharing no entity are two subjects, not one.
	if pass.Communities != 2 {
		t.Fatalf("two unconnected groups produced %d subjects", pass.Communities)
	}
	if pass.Written != 2 || pass.Failed != 0 {
		t.Fatalf("wrote %d reports with %d failures", pass.Written, pass.Failed)
	}

	// Each report was written from the material of its own subject, and from nothing else. A report
	// handed another subject's facts would describe something the community does not contain.
	if len(writer.seen) != 2 {
		t.Fatalf("the model was asked %d times", len(writer.seen))
	}
	for _, c := range writer.seen {
		names := strings.Join(c.Entities, " ")
		if strings.Contains(names, "Marta") && strings.Contains(names, "Tomas") {
			t.Fatalf("one report was written from two subjects' material: %v", c.Entities)
		}
		if len(c.Facts) == 0 {
			t.Fatal("a report was written from no relations")
		}
		for _, f := range c.Facts {
			if f.Quote == "" {
				t.Fatal("a fact reached the model without the words behind it")
			}
		}
	}

	var communities, members, reports int
	if err := pool.QueryRow(ctx, schema.SQL(`
		SELECT (SELECT count(*) FROM {schema}.community WHERE scope = 'p1'),
		       (SELECT count(*) FROM {schema}.community_member WHERE scope = 'p1'),
		       (SELECT count(*) FROM {schema}.community_report WHERE scope = 'p1')`)).
		Scan(&communities, &members, &reports); err != nil {
		t.Fatalf("count: %v", err)
	}
	if communities != 2 || reports != 2 {
		t.Fatalf("%d communities and %d reports were stored", communities, reports)
	}
	if members == 0 {
		t.Fatal("no memberships were stored, so nothing says what a subject contains")
	}
}

// A second run over an unchanged scope writes nothing new. Without this the pass would pay a model
// call per subject on every tick, forever, for material that has not moved.
func TestASecondPassOverAnUnchangedScopeWritesNothing(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "fm_subjects_again")
	subjectsFixture(t, pool, schema, "subject-1")

	writer := &scriptedWriter{}
	pass := formation.NewSubjects(pg.NewCommunityStore(pool, schema), report.New(writer))
	if _, err := pass.Run(ctx, "p1", 50); err != nil {
		t.Fatalf("first run: %v", err)
	}
	first := len(writer.seen)

	second, err := pass.Run(ctx, "p1", 50)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if second.Written != 0 {
		t.Fatalf("the second pass rewrote %d reports over unchanged material", second.Written)
	}
	if len(writer.seen) != first {
		t.Fatalf("the model was asked %d more times for material that had not moved",
			len(writer.seen)-first)
	}
}

// A provider that is down costs a tick, not a scope. The next pass picks up what has no report, which
// is what selecting on the absence of one already arranges.
func TestAModelThatIsDownLosesATickRatherThanAScope(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "fm_subjects_down")
	subjectsFixture(t, pool, schema, "subject-1")

	writer := &scriptedWriter{fail: true}
	store := pg.NewCommunityStore(pool, schema)
	pass, err := formation.NewSubjects(store, report.New(writer)).Run(ctx, "p1", 50)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if pass.Failed != 2 || pass.Written != 0 {
		t.Fatalf("wrote %d and failed %d", pass.Written, pass.Failed)
	}
	// The subjects are still there. Only the prose is missing, and the next pass knows which.
	if pass.Communities != 2 {
		t.Fatalf("a failing model cost the partition: %d communities", pass.Communities)
	}

	writer.fail = false
	recovered, err := formation.NewSubjects(store, report.New(writer)).Run(ctx, "p1", 50)
	if err != nil {
		t.Fatalf("recovered run: %v", err)
	}
	if recovered.Written != 2 {
		t.Fatalf("the next pass wrote %d of the two that were missing", recovered.Written)
	}
}

// A deployment with no model still forms subjects. That is what the portal needs to show the shape of
// a memory, and the alternative is a pass that fails on every tick in a configuration that is
// otherwise working.
func TestASubjectFormsWithoutAModel(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "fm_subjects_nomodel")
	subjectsFixture(t, pool, schema, "subject-1")

	pass, err := formation.NewSubjects(pg.NewCommunityStore(pool, schema), nil).Run(ctx, "p1", 50)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if pass.Communities != 2 || pass.Written != 0 || pass.Failed != 0 {
		t.Fatalf("%+v", pass)
	}
}

// An empty scope is every scope's first hour, not an error.
func TestAScopeWithNoRelationsFormsNothingAndSaysSo(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "fm_subjects_empty")

	pass, err := formation.NewSubjects(pg.NewCommunityStore(pool, schema), report.New(&scriptedWriter{})).
		Run(ctx, "p1", 50)
	if err != nil {
		t.Fatalf("an empty scope was an error: %v", err)
	}
	if pass.Communities != 0 || pass.Entities != 0 {
		t.Fatalf("%+v", pass)
	}
}

// ── The governance property, which is the reason the storage is shaped this way ───────────────

// A report holds what somebody said. Erasing them removes it, counted in the same receipt as
// everything else — and it is removed even though the other subject also contributed to it, because
// its kind declares that sharing does not save text.
func TestErasingOneContributorRemovesTheProseTheyAreIn(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "fm_subjects_erase")

	// One subject per group, so each report has exactly one contributor — and then a turn from the
	// other subject about the same group, so one report has two.
	subjectsFixture(t, pool, schema, "subject-1")
	obs := pg.NewObservationStore(pool)
	facts := pg.NewFactStore(pool)
	content := "Marta knows Tomas."
	stored, err := obs.Append(ctx, schema, domain.Turn{
		Scope: "p1", DataSubjectID: "subject-2",
		OccurredAt: time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC),
		Messages: []domain.Message{{Ordinal: 0, Role: domain.RoleUser, Content: content,
			OccurredAt: time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)}},
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if _, err := facts.Assert(ctx, schema, "p1", stored.ID, domain.RoleUser, "subject-2",
		domain.Claim{Subject: "Marta", Predicate: "knows", Object: "Tomas",
			Cardinality: domain.CardinalityMany, Statement: content, Quote: "Marta knows Tomas",
			ByteStart: 0, ByteEnd: 17}); err != nil {
		t.Fatalf("assert: %v", err)
	}

	if _, err := formation.NewSubjects(pg.NewCommunityStore(pool, schema),
		report.New(&scriptedWriter{})).Run(ctx, "p1", 50); err != nil {
		t.Fatalf("run: %v", err)
	}
	var before int
	if err := pool.QueryRow(ctx, schema.SQL(
		`SELECT count(*) FROM {schema}.community_report WHERE scope = 'p1'`)).Scan(&before); err != nil {
		t.Fatalf("count: %v", err)
	}
	if before == 0 {
		t.Fatal("nothing was written, so erasing it proves nothing")
	}

	receipt, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", "subject-2", "subject request")
	if err != nil {
		t.Fatalf("erase: %v", err)
	}
	if !receipt.Clean() {
		t.Fatalf("the erasure left a residual: %+v", receipt.Residual)
	}
	if _, covered := receipt.Residual[pg.ProjectionCommunityReport]; !covered {
		t.Fatal("the receipt says nothing about reports, which is how a residual stops meaning what " +
			"it says")
	}
	if receipt.Deleted[pg.ProjectionCommunityReport] == 0 {
		t.Fatal("a report the erased subject contributed to was kept, because the other contributor " +
			"survived — which is the leak the kind's sharing rule exists to close")
	}
}

// A subject that did not change keeps its name, and a rebuild reproduces it.
//
// This is what makes the pass affordable and what makes a restore not rename everything: the
// identifier is a function of the membership, so the same facts produce the same partition and the
// same partition produces the same identifiers.
func TestASubjectThatDidNotChangeKeepsItsIdentity(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "fm_subject_identity")
	subjectsFixture(t, pool, schema, "subject-1")

	store := pg.NewCommunityStore(pool, schema)
	pass := formation.NewSubjects(store, report.New(&scriptedWriter{}))
	if _, err := pass.Run(ctx, "p1", 50); err != nil {
		t.Fatalf("first run: %v", err)
	}
	before := subjectIdentities(t, pool, schema)

	if _, err := pass.Run(ctx, "p1", 50); err != nil {
		t.Fatalf("second run: %v", err)
	}
	after := subjectIdentities(t, pool, schema)

	if len(before) == 0 {
		t.Fatal("nothing was formed, so this asserts nothing")
	}
	if strings.Join(before, ",") != strings.Join(after, ",") {
		t.Fatalf("re-forming an unchanged scope renamed its subjects:\n before %v\n  after %v",
			before, after)
	}

	// And the prose written about them survived, which is the cost this identity exists to avoid.
	var written int
	if err := pool.QueryRow(ctx, schema.SQL(
		`SELECT count(*) FROM {schema}.community_report WHERE scope = 'p1'`)).Scan(&written); err != nil {
		t.Fatalf("count: %v", err)
	}
	if written != len(before) {
		t.Fatalf("%d subjects have %d reports between them", len(before), written)
	}
}

// A subject that gained a member is a different subject, and what was written about the old one goes
// with it — because a report describes a set of members, and that set no longer exists.
func TestASubjectThatChangedLosesTheProseAboutTheOldOne(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "fm_subject_changed")
	subjectsFixture(t, pool, schema, "subject-1")

	store := pg.NewCommunityStore(pool, schema)
	writer := &scriptedWriter{}
	if _, err := formation.NewSubjects(store, report.New(writer)).Run(ctx, "p1", 50); err != nil {
		t.Fatalf("first run: %v", err)
	}
	before := subjectIdentities(t, pool, schema)

	// A relation that joins the two groups: what were two subjects may now be one, and either way the
	// membership of at least one of them has changed.
	obs := pg.NewObservationStore(pool)
	facts := pg.NewFactStore(pool)
	content := "Marta knows Tomas."
	stored, err := obs.Append(ctx, schema, domain.Turn{
		Scope: "p1", DataSubjectID: "subject-1",
		OccurredAt: time.Date(2026, 3, 3, 9, 0, 0, 0, time.UTC),
		Messages: []domain.Message{{Ordinal: 0, Role: domain.RoleUser, Content: content,
			OccurredAt: time.Date(2026, 3, 3, 9, 0, 0, 0, time.UTC)}},
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	for i := 0; i < 6; i++ {
		// Enough weight that the two groups genuinely merge rather than staying apart, so the
		// membership change is the thing being tested rather than the algorithm's threshold.
		if _, err := facts.Assert(ctx, schema, "p1", stored.ID, domain.RoleUser, "subject-1",
			domain.Claim{Subject: "Marta", Predicate: "knows", Object: "Tomas",
				Cardinality: domain.CardinalityMany,
				Statement:   content, Quote: "Marta knows Tomas", ByteStart: 0, ByteEnd: 17}); err != nil {
			t.Fatalf("assert: %v", err)
		}
	}

	if _, err := formation.NewSubjects(store, report.New(writer)).Run(ctx, "p1", 50); err != nil {
		t.Fatalf("second run: %v", err)
	}
	after := subjectIdentities(t, pool, schema)
	if strings.Join(before, ",") == strings.Join(after, ",") {
		t.Fatal("joining two subjects with six relations changed no subject's membership, so this " +
			"test is not exercising what it says")
	}

	// Every surviving subject has a report, and no report is left describing a set that is gone.
	var subjects, reports, orphans int
	if err := pool.QueryRow(ctx, schema.SQL(`
		SELECT (SELECT count(*) FROM {schema}.community WHERE scope = 'p1'),
		       (SELECT count(*) FROM {schema}.community_report WHERE scope = 'p1'),
		       (SELECT count(*) FROM {schema}.community_report r
		         WHERE r.scope = 'p1' AND NOT EXISTS (
		               SELECT 1 FROM {schema}.community c
		                WHERE c.scope = r.scope AND c.community_id = r.community_id))`)).
		Scan(&subjects, &reports, &orphans); err != nil {
		t.Fatalf("count: %v", err)
	}
	if orphans != 0 {
		t.Fatalf("%d reports describe a subject that no longer exists", orphans)
	}
	if reports != subjects {
		t.Fatalf("%d subjects and %d reports", subjects, reports)
	}
}

func subjectIdentities(t *testing.T, pool *pgxpool.Pool, schema pg.Schema) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), schema.SQL(
		`SELECT community_id::text FROM {schema}.community WHERE scope = 'p1' ORDER BY community_id`))
	if err != nil {
		t.Fatalf("read subjects: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, id)
	}
	return out
}

// A parent too large to describe from its own facts is described from its children's reports.
//
// This is the roll-up, end to end and against a real partition rather than a hand-built one: two dense
// groups tied together enough to be one subject at the coarse scale, large enough to be split, and
// with more material than a report is written from — which is the only arrangement in which
// substitution is what happens rather than a branch nobody takes.
func TestAParentIsDescribedFromItsChildrenWhenItsOwnMaterialDoesNotFit(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "fm_rollup")
	twoGroupsTooLargeToDescribe(t, pool, schema)

	writer := &scriptedWriter{}
	store := pg.NewCommunityStore(pool, schema)
	pass, err := formation.NewSubjects(store, report.New(writer)).Run(ctx, "p1", 50)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if pass.Communities < 3 {
		t.Fatalf("expected a parent and its children, got %d communities", pass.Communities)
	}

	var parents, children int
	if err := pool.QueryRow(ctx, schema.SQL(`
		SELECT (SELECT count(*) FROM {schema}.community WHERE scope = 'p1' AND parent_id IS NULL),
		       (SELECT count(*) FROM {schema}.community WHERE scope = 'p1' AND parent_id IS NOT NULL)`)).
		Scan(&parents, &children); err != nil {
		t.Fatalf("count: %v", err)
	}
	if parents != 1 || children < 2 {
		t.Fatalf("%d parents and %d children", parents, children)
	}

	// The children were written first — the pass orders by descending level precisely so that a
	// parent has something to substitute by the time it is reached.
	if len(writer.seen) < 3 {
		t.Fatalf("the model was asked %d times for %d subjects", len(writer.seen), pass.Communities)
	}
	last := writer.seen[len(writer.seen)-1]
	if last.Substituted == 0 {
		t.Fatalf("the parent was written without substituting any child, so the roll-up did not "+
			"happen: %d facts, %d dropped", len(last.Facts), last.Dropped)
	}
	if len(last.Children) != last.Substituted {
		t.Fatalf("%d children substituted and %d reports carried", last.Substituted, len(last.Children))
	}
	for _, r := range last.Children {
		if r.Title == "" || r.Summary == "" {
			t.Fatal("a child was substituted by a report with nothing in it, which removes the " +
				"subject rather than compressing it")
		}
	}
}

// twoGroupsTooLargeToDescribe writes two dense groups, tied together enough to be one subject at the
// coarse scale and large enough that the coarse subject is split — with quotes long enough that the
// parent's own material does not fit in a report's budget.
func twoGroupsTooLargeToDescribe(t *testing.T, pool *pgxpool.Pool, schema pg.Schema) {
	t.Helper()
	ctx := context.Background()
	obs := pg.NewObservationStore(pool)
	facts := pg.NewFactStore(pool)

	// Sized from the partitioner's own bound, so the fixture stays honest if the bound moves.
	const half = 13
	if half*2 <= community.MaxSize {
		t.Fatalf("the fixture is not larger than the split bound of %d", community.MaxSize)
	}

	stored, err := obs.Append(ctx, schema, domain.Turn{
		Scope: "p1", DataSubjectID: "subject-1",
		OccurredAt: time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC),
		Messages: []domain.Message{{Ordinal: 0, Role: domain.RoleUser,
			Content:    "Everybody here knows everybody else.",
			OccurredAt: time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)}},
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}

	// Long enough that the parent's material overflows the report budget and the roll-up has to
	// substitute rather than simply fitting everything.
	padding := strings.Repeat("and they have said so at length on several occasions ", 4)
	assert := func(subject, object string) {
		t.Helper()
		if _, err := facts.Assert(ctx, schema, "p1", stored.ID, domain.RoleUser, "subject-1",
			domain.Claim{
				Subject: subject, Predicate: "knows", Object: object,
				Cardinality: domain.CardinalityMany,
				Statement:   subject + " knows " + object + ".",
				Quote:       "Everybody here knows everybody else", ByteStart: 0, ByteEnd: 35,
			}); err != nil {
			t.Fatalf("assert %s knows %s: %v", subject, object, err)
		}
	}

	for _, prefix := range []string{"a", "b"} {
		for i := 0; i < half; i++ {
			for j := i + 1; j < half; j++ {
				assert(fmt.Sprintf("%s%d %s", prefix, i, padding), fmt.Sprintf("%s%d %s", prefix, j, padding))
			}
		}
	}
	// The ties that make the two groups one subject at the coarse scale.
	for i := 0; i < half; i++ {
		assert(fmt.Sprintf("a%d %s", i, padding), fmt.Sprintf("b%d %s", i, padding))
	}
}

// identifiedWriter says who it is, which is what lets a report record what wrote it.
type identifiedWriter struct {
	scriptedWriter
	identity string
}

func (w *identifiedWriter) ReportIdentity() string { return w.identity }

// ── A report carries what wrote it, at the pass ───────────────────────────────────────────────
//
// A prompt edit is a change to behaviour (rule 14), and until a report recorded what wrote it the
// edit could not reach a single paragraph already stored. Now a writer that is not the one which
// wrote a report is offered the community back, and one that is gets nothing to do.
func TestAChangedWriterIsOfferedTheReportItDidNotWriteAndAnUnchangedOneIsNot(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "fm_subjects_writer")
	subjectsFixture(t, pool, schema, "subject-1")
	store := pg.NewCommunityStore(pool, schema)

	first := &identifiedWriter{identity: "report/v1:first"}
	if _, err := formation.NewSubjects(store, report.New(first)).Run(ctx, "p1", 50); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if len(first.seen) == 0 {
		t.Fatal("nothing was written, so this test asserts nothing")
	}

	// The same writer again: nothing to do.
	written := len(first.seen)
	if pass, err := formation.NewSubjects(store, report.New(first)).Run(ctx, "p1", 50); err != nil || pass.Written != 0 {
		t.Fatalf("the same writer rewrote %d reports: %v", pass.Written, err)
	}
	if len(first.seen) != written {
		t.Fatal("the same writer was asked again for material that had not moved")
	}

	// A different one — a tuned prompt, a changed model — and every report is offered back.
	second := &identifiedWriter{identity: "report/v1:second"}
	pass, err := formation.NewSubjects(store, report.New(second)).Run(ctx, "p1", 50)
	if err != nil {
		t.Fatalf("the changed writer failed: %v", err)
	}
	if pass.Written != written {
		t.Fatalf("a changed writer rewrote %d of %d reports", pass.Written, written)
	}

	// And a writer that cannot say who it is gets no identity rather than a made-up one, so it is
	// offered everything and can never be evidence that two reports share rules.
	anonymous := &scriptedWriter{}
	if pass, err := formation.NewSubjects(store, report.New(anonymous)).Run(ctx, "p1", 50); err != nil || pass.Written != written {
		t.Fatalf("an unidentified writer wrote %d of %d: %v", pass.Written, written, err)
	}
	// A pass with no writer at all still forms subjects and writes nothing.
	if pass, err := formation.NewSubjects(store, nil).Run(ctx, "p1", 50); err != nil || pass.Written != 0 {
		t.Fatalf("a pass with no writer wrote %d reports: %v", pass.Written, err)
	}
}
