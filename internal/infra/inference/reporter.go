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

	"github.com/ensera-ai/taisce/internal/report"
)

// The report prompt is a file for the same reasons the extraction prompt is, and compiled in for the
// same reason: a report is what a thematic question is answered from, so a prompt the filesystem can
// change is a path by which whoever can write next to the binary changes what this system says about
// somebody's memory.
//
//go:embed prompts/report.yaml
var reportPromptFile []byte

// reportPrompt is parsed once, at startup. A panic is right: the file is embedded, so a failure is a
// binary that must not start rather than a runtime condition that might clear.
var reportPrompt = mustLoadReportPrompt(reportPromptFile)

func mustLoadReportPrompt(raw []byte) reportPromptFileShape {
	p, err := loadReportPrompt(raw)
	if err != nil {
		panic("inference: the embedded report prompt is unusable: " + err.Error())
	}
	return p
}

// Reporter writes community reports by asking a chat model.
//
// It writes prose and nothing more. What material goes in, what is refused on the way out, and how a
// context is cut when it does not fit are all in the report package — those are the decisions with a
// correctness argument, and they must not vary with the vendor.
type Reporter struct {
	config Config
	client *http.Client
}

// NewReporter builds a report writer against an OpenAI-compatible endpoint.
func NewReporter(config Config) *Reporter {
	return &Reporter{
		config: config,
		// Longer than extraction's, because a report is a paragraph and several findings rather than
		// a short JSON object, and it is written on a background pass rather than on a path somebody
		// is waiting on. Still bounded: an unbounded call is a worker held open for as long as a
		// provider feels like.
		client: modelClient(5 * time.Minute),
	}
}

// Write asks the model to describe one community.
func (r *Reporter) Write(ctx context.Context, c report.Context) (report.Report, error) {
	material := renderContext(c)
	body, err := json.Marshal(chatRequest{
		Model:          r.config.Model,
		Temperature:    reportPrompt.ModelContract.Temperature,
		ResponseFormat: &responseFormat{Type: reportPrompt.ModelContract.ResponseFormat},
		Messages: []chatMessage{
			{Role: "system", Content: reportSystemPrompt()},
			{Role: "user", Content: material},
		},
	})
	if err != nil {
		return report.Report{}, fmt.Errorf("build request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.config.Endpoint+"/chat/completions",
		bytes.NewReader(body))
	if err != nil {
		return report.Report{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if r.config.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+r.config.APIKey)
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return report.Report{}, fmt.Errorf("call model: %w", err)
	}
	defer resp.Body.Close()
	// Bounded for the reason extraction's reply is: a remote system does not decide how much memory
	// this process spends on one answer.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return report.Report{}, fmt.Errorf("read reply: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return report.Report{}, fmt.Errorf("model returned %d", resp.StatusCode)
	}

	var completion chatResponse
	if err := json.Unmarshal(raw, &completion); err != nil {
		return report.Report{}, fmt.Errorf("decode reply: %w", err)
	}
	if len(completion.Choices) == 0 {
		return report.Report{}, fmt.Errorf("the model returned no reply")
	}
	return decodeReport(completion.Choices[0].Message.Content)
}

// reportEnvelope is the reply shape the prompt asks for.
type reportEnvelope struct {
	Title            string  `json:"title"`
	Summary          string  `json:"summary"`
	Importance       float32 `json:"importance"`
	ImportanceReason string  `json:"importance_reason"`
	Findings         []struct {
		Summary     string `json:"summary"`
		Explanation string `json:"explanation"`
	} `json:"findings"`
}

// decodeReport reads the reply, locating the JSON inside whatever the model actually sent.
//
// The same problem extraction has and the same answer: a model in JSON mode still wraps its answer in
// prose or a code fence often enough that a plain Unmarshal turns a good report into a failure. The
// object is recognised by its title, so a fragment that happens to parse is skipped rather than
// returned as a report with everything empty.
func decodeReport(content string) (report.Report, error) {
	const maxCandidates = 32
	var envelope reportEnvelope
	found := false
	for offset, tried := 0, 0; tried < maxCandidates; tried++ {
		relative := strings.IndexByte(content[offset:], '{')
		if relative < 0 {
			break
		}
		start := offset + relative
		offset = start + 1
		var candidate reportEnvelope
		if err := json.NewDecoder(strings.NewReader(content[start:])).Decode(&candidate); err != nil {
			continue
		}
		if strings.TrimSpace(candidate.Title) == "" {
			continue
		}
		envelope, found = candidate, true
		break
	}
	if !found {
		return report.Report{}, fmt.Errorf(
			"model did not return the requested JSON: no titled report object in the reply (%s)",
			truncateForError(content))
	}
	out := report.Report{
		Title:            strings.TrimSpace(envelope.Title),
		Summary:          strings.TrimSpace(envelope.Summary),
		Importance:       envelope.Importance,
		ImportanceReason: strings.TrimSpace(envelope.ImportanceReason),
	}
	for _, f := range envelope.Findings {
		out.Findings = append(out.Findings, report.Finding{
			Summary:     strings.TrimSpace(f.Summary),
			Explanation: strings.TrimSpace(f.Explanation),
		})
	}
	return out, nil
}

// renderContext writes the material out, fenced.
//
// The order is fixed — entities, then relations, then sub-reports — because a model given the same
// material in a different arrangement writes a different paragraph, and two rebuilds of one community
// would then differ for no reason anybody could name.
func renderContext(c report.Context) string {
	var b strings.Builder
	fence := newFence(materialText(c))
	fmt.Fprintf(&b, "<<<material %s>>>\n", fence)

	b.WriteString("THINGS IN THIS GROUP\n")
	entities := append([]string(nil), c.Entities...)
	sort.Strings(entities)
	for _, e := range entities {
		fmt.Fprintf(&b, "- %s\n", e)
	}

	if len(c.Facts) > 0 {
		b.WriteString("\nWHAT WAS RECORDED ABOUT THEM\n")
		for _, f := range c.Facts {
			fmt.Fprintf(&b, "- %s %s %s\n  said: %q\n", f.SubjectLabel(), f.Predicate, f.ObjectLabel(), f.Quote)
		}
	}

	if len(c.Children) > 0 {
		b.WriteString("\nREPORTS ABOUT SUB-GROUPS, WHOSE OWN MATERIAL IS NOT SHOWN HERE\n")
		for _, r := range c.Children {
			fmt.Fprintf(&b, "- %s: %s\n", r.Title, r.Summary)
			for _, f := range r.Findings {
				fmt.Fprintf(&b, "  · %s\n", f.Summary)
			}
		}
	}

	fmt.Fprintf(&b, "<<<end %s>>>\n", fence)
	return b.String()
}

// materialText is every byte of somebody else's words in this context, which is what the fence has to
// be absent from. Assembled separately rather than checked afterwards, because a fence chosen against
// half the material is a fence the other half can contain.
func materialText(c report.Context) string {
	var b strings.Builder
	for _, e := range c.Entities {
		b.WriteString(e)
	}
	for _, f := range c.Facts {
		b.WriteString(f.SubjectLabel())
		b.WriteString(f.Predicate)
		b.WriteString(f.ObjectLabel())
		b.WriteString(f.Statement)
		b.WriteString(f.Quote)
	}
	for _, r := range c.Children {
		b.WriteString(r.Title)
		b.WriteString(r.Summary)
		for _, f := range r.Findings {
			b.WriteString(f.Summary)
			b.WriteString(f.Explanation)
		}
	}
	return b.String()
}

// reportSystemPrompt composes the prompt in an order the file does not choose.
//
// data_boundary is last for the reason it is last in extraction: that section's job is to be the final
// word before somebody else's text arrives, and a file that could reorder the sections could put it in
// the middle, where an instruction after it appears to supersede it.
func reportSystemPrompt() string {
	return strings.Join([]string{
		reportPrompt.Task,
		reportPrompt.Grounding,
		reportPrompt.Shape,
		reportPrompt.ReplyShape,
		reportPrompt.DataBoundary,
	}, "\n\n")
}
