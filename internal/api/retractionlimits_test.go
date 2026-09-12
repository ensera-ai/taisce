// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/infra/pg"
)

func TestRetractionHTTPSourceLimitRefusesWithoutChangingRecords(t *testing.T) {
	h := newHarness(t, "api_retraction_limit")
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": "subject-1", "messages": []map[string]any{{"role": "user", "content": "I work at Ensera. " + strings.Repeat("x", 256)}}}, 201, nil)
	h.form(t)
	var page pg.RecordPage
	h.do(t, http.MethodPost, "/v1/records/list", map[string]any{}, 200, &page)
	record := page.Records[0]
	if _, err := h.pool.Exec(context.Background(), h.schema.SQL(`INSERT INTO {schema}.fact_evidence(fact_id,source_observation_id,source_ordinal,quote,byte_start,byte_end,extractor_version,scope)
        SELECT e.fact_id,e.source_observation_id,0,'x',n,n+1,'fixture',e.scope FROM {schema}.fact_evidence e CROSS JOIN generate_series(30,157) n WHERE e.fact_id=$1`), record.ID); err != nil {
		t.Fatal(err)
	}
	h.do(t, http.MethodPost, "/v1/records/retract", map[string]any{"records": []pg.RecordMutation{{ID: record.ID, ExpectedVersion: record.Version}}}, 413, nil)
	h.do(t, http.MethodPost, "/v1/records/list", map[string]any{}, 200, &page)
	if len(page.Records) != 1 || page.Records[0].Version != record.Version {
		t.Fatal("source limit changed the record")
	}
}
