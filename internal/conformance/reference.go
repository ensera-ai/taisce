// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Reference is the smallest adapter that keeps every rule, in this process, over the public
// contract. It exists for two reasons. It proves the suite can pass, so a failing adapter is
// failing the rules and not the runner. And its knobs let a test break each rule on purpose and
// watch the suite name it, which is what makes the suite a proof rather than a checklist: a
// suite nobody has seen fail has asserted nothing.
//
// It is not a product adapter. It has no framework seam and no filters beyond the defaults the
// rules require; an adapter in another language reproduces its behaviour, not its code.
type Reference struct {
	// Each knob, when set, breaks one rule. All false is conformant.
	InjectAsRole       string // inject memory as this role instead of user
	UnmarkedMemory     bool   // inject memory without the untrusted mark
	RecallFatal        bool   // raise a recall failure to the application
	SilentWriteFailure bool   // swallow a store failure
	StoreOnFailure     bool   // store the turn even when the model failed
	StoreToolTraffic   bool   // store the tool call and result as memory
	StoreSynthetic     bool   // store a framework-written message as memory
	StoreMemoryMessage bool   // store the injected memory message as a user message
	CompactLocally     bool   // replace the history with a summary written here instead of the deployment's context
	KeepHistory        bool   // hand the model the history beside the context that replaced it
	DropInput          bool   // hand the model the context and not the person's message
	ContextFatal       bool   // raise a context failure to the application
}

func (r *Reference) Run(ctx context.Context, in Instruction) (Report, error) {
	client := &apiClient{base: strings.TrimRight(in.API, "/"), token: in.Token, http: &http.Client{Timeout: 10 * time.Second}}
	var report Report

	// Provide: freshness and recall, both non-fatal. Nothing is injected when there is nothing
	// to say; an empty memory message spends the model's attention on nothing.
	memory, err := r.provide(ctx, client, in)
	if err != nil {
		if r.RecallFatal {
			report.Fatal = true
			return report, nil
		}
		memory = nil
	}
	var toModel []Message
	// The session's history is what the framework holds; a compaction, when it is due, replaces
	// it with the deployment's context and nothing else. Failing to fetch that context leaves the
	// history as it was: the model reads more, not less, and the turn goes on.
	history := in.Turn.History
	if in.Turn.Compact {
		if r.CompactLocally {
			toModel = append(toModel, Message{Role: "assistant", Content: "Summary of earlier conversation: " + summarise(history)})
			history = nil
		} else if content, err := r.compact(ctx, client, in); err != nil {
			if r.ContextFatal {
				report.Fatal = true
				return report, nil
			}
		} else {
			if !r.KeepHistory {
				history = nil
			}
			toModel = append(toModel, Message{Role: "user", Content: content, Untrusted: true})
		}
	}
	toModel = append(toModel, history...)
	toModel = append(toModel, in.Turn.Synthetic...)
	if memory != nil {
		role := "user"
		if r.InjectAsRole != "" {
			role = r.InjectAsRole
		}
		toModel = append(toModel, Message{Role: role, Content: *memory, Untrusted: !r.UnmarkedMemory})
	}
	if !(r.DropInput && in.Turn.Compact) {
		toModel = append(toModel, Message{Role: "user", Content: in.Turn.User})
	}
	for _, t := range in.Turn.ToolCalls {
		toModel = append(toModel, Message{Role: "assistant", Content: t.Call}, Message{Role: "tool", Content: t.Result})
	}
	report.ModelMessages = toModel

	// The stubbed model: a fixed reply, or the failure the case asked for.
	if in.Turn.ModelFailure {
		report.Fatal = true
		if !r.StoreOnFailure {
			return report, nil
		}
	}

	// Store: the person's message and the final reply, and nothing the framework wrote.
	stored, err := r.store(ctx, client, in, memory)
	report.Observed = true
	if err != nil {
		if !r.SilentWriteFailure {
			report.StoreError = err.Error()
		}
		return report, nil
	}
	_ = stored
	return report, nil
}

func (r *Reference) provide(ctx context.Context, client *apiClient, in Instruction) (*string, error) {
	status, raw, err := client.get(ctx, "/v1/freshness")
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("freshness %d", status)
	}
	var watermark Watermark
	if err := json.Unmarshal(raw, &watermark); err != nil {
		return nil, err
	}
	status, raw, err = client.post(ctx, "/v1/recalls", map[string]any{"data_subject_id": in.DataSubjectID, "question": in.Turn.User})
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("recall %d", status)
	}
	var bundle struct {
		Controls json.RawMessage `json:"controls"`
		Facts    json.RawMessage `json:"facts"`
		Reports  json.RawMessage `json:"reports"`
		Passages json.RawMessage `json:"passages"`
		Degraded json.RawMessage `json:"degraded"`
		Reach    json.RawMessage `json:"reach"`
	}
	if err := json.Unmarshal(raw, &bundle); err != nil {
		return nil, err
	}
	var facts []json.RawMessage
	_ = json.Unmarshal(bundle.Facts, &facts)
	if len(facts) == 0 {
		return nil, nil
	}
	plan, _ := json.Marshal(map[string]json.RawMessage{"controls": bundle.Controls, "degraded": bundle.Degraded, "reach": bundle.Reach})
	content, err := RenderMemoryMessage(MemoryMessage{Watermark: watermark, Plan: plan, Facts: bundle.Facts, Reports: bundle.Reports, Passages: bundle.Passages})
	if err != nil {
		return nil, err
	}
	return &content, nil
}

// compact asks the deployment for the subject's assembled history and renders it as the one
// memory message: the watermark, the assembly's cost as the plan, and its arrays unchanged.
func (r *Reference) compact(ctx context.Context, client *apiClient, in Instruction) (string, error) {
	status, raw, err := client.post(ctx, "/v1/contexts", map[string]any{"data_subject_id": in.DataSubjectID})
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("context %d", status)
	}
	var assembled struct {
		Watermark  Watermark       `json:"watermark"`
		Segments   json.RawMessage `json:"segments"`
		Turns      json.RawMessage `json:"turns"`
		Characters int             `json:"characters"`
		Truncated  bool            `json:"truncated"`
	}
	if err := json.Unmarshal(raw, &assembled); err != nil {
		return "", err
	}
	plan, _ := json.Marshal(map[string]any{"characters": assembled.Characters, "truncated": assembled.Truncated})
	return RenderMemoryMessage(MemoryMessage{Watermark: assembled.Watermark, Plan: plan, Segments: assembled.Segments, Turns: assembled.Turns})
}

// summarise is what a broken adapter does: prose of its own over the history.
func summarise(history []Message) string {
	var parts []string
	for _, m := range history {
		parts = append(parts, m.Role+" said "+m.Content)
	}
	return strings.Join(parts, "; ")
}

func (r *Reference) store(ctx context.Context, client *apiClient, in Instruction, memory *string) (bool, error) {
	type message struct {
		GroupOrdinal int    `json:"group_ordinal"`
		Role         string `json:"role"`
		Content      string `json:"content"`
	}
	var messages []message
	group := 0
	if r.StoreSynthetic {
		for _, s := range in.Turn.Synthetic {
			messages = append(messages, message{group, s.Role, s.Content})
			group++
		}
	}
	if r.StoreMemoryMessage && memory != nil {
		messages = append(messages, message{group, "user", *memory})
		group++
	}
	messages = append(messages, message{group, "user", in.Turn.User})
	group++
	if r.StoreToolTraffic {
		for _, t := range in.Turn.ToolCalls {
			messages = append(messages, message{group, "assistant", t.Call}, message{group, "tool", t.Result})
			group++
		}
	}
	if in.Turn.Assistant != "" {
		messages = append(messages, message{group, "assistant", in.Turn.Assistant})
	}
	status, raw, err := client.post(ctx, "/v1/observations", map[string]any{
		"idempotency_key": uuid.NewString(), "data_subject_id": in.DataSubjectID, "messages": messages,
	})
	if err != nil {
		return false, err
	}
	if status != http.StatusCreated {
		return false, errors.New("observe refused: " + string(raw))
	}
	return true, nil
}
