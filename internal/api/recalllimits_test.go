// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/recall"
)

func TestRecallAdmissionLimitsAreClientRefusalsOverHTTP(t *testing.T) {
	h := newHarness(t, "api_recall_limits")
	var words []string
	for i := 0; i < 150; i++ {
		words = append(words, fmt.Sprintf("word%d", i))
	}
	for _, question := range []string{strings.Repeat("é", domain.MaxRecallQuestionBytes/2+1), strings.Join(words, " ")} {
		status, body := h.raw(t, http.MethodPost, "/v1/recalls", map[string]any{"question": question}, h.token)
		var refused struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(body, &refused); err != nil {
			t.Fatal(err)
		}
		if status != http.StatusBadRequest || refused.Error.Code != "invalid_question" {
			t.Fatalf("refusal %d %s", status, body)
		}
	}
	status, body := h.raw(t, http.MethodPost, "/v1/recalls", map[string]any{"question": strings.Repeat("é", domain.MaxRecallQuestionBytes/2)}, h.token)
	if status != http.StatusOK {
		t.Fatalf("boundary refused %d %s", status, body)
	}
	words = words[:0]
	for i := 0; i < 87; i++ {
		words = append(words, fmt.Sprintf("anchor%d", i))
	}
	question := strings.Join(words, " ")
	terms := recall.Terms(question)
	if len(terms) <= domain.MaxRecallMatches || len(terms) > domain.MaxRecallTerms {
		t.Fatalf("overflow fixture has %d terms", len(terms))
	}
	if _, err := h.pool.Exec(context.Background(), h.schema.SQL(`INSERT INTO {schema}.entity(entity_id,scope,canonical_name,normalized_name)
SELECT gen_random_uuid(),'p1',term,term FROM unnest($1::text[]) term`), terms); err != nil {
		t.Fatal(err)
	}
	status, body = h.raw(t, http.MethodPost, "/v1/recalls", map[string]any{"question": question}, h.token)
	if status != http.StatusBadRequest || !strings.Contains(string(body), "invalid_question") || strings.Contains(string(body), "anchors") {
		t.Fatalf("overflow must refuse without partial bundle: %d %s", status, body)
	}
}
