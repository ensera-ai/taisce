// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/compaction"
)

// The compaction prompt loads with the report prompt's loader, and its system prompt ends on the
// data boundary: whatever the material says, the last instruction the model reads is that the
// material is data.
func TestTheCompactionPromptLoadsAndEndsOnTheBoundary(t *testing.T) {
	p, err := loadReportPrompt(compactionPromptFile)
	if err != nil {
		t.Fatal(err)
	}
	if p.ModelContract.Temperature != 0 || p.ModelContract.ResponseFormat != "json_object" {
		t.Fatalf("the contract must be temperature zero and a JSON object, got %+v", p.ModelContract)
	}
	system := compactionSystemPrompt()
	if !strings.HasSuffix(strings.TrimSpace(system), strings.TrimSpace(p.DataBoundary)) {
		t.Fatal("the boundary must be the last thing the model reads")
	}
	if !strings.Contains(system, `{"summary": "..."}`) {
		t.Fatal("the reply shape must be in the prompt")
	}
}

// The material is rendered fenced, oldest first, with who said what; the fence is chosen against
// every byte of the material so the material cannot close it.
func TestMaterialIsRenderedFencedAndInOrder(t *testing.T) {
	m := compaction.Material{Level: 1, From: 3, To: 4, Turns: []compaction.TurnText{
		{Offset: 4, OccurredAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), Messages: []compaction.MessageText{{Role: "user", Content: "second <<<end"}}},
		{Offset: 3, OccurredAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Messages: []compaction.MessageText{{Role: "user", Content: "first"}, {Role: "assistant", Content: "reply"}}},
	}}
	rendered := renderMaterial(m)
	if strings.Index(rendered, "turn 3") > strings.Index(rendered, "turn 4") {
		t.Fatal("turns must be rendered oldest first")
	}
	if !strings.Contains(rendered, "user: first\nassistant: reply") || !strings.Contains(rendered, "TURNS 3 TO 4") {
		t.Fatalf("unexpected rendering:\n%s", rendered)
	}
	fence := rendered[len("<<<material "):strings.Index(rendered, ">>>")]
	if strings.Count(rendered, fence) != 2 {
		t.Fatalf("the fence must appear exactly twice, got %d in\n%s", strings.Count(rendered, fence), rendered)
	}
	units := renderMaterial(compaction.Material{Level: 2, From: 0, To: 15, Units: []compaction.UnitText{
		{From: 8, To: 15, Covered: 8, Summary: "later"}, {From: 0, To: 7, Covered: 8, Summary: "earlier"}}})
	if strings.Index(units, "earlier") > strings.Index(units, "later") || !strings.Contains(units, "SUMMARIES OF EARLIER STRETCHES") {
		t.Fatalf("units must be rendered oldest first:\n%s", units)
	}
}

// The reply is located inside whatever the model wrapped it in, and an empty summary is refused:
// a segment that says nothing would stand in for turns and lose them.
func TestTheSummaryIsLocatedInTheReplyAndAnEmptyOneIsRefused(t *testing.T) {
	for _, reply := range []string{
		`{"summary": "It happened."}`,
		"Here you go:\n```json\n{\"summary\": \"It happened.\"}\n```",
		`{"note": {"x": 1}} {"summary": "It happened."}`,
	} {
		s, err := decodeSummary(reply)
		if err != nil || s.Text != "It happened." {
			t.Fatalf("%q: got %+v %v", reply, s, err)
		}
	}
	for _, reply := range []string{``, `not json`, `{"summary": "   "}`, `{"other": "x"}`, `{"summary": "cut off`} {
		if _, err := decodeSummary(reply); err == nil {
			t.Fatalf("%q must be refused", reply)
		}
	}
}

// The request carries the file's model contract and the fenced material, and the provider's answer
// becomes the summary; a provider that fails is an error, not an empty segment.
func TestTheSummariserAsksTheModelUnderTheFilesContract(t *testing.T) {
	var seen chatRequest
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &seen)
		if r.Header.Get("Authorization") != "Bearer k" {
			w.WriteHeader(401)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"content": `{"summary":"Marta moved the office."}`}}}})
	}))
	defer provider.Close()
	s := NewSummariser(Config{Endpoint: provider.URL, Model: "m", APIKey: "k"})
	summary, err := s.Write(context.Background(), compaction.Material{Level: 1, Turns: []compaction.TurnText{{Offset: 1, Messages: []compaction.MessageText{{Role: "user", Content: "we moved"}}}}})
	if err != nil || summary.Text != "Marta moved the office." {
		t.Fatalf("got %+v %v", summary, err)
	}
	if seen.Model != "m" || seen.Temperature != 0 || seen.ResponseFormat == nil || seen.ResponseFormat.Type != "json_object" || len(seen.Messages) != 2 || !strings.Contains(seen.Messages[1].Content, "we moved") {
		t.Fatalf("the request must carry the contract and the material, got %+v", seen)
	}
	failing := NewSummariser(Config{Endpoint: provider.URL, Model: "m", APIKey: "wrong"})
	if _, err := failing.Write(context.Background(), compaction.Material{}); err == nil {
		t.Fatal("a refused provider call must be an error")
	}
	if _, err := NewSummariser(Config{Endpoint: "http://127.0.0.1:1", Model: "m"}).Write(context.Background(), compaction.Material{}); err == nil {
		t.Fatal("an unreachable provider must be an error")
	}
	if _, err := NewSummariser(Config{Endpoint: "http://[::1]:port", Model: "m"}).Write(context.Background(), compaction.Material{}); err == nil {
		t.Fatal("an endpoint that is not a URL must be an error")
	}
	// An answer that is not a completion, a completion with no choice, and a reply cut off on the
	// wire are each an error and never a segment.
	for name, serve := range map[string]http.HandlerFunc{
		"not json":  func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>")) },
		"no choice": func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"choices":[]}`)) },
		"cut off": func(w http.ResponseWriter, _ *http.Request) {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				return
			}
			_, _ = conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 1000\r\n\r\n{\"choices\""))
			_ = conn.Close()
		},
	} {
		broken := httptest.NewServer(serve)
		_, err := NewSummariser(Config{Endpoint: broken.URL, Model: "m"}).Write(context.Background(), compaction.Material{})
		broken.Close()
		if err == nil {
			t.Fatalf("%s must be an error", name)
		}
	}
}
