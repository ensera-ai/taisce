// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
)

// Fail writes at different publication stages, including after retirement and after the new
// interpretation exists. The observable contract is rollback of the entire memory transaction.
func TestGenerationPublicationFailuresLeaveEveryProjectionUnchanged(t *testing.T) {
	ctx := context.Background()
	f := generationSource(t, "generation_write_failures")
	plan := pg.GenerationPlan{Claims: []domain.Claim{f.claim}, Rejected: []domain.RejectedClaim{{
		SourceOrdinal: 0, Statement: "unmapped", Predicate: "unknown", Quote: f.claim.Quote,
		Reason: domain.ReasonUnresolvableSubject,
	}}}
	tables := []string{"fact", "fact_receipt", "fact_history", "entity", "fact_evidence", "projection_dependency", "rejected_claim", "source_extraction", "fact_generation", "fact_generation_record", "audit_entry"}
	before := map[string]string{}
	for _, table := range tables {
		before[table] = generationState(t, f, table)
	}
	if _, err := f.pool.Exec(ctx, f.schema.SQL(`CREATE FUNCTION {schema}.fail_generation_write() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'generation rollback probe'; END $$`)); err != nil {
		t.Fatal(err)
	}
	for _, stage := range []struct{ table, event, condition string }{
		{"fact", "UPDATE", ""},
		{"fact", "INSERT", ""},
		{"fact_receipt", "INSERT", ""},
		{"fact_evidence", "INSERT", ""},
		{"fact_generation_record", "INSERT", "WHEN (NEW.disposition='admitted')"},
		{"fact_generation_record", "INSERT", "WHEN (NEW.disposition='retired')"},
		{"rejected_claim", "INSERT", ""},
		{"projection_dependency", "INSERT", ""},
		{"source_extraction", "DELETE", ""},
		{"source_extraction", "INSERT", ""},
		{"audit_entry", "INSERT", ""},
	} {
		t.Run(stage.table+stage.event+stage.condition, func(t *testing.T) {
			if _, err := f.pool.Exec(ctx, f.schema.SQL("CREATE TRIGGER fail_generation BEFORE "+stage.event+" ON {schema}."+stage.table+" FOR EACH ROW "+stage.condition+" EXECUTE FUNCTION {schema}.fail_generation_write()")); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if _, err := f.pool.Exec(ctx, f.schema.SQL("DROP TRIGGER fail_generation ON {schema}."+stage.table)); err != nil {
					t.Error(err)
				}
			})
			_, err := f.facts.ApplyGeneration(ctx, f.schema, f.snapshot, uuid.NewString(), f.version, uuid.NewString(), plan)
			if err == nil || !strings.Contains(err.Error(), "generation rollback probe") {
				t.Fatalf("publication did not reach and refuse the injected failure: %v", err)
			}
			for _, table := range tables {
				if got := generationState(t, f, table); got != before[table] {
					t.Errorf("failed publication changed %s", table)
				}
			}
		})
	}
}

// Include optimistic versions and audit rows: neither may advance on a rolled-back publication.
func generationState(t *testing.T, f generationFixture, table string) string {
	t.Helper()
	var out string
	err := f.pool.QueryRow(context.Background(), f.schema.SQL("SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text),'[]'::jsonb)::text FROM {schema}."+table+" t")).Scan(&out)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestConcurrentGenerationCutoversHaveOnePublication(t *testing.T) {
	for _, sameKey := range []bool{true, false} {
		name := "generation_distinct_keys"
		if sameKey {
			name = "generation_same_key"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			f := generationSource(t, name)
			keys := []string{uuid.NewString(), uuid.NewString()}
			if sameKey {
				keys[1] = keys[0]
			}
			start := make(chan struct{})
			results := make([]pg.GenerationResult, 2)
			errs := make([]error, 2)
			var wg sync.WaitGroup
			for i := range keys {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					<-start
					results[i], errs[i] = f.facts.ApplyGeneration(ctx, f.schema, f.snapshot, keys[i], f.version, uuid.NewString(), pg.GenerationPlan{Claims: []domain.Claim{f.claim}})
				}(i)
			}
			close(start)
			wg.Wait()
			if sameKey {
				if errs[0] != nil || errs[1] != nil || results[0].ID != results[1].ID || results[0].Replayed == results[1].Replayed {
					t.Fatalf("concurrent retries did not converge: %+v %v", results, errs)
				}
			} else {
				conflicts, published := 0, 0
				for _, err := range errs {
					if errors.Is(err, pg.ErrGenerationConflict) {
						conflicts++
					} else if err == nil {
						published++
					} else {
						t.Fatal(err)
					}
				}
				if conflicts != 1 || published != 1 {
					t.Fatalf("stale snapshot was not refused: %v", errs)
				}
			}
			var count int
			if err := f.pool.QueryRow(ctx, f.schema.SQL(`SELECT count(*) FROM {schema}.fact_generation`)).Scan(&count); err != nil || count != 1 {
				t.Fatalf("multiple generations committed: count=%d err=%v", count, err)
			}
			if err := f.pool.QueryRow(ctx, f.schema.SQL(`SELECT count(*) FROM {schema}.fact WHERE upper_inf(known)`)).Scan(&count); err != nil || count != 1 {
				t.Fatalf("cutover left duplicate current facts: count=%d err=%v", count, err)
			}
		})
	}
}
