// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api_test

import (
	"net/http"
	"testing"
)

func TestAnEmptyProjectHasNoStoredOffsetAndItsFirstObservationHasZero(t *testing.T) {
	h := newHarness(t, "api_empty_freshness")
	var state struct {
		Scope  string `json:"scope"`
		Stored *int64 `json:"stored"`
		Formed *int64 `json:"formed"`
		Parked int    `json:"parked"`
	}
	h.do(t, http.MethodGet, "/v1/freshness", nil, http.StatusOK, &state)
	if state.Scope != "p1" || state.Stored != nil || state.Formed != nil || state.Parked != 0 {
		t.Fatalf("empty project: %+v", state)
	}
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{
		"data_subject_id": "empty-test", "messages": []map[string]any{{"role": "user", "content": "I work at Ensera and I live in Dublin."}},
	}, http.StatusCreated, nil)
	h.do(t, http.MethodGet, "/v1/freshness", nil, http.StatusOK, &state)
	if state.Stored == nil || *state.Stored != 0 || state.Formed != nil {
		t.Fatalf("first stored observation: %+v", state)
	}
	h.form(t)
	h.do(t, http.MethodGet, "/v1/freshness", nil, http.StatusOK, &state)
	if state.Formed == nil || *state.Formed != 0 {
		t.Fatalf("first formed observation: %+v", state)
	}
	h.do(t, http.MethodPost, "/v1/erasures", map[string]any{"data_subject_id": "empty-test"}, http.StatusOK, nil)
	h.do(t, http.MethodGet, "/v1/freshness", nil, http.StatusOK, &state)
	if state.Stored == nil || *state.Stored != 0 {
		t.Fatalf("erasure discarded the committed watermark: %+v", state)
	}
}
