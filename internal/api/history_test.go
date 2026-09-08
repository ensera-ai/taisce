// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestKnowledgeBeforeFirstAssertionReturnsNoFacts(t *testing.T) {
	h := newHarness(t, "api_history_start")
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": "subject-1", "messages": []map[string]any{{"role": "user", "content": "I work at Ensera and I live in Dublin."}}}, http.StatusCreated, nil)
	h.form(t)
	for _, question := range []string{"Where does the user live?", "Unknown Atlas"} {
		status, body := h.raw(t, http.MethodPost, "/v1/recalls", map[string]any{"question": question, "data_subject_id": "subject-1", "as_known_at": "2000-01-01T00:00:00Z"}, h.token)
		var got struct {
			Facts []json.RawMessage `json:"facts"`
		}
		if status != http.StatusOK || json.Unmarshal(body, &got) != nil || got.Facts == nil || len(got.Facts) != 0 {
			t.Fatalf("knowledge before first assertion: %d %s", status, body)
		}
	}
	var current struct {
		Facts []json.RawMessage `json:"facts"`
	}
	h.do(t, http.MethodPost, "/v1/recalls", map[string]any{"question": "Where does the user live?", "data_subject_id": "subject-1"}, http.StatusOK, &current)
	if len(current.Facts) == 0 {
		t.Fatal("current knowledge unexpectedly empty")
	}
}
