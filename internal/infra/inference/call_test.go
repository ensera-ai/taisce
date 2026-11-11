// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package inference_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/ensera-ai/taisce/internal/compaction"
	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/inference"
	"github.com/ensera-ai/taisce/internal/report"
)

// generationCalls is each of the three ways a generation model is reached, so a property stated
// about "every generation call" is checked on every one and on any added here later.
func generationCalls(t *testing.T) map[inference.Purpose]func(inference.Config) error {
	t.Helper()
	vocabulary, err := domain.NewOntology([]domain.Predicate{
		{Name: "works_at", SemanticType: "identity", Cardinality: domain.CardinalityMany,
			ObjectKind: "organisation", Description: "An organisation the subject works for."},
	})
	if err != nil {
		t.Fatalf("vocabulary: %v", err)
	}
	return map[inference.Purpose]func(inference.Config) error{
		inference.PurposeExtraction: func(c inference.Config) error {
			_, err := inference.NewModel(c).Propose(context.Background(), domain.Message{Content: "I work at Ensera."}, vocabulary)
			return err
		},
		inference.PurposeReport: func(c inference.Config) error {
			_, err := inference.NewReporter(c).Write(context.Background(), report.Context{Entities: []string{"Ensera"}})
			return err
		},
		inference.PurposeCompaction: func(c inference.Config) error {
			_, err := inference.NewSummariser(c).Write(context.Background(), compaction.Material{})
			return err
		},
	}
}

// A reply that was cut off is refused whole, whatever could be parsed from it, and is still counted.
//
// A cut reply is a document that stops in the middle. Sometimes that fails to parse. Sometimes the
// cut lands where the fragment is still well-formed and holds the first half of what the model
// meant to say — and using it would store half of a message's claims and mark the message formed,
// with nothing afterwards able to tell that from a message that asserted half as much. The reply
// served here is exactly that case: complete, valid JSON, and marked as cut.
//
// It is counted as well as refused, with what it cost, because a reply that was cut was still paid
// for and a ceiling that hid its own cost would make raising it look free.
func TestAReplyThatWasCutIsRefusedWholeAndStillCounted(t *testing.T) {
	wellFormed := map[inference.Purpose]string{
		inference.PurposeExtraction: `{"claims":[]}`,
		inference.PurposeReport:     `{"title":"Ensera","summary":"A company."}`,
		inference.PurposeCompaction: `{"summary":"Nothing was said."}`,
	}
	for purpose, call := range generationCalls(t) {
		for _, ceiling := range []int{0, 512} {
			var mu sync.Mutex
			var counted []inference.Call
			config := serving(t, func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"choices": []map[string]any{{"finish_reason": "length", "message": map[string]string{"role": "assistant", "content": wellFormed[purpose]}}},
					"usage":   map[string]int{"prompt_tokens": 900, "completion_tokens": 512},
				})
			})
			config.MaxOutputTokens = ceiling
			config.Metered = func(c inference.Call) { mu.Lock(); counted = append(counted, c); mu.Unlock() }

			err := call(config)
			if !errors.Is(err, inference.ErrReplyCut) {
				t.Fatalf("%s, ceiling %d: a reply marked as cut was used, or refused for another reason: %v", purpose, ceiling, err)
			}
			// The refusal tells the operator which limit it was, because the remedy differs.
			if ceiling == 0 && !strings.Contains(err.Error(), "provider's own limit") {
				t.Errorf("%s: with no ceiling stated, the refusal does not say the provider cut it: %v", purpose, err)
			}
			if ceiling > 0 && !strings.Contains(err.Error(), "512") {
				t.Errorf("%s: the refusal does not name the ceiling that was reached: %v", purpose, err)
			}
			if !strings.Contains(err.Error(), inference.EnvMaxOutputTokens) {
				t.Errorf("%s: the refusal does not name the setting that changes it: %v", purpose, err)
			}
			if len(counted) != 1 || counted[0] != (inference.Call{Purpose: purpose, Reported: true, PromptTokens: 900, CompletionTokens: 512, Cut: true}) {
				t.Errorf("%s: the cut reply was counted as %+v", purpose, counted)
			}
		}
	}
}

// A ceiling is stated on the request only when the operator set one, and a call is counted with what
// the provider said it cost — or as unknown when the provider said nothing.
//
// No default ceiling is stated, on evidence: a model that reasons before answering spends the
// allowance on its reasoning, so a number that suits one model returns nothing from another. What
// this holds is that the operator's number reaches every generation request when there is one, that
// nothing is sent when there is not, and that a provider reporting no usage is not counted as a
// call that cost nothing.
func TestTheOperatorsCeilingReachesEveryGenerationRequestAndEveryCallIsCounted(t *testing.T) {
	replies := map[inference.Purpose]string{
		inference.PurposeExtraction: `{"claims":[]}`,
		inference.PurposeReport:     `{"title":"Ensera","summary":"A company."}`,
		inference.PurposeCompaction: `{"summary":"Nothing was said."}`,
	}
	for purpose, call := range generationCalls(t) {
		for _, c := range []struct {
			ceiling     int
			usage       bool
			wantRequest string
		}{
			{0, true, "absent"},
			{2048, true, "2048"},
			{2048, false, "2048"},
		} {
			var mu sync.Mutex
			var counted []inference.Call
			var sent map[string]json.RawMessage
			config := serving(t, func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				mu.Lock()
				_ = json.Unmarshal(raw, &sent)
				mu.Unlock()
				reply := map[string]any{"choices": []map[string]any{{"finish_reason": "stop", "message": map[string]string{"role": "assistant", "content": replies[purpose]}}}}
				if c.usage {
					reply["usage"] = map[string]int{"prompt_tokens": 310, "completion_tokens": 42}
				}
				_ = json.NewEncoder(w).Encode(reply)
			})
			config.MaxOutputTokens = c.ceiling
			config.Metered = func(call inference.Call) { mu.Lock(); counted = append(counted, call); mu.Unlock() }
			if err := call(config); err != nil {
				t.Fatalf("%s: a complete reply was refused: %v", purpose, err)
			}
			got := "absent"
			if raw, ok := sent["max_tokens"]; ok {
				got = string(raw)
			}
			if got != c.wantRequest {
				t.Errorf("%s, ceiling %d: the request's max_tokens is %s, want %s", purpose, c.ceiling, got, c.wantRequest)
			}
			want := inference.Call{Purpose: purpose, Reported: c.usage}
			if c.usage {
				want.PromptTokens, want.CompletionTokens = 310, 42
			}
			if len(counted) != 1 || counted[0] != want {
				t.Errorf("%s, usage reported %v: counted %+v, want %+v", purpose, c.usage, counted, want)
			}
		}
	}
}

// A ceiling nobody could have meant is refused at startup rather than ignored.
//
// Ignoring it would run with no ceiling while the operator believed theirs was in force.
func TestACeilingThatCannotBeReadIsRefused(t *testing.T) {
	for _, bad := range []string{"lots", "0", "-5", "4096.5", "1000001"} {
		hermetic(t)
		t.Setenv(inference.EnvEndpoint, "http://localhost:1/v1")
		t.Setenv(inference.EnvModel, "test-model")
		t.Setenv(inference.EnvAllowlist, "localhost:1")
		t.Setenv(inference.EnvMaxOutputTokens, bad)
		if _, err := inference.ConfigFromEnv(); err == nil || !strings.Contains(err.Error(), inference.EnvMaxOutputTokens) {
			t.Errorf("a ceiling of %q was accepted, or refused without naming the setting: %v", bad, err)
		}
	}
	t.Setenv(inference.EnvMaxOutputTokens, " 8192 ")
	config, err := inference.ConfigFromEnv()
	if err != nil || config.MaxOutputTokens != 8192 {
		t.Fatalf("a readable ceiling was not read: %d %v", config.MaxOutputTokens, err)
	}
	t.Setenv(inference.EnvMaxOutputTokens, "")
	if config, err := inference.ConfigFromEnv(); err != nil || config.MaxOutputTokens != 0 {
		t.Fatalf("with nothing set a ceiling of %d is stated (%v)", config.MaxOutputTokens, err)
	}
}
