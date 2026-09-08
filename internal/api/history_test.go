// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestUnavailableKnowledgeIsExplicitEvenWithoutAnchors(t *testing.T) {
	h := newHarness(t, "api_history_boundary")
	if _, err := h.pool.Exec(context.Background(), h.schema.SQL(`INSERT INTO {schema}.fact_history_boundary VALUES('p1',now())`)); err != nil {
		t.Fatal(err)
	}
	status, body := h.raw(t, http.MethodPost, "/v1/recalls", map[string]any{"question": "Unknown Atlas", "as_known_at": "2000-01-01T00:00:00Z"}, h.token)
	if status != 422 || !strings.Contains(string(body), "history_unavailable") {
		t.Fatalf("historical omission looks successful: %d %s", status, body)
	}
	h.do(t, http.MethodPost, "/v1/recalls", map[string]any{"question": "Unknown Atlas"}, http.StatusOK, nil)
}
