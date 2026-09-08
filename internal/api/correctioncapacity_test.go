// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/ensera-ai/taisce/internal/infra/pg"
)

// The reservation trigger evaluates final transaction state. A synchronous correction adds no
// unfinished work, so a full model backlog must not prevent editing already formed memory.
func TestCorrectionAtBacklogCapacityDoesNotSpendAReservationOrSkipFreshness(t *testing.T) {
	h := newHarness(t, "api_correction_capacity")
	ctx := context.Background()
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": "subject-1", "messages": []map[string]any{{"role": "user", "content": "I work at Ensera."}}}, 201, nil)
	h.form(t)
	var page pg.RecordPage
	h.do(t, http.MethodPost, "/v1/records/list", map[string]any{}, 200, &page)
	record := page.Records[0]
	if _, err := h.pool.Exec(ctx, h.schema.SQL(`UPDATE {schema}.ingestion_budget SET max_pending=1,max_pending_per_project=1`)); err != nil {
		t.Fatal(err)
	}
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": "subject-1", "messages": []map[string]any{{"role": "user", "content": "This turn is awaiting extraction."}}}, 201, nil)
	h.do(t, http.MethodPost, "/v1/records/correct", map[string]any{"records": []pg.RecordCorrection{{RecordMutation: pg.RecordMutation{ID: record.ID, ExpectedVersion: record.Version}, Object: "Atlas", Statement: "I work at Atlas."}}}, 200, nil)
	var pending, reservations int
	if err := h.pool.QueryRow(ctx, h.schema.SQL(`SELECT pending,(SELECT count(*) FROM {schema}.ingestion_reservation) FROM {schema}.ingestion_budget`)).Scan(&pending, &reservations); err != nil || pending != 1 || reservations != 1 {
		t.Fatalf("correction changed pending admission: %d %d %v", pending, reservations, err)
	}
	var freshness struct {
		Stored int64  `json:"stored"`
		Formed *int64 `json:"formed"`
	}
	h.do(t, http.MethodGet, "/v1/freshness", nil, 200, &freshness)
	if freshness.Stored != 2 || freshness.Formed == nil || *freshness.Formed != 0 {
		t.Fatalf("correction skipped pending history: %+v", freshness)
	}
}
