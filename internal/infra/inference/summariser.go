// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/ensera-ai/taisce/internal/compaction"
)

// The compaction prompt is a file for the same reasons the report prompt is, and compiled in for the
// same reason: a segment stands in for somebody's history, so a prompt the filesystem can change is a
// path by which whoever can write next to the binary changes what an agent is told happened.
//
//go:embed prompts/compaction.yaml
var compactionPromptFile []byte

// compactionPrompt is parsed once, at startup, with the report prompt's loader: the two files have the
// same sections because they answer the same questions, what the model is for, what grounds it, what
// shape it answers in, and where the data begins.
var compactionPrompt = mustLoadCompactionPrompt(compactionPromptFile)

func mustLoadCompactionPrompt(raw []byte) reportPromptFileShape {
	p, err := loadReportPrompt(raw)
	if err != nil {
		panic("inference: the embedded compaction prompt is unusable: " + err.Error())
	}
	return p
}

// Summariser writes segments by asking a chat model.
//
// It writes prose and nothing more. What a segment covers, when it is written and what an erasure
// does to it are the compaction package's and the pass's decisions, and they must not vary with the
// vendor.
type Summariser struct {
	config Config
	client *http.Client
}

// NewSummariser builds a segment writer against an OpenAI-compatible endpoint.
func NewSummariser(config Config) *Summariser {
	return &Summariser{config: config, client: modelClient(5 * time.Minute)}
}

// Write asks the model to condense one stretch of history.
func (s *Summariser) Write(ctx context.Context, m compaction.Material) (compaction.Summary, error) {
	body, err := json.Marshal(chatRequest{
		Model:          s.config.Model,
		Temperature:    compactionPrompt.ModelContract.Temperature,
		ResponseFormat: &responseFormat{Type: compactionPrompt.ModelContract.ResponseFormat},
		Messages: []chatMessage{
			{Role: "system", Content: compactionSystemPrompt()},
			{Role: "user", Content: renderMaterial(m)},
		},
	})
	if err != nil {
		return compaction.Summary{}, fmt.Errorf("build request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.config.Endpoint+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return compaction.Summary{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if s.config.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.config.APIKey)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return compaction.Summary{}, fmt.Errorf("call model: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return compaction.Summary{}, fmt.Errorf("read reply: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return compaction.Summary{}, fmt.Errorf("model returned %d", resp.StatusCode)
	}
	var completion chatResponse
	if err := json.Unmarshal(raw, &completion); err != nil {
		return compaction.Summary{}, fmt.Errorf("decode reply: %w", err)
	}
	if len(completion.Choices) == 0 {
		return compaction.Summary{}, fmt.Errorf("the model returned no reply")
	}
	return decodeSummary(completion.Choices[0].Message.Content)
}

// compactionSystemPrompt composes the file's sections in their fixed order, the boundary last.
func compactionSystemPrompt() string {
	return strings.Join([]string{
		compactionPrompt.Task, compactionPrompt.Grounding, compactionPrompt.Shape,
		compactionPrompt.ReplyShape, compactionPrompt.DataBoundary,
	}, "\n\n")
}

// decodeSummary reads the reply, locating the JSON object inside whatever the model sent, and refuses
// an empty summary: a segment that says nothing would stand in for turns and lose them.
func decodeSummary(content string) (compaction.Summary, error) {
	var reply struct {
		Summary string `json:"summary"`
	}
	for start := strings.Index(content, "{"); start >= 0; start = nextBrace(content, start) {
		if err := json.Unmarshal([]byte(content[start:lastBrace(content)+1]), &reply); err == nil {
			break
		}
	}
	text := strings.TrimSpace(reply.Summary)
	if text == "" {
		return compaction.Summary{}, fmt.Errorf("the reply carries no summary")
	}
	return compaction.Summary{Text: text}, nil
}

func nextBrace(s string, after int) int {
	i := strings.Index(s[after+1:], "{")
	if i < 0 {
		return -1
	}
	return after + 1 + i
}

func lastBrace(s string) int {
	return strings.LastIndex(s, "}")
}

// renderMaterial writes the material out, fenced, in log order. The order is the conversation's own,
// and it is fixed so two writes of one range say the same thing.
func renderMaterial(m compaction.Material) string {
	var b strings.Builder
	fence := newFence(materialWords(m))
	fmt.Fprintf(&b, "<<<material %s>>>\n", fence)
	if len(m.Units) > 0 {
		fmt.Fprintf(&b, "SUMMARIES OF EARLIER STRETCHES, OLDEST FIRST, TOGETHER COVERING TURNS %d TO %d\n", m.From, m.To)
		units := append([]compaction.UnitText(nil), m.Units...)
		sort.Slice(units, func(i, j int) bool { return units[i].From < units[j].From })
		for _, u := range units {
			fmt.Fprintf(&b, "- turns %d to %d (%d turns): %s\n", u.From, u.To, u.Covered, u.Summary)
		}
	} else {
		fmt.Fprintf(&b, "TURNS %d TO %d, OLDEST FIRST\n", m.From, m.To)
		turns := append([]compaction.TurnText(nil), m.Turns...)
		sort.Slice(turns, func(i, j int) bool { return turns[i].Offset < turns[j].Offset })
		for _, t := range turns {
			fmt.Fprintf(&b, "\nturn %d at %s\n", t.Offset, t.OccurredAt.UTC().Format(time.RFC3339))
			for _, msg := range t.Messages {
				fmt.Fprintf(&b, "%s: %s\n", msg.Role, msg.Content)
			}
		}
	}
	fmt.Fprintf(&b, "<<<end %s>>>\n", fence)
	return b.String()
}

// materialWords is every byte of somebody else's words in the material, which the fence must be
// absent from.
func materialWords(m compaction.Material) string {
	var b strings.Builder
	for _, t := range m.Turns {
		for _, msg := range t.Messages {
			b.WriteString(msg.Role)
			b.WriteString(msg.Content)
		}
	}
	for _, u := range m.Units {
		b.WriteString(u.Summary)
	}
	return b.String()
}
