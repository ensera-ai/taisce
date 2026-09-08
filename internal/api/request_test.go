// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestAmbiguousBodiesCannotAppendOrErase(t *testing.T) {
	h := newHarness(t, "api_strict_json")
	valid := `{"data_subject_id":"preserve-me","messages":[{"role":"user","content":"مرحبا 🌍"}]}`
	h.rawBody(t, http.MethodPost, "/v1/observations", valid, h.token)
	for name, body := range map[string]string{
		"second value":            valid + `{}`,
		"trailing garbage":        valid + `secret-suffix`,
		"duplicate root":          `{"data_subject_id":"a","data_subject_id":"b","messages":[{"role":"user","content":"x"}]}`,
		"case alias":              `{"data_subject_id":"a","DATA_SUBJECT_ID":"b","messages":[]}`,
		"escaped duplicate":       `{"messages":[],"\u006dessages":[]}`,
		"nested duplicate":        `{"messages":[{"role":"user","content":"x","content":"y"}]}`,
		"nested case alias":       `{"messages":[{"role":"user","ROLE":"tool","content":"x"}]}`,
		"invalid utf8":            `{"messages":[{"role":"user","content":"` + string([]byte{0xff}) + `"}]}`,
		"unpaired high surrogate": `{"messages":[{"role":"user","content":"\uD800"}]}`,
		"unpaired low surrogate":  `{"messages":[{"role":"user","content":"\uDC00"}]}`,
		"wrong surrogate pair":    `{"messages":[{"role":"user","content":"\uD800\u0041"}]}`,
		"invalid surrogate hex":   `{"messages":[{"role":"user","content":"\uD800\uZZZZ"}]}`,
		"short unicode":           `{"messages":[{"role":"user","content":"\uD8`,
		"invalid unicode":         `{"messages":[{"role":"user","content":"\uZZZZ"}]}`,
		"null":                    `null`,
		"array":                   `[]`,
		"missing value":           `{"messages":}`,
		"trailing comma":          `{"messages":[],}`,
		"truncated nested":        `{"messages":[{"role":`,
		"deep object":             `{"unknown":` + strings.Repeat(`{"x":`, 129) + `0` + strings.Repeat(`}`, 129) + `}`,
		"deep array":              `{"messages":` + strings.Repeat(`[`, 129) + `0` + strings.Repeat(`]`, 129) + `}`,
		"oversize suffix":         valid + strings.Repeat(" ", 1<<20),
	} {
		t.Run(name, func(t *testing.T) {
			status, response := h.rawBody(t, http.MethodPost, "/v1/observations", body, h.token)
			if status != http.StatusBadRequest || !strings.Contains(string(response), `"invalid_body"`) {
				t.Fatalf("ambiguous append returned %d: %s", status, response)
			}
		})
	}
	for _, body := range []string{
		`{"data_subject_id":"preserve-me"}{}`,
		`{"data_subject_id":"other","data_subject_id":"preserve-me"}`,
	} {
		status, response := h.rawBody(t, http.MethodPost, "/v1/erasures", body, h.token)
		if status != http.StatusBadRequest {
			t.Fatalf("ambiguous erasure returned %d: %s", status, response)
		}
	}
	var count, offset, successful int
	if err := h.pool.QueryRow(context.Background(), h.schema.SQL(`SELECT count(*),max(log_offset) FROM {schema}.observation`)).Scan(&count, &offset); err != nil {
		t.Fatal(err)
	}
	if count != 1 || offset != 0 {
		t.Fatalf("refused input changed observations: count=%d offset=%d", count, offset)
	}
	if err := h.pool.QueryRow(context.Background(), h.schema.SQL(`SELECT count(*) FROM {schema}.audit_entry WHERE outcome='allowed'`)).Scan(&successful); err != nil {
		t.Fatal(err)
	}
	if successful != 1 {
		t.Fatalf("refused input produced successful audit entries: %d", successful)
	}
	status, response := h.rawBody(t, http.MethodPost, "/v1/observations", strings.ReplaceAll(valid, "🌍", `\uD83C\uDF0D`)+" \r\n\t", h.token)
	if status != http.StatusCreated {
		t.Fatalf("valid Unicode and whitespace refused: %d %s", status, response)
	}
	var receipt struct {
		LogOffset int `json:"log_offset"`
	}
	if err := json.Unmarshal(response, &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.LogOffset != 1 {
		t.Fatalf("refusals consumed offsets: %d", receipt.LogOffset)
	}
}
