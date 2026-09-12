// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
)

func assertionRequest() pg.RecordAssertion {
	return pg.RecordAssertion{IdempotencyKey: uuid.NewString(), DataSubjectID: "subject-1", Subject: "I", Predicate: "works_at", Object: "Ensera", Statement: "I work at Ensera"}
}

func TestAuthoredAssertionsKeepAttributionAndRetryWithoutResurrectingErasure(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "assertion_lifecycle")
	store := pg.NewRecordStore(pool, schema)
	actor := uuid.NewString()
	request := assertionRequest()
	request.ValidFrom = time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	batch, err := store.AssertRecords(ctx, "p1", actor, []pg.RecordAssertion{request})
	if err != nil || len(batch.Records) != 1 {
		t.Fatalf("assert: %+v %v", batch, err)
	}
	first := batch.Records[0]
	citation, err := pg.NewCitationStore(pool, schema).Resolve(ctx, "p1", first.ID, nil, 8)
	if err != nil || citation.Statement != request.Statement || citation.Evidence[0].AuthoredBy == nil || *citation.Evidence[0].AuthoredBy != actor || citation.Evidence[0].ExtractorVersion != pg.CuratedExtractorVersion || citation.Confidence != 0 {
		t.Fatalf("authorship: %+v %v", citation, err)
	}
	if first.Replayed || first.Version == "" || !citation.Valid.From.Equal(request.ValidFrom) {
		t.Fatal("incorrect authored receipt")
	}
	fresh, err := pg.NewObservationStore(pool).Freshness(ctx, schema, "p1")
	if err != nil || !fresh.HasFormed || fresh.Formed != fresh.Stored {
		t.Fatalf("authored freshness: %+v %v", fresh, err)
	}
	// Different textual time zones represent the same instant in the retry fingerprint.
	request.ValidFrom = request.ValidFrom.In(time.FixedZone("offset", 3*3600))
	request.IdempotencyKey = strings.ToUpper(request.IdempotencyKey)
	replay, err := store.AssertRecords(ctx, "p1", uuid.NewString(), []pg.RecordAssertion{request})
	if err != nil || len(replay.Records) != 1 || !replay.Records[0].Replayed || replay.Records[0].ID != first.ID || replay.Records[0].Version != first.Version {
		t.Fatalf("replay: %+v %v", replay, err)
	}
	// Missing derived state uses the admitted receipt, including after a later withdrawal.
	if _, err := store.Retract(ctx, "p1", actor, []pg.RecordMutation{recordMutation(t, pool, schema, first.ID)}); err != nil {
		t.Fatal(err)
	}
	withdrawnVersion := recordMutation(t, pool, schema, first.ID).ExpectedVersion
	if _, err := pool.Exec(ctx, schema.SQL(`DELETE FROM {schema}.fact; DELETE FROM {schema}.entity`)); err != nil {
		t.Fatal(err)
	}
	recovered, err := store.AssertRecords(ctx, "p1", actor, []pg.RecordAssertion{request})
	if err != nil || recovered.Records[0].Version != withdrawnVersion || !recovered.Records[0].Replayed {
		t.Fatalf("withdrawn replay: %+v %v", recovered, err)
	}
	withdrawn, err := pg.NewCitationStore(pool, schema).Resolve(ctx, "p1", first.ID, nil, 8)
	if err != nil || withdrawn.Known.Until == nil || withdrawn.Retraction == nil || !withdrawn.RecordedAt.Equal(citation.RecordedAt) {
		t.Fatal("retry reinterpreted or reactivated withdrawn input")
	}
	exported, err := pg.NewExporter(pool, schema).Export(ctx, schema, "p1", "subject-1")
	if err != nil || len(exported.Sections["curated_claim"]) != 1 || len(exported.Sections["fact_receipt"]) != 1 {
		t.Fatalf("export: %v", err)
	}
	erased, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", "subject-1", "requested")
	if err != nil || erased.Deleted["observation"] != 1 {
		t.Fatalf("erase: %+v %v", erased, err)
	}
	if _, err := store.AssertRecords(ctx, "p1", actor, []pg.RecordAssertion{request}); !errors.Is(err, pg.ErrIdempotencyConflict) {
		t.Fatalf("erased retry recreated content: %v", err)
	}
	var live int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.observation_retry WHERE observation_id IS NOT NULL OR request_digest IS NOT NULL`)).Scan(&live); err != nil || live != 0 {
		t.Fatalf("retry retained source metadata: %d %v", live, err)
	}
}

func TestConcurrentAuthoredAssertionRetriesCreateOneSource(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "assertion_concurrency")
	store := pg.NewRecordStore(pool, schema)
	request := assertionRequest()
	actor := uuid.NewString()
	var wg sync.WaitGroup
	results := make(chan pg.AssertionBatch, 16)
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b, e := store.AssertRecords(ctx, "p1", actor, []pg.RecordAssertion{request})
			results <- b
			errs <- e
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	id := ""
	created := 0
	for batch := range results {
		r := batch.Records[0]
		if id != "" && id != r.ID {
			t.Fatal("retry changed identity")
		}
		id = r.ID
		if !r.Replayed {
			created++
		}
	}
	var sources int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.observation`)).Scan(&sources); err != nil || sources != 1 || created != 1 {
		t.Fatalf("retry duplicates: %d %d %v", sources, created, err)
	}
	// Operation namespaces are distinct even when a client uses the same UUID in each operation.
	turn := turnOf(domain.Message{Role: domain.RoleUser, Content: request.Statement})
	if _, replayed, err := pg.NewObservationStore(pool).AppendIdempotent(ctx, schema, turn, request.IdempotencyKey); err != nil || replayed {
		t.Fatalf("operation key collision: %v", err)
	}
	changed := request
	changed.Statement = "A different statement"
	if _, err := store.AssertRecords(ctx, "p1", actor, []pg.RecordAssertion{changed}); !errors.Is(err, pg.ErrIdempotencyConflict) {
		t.Fatalf("changed request accepted: %v", err)
	}
}

func TestAssertionBatchFailuresRollbackSourcesHistoryAndRetryKeys(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "assertion_atomic")
	store := pg.NewRecordStore(pool, schema)
	request := assertionRequest()
	actor := uuid.NewString()
	invalid := assertionRequest()
	invalid.Predicate = "unknown_predicate"
	if _, err := store.AssertRecords(ctx, "p1", actor, []pg.RecordAssertion{request, invalid}); !errors.Is(err, pg.ErrInvalidRecordMutation) {
		t.Fatalf("invalid predicate accepted: %v", err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`ALTER TABLE {schema}.audit_entry ADD CONSTRAINT refuse_assertion CHECK(operation<>'record.assert')`)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AssertRecords(ctx, "p1", actor, []pg.RecordAssertion{request}); err == nil {
		t.Fatal("failed audit committed assertion")
	}
	for _, table := range []string{"observation", "fact", "entity", "curated_claim", "fact_receipt", "observation_retry"} {
		var n int
		if err := pool.QueryRow(ctx, schema.SQL("SELECT count(*) FROM {schema}."+table)).Scan(&n); err != nil || n != 0 {
			t.Fatalf("failed batch left %s=%d %v", table, n, err)
		}
	}
	if _, err := pool.Exec(ctx, schema.SQL(`ALTER TABLE {schema}.audit_entry DROP CONSTRAINT refuse_assertion`)); err != nil {
		t.Fatal(err)
	}
	request.Predicate = "lives_in"
	request.Object = "Dublin"
	request.Statement = "I live in Dublin"
	request.ValidFrom = time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	first, err := store.AssertRecords(ctx, "p1", actor, []pg.RecordAssertion{request})
	if err != nil {
		t.Fatal(err)
	}
	before := recordMutation(t, pool, schema, first.Records[0].ID)
	conflicting := request
	conflicting.IdempotencyKey = uuid.NewString()
	conflicting.Object = "Oslo"
	conflicting.Statement = "I live in Oslo"
	conflicting.ValidFrom = request.ValidFrom.AddDate(0, -1, 0)
	if _, err := store.AssertRecords(ctx, "p1", actor, []pg.RecordAssertion{conflicting}); !errors.Is(err, pg.ErrRecordConflict) {
		t.Fatalf("overlapping validity accepted: %v", err)
	}
	if recordMutation(t, pool, schema, first.Records[0].ID) != before {
		t.Fatal("failed assertion altered prior knowledge")
	}
	unbound := assertionRequest()
	unbound.DataSubjectID = ""
	if _, err := store.AssertRecords(ctx, "p1", actor, []pg.RecordAssertion{unbound}); !errors.Is(err, pg.ErrUnboundSpeaker) {
		t.Fatalf("unbound speaker accepted: %v", err)
	}
	unresolved := assertionRequest()
	unresolved.Subject = "that"
	if _, err := store.AssertRecords(ctx, "p1", actor, []pg.RecordAssertion{unresolved}); !errors.Is(err, pg.ErrInvalidRecordMutation) {
		t.Fatalf("unresolved subject accepted: %v", err)
	}
}

func TestAssertionInputBoundsRefuseBeforeMutation(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "assertion_bounds")
	store := pg.NewRecordStore(pool, schema)
	actor := uuid.NewString()
	valid := assertionRequest()
	for _, batch := range [][]pg.RecordAssertion{nil, make([]pg.RecordAssertion, 21), {valid, valid}} {
		if _, err := store.AssertRecords(ctx, "p1", actor, batch); !errors.Is(err, pg.ErrInvalidRecordMutation) {
			t.Fatalf("invalid batch: %v", err)
		}
	}
	for _, mutate := range []func(*pg.RecordAssertion){
		func(r *pg.RecordAssertion) { r.IdempotencyKey = "bad" }, func(r *pg.RecordAssertion) { r.Subject = " " }, func(r *pg.RecordAssertion) { r.Subject = strings.Repeat("a", 1025) }, func(r *pg.RecordAssertion) { r.Object = "" }, func(r *pg.RecordAssertion) { r.Predicate = "" }, func(r *pg.RecordAssertion) { r.Statement = "" }, func(r *pg.RecordAssertion) { r.Statement = strings.Repeat("a", 16385) }, func(r *pg.RecordAssertion) { r.Statement = "\xff" }, func(r *pg.RecordAssertion) { r.Statement = "a\x00b" }, func(r *pg.RecordAssertion) { r.DataSubjectID = " " }, func(r *pg.RecordAssertion) { r.DataSubjectID = strings.Repeat("a", 1025) }, func(r *pg.RecordAssertion) { r.ValidFrom = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) },
	} {
		r := valid
		mutate(&r)
		if _, err := store.AssertRecords(ctx, "p1", actor, []pg.RecordAssertion{r}); !errors.Is(err, pg.ErrInvalidRecordMutation) {
			t.Fatalf("invalid assertion: %v", err)
		}
	}
	var n int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.observation`)).Scan(&n); err != nil || n != 0 {
		t.Fatal("invalid request wrote a source")
	}
}

func TestAuthoredAssertionPreservesOrderedKnowledgeAfterClockRollback(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "assertion_clock")
	store := pg.NewRecordStore(pool, schema)
	r := assertionRequest()
	r.Predicate = "lives_in"
	r.Object = "Dublin"
	r.Statement = "I live in Dublin"
	r.ValidFrom = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	actor := uuid.NewString()
	first, err := store.AssertRecords(ctx, "p1", actor, []pg.RecordAssertion{r})
	if err != nil {
		t.Fatal(err)
	}
	var future time.Time
	if err := pool.QueryRow(ctx, schema.SQL(`UPDATE {schema}.fact SET known=tstzrange(clock_timestamp()+interval '1 minute',NULL) WHERE fact_id=$1 RETURNING lower(known)`), first.Records[0].ID).Scan(&future); err != nil {
		t.Fatal(err)
	}
	r.IdempotencyKey = uuid.NewString()
	r.Object = "Oslo"
	r.Statement = "I live in Oslo"
	r.ValidFrom = r.ValidFrom.AddDate(0, 1, 0)
	second, err := store.AssertRecords(ctx, "p1", actor, []pg.RecordAssertion{r})
	if err != nil {
		t.Fatal(err)
	}
	var learned, sourceTime, messageTime, chunkTime time.Time
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT lower(f.known),o.occurred_at,m.occurred_at,c.occurred_at FROM {schema}.fact f JOIN {schema}.fact_evidence e ON e.fact_id=f.fact_id JOIN {schema}.observation o ON o.observation_id=e.source_observation_id JOIN {schema}.turn_message m ON m.observation_id=o.observation_id JOIN {schema}.chunk c ON c.source_observation_id=o.observation_id WHERE f.fact_id=$1`), second.Records[0].ID).Scan(&learned, &sourceTime, &messageTime, &chunkTime); err != nil {
		t.Fatal(err)
	}
	if !learned.After(future) || !learned.Equal(sourceTime) || !learned.Equal(messageTime) || !learned.Equal(chunkTime) {
		t.Fatal("authored knowledge and source times diverged")
	}
}

func TestMaximumAssertionBatchReturnsFinalVersionsInRequestOrder(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "assertion_max_batch")
	store := pg.NewRecordStore(pool, schema)
	requests := make([]pg.RecordAssertion, pg.MaxRecordMutations)
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range requests {
		r := assertionRequest()
		r.Predicate = "lives_in"
		r.Object = fmt.Sprintf("City %d", i)
		r.Statement = "I live in " + r.Object
		r.ValidFrom = start.AddDate(0, 0, i)
		requests[i] = r
	}
	out, err := store.AssertRecords(ctx, "p1", uuid.NewString(), requests)
	if err != nil || len(out.Records) != 20 {
		t.Fatalf("maximum batch: %+v %v", out, err)
	}
	for i, r := range out.Records {
		citation, err := pg.NewCitationStore(pool, schema).Resolve(ctx, "p1", r.ID, nil, 8)
		if err != nil || citation.Statement != requests[i].Statement || citation.Version != r.Version || r.Replayed {
			t.Fatalf("batch lost order or returned stale version: %v", err)
		}
		if (i < 19) != (citation.Valid.Until != nil) {
			t.Fatal("batch did not preserve intermediate history")
		}
	}
	// A retry can group the same logical operations differently without reapplying them.
	slices.Reverse(requests)
	replay, err := store.AssertRecords(ctx, "p1", uuid.NewString(), requests)
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range replay.Records {
		if !r.Replayed || r.ID != out.Records[19-i].ID {
			t.Fatal("reordered retries changed source identity")
		}
	}
}

func TestAuthoredAssertionStorageFailuresRollbackTheWholeSource(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	for _, failure := range []struct{ name, ddl string }{
		{"observation", `ALTER TABLE {schema}.observation ADD CONSTRAINT injected_failure CHECK(false)`},
		{"message", `ALTER TABLE {schema}.turn_message ADD CONSTRAINT injected_failure CHECK(false)`},
		{"fact", `ALTER TABLE {schema}.fact ADD CONSTRAINT injected_failure CHECK(false)`},
		{"curated", `ALTER TABLE {schema}.curated_claim ADD CONSTRAINT injected_failure CHECK(false)`},
		{"formed", `ALTER TABLE {schema}.observation ADD CONSTRAINT injected_failure CHECK(kind<>'curated')`},
		{"missing_fact", `CREATE FUNCTION {schema}.damage_authored_fact() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN DELETE FROM {schema}.fact WHERE scope=NEW.scope AND fact_id=NEW.fact_id; RETURN NEW; END $$;
		CREATE TRIGGER injected_failure AFTER INSERT ON {schema}.curated_claim FOR EACH ROW EXECUTE FUNCTION {schema}.damage_authored_fact()`},
	} {
		t.Run(failure.name, func(t *testing.T) {
			schema := tenant(t, pool, "assert_fail_"+failure.name)
			if _, err := pool.Exec(ctx, schema.SQL(failure.ddl)); err != nil {
				t.Fatal(err)
			}
			if _, err := pg.NewRecordStore(pool, schema).AssertRecords(ctx, "p1", uuid.NewString(), []pg.RecordAssertion{assertionRequest()}); err == nil {
				t.Fatal("failed source write reported success")
			}
			for _, table := range []string{"observation", "turn_message", "chunk", "fact", "entity", "curated_claim", "fact_receipt", "projection_dependency", "watermark", "observation_retry", "audit_entry"} {
				var rows int
				if err := pool.QueryRow(ctx, schema.SQL("SELECT count(*) FROM {schema}."+table)).Scan(&rows); err != nil || rows != 0 {
					t.Fatalf("failed source retained %s: count=%d error=%v", table, rows, err)
				}
			}
		})
	}
}
