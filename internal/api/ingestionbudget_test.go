// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/infra/pg"
)

func TestBacklogRefusalIsRetryableAndPreservesTheOriginalReceipt(t *testing.T) {
	h := newHarness(t, "api_backlog_capacity")
	ctx := context.Background()
	if _, err := h.pool.Exec(ctx, h.schema.SQL(`UPDATE {schema}.ingestion_budget SET max_pending=1,max_pending_per_project=1`)); err != nil {
		t.Fatal(err)
	}
	first := map[string]any{"idempotency_key": "4d6b5477-8cdb-4310-aa23-a865d8ea56a1", "messages": []map[string]any{{"role": "user", "content": "accepted during outage"}}}
	status, body := h.raw(t, http.MethodPost, "/v1/observations", first, h.token)
	if status != http.StatusCreated {
		t.Fatalf("first write %d %s", status, body)
	}
	var original retryReceipt
	if err := json.Unmarshal(body, &original); err != nil {
		t.Fatal(err)
	}
	second := map[string]any{"idempotency_key": "063a9f79-ac43-4610-93e7-b9da5a9725cb", "messages": []map[string]any{{"role": "user", "content": "wait for capacity"}}}
	status, body = h.raw(t, http.MethodPost, "/v1/observations", second, h.token)
	if status != http.StatusTooManyRequests || !strings.Contains(string(body), "rate_limited") {
		t.Fatalf("capacity refusal %d %s", status, body)
	}
	wire, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, h.server.URL+"/v1/observations", strings.NewReader(string(wire)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+h.token)
	resp, err := h.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") != "1" {
		t.Fatal("backlog refusal omitted its retry contract")
	}
	status, body = h.raw(t, http.MethodPost, "/v1/observations", first, h.token)
	var replay retryReceipt
	if err := json.Unmarshal(body, &replay); err != nil {
		t.Fatal(err)
	}
	if status != http.StatusCreated || replay != original {
		t.Fatalf("full capacity broke receipt: %d %+v %+v", status, replay, original)
	}
	if err := pg.NewObservationStore(h.pool).MarkFormed(ctx, h.schema, original.ID); err != nil {
		t.Fatal(err)
	}
	status, body = h.raw(t, http.MethodPost, "/v1/observations", second, h.token)
	var resumed retryReceipt
	if err := json.Unmarshal(body, &resumed); err != nil {
		t.Fatal(err)
	}
	if status != http.StatusCreated || resumed.Offset != 1 || resumed.ID == original.ID {
		t.Fatalf("refusal was not recoverable: %d %+v", status, resumed)
	}
}
