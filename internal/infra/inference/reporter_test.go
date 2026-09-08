// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package inference_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/infra/inference"
	"github.com/ensera-ai/taisce/internal/report"
)

// ── The transport, against a real server ──────────────────────────────────────────────────────
//
// What a MODEL does with good material is measured against a live one, behind the inference tag.
// What this code does with a model's answer — how the request is shaped, what a refusal is, what is
// read and how much — is ours, and it is exercised here against a server that speaks the same
// protocol. A model is not being simulated: the assertions are all about our side of the wire.

func material() report.Context {
	return report.Context{
		Entities: []string{"Marta", "Ensera"},
		Facts: []report.Fact{{
			Subject: "Marta", Predicate: "works_at", Object: "Ensera",
			Statement: "Marta works at Ensera.", Quote: "Marta works at Ensera",
		}},
	}
}

func serving(t *testing.T, handler http.HandlerFunc) inference.Config {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	// Built through the environment, so the allowlist is satisfied the way a deployment satisfies it:
	// the test cannot reach a host it did not list either.
	//
	// EVERY inference variable is set, including the ones this test does not use. A test that sets
	// only what it needs inherits the rest from whoever ran it — and that is not hypothetical: these
	// passed under `make test` and failed under `make test-inference`, because a profile had exported
	// an embedding endpoint that this test's allowlist does not name. A test whose result depends on
	// the shell it was started from is not a test.
	hermetic(t)
	t.Setenv(inference.EnvEndpoint, server.URL+"/v1")
	t.Setenv(inference.EnvModel, "test-model")
	t.Setenv(inference.EnvAllowlist, strings.TrimPrefix(server.URL, "http://"))
	config, err := inference.ConfigFromEnv()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	return config
}

func reply(content string) string {
	body, _ := json.Marshal(map[string]any{
		"choices": []map[string]any{{"message": map[string]string{"content": content}}},
	})
	return string(body)
}

func TestAReportRequestCarriesTheContractAndTheMaterial(t *testing.T) {
	var got struct {
		Model          string  `json:"model"`
		Temperature    float32 `json:"temperature"`
		ResponseFormat *struct {
			Type string `json:"type"`
		} `json:"response_format"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	config := serving(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Errorf("the request is not JSON: %v", err)
		}
		io.WriteString(w, reply(`{"title":"Marta at Ensera","summary":"She works there.",`+
			`"importance":4,"importance_reason":"Two relations","findings":[]}`))
	})

	r, err := report.New(inference.NewReporter(config)).Write(context.Background(), material())
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if r.Title != "Marta at Ensera" || r.Importance != 4 {
		t.Fatalf("the report did not come back: %+v", r)
	}

	// The contract in the prompt file is the contract on the wire. A prompt asking for JSON with a
	// request that does not is a parse failure standing in for a description failure.
	if got.Temperature != 0 {
		t.Fatalf("temperature %v: two rebuilds of one community would differ", got.Temperature)
	}
	if got.ResponseFormat == nil || got.ResponseFormat.Type != "json_object" {
		t.Fatalf("the request did not ask for JSON: %+v", got.ResponseFormat)
	}
	if len(got.Messages) != 2 || got.Messages[0].Role != "system" || got.Messages[1].Role != "user" {
		t.Fatalf("the request is not a system prompt and material: %+v", got.Messages)
	}
	// The material is there, fenced, and the instructions are not in the same message as it.
	if !strings.Contains(got.Messages[1].Content, "Marta works at Ensera") {
		t.Fatal("the material never reached the model")
	}
	if strings.Contains(got.Messages[0].Content, "Marta works at Ensera") {
		t.Fatal("somebody's words are in the instruction message")
	}
}

func TestAModelThatRefusesIsARefusalRatherThanAnEmptyReport(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"a rate limit": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
			io.WriteString(w, `{"error":"slow down"}`)
		},
		"a server error": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		},
		"a body that is not JSON": func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, "gateway timeout, in prose")
		},
		"no choices at all": func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"choices":[]}`)
		},
		"a reply with no report in it": func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, reply("I am not able to help with that."))
		},
		"a report with no summary": func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, reply(`{"title":"A subject","summary":"","importance":0}`))
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := report.New(inference.NewReporter(serving(t, handler))).
				Write(context.Background(), material())
			if err == nil {
				t.Fatalf("%s produced a report", name)
			}
		})
	}
}

// A reply is read up to a bound, because a remote system does not decide how much memory this process
// spends on one answer.
func TestAnEnormousReplyDoesNotDecideHowMuchMemoryIsSpent(t *testing.T) {
	config := serving(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"content":"`)
		// Far past the bound, and never terminated.
		for i := 0; i < 4096; i++ {
			io.WriteString(w, strings.Repeat("a", 1024))
		}
	})
	if _, err := report.New(inference.NewReporter(config)).Write(context.Background(), material()); err == nil {
		t.Fatal("an unbounded reply was accepted")
	}
}

// A cancelled request stops rather than running to completion somewhere nobody is waiting.
func TestACancelledReportStops(t *testing.T) {
	config := serving(t, func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := report.New(inference.NewReporter(config)).Write(ctx, material()); err == nil {
		t.Fatal("a cancelled write returned a report")
	}
}

func TestReportInputDistinguishesEqualSpeakerNamesWithoutChangingQuotes(t *testing.T) {
	cfg := serving(t, func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		content := request.Messages[len(request.Messages)-1].Content
		for _, label := range []string{"speaker [entity:speaker-a]", "speaker [entity:speaker-b]", "Ensera [entity:shared-company]"} {
			if !strings.Contains(content, label) {
				t.Errorf("missing distinct reference %q", label)
			}
		}
		if strings.Count(content, `said: "I work at Ensera"`) != 2 {
			t.Error("source quotes were collapsed or rewritten")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": `{"title":"Work","summary":"Two speakers work at Ensera.","importance":1,"importance_reason":"work","findings":[]}`}}}})
	})
	first := report.Fact{SubjectID: "speaker-a", ObjectID: "shared-company", Subject: "speaker", Object: "Ensera", Predicate: "works_at", Quote: "I work at Ensera"}
	second := first
	second.SubjectID = "speaker-b"
	if _, err := inference.NewReporter(cfg).Write(context.Background(), report.Context{Entities: []string{"speaker", "speaker", "Ensera"}, Facts: []report.Fact{first, second}}); err != nil {
		t.Fatal(err)
	}
}
