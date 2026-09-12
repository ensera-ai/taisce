// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
)

func TestErasureAndRetentionKeepSharedIdentityButRemoveDepartingEvidence(t *testing.T) {
	for _, retention := range []bool{false, true} {
		t.Run(map[bool]string{false: "erasure", true: "retention"}[retention], func(t *testing.T) {
			ctx := context.Background()
			pool := testPool(t)
			schema := tenant(t, pool, map[bool]string{false: "er_joint_evidence", true: "er_joint_expiry"}[retention])
			observations := pg.NewObservationStore(pool)
			facts := pg.NewFactStore(pool)
			ids := map[string]string{}
			for _, subject := range []string{"alice", "bob"} {
				content := "Marta works at Ensera."
				o, err := observations.Append(ctx, schema, domain.Turn{Scope: "p1", DataSubjectID: subject, OccurredAt: time.Now(), Messages: []domain.Message{{Role: domain.RoleUser, Content: content}}})
				if err != nil {
					t.Fatal(err)
				}
				ids[subject] = o.ID
				_, err = facts.Assert(ctx, schema, "p1", o.ID, domain.RoleUser, subject, domain.Claim{Subject: "Marta", Object: "Ensera", Predicate: "works_at", Cardinality: domain.CardinalityMany, Statement: content, Quote: content, ByteEnd: len(content)})
				if err != nil {
					t.Fatal(err)
				}
			}
			// The current formation path partitions facts by attribution. Reproduce a valid shared
			// fact supported by two source observations, as the registry contract explicitly allows.
			if _, err := pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.projection_dependency(source_observation_id,scope,projection_kind,projection_id,data_subject_id)
       SELECT $1,'p1','fact',fact_id::text,'bob' FROM {schema}.fact WHERE data_subject_id='alice'`), ids["bob"]); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.fact_evidence(fact_id,source_observation_id,source_ordinal,byte_start,byte_end,quote,scope,extractor_version)
       SELECT fact_id,$1,0,0,21,'Marta works at Ensera.','p1','fixture' FROM {schema}.fact WHERE data_subject_id='alice'`), ids["bob"]); err != nil {
				t.Fatal(err)
			}
			if retention {
				if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.observation SET retention_until=now()-interval '1 hour' WHERE observation_id=$1`), ids["alice"]); err != nil {
					t.Fatal(err)
				}
				sweep, err := pg.NewRetentionStore(pool, schema).Sweep(ctx, "p1", 1)
				if err != nil || sweep.Observations != 1 {
					t.Fatalf("sweep: %+v %v", sweep, err)
				}
			} else {
				receipt, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", "alice", "fixture")
				if err != nil || !receipt.Clean() {
					t.Fatalf("erase: %+v %v", receipt, err)
				}
			}
			var entities, factsLeft, aliceEvidence, bobEvidence int
			if err := pool.QueryRow(ctx, schema.SQL(`SELECT
       (SELECT count(*) FROM {schema}.entity),
       (SELECT count(*) FROM {schema}.fact),
       (SELECT count(*) FROM {schema}.fact_evidence WHERE source_observation_id=$1),
       (SELECT count(*) FROM {schema}.fact_evidence WHERE source_observation_id=$2)`), ids["alice"], ids["bob"]).Scan(&entities, &factsLeft, &aliceEvidence, &bobEvidence); err != nil {
				t.Fatal(err)
			}
			if entities != 2 || factsLeft != 2 || aliceEvidence != 0 || bobEvidence != 2 {
				t.Fatalf("shared rows/evidence: entities=%d facts=%d departed=%d retained=%d", entities, factsLeft, aliceEvidence, bobEvidence)
			}
		})
	}
}
