// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/report"
)

func TestFailedReportRegistrationRollsBackTheGeneratedRow(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "wp_report_rollback")
	store, f := reportSources(t, pool, schema)
	id := reportCommunity(t, pool, schema, "p1")
	if _, err := pool.Exec(ctx, schema.SQL(`CREATE FUNCTION {schema}.refuse_report_registration() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN IF NEW.projection_kind='community_report' THEN RAISE EXCEPTION 'injected registration failure'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER refuse_report_registration BEFORE INSERT ON {schema}.projection_dependency FOR EACH ROW EXECUTE FUNCTION {schema}.refuse_report_registration()`)); err != nil {
		t.Fatal(err)
	}
	err := store.Write(ctx, "p1", id, report.Report{Title: "work", Summary: "one person"}, report.Context{Facts: []report.Fact{f["alice"]}}, "report/test")
	if err == nil || !strings.Contains(err.Error(), "injected registration failure") {
		t.Fatalf("unexpected registration result: %v", err)
	}
	var reports int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.community_report`)).Scan(&reports); err != nil || reports != 0 {
		t.Fatalf("partially persisted report: %d %v", reports, err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`DROP TRIGGER refuse_report_registration ON {schema}.projection_dependency`)); err != nil {
		t.Fatal(err)
	}
	// A retry after the transient database failure can commit the same logical report.
	if err := store.Write(ctx, "p1", id, report.Report{Title: "work", Summary: "one person"}, report.Context{Facts: []report.Fact{f["alice"]}}, "report/test"); err != nil {
		t.Fatal(err)
	}
}

func TestEvidenceDeleteFailureRollsBackErasureAndRetention(t *testing.T) {
	for _, retention := range []bool{false, true} {
		t.Run(map[bool]string{false: "erasure", true: "retention"}[retention], func(t *testing.T) {
			ctx := context.Background()
			pool := testPool(t)
			schema := tenant(t, pool, map[bool]string{false: "er_evidence_rollback", true: "rt_evidence_rollback"}[retention])
			_, f := reportSources(t, pool, schema)
			if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.observation SET retention_until=now()-interval '1 hour' WHERE observation_id=$1`), f["alice"].SourceObservationID); err != nil {
				t.Fatal(err)
			}
			// Statement trigger fires on the explicit source-evidence cleanup, after projection deletes.
			if _, err := pool.Exec(ctx, schema.SQL(`CREATE FUNCTION {schema}.refuse_evidence_cleanup() RETURNS trigger LANGUAGE plpgsql AS $$
    BEGIN RAISE EXCEPTION 'injected evidence cleanup failure'; END $$;
    CREATE TRIGGER refuse_evidence_cleanup BEFORE DELETE ON {schema}.fact_evidence FOR EACH STATEMENT EXECUTE FUNCTION {schema}.refuse_evidence_cleanup()`)); err != nil {
				t.Fatal(err)
			}
			var err error
			if retention {
				_, err = pg.NewRetentionStore(pool, schema).Sweep(ctx, "p1", 1)
			} else {
				_, err = pg.NewEraser(pool).Erase(ctx, schema, "p1", "alice", "fixture")
			}
			if err == nil || !strings.Contains(err.Error(), "injected evidence cleanup failure") {
				t.Fatalf("expected injected refusal: %v", err)
			}
			var observations, facts, evidence, entities, receipts int
			if err := pool.QueryRow(ctx, schema.SQL(`SELECT (SELECT count(*) FROM {schema}.observation),
    (SELECT count(*) FROM {schema}.fact),(SELECT count(*) FROM {schema}.fact_evidence),
    (SELECT count(*) FROM {schema}.entity),(SELECT count(*) FROM {schema}.erasure_request)`)).Scan(&observations, &facts, &evidence, &entities, &receipts); err != nil {
				t.Fatal(err)
			}
			if observations != 2 || facts != 2 || evidence != 2 || entities != 3 || receipts != 0 {
				t.Fatalf("partial deletion or false receipt: %d %d %d %d %d", observations, facts, evidence, entities, receipts)
			}
		})
	}
}
