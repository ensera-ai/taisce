// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
)

func TestEntityNameBoundsRefuseAuthoredWritesWithoutPartialSources(t *testing.T) {
	h := newHarness(t, "api_entity_name_bounds")
	ctx := context.Background()
	request := pg.RecordAssertion{IdempotencyKey: uuid.NewString(), DataSubjectID: "subject-1", Subject: "I", Predicate: "works_at", Object: "Ensera", Statement: "I work at Ensera"}
	var batch pg.AssertionBatch
	h.do(t, http.MethodPost, "/v1/records/assert", map[string]any{"records": []pg.RecordAssertion{request}}, 200, &batch)
	// Fill the bounded cache to exercise transport mapping independently of model behavior.
	if _, err := h.pool.Exec(ctx, h.schema.SQL(`UPDATE {schema}.entity SET aliases=ARRAY(SELECT repeat(' ',n)||'Ensera' FROM generate_series(1,64) n) WHERE normalized_name='ensera'`)); err != nil {
		t.Fatal(err)
	}
	request.IdempotencyKey = uuid.NewString()
	request.Object = "ENSERA"
	request.Statement = "I work at ENSERA"
	h.do(t, http.MethodPost, "/v1/records/assert", map[string]any{"records": []pg.RecordAssertion{request}}, 400, nil)
	var version string
	if err := h.pool.QueryRow(ctx, h.schema.SQL(`SELECT version::text FROM {schema}.fact WHERE fact_id=$1`), batch.Records[0].ID).Scan(&version); err != nil {
		t.Fatal(err)
	}
	h.do(t, http.MethodPost, "/v1/records/correct", map[string]any{"records": []pg.RecordCorrection{{RecordMutation: pg.RecordMutation{ID: batch.Records[0].ID, ExpectedVersion: version}, Object: "ENSERA", Statement: "I work at ENSERA"}}}, 400, nil)
	var sources int
	if err := h.pool.QueryRow(ctx, h.schema.SQL(`SELECT count(*) FROM {schema}.observation`)).Scan(&sources); err != nil || sources != 1 {
		t.Fatalf("refused writes left sources: %d %v", sources, err)
	}
}
