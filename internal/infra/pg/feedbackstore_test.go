// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// feedbackFixture is one current record and the two stores that act on it.
func feedbackFixture(t *testing.T, name string) (context.Context, *pgxpool.Pool, pg.Schema, *pg.FeedbackStore, *pg.RecordStore, string) {
	t.Helper()
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, name)
	march := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	id := statedAt(t, ctx, pg.NewObservationStore(pool), pg.NewFactStore(pool), schema, march,
		"Atlas lives in Dublin", domain.Claim{Subject: "Atlas", Predicate: "lives_in", Object: "Dublin",
			Statement: "Atlas lives in Dublin", Cardinality: domain.CardinalityOne, ValidFrom: march})
	records := pg.NewRecordStore(pool, schema)
	return ctx, pool, schema, pg.NewFeedbackStore(pool, schema, records), records, id
}

func object(s string) *string { return &s }

// The property the whole feature exists for. A report of a doubt is not a change of belief, so
// recording one must leave the graph and the read path exactly as they were.
func TestRecordingFeedbackChangesNoFactAndNoRecall(t *testing.T) {
	ctx, pool, schema, feedback, _, id := feedbackFixture(t, "feedback_inert")
	resolved, err := pg.NewCitationStore(pool, schema).Resolve(ctx, "p1", id, nil, 8)
	if err != nil {
		t.Fatal(err)
	}
	recall := pg.NewRecallStore(pool, schema)
	before, err := recall.FactsAbout(ctx, []string{"p1"}, []string{*resolved.SubjectID}, 10, []string{"user"}, domain.AsOf{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	var factsBefore, watermarkBefore int64
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT (SELECT count(*) FROM {schema}.fact),
        (SELECT count(*) FROM {schema}.fact WHERE upper_inf(known))`)).Scan(&factsBefore, &watermarkBefore); err != nil {
		t.Fatal(err)
	}
	written, err := feedback.Record(ctx, "p1", uuid.NewString(), pg.FeedbackRequest{
		RecordID: id, Note: "he moved last year", ProposedObject: object("Oslo")})
	if err != nil || written.ID == "" || written.RecordID != id || written.PromotedAt != nil {
		t.Fatalf("record feedback: %+v %v", written, err)
	}
	after, err := recall.FactsAbout(ctx, []string{"p1"}, []string{*resolved.SubjectID}, 10, []string{"user"}, domain.AsOf{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) || (len(after) == 1 && after[0].ID != before[0].ID) {
		t.Fatalf("recall changed: %+v then %+v", before, after)
	}
	var factsAfter, currentAfter int64
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT (SELECT count(*) FROM {schema}.fact),
        (SELECT count(*) FROM {schema}.fact WHERE upper_inf(known))`)).Scan(&factsAfter, &currentAfter); err != nil {
		t.Fatal(err)
	}
	if factsAfter != factsBefore || currentAfter != watermarkBefore {
		t.Fatalf("feedback wrote a fact: %d/%d became %d/%d", factsBefore, watermarkBefore, factsAfter, currentAfter)
	}
}

// A proposed object makes the promotion a correction, and it must be the same correction a caller
// could have written by hand: the same withdrawal, the same curated authorship, the same boundary.
func TestPromotingFeedbackWithAProposedObjectProducesTheCorrection(t *testing.T) {
	ctx, pool, schema, feedback, _, id := feedbackFixture(t, "feedback_promote_correct")
	actor := uuid.NewString()
	written, err := feedback.Record(ctx, "p1", uuid.NewString(), pg.FeedbackRequest{
		RecordID: id, Note: "Atlas lives in Oslo", ProposedObject: object("Oslo")})
	if err != nil {
		t.Fatal(err)
	}
	out, err := feedback.Promote(ctx, "p1", actor, pg.PromotionRequest{
		ID: written.ID, ExpectedVersion: recordMutation(t, pool, schema, id).ExpectedVersion})
	if err != nil || out.Operation != domain.AuditRecordCorrect || out.ReplacementID == nil {
		t.Fatalf("promote: %+v %v", out, err)
	}
	citations := pg.NewCitationStore(pool, schema)
	original, err := citations.Resolve(ctx, "p1", id, nil, 8)
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := citations.Resolve(ctx, "p1", *out.ReplacementID, nil, 8)
	if err != nil {
		t.Fatal(err)
	}
	if original.Retraction == nil || original.Retraction.ReplacementFactID == nil ||
		*original.Retraction.ReplacementFactID != *out.ReplacementID {
		t.Fatal("the promotion did not link the withdrawal to its replacement")
	}
	// The note becomes the replacement's own authored evidence, attributed to whoever promoted it.
	// Attributing it to the reporter would make a report into an assertion nobody approved.
	if replacement.Evidence[0].Quote != "Atlas lives in Oslo" ||
		replacement.Evidence[0].ExtractorVersion != pg.CuratedExtractorVersion ||
		replacement.Evidence[0].AuthoredBy == nil || *replacement.Evidence[0].AuthoredBy != actor {
		t.Fatalf("promoted correction lost curated authorship: %+v", replacement.Evidence[0])
	}
	if !original.Known.Until.Equal(*replacement.Known.From) {
		t.Fatal("the promotion left a gap or an overlap in knowledge time")
	}
	page, err := feedback.List(ctx, "p1", "", false, nil, pg.DefaultFeedbackPage)
	if err != nil || len(page.Feedback) != 1 || page.Feedback[0].PromotedAt == nil ||
		page.Feedback[0].PromotedBy == nil || *page.Feedback[0].PromotedBy != actor ||
		page.Feedback[0].ReplacementID == nil || *page.Feedback[0].ReplacementID != *out.ReplacementID {
		t.Fatalf("promotion was not recorded on the feedback: %+v %v", page.Feedback, err)
	}
}

// No proposed object means "this should not be here", and the promotion withdraws without replacing.
func TestPromotingFeedbackWithoutAProposedObjectProducesTheRetraction(t *testing.T) {
	ctx, pool, schema, feedback, _, id := feedbackFixture(t, "feedback_promote_retract")
	written, err := feedback.Record(ctx, "p1", uuid.NewString(), pg.FeedbackRequest{
		RecordID: id, Note: "he never lived there"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := feedback.Promote(ctx, "p1", uuid.NewString(), pg.PromotionRequest{
		ID: written.ID, ExpectedVersion: recordMutation(t, pool, schema, id).ExpectedVersion})
	if err != nil || out.Operation != domain.AuditRecordRetract || out.ReplacementID != nil {
		t.Fatalf("promote: %+v %v", out, err)
	}
	withdrawn, err := pg.NewCitationStore(pool, schema).Resolve(ctx, "p1", id, nil, 8)
	if err != nil {
		t.Fatal(err)
	}
	if withdrawn.Retraction == nil || withdrawn.Retraction.ReplacementFactID != nil {
		t.Fatalf("record was not withdrawn, or was replaced: %+v", withdrawn.Retraction)
	}
	current, err := pg.NewRecallStore(pool, schema).FactsAbout(ctx, []string{"p1"}, []string{*withdrawn.SubjectID}, 10, []string{"user"}, domain.AsOf{}, 1)
	if err != nil || len(current) != 0 {
		t.Fatalf("withdrawn record still recalls: %+v %v", current, err)
	}
}

// Promoting twice would withdraw a record nobody disputed. Under concurrency exactly one promotion
// wins, because the feedback row is taken FOR UPDATE before the correction runs.
func TestOneFeedbackIsPromotedOnceEvenWhenTwoPromotionsRace(t *testing.T) {
	ctx, pool, schema, feedback, _, id := feedbackFixture(t, "feedback_promote_once")
	written, err := feedback.Record(ctx, "p1", uuid.NewString(), pg.FeedbackRequest{
		RecordID: id, Note: "Atlas lives in Oslo", ProposedObject: object("Oslo")})
	if err != nil {
		t.Fatal(err)
	}
	version := recordMutation(t, pool, schema, id).ExpectedVersion
	results := make([]error, 2)
	var wait sync.WaitGroup
	for i := range results {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, results[i] = feedback.Promote(ctx, "p1", uuid.NewString(),
				pg.PromotionRequest{ID: written.ID, ExpectedVersion: version})
		}()
	}
	wait.Wait()
	won := 0
	for _, err := range results {
		switch {
		case err == nil:
			won++
		// The loser sees the feedback promoted, or the record moved under it — both are refusals
		// naming what happened, and neither applied the promotion a second time.
		case errors.Is(err, pg.ErrFeedbackPromoted), errors.Is(err, pg.ErrRecordConflict):
		default:
			t.Fatalf("unexpected refusal: %v", err)
		}
	}
	if won != 1 {
		t.Fatalf("%d promotions succeeded", won)
	}
	var withdrawals int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.record_retraction
        WHERE scope='p1' AND target_fact_id=$1::uuid`), id).Scan(&withdrawals); err != nil {
		t.Fatal(err)
	}
	if withdrawals != 1 {
		t.Fatalf("the record was withdrawn %d times", withdrawals)
	}
	if _, err := feedback.Promote(ctx, "p1", uuid.NewString(),
		pg.PromotionRequest{ID: written.ID, ExpectedVersion: version}); !errors.Is(err, pg.ErrFeedbackPromoted) {
		t.Fatalf("a later promotion was not refused as promoted: %v", err)
	}
}

// The record moves between reading it and promoting the feedback about it. Promoting anyway would
// apply somebody's judgement to a claim they never read.
func TestPromotionAgainstAStaleRecordVersionIsRefused(t *testing.T) {
	ctx, pool, schema, feedback, records, id := feedbackFixture(t, "feedback_stale")
	stale := recordMutation(t, pool, schema, id).ExpectedVersion
	written, err := feedback.Record(ctx, "p1", uuid.NewString(), pg.FeedbackRequest{
		RecordID: id, Note: "Atlas lives in Oslo", ProposedObject: object("Oslo")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := records.Correct(ctx, "p1", uuid.NewString(), []pg.RecordCorrection{{
		RecordMutation: recordMutation(t, pool, schema, id), Object: "Cork", Statement: "Atlas lives in Cork"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := feedback.Promote(ctx, "p1", uuid.NewString(),
		pg.PromotionRequest{ID: written.ID, ExpectedVersion: stale}); !errors.Is(err, pg.ErrRecordConflict) {
		t.Fatalf("stale promotion was not refused: %v", err)
	}
	page, err := feedback.List(ctx, "p1", "", true, nil, pg.DefaultFeedbackPage)
	if err != nil || len(page.Feedback) != 1 {
		t.Fatalf("a refused promotion consumed the feedback: %+v %v", page.Feedback, err)
	}
}

// Every refusal the writer can produce, named. A bound nothing exercises is a bound nobody has
// watched refuse.
func TestFeedbackRefusesWhatItCannotStore(t *testing.T) {
	ctx, _, _, feedback, _, id := feedbackFixture(t, "feedback_refusals")
	actor := uuid.NewString()
	cases := map[string]struct {
		request pg.FeedbackRequest
		want    error
	}{
		"an empty note":              {pg.FeedbackRequest{RecordID: id, Note: "  "}, pg.ErrInvalidFeedback},
		"a note past the bound":      {pg.FeedbackRequest{RecordID: id, Note: strings.Repeat("a", pg.MaxFeedbackNote+1)}, pg.ErrInvalidFeedback},
		"an object past the bound":   {pg.FeedbackRequest{RecordID: id, Note: "n", ProposedObject: object(strings.Repeat("a", pg.MaxFeedbackObject+1))}, pg.ErrInvalidFeedback},
		"an empty object":            {pg.FeedbackRequest{RecordID: id, Note: "n", ProposedObject: object("")}, pg.ErrInvalidFeedback},
		"a note carrying a NUL":      {pg.FeedbackRequest{RecordID: id, Note: "a\x00b"}, pg.ErrInvalidFeedback},
		"an unparseable record":      {pg.FeedbackRequest{RecordID: "not-a-uuid", Note: "n"}, pg.ErrInvalidFeedback},
		"a record that is not there": {pg.FeedbackRequest{RecordID: uuid.NewString(), Note: "n"}, pg.ErrRecordNotFound},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := feedback.Record(ctx, "p1", actor, c.request); !errors.Is(err, c.want) {
				t.Fatalf("got %v, want %v", err, c.want)
			}
		})
	}
	if _, err := feedback.Record(ctx, "p1", "not-a-credential", pg.FeedbackRequest{RecordID: id, Note: "n"}); !errors.Is(err, pg.ErrInvalidFeedback) {
		t.Fatalf("an unparseable principal was accepted: %v", err)
	}
	if _, err := feedback.Record(ctx, "no-such-scope!", actor, pg.FeedbackRequest{RecordID: id, Note: "n"}); !errors.Is(err, pg.ErrInvalidFeedback) {
		t.Fatalf("an unusable scope was accepted: %v", err)
	}
	for name, request := range map[string]pg.PromotionRequest{
		"an unparseable feedback": {ID: "not-a-uuid", ExpectedVersion: uuid.NewString()},
		"no expected version":     {ID: uuid.NewString(), ExpectedVersion: ""},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := feedback.Promote(ctx, "p1", actor, request); !errors.Is(err, pg.ErrInvalidFeedback) {
				t.Fatalf("got %v", err)
			}
		})
	}
	if _, err := feedback.Promote(ctx, "p1", "not-a-credential", pg.PromotionRequest{ID: uuid.NewString(), ExpectedVersion: uuid.NewString()}); !errors.Is(err, pg.ErrInvalidFeedback) {
		t.Fatal("an unparseable promoter was accepted")
	}
	if _, err := feedback.Promote(ctx, "no-such-scope!", actor, pg.PromotionRequest{ID: uuid.NewString(), ExpectedVersion: uuid.NewString()}); !errors.Is(err, pg.ErrInvalidFeedback) {
		t.Fatal("an unusable scope was accepted")
	}
	if _, err := feedback.Promote(ctx, "p1", actor, pg.PromotionRequest{ID: uuid.NewString(), ExpectedVersion: uuid.NewString()}); !errors.Is(err, pg.ErrFeedbackNotFound) {
		t.Fatal("promoting feedback that is not there was not refused")
	}
}

// The queue is paged, narrowed and bounded. An unbounded list of somebody's complaints is a way to
// read the project's text through a route that was only ever meant to say what is outstanding.
func TestFeedbackListPagesNarrowsAndRefusesAnUnusablePage(t *testing.T) {
	ctx, pool, schema, feedback, _, id := feedbackFixture(t, "feedback_list")
	march := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	other := statedAt(t, ctx, pg.NewObservationStore(pool), pg.NewFactStore(pool), schema, march,
		"Atlas works at Ensera", domain.Claim{Subject: "Atlas", Predicate: "works_at", Object: "Ensera",
			Statement: "Atlas works at Ensera", Cardinality: domain.CardinalityMany, ValidFrom: march})
	written := make([]string, 0, 3)
	for _, request := range []pg.FeedbackRequest{
		{RecordID: id, Note: "first"},
		{RecordID: id, Note: "second", ProposedObject: object("Oslo")},
		{RecordID: other, Note: "third"},
	} {
		out, err := feedback.Record(ctx, "p1", uuid.NewString(), request)
		if err != nil {
			t.Fatal(err)
		}
		written = append(written, out.ID)
	}
	first, err := feedback.List(ctx, "p1", "", false, nil, 2)
	if err != nil || len(first.Feedback) != 2 || first.Next == nil {
		t.Fatalf("first page: %+v %v", first, err)
	}
	second, err := feedback.List(ctx, "p1", "", false, first.Next, 2)
	if err != nil || len(second.Feedback) != 1 || second.Next != nil {
		t.Fatalf("second page: %+v %v", second, err)
	}
	seen := map[string]bool{}
	for _, item := range append(first.Feedback, second.Feedback...) {
		if seen[item.ID] {
			t.Fatalf("%s appeared on both pages", item.ID)
		}
		seen[item.ID] = true
	}
	if len(seen) != len(written) {
		t.Fatalf("paging returned %d of %d", len(seen), len(written))
	}
	narrowed, err := feedback.List(ctx, "p1", other, false, nil, pg.DefaultFeedbackPage)
	if err != nil || len(narrowed.Feedback) != 1 || narrowed.Feedback[0].RecordID != other {
		t.Fatalf("narrowing to one record: %+v %v", narrowed, err)
	}
	if _, err := feedback.Promote(ctx, "p1", uuid.NewString(), pg.PromotionRequest{
		ID: written[1], ExpectedVersion: recordMutation(t, pool, schema, id).ExpectedVersion}); err != nil {
		t.Fatal(err)
	}
	open, err := feedback.List(ctx, "p1", "", true, nil, pg.DefaultFeedbackPage)
	if err != nil || len(open.Feedback) != 2 {
		t.Fatalf("open-only still lists the promoted one: %+v %v", open, err)
	}
	for name, call := range map[string]func() (pg.FeedbackPage, error){
		"no limit":       func() (pg.FeedbackPage, error) { return feedback.List(ctx, "p1", "", false, nil, 0) },
		"past the bound": func() (pg.FeedbackPage, error) { return feedback.List(ctx, "p1", "", false, nil, pg.MaxFeedbackPage+1) },
		"an unusable scope": func() (pg.FeedbackPage, error) {
			return feedback.List(ctx, "no-such-scope!", "", false, nil, pg.DefaultFeedbackPage)
		},
		"an unparseable record": func() (pg.FeedbackPage, error) {
			return feedback.List(ctx, "p1", "not-a-uuid", false, nil, pg.DefaultFeedbackPage)
		},
		"an unparseable cursor": func() (pg.FeedbackPage, error) {
			return feedback.List(ctx, "p1", "", false, &pg.FeedbackCursor{ID: "not-a-uuid", RecordedAt: march}, pg.DefaultFeedbackPage)
		},
		"a cursor with no time": func() (pg.FeedbackPage, error) {
			return feedback.List(ctx, "p1", "", false, &pg.FeedbackCursor{ID: uuid.NewString()}, pg.DefaultFeedbackPage)
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := call(); !errors.Is(err, pg.ErrInvalidFeedback) {
				t.Fatalf("got %v", err)
			}
		})
	}
}

// Erasure reaches feedback through the registry, and the residual is what proves it. A feedback row
// surviving the erasure of the person its record is about is the failure this product is sold on not
// having.
func TestErasingTheSubjectOfARecordRemovesTheFeedbackAboutIt(t *testing.T) {
	ctx, pool, schema, feedback, _, id := feedbackFixture(t, "feedback_erasure")
	if _, err := feedback.Record(ctx, "p1", uuid.NewString(), pg.FeedbackRequest{
		RecordID: id, Note: "this is about me and I am leaving", ProposedObject: object("Oslo")}); err != nil {
		t.Fatal(err)
	}
	// Export reads the same registry erasure does, so a subject access request produces the
	// feedback written about that person's records without the exporter being taught about it.
	exported, err := pg.NewExporter(pool, schema).Export(ctx, schema, "p1", "subject-1")
	if err != nil || len(exported.Sections["feedback"]) != 1 {
		t.Fatalf("export did not produce the feedback: %d sections %v", len(exported.Sections["feedback"]), err)
	}
	receipt, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", "subject-1", "the feedback erasure test")
	if err != nil {
		t.Fatalf("erase: %v", err)
	}
	// The kind has to appear in the receipt as well as count zero: a kind the walk never visited
	// also reports nothing, and the two are indistinguishable without this.
	left, covered := receipt.Residual["feedback"]
	if !covered {
		t.Fatalf("the receipt does not account for feedback at all: %+v", receipt.Residual)
	}
	if left != 0 {
		t.Fatalf("erasure left %d feedback rows behind", left)
	}
	var surviving int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.memory_feedback WHERE scope='p1'`)).Scan(&surviving); err != nil {
		t.Fatal(err)
	}
	if surviving != 0 {
		t.Fatalf("%d feedback rows survived the erasure of their subject", surviving)
	}
}

// A record two people contributed to survives one of them leaving. The feedback about it does not:
// one person wrote those words. This is the case the registry exists for — the cascade from the
// target record cannot reach it, because the target record is still there.
func TestFeedbackOnASharedRecordGoesEvenThoughTheRecordStays(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "feedback_shared")
	observations, facts := pg.NewObservationStore(pool), pg.NewFactStore(pool)
	content := "Marta works at Ensera."
	sources := map[string]string{}
	for _, subject := range []string{"alice", "bob"} {
		stored, err := observations.Append(ctx, schema, domain.Turn{Scope: "p1", DataSubjectID: subject,
			OccurredAt: time.Now(), Messages: []domain.Message{{Role: domain.RoleUser, Content: content}}})
		if err != nil {
			t.Fatal(err)
		}
		sources[subject] = stored.ID
		if _, err := facts.Assert(ctx, schema, "p1", stored.ID, domain.RoleUser, subject, domain.Claim{
			Subject: "Marta", Object: "Ensera", Predicate: "works_at", Cardinality: domain.CardinalityMany,
			Statement: content, Quote: content, ByteEnd: len(content)}); err != nil {
			t.Fatal(err)
		}
	}
	// Formation partitions facts by attribution, so a shared fact is reproduced here the way the
	// registry contract allows: alice's fact, registered by bob's observation as well.
	if _, err := pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.projection_dependency
        (source_observation_id,scope,projection_kind,projection_id,data_subject_id)
        SELECT $1,'p1','fact',fact_id::text,'bob' FROM {schema}.fact WHERE data_subject_id='alice'`),
		sources["bob"]); err != nil {
		t.Fatal(err)
	}
	var shared string
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT fact_id::text FROM {schema}.fact WHERE data_subject_id='alice'`)).Scan(&shared); err != nil {
		t.Fatal(err)
	}
	feedback := pg.NewFeedbackStore(pool, schema, pg.NewRecordStore(pool, schema))
	if _, err := feedback.Record(ctx, "p1", uuid.NewString(), pg.FeedbackRequest{
		RecordID: shared, Note: "alice wrote this and alice is leaving"}); err != nil {
		t.Fatal(err)
	}
	receipt, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", "alice", "the shared feedback test")
	if err != nil || !receipt.Clean() {
		t.Fatalf("erase: %+v %v", receipt, err)
	}
	var survived bool
	var left int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT EXISTS(SELECT 1 FROM {schema}.fact WHERE fact_id=$1::uuid),
        (SELECT count(*) FROM {schema}.memory_feedback)`), shared).Scan(&survived, &left); err != nil {
		t.Fatal(err)
	}
	if !survived {
		t.Fatal("the shared record did not survive, so this proves nothing about the registry")
	}
	if left != 0 || receipt.Deleted["feedback"] != 1 {
		t.Fatalf("erasure deleted %d feedback rows and left %d", receipt.Deleted["feedback"], left)
	}
}

// A bound the feedback writer cannot see. The note and the proposed object are bounded here at the
// correction's own limits, and neither of them is the limit on how many spellings one entity may
// carry. So a feedback that was accepted can still be refused at promotion, and the refusal has to
// name that rather than becoming an internal error.
func TestPromotionIsRefusedWhenTheProposedObjectWouldPassTheEntityNameBound(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "feedback_name_bound")
	if _, err := storeEntityName(ctx, pool, schema, "base", "Ensera"); err != nil {
		t.Fatal(err)
	}
	if _, err := storeEntityName(ctx, pool, schema, "shouted", "ENSERA"); err != nil {
		t.Fatal(err)
	}
	// Whitespace variants normalise to one identity while keeping distinct source spellings, which
	// is how an entity reaches its variant bound without becoming a different entity. The canonical
	// spelling is not an alias, so this leaves exactly MaxEntityNameVariants of them.
	for i := 1; i < pg.MaxEntityNameVariants; i++ {
		if _, err := storeEntityName(ctx, pool, schema, "variants", strings.Repeat(" ", i)+"Ensera"); err != nil {
			t.Fatal(err)
		}
	}
	var target string
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT fact_id::text FROM {schema}.fact
        WHERE scope='p1' AND upper_inf(known) ORDER BY fact_id LIMIT 1`)).Scan(&target); err != nil {
		t.Fatal(err)
	}
	feedback := pg.NewFeedbackStore(pool, schema, pg.NewRecordStore(pool, schema))
	// Accepted: nothing about this request is over the feedback writer's own bounds.
	written, err := feedback.Record(ctx, "p1", uuid.NewString(), pg.FeedbackRequest{
		RecordID: target, Note: "spell it with the padding",
		ProposedObject: object(strings.Repeat(" ", pg.MaxEntityNameVariants) + "Ensera")})
	if err != nil {
		t.Fatalf("the bound was applied at the wrong end: %v", err)
	}
	if _, err := feedback.Promote(ctx, "p1", uuid.NewString(), pg.PromotionRequest{
		ID: written.ID, ExpectedVersion: recordMutation(t, pool, schema, target).ExpectedVersion}); !errors.Is(err, pg.ErrEntityNameLimit) {
		t.Fatalf("got %v, want the entity name bound", err)
	}
	// Refused, and still open: a promotion that did not happen must not read as one that did.
	page, err := feedback.List(ctx, "p1", "", true, nil, pg.DefaultFeedbackPage)
	if err != nil || len(page.Feedback) != 1 || page.Feedback[0].PromotedAt != nil {
		t.Fatalf("a refused promotion changed the feedback: %+v %v", page.Feedback, err)
	}
}

// Every write in the feedback store is one transaction, so a failure anywhere in it leaves nothing:
// no report, no registration, no ledger row. Injected as constraints rather than simulated, because
// a fake store proves the fake rolls back.
func TestFeedbackWritesRollBackCompletelyWhenAnyPartOfThemFails(t *testing.T) {
	ctx := context.Background()
	for _, failure := range []struct{ name, ddl string }{
		{"the report", `ALTER TABLE {schema}.memory_feedback ADD CONSTRAINT injected_failure CHECK(false)`},
		{"the registration", `ALTER TABLE {schema}.projection_dependency ADD CONSTRAINT injected_failure CHECK(projection_kind<>'feedback')`},
		{"the ledger", `ALTER TABLE {schema}.audit_entry ADD CONSTRAINT injected_failure CHECK(operation<>'feedback.record')`},
	} {
		t.Run(failure.name, func(t *testing.T) {
			ctx, pool, schema, feedback, _, id := feedbackFixture(t, "feedback_fail_"+strings.ReplaceAll(failure.name, " ", "_"))
			if _, err := pool.Exec(ctx, schema.SQL(failure.ddl)); err != nil {
				t.Fatal(err)
			}
			if _, err := feedback.Record(ctx, "p1", uuid.NewString(),
				pg.FeedbackRequest{RecordID: id, Note: "a report nobody will find"}); err == nil {
				t.Fatal("a failed write reported success")
			}
			var reports, registrations, entries int
			if err := pool.QueryRow(ctx, schema.SQL(`SELECT
                (SELECT count(*) FROM {schema}.memory_feedback),
                (SELECT count(*) FROM {schema}.projection_dependency WHERE projection_kind='feedback'),
                (SELECT count(*) FROM {schema}.audit_entry WHERE operation='feedback.record')`)).
				Scan(&reports, &registrations, &entries); err != nil {
				t.Fatal(err)
			}
			if reports != 0 || registrations != 0 || entries != 0 {
				t.Fatalf("a failed write left %d reports, %d registrations, %d ledger rows",
					reports, registrations, entries)
			}
		})
	}
	// And a promotion that fails at the ledger leaves the record it was about untouched: the whole
	// reason promotion runs the correction inside its own transaction.
	ctx, pool, schema, feedback, _, id := feedbackFixture(t, "feedback_fail_promotion")
	written, err := feedback.Record(ctx, "p1", uuid.NewString(),
		pg.FeedbackRequest{RecordID: id, Note: "Atlas lives in Oslo", ProposedObject: object("Oslo")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(
		`ALTER TABLE {schema}.audit_entry ADD CONSTRAINT injected_failure CHECK(operation<>'feedback.promote')`)); err != nil {
		t.Fatal(err)
	}
	if _, err := feedback.Promote(ctx, "p1", uuid.NewString(), pg.PromotionRequest{
		ID: written.ID, ExpectedVersion: recordMutation(t, pool, schema, id).ExpectedVersion}); err == nil {
		t.Fatal("a failed promotion reported success")
	}
	var withdrawals, promoted int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT
        (SELECT count(*) FROM {schema}.record_retraction WHERE scope='p1'),
        (SELECT count(*) FROM {schema}.memory_feedback WHERE promoted_at IS NOT NULL)`)).
		Scan(&withdrawals, &promoted); err != nil {
		t.Fatal(err)
	}
	if withdrawals != 0 || promoted != 0 {
		t.Fatalf("a failed promotion withdrew %d records and marked %d reports", withdrawals, promoted)
	}
	current, err := pg.NewCitationStore(pool, schema).Resolve(ctx, "p1", id, nil, 8)
	if err != nil || current.Retraction != nil {
		t.Fatalf("the target was withdrawn by a promotion that failed: %+v %v", current.Retraction, err)
	}
}
