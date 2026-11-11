// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// An explanation crosses the wire with references, and with none of the words it withheld.
//
// # Why this is here and not only in the recall package
//
// The budget bounds what crosses this boundary. The recall package holds the accounting; what this
// holds is the response as JSON, because that is where a leak would actually happen — a field added
// to a payload and rendered without anybody noticing that the thing it names was cut on purpose.
// Asserted against the explanation's own bytes rather than field by field, so text reaching the wire
// through a field this test does not know about still fails it.
func TestAnExplainedRecallCrossesTheWireWithReferencesAndNoWithheldWords(t *testing.T) {
	h := newHarness(t, "api_explain")

	var observed struct {
		ID string `json:"id"`
	}
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{
		"idempotency_key": uuid.NewString(),
		"data_subject_id": "subject-1",
		"occurred_at":     "2026-03-01T09:00:00Z",
		"messages": []map[string]any{
			{"role": "user", "content": "I work at Ensera and I live in Dublin."},
		},
	}, http.StatusCreated, &observed)
	h.form(t)

	// Without the control the object is absent, not empty: an explanation nobody asked for and an
	// explanation with nothing in it are different answers. (The `controls` echo says `explain:
	// false`, which is the control being reported back rather than an explanation.)
	var unasked struct {
		Explain json.RawMessage `json:"explain"`
	}
	_, plain := h.raw(t, http.MethodPost, "/v1/recalls", map[string]any{
		"question":        "Where does the user work?",
		"data_subject_id": "subject-1",
	}, h.token)
	if err := json.Unmarshal(plain, &unasked); err != nil {
		t.Fatalf("decode: %v (%s)", err, plain)
	}
	if len(unasked.Explain) != 0 {
		t.Fatalf("an explanation was published to a caller who did not ask: %s", unasked.Explain)
	}

	// With it, and with a budget too small for both facts, so a cut has something to report.
	_, body := h.raw(t, http.MethodPost, "/v1/recalls", map[string]any{
		"question":        "Where does the user work?",
		"data_subject_id": "subject-1",
		"explain":         true,
		"max_characters":  80,
	}, h.token)

	var answer struct {
		Controls struct {
			Explain bool `json:"explain"`
		} `json:"controls"`
		Facts []struct {
			Statement string `json:"statement"`
		} `json:"facts"`
		Truncated bool            `json:"truncated"`
		Explain   json.RawMessage `json:"explain"`
	}
	if err := json.Unmarshal(body, &answer); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if !answer.Controls.Explain || len(answer.Explain) == 0 {
		t.Fatalf("the explanation did not come back: %s", body)
	}

	var explained struct {
		Anchors []struct {
			EntityID string  `json:"entity_id"`
			Name     string  `json:"name"`
			Arm      string  `json:"arm"`
			Matched  string  `json:"matched"`
			Score    float64 `json:"score"`
			Kept     bool    `json:"kept"`
			Facts    int     `json:"facts"`
		} `json:"anchors"`
		Stages []struct {
			Surface string `json:"surface"`
			Ran     bool   `json:"ran"`
			Offered int    `json:"offered"`
			Kept    int    `json:"kept"`
			Dropped []struct {
				Ref        string  `json:"ref"`
				Cut        string  `json:"cut"`
				Characters int     `json:"characters"`
				Score      float64 `json:"score"`
			} `json:"dropped"`
		} `json:"stages"`
	}
	if err := json.Unmarshal(answer.Explain, &explained); err != nil {
		t.Fatalf("decode the explanation: %v (%s)", err, answer.Explain)
	}
	if len(explained.Anchors) == 0 || len(explained.Stages) == 0 {
		t.Fatalf("the explanation is empty: %s", answer.Explain)
	}
	for _, a := range explained.Anchors {
		if a.EntityID == "" || a.Arm == "" {
			t.Fatalf("an anchor says nothing about where it came from: %+v", a)
		}
		if !a.Kept && a.Name != "" {
			t.Fatalf("a dropped anchor was named: %+v", a)
		}
	}

	// The cut has to have bitten, or the next assertion proves nothing.
	var dropped int
	for _, s := range explained.Stages {
		for _, d := range s.Dropped {
			dropped++
			if d.Ref == "" || d.Cut == "" || d.Characters <= 0 {
				t.Fatalf("a dropped candidate says nothing about itself: %+v", d)
			}
		}
	}
	if dropped == 0 || !answer.Truncated {
		t.Fatalf("nothing was cut, so this test cannot say what a cut publishes: %s", body)
	}

	// The property. The turn's own words are checked against the explanation's own bytes: a
	// statement, a quote or a context that reached it would carry one of them, and a reference, a
	// cut name, a count and a score carry none.
	//
	// The caller's question is not in this list. `matched` echoes the term that reached an anchor,
	// which is the caller's own words coming back and is in the bundle already.
	for _, word := range []string{"Ensera", "Dublin", "The user", "I work", "I live"} {
		if strings.Contains(string(answer.Explain), word) {
			t.Fatalf("the explanation carried %q, which is the turn's words rather than a reference to them: %s",
				word, answer.Explain)
		}
	}
}
