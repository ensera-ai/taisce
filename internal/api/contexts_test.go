// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package api_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/ensera-ai/taisce/internal/compaction"
	"github.com/ensera-ai/taisce/internal/formation"
	"github.com/ensera-ai/taisce/internal/infra/pg"
)

type contextSummariser struct{ calls int }

func (s *contextSummariser) Write(_ context.Context, m compaction.Material) (compaction.Summary, error) {
	s.calls++
	return compaction.Summary{Text: fmt.Sprintf("summary of %d-%d", m.From, m.To)}, nil
}

// A context is one subject's history: the newest turns as they were said, the segments the pass
// wrote over the rest, the watermark, and the cut. It costs no model call; a subject is required;
// a budget outside the server's is refused; another subject's turns are never in it.
func TestAContextIsTheNewestTurnsVerbatimAndSegmentsOverTheRestUnderABudget(t *testing.T) {
	h := newHarness(t, "api_contexts")
	ctx := context.Background()
	for i := 0; i < compaction.Verbatim+compaction.Branch+1; i++ {
		h.do(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": "subject-1", "messages": []map[string]any{
			{"role": "user", "content": fmt.Sprintf("turn %d: I work at Ensera.", i)}, {"role": "assistant", "content": "noted"}}}, 201, nil)
	}
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": "subject-2", "messages": []map[string]any{
		{"role": "user", "content": "somebody else's turn"}}}, 201, nil)
	h.form(t)
	model := &contextSummariser{}
	if _, err := formation.NewCompaction(pg.NewSegmentStore(h.pool, h.schema), model).Run(ctx, "p1", 10); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Segments []struct {
			Level   int    `json:"level"`
			From    int64  `json:"from_offset"`
			To      int64  `json:"to_offset"`
			Covered int    `json:"covered"`
			Summary string `json:"summary"`
		} `json:"segments"`
		Turns []struct {
			LogOffset int64 `json:"log_offset"`
			Messages  []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		} `json:"turns"`
		Watermark struct {
			Stored *int64 `json:"stored"`
			Formed *int64 `json:"formed"`
		} `json:"watermark"`
		Characters int  `json:"characters"`
		Truncated  bool `json:"truncated"`
	}
	calls := model.calls
	h.do(t, http.MethodPost, "/v1/contexts", map[string]any{"data_subject_id": "subject-1"}, 200, &out)
	if model.calls != calls {
		t.Fatal("assembling a context must make no model call")
	}
	if len(out.Segments) != 1 || out.Segments[0].Level != 1 || out.Segments[0].Covered != compaction.Branch || out.Segments[0].Summary == "" {
		t.Fatalf("expected one level-1 segment over the oldest run, got %+v", out.Segments)
	}
	if len(out.Turns) != compaction.Verbatim+1 || out.Turns[0].LogOffset <= out.Segments[0].To {
		t.Fatalf("expected the newest turns verbatim after the segment, got %d turns starting at %d", len(out.Turns), out.Turns[0].LogOffset)
	}
	for _, turn := range out.Turns {
		for _, m := range turn.Messages {
			if m.Content == "somebody else's turn" {
				t.Fatal("another subject's turn must never be in a context")
			}
		}
	}
	if out.Watermark.Stored == nil || out.Watermark.Formed == nil || out.Characters == 0 || out.Truncated {
		t.Fatalf("the watermark and the count must be reported, got %+v", out)
	}
	// A tight budget keeps the newest and says it cut.
	var tight struct {
		Segments  []map[string]any `json:"segments"`
		Turns     []map[string]any `json:"turns"`
		Truncated bool             `json:"truncated"`
	}
	h.do(t, http.MethodPost, "/v1/contexts", map[string]any{"data_subject_id": "subject-1", "max_characters": 60}, 200, &tight)
	if !tight.Truncated || len(tight.Segments) != 0 || len(tight.Turns) == 0 || len(tight.Turns) > 2 {
		t.Fatalf("a tight budget must keep only the newest and report the cut, got %+v", tight)
	}
	h.do(t, http.MethodPost, "/v1/contexts", map[string]any{"question": "x"}, 400, nil)
	h.do(t, http.MethodPost, "/v1/contexts", map[string]any{"data_subject_id": "  "}, 400, nil)
	h.do(t, http.MethodPost, "/v1/contexts", map[string]any{"data_subject_id": "subject-1", "max_characters": 0}, 400, nil)
	h.do(t, http.MethodPost, "/v1/contexts", map[string]any{"data_subject_id": "subject-1", "max_characters": 100001}, 400, nil)
	// An unknown subject is an empty context, not an error: nothing is known, and that is an answer.
	var empty struct {
		Segments []map[string]any `json:"segments"`
		Turns    []map[string]any `json:"turns"`
	}
	h.do(t, http.MethodPost, "/v1/contexts", map[string]any{"data_subject_id": "nobody"}, 200, &empty)
	if len(empty.Segments) != 0 || len(empty.Turns) != 0 {
		t.Fatalf("an unknown subject has an empty context, got %+v", empty)
	}
	// A store that cannot answer is an internal error, not an empty context: an empty answer
	// would be read as "nothing is known", which is not what happened.
	if _, err := h.pool.Exec(ctx, h.schema.SQL(`ALTER TABLE {schema}.segment RENAME TO segment_moved`)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = h.pool.Exec(context.Background(), h.schema.SQL(`ALTER TABLE {schema}.segment_moved RENAME TO segment`))
	})
	h.do(t, http.MethodPost, "/v1/contexts", map[string]any{"data_subject_id": "subject-1"}, 500, nil)
}
