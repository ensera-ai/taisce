// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestObservationGroupsRoundTripThroughHTTPAndExport(t *testing.T) {
	h := newHarness(t, "api_groups")
	messages := []map[string]any{
		{"role": "user", "content": "compare the weather", "group_ordinal": 0},
		{"role": "assistant", "content": "call weather for Dublin", "group_ordinal": 1},
		{"role": "tool", "content": "Dublin is wet", "group_ordinal": 1},
		{"role": "assistant", "content": "call weather for Amman", "group_ordinal": 2},
		{"role": "tool", "content": "Amman is dry", "group_ordinal": 2},
		{"role": "assistant", "content": "comparison complete", "group_ordinal": 3},
	}
	var receipt struct {
		ID string `json:"id"`
	}
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": "groups", "messages": messages}, http.StatusCreated, &receipt)
	// A second turn starts again at group zero, so an export must preserve observation identity too.
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": "groups", "messages": []map[string]any{
		{"role": "user", "content": "ordinary message"}, {"role": "assistant", "content": "ordinary reply"},
	}}, http.StatusCreated, nil)
	var exported struct {
		Sections map[string][]json.RawMessage `json:"sections"`
	}
	h.do(t, http.MethodPost, "/v1/exports", map[string]any{"data_subject_id": "groups"}, http.StatusOK, &exported)
	rows := exported.Sections["message"]
	if len(rows) != 8 {
		t.Fatalf("exported %d messages", len(rows))
	}
	for i, row := range rows {
		var m struct {
			Observation string `json:"observation_id"`
			Ordinal     int    `json:"ordinal"`
			Group       int    `json:"group_ordinal"`
			Role        string `json:"role"`
			Content     string `json:"content"`
		}
		if err := json.Unmarshal(row, &m); err != nil {
			t.Fatal(err)
		}
		if i < 6 {
			if m.Observation != receipt.ID || m.Ordinal != i || m.Group != messages[i]["group_ordinal"] || m.Role != messages[i]["role"] || m.Content != messages[i]["content"] {
				t.Fatalf("message %d changed: %+v", i, m)
			}
		} else if m.Observation == "" || m.Observation == receipt.ID || m.Group != i-6 || m.Ordinal != i-6 {
			t.Fatalf("ordinary groups or observation identity collapsed: %+v", m)
		}
	}
}

func TestInvalidGroupsLeaveNoObservationOrOffset(t *testing.T) {
	h := newHarness(t, "api_invalid_groups")
	for _, body := range []string{
		`{"messages":[{"role":"user","content":"x","group_ordinal":-1}]}`,
		`{"messages":[{"role":"user","content":"x","group_ordinal":1}]}`,
		`{"messages":[{"role":"user","content":"x","group_ordinal":0},{"role":"assistant","content":"x","group_ordinal":2}]}`,
		`{"messages":[{"role":"user","content":"x","group_ordinal":0},{"role":"assistant","content":"x","group_ordinal":1},{"role":"tool","content":"x","group_ordinal":0}]}`,
		`{"messages":[{"role":"user","content":"x","group_ordinal":0},{"role":"assistant","content":"x"}]}`,
		`{"messages":[{"role":"user","content":"x"},{"role":"assistant","content":"x","group_ordinal":0}]}`,
		`{"messages":[{"role":"assistant","content":"call"},{"role":"tool","content":"result"}]}`,
	} {
		status, response := h.rawBody(t, http.MethodPost, "/v1/observations", body, h.token)
		if status != http.StatusBadRequest {
			t.Fatalf("invalid group accepted: %d %s", status, response)
		}
	}
	var count int
	if err := h.pool.QueryRow(context.Background(), h.schema.SQL(`SELECT count(*) FROM {schema}.observation`)).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("invalid groups stored %d observations", count)
	}
	if err := h.pool.QueryRow(context.Background(), h.schema.SQL(`SELECT count(*) FROM {schema}.watermark`)).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("invalid groups allocated %d watermarks", count)
	}
}
