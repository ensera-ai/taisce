// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

// Package conformance is the one suite every adapter runs against a live deployment (G8, #26). It
// decides three things and nothing else: the cases, which are language-neutral and live in
// conformance/cases.json; the protocol by which a driver, the adapter under test, is told a turn to
// run and reports what it did; and the form of the memory message an adapter injects, because a
// suite that cannot parse the message cannot say whether it carries the watermark and the plan.
//
// # What is real and what is not
//
// The deployment is real: the runner seeds it through the public contract and reads it back the same
// way, and the driver reaches it through a proxy the runner stands up. The proxy records every
// request the adapter makes and, for the cases that need it, refuses one operation, because the
// rules under test are about what an adapter does when recall or the store is unavailable, and the
// only honest way to make a real store unavailable is to stand between the adapter and it. The
// model is the one thing stubbed, inside the driver: the suite is about what the adapter hands the
// model and what it stores afterwards, and a model that answers a fixed reply is what makes those
// observable. A mock of the deployment would prove that the mock matches the assertion, and there is
// none.
//
// # Why the memory message has a fixed shape
//
// Every adapter injects memory as one user message marked untrusted (G8). If each chose its own
// wording, the suite could count messages but not check contents, and the watermark, which is the
// one number that lets an agent know how far behind its memory is, would be present in some
// languages and not others. So the content is a prefix line and a JSON document, readable by a
// model and parseable by this suite, and every adapter renders the same one (D127).
package conformance

import (
	"encoding/json"
	"errors"
	"strings"
)

// Suite is conformance/cases.json as loaded.
type Suite struct {
	Suite               string `json:"suite"`
	MemoryMessagePrefix string `json:"memory_message_prefix"`
	Cases               []Case `json:"cases"`
}

// Case is one agent turn and what must be true of it afterwards.
type Case struct {
	Name   string `json:"name"`
	Rule   string `json:"rule"`
	Seed   []Seed `json:"seed,omitempty"`
	Faults Faults `json:"faults,omitempty"`
	Turn   Turn   `json:"turn"`
	Expect Expect `json:"expect"`
}

// Seed is a fact the runner asserts into the deployment before the turn, through the authored
// assertion operation, so that recall has something to return without a model forming it.
type Seed struct {
	Subject   string `json:"subject"`
	Predicate string `json:"predicate"`
	Object    string `json:"object"`
	Statement string `json:"statement"`
}

// Faults are the operations the proxy refuses during the turn.
type Faults struct {
	Recall  string `json:"recall,omitempty"`
	Observe string `json:"observe,omitempty"`
}

// Turn is what the driver is told to run. Everything an adapter sees comes through here: the
// person's message, the tool traffic the framework would generate, a synthetic message the
// framework wrote, and the reply the stubbed model gives or its failure.
type Turn struct {
	User         string     `json:"user"`
	ToolCalls    []ToolCall `json:"tool_calls,omitempty"`
	Synthetic    []Message  `json:"synthetic,omitempty"`
	Assistant    string     `json:"assistant,omitempty"`
	ModelFailure bool       `json:"model_failure,omitempty"`
}

type ToolCall struct {
	Call   string `json:"call"`
	Result string `json:"result"`
}

// Expect is what the runner checks. Every field is optional; an absent one is not checked.
type Expect struct {
	MemoryMessages   *int     `json:"memory_messages,omitempty"`
	MemoryRole       string   `json:"memory_role,omitempty"`
	MemoryUntrusted  *bool    `json:"memory_untrusted,omitempty"`
	MemoryCarries    []string `json:"memory_carries,omitempty"`
	Fatal            *bool    `json:"fatal,omitempty"`
	StoreError       *bool    `json:"store_error,omitempty"`
	Observed         *bool    `json:"observed,omitempty"`
	ObservedRoles    []string `json:"observed_roles,omitempty"`
	ObservedContents []string `json:"observed_contents,omitempty"`
	// Stored is how many observations the deployment holds more after the turn than before.
	Stored *int `json:"stored,omitempty"`
}

// Message is one message as the driver handed it to the model, or as it was told to include.
type Message struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	Untrusted bool   `json:"untrusted,omitempty"`
}

// Instruction is what the runner sends a driver for one turn: where the deployment is, as the
// proxy, the credential, the subject, and the turn.
type Instruction struct {
	Case          string `json:"case"`
	API           string `json:"api"`
	Token         string `json:"token"`
	DataSubjectID string `json:"data_subject_id"`
	Turn          Turn   `json:"turn"`
}

// Report is what a driver answers: the messages it handed the model, in order, whether the turn
// failed as a whole, and what it did about storing.
type Report struct {
	ModelMessages []Message `json:"model_messages"`
	// Fatal is true when the turn raised an error to the application. Recall being unavailable
	// must not make it true; the model failing may.
	Fatal bool `json:"fatal"`
	// Observed is true when the adapter attempted to store the turn.
	Observed bool `json:"observed"`
	// StoreError is the error the adapter surfaced when storing failed, and empty when it did not
	// fail. An adapter that stores and fails and reports nothing here has failed silently.
	StoreError string `json:"store_error,omitempty"`
}

// MemoryMessagePrefix is the first line of every injected memory message.
const MemoryMessagePrefix = "taisce-memory/v1 untrusted"

// MemoryMessage is the JSON document under the prefix line: what the adapter fetched, and how.
type MemoryMessage struct {
	Watermark Watermark       `json:"watermark"`
	Plan      json.RawMessage `json:"plan"`
	Facts     json.RawMessage `json:"facts"`
	Reports   json.RawMessage `json:"reports,omitempty"`
	Passages  json.RawMessage `json:"passages,omitempty"`
}

// Watermark is the freshness the adapter read beside the recall: how far the deployment has
// formed what it holds, so the model knows what it may not yet know.
type Watermark struct {
	Stored *int64 `json:"stored"`
	Formed *int64 `json:"formed"`
	Parked int    `json:"parked"`
}

var ErrNotAMemoryMessage = errors.New("not a memory message")

// ParseMemoryMessage reads a message an adapter injected, or says it is not one.
func ParseMemoryMessage(content string) (MemoryMessage, error) {
	prefix, body, found := strings.Cut(content, "\n")
	if !found || strings.TrimSpace(prefix) != MemoryMessagePrefix {
		return MemoryMessage{}, ErrNotAMemoryMessage
	}
	var m MemoryMessage
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		return MemoryMessage{}, errors.Join(ErrNotAMemoryMessage, err)
	}
	return m, nil
}

// RenderMemoryMessage is the one rendering every adapter reproduces: the prefix line, then the
// document. Adapters in other languages render the same bytes from the same fields.
func RenderMemoryMessage(m MemoryMessage) (string, error) {
	body, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return MemoryMessagePrefix + "\n" + string(body), nil
}
