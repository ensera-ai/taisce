// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func ptr[T any](v T) *T { return &v }

// Every refusal the judge can make is watched being made, from a report that breaks each rule
// in turn: a memory message that does not parse, one with the wrong role, one unmarked, one
// missing its watermark, plan or facts, a turn that was fatal, a store error where none was
// expected, an observe body that does not parse, the wrong roles, the wrong contents, and a
// stored count that is not the expected one. A judge that can only pass is a checklist.
func TestTheJudgeNamesEveryBrokenRule(t *testing.T) {
	stored := int64(3)
	good, _ := RenderMemoryMessage(MemoryMessage{Watermark: Watermark{Stored: &stored}, Plan: []byte(`{"hops":1}`), Facts: []byte(`[{"statement":"x"}]`)})
	noWatermark, _ := RenderMemoryMessage(MemoryMessage{Plan: []byte(`{"hops":1}`), Facts: []byte(`[{"statement":"x"}]`)})
	noPlan, _ := RenderMemoryMessage(MemoryMessage{Watermark: Watermark{Stored: &stored}, Plan: []byte(`null`), Facts: []byte(`[{"statement":"x"}]`)})
	noFacts, _ := RenderMemoryMessage(MemoryMessage{Watermark: Watermark{Stored: &stored}, Plan: []byte(`{"hops":1}`), Facts: []byte(`[]`)})
	noSegments, _ := RenderMemoryMessage(MemoryMessage{Watermark: Watermark{Stored: &stored}, Plan: []byte(`{"truncated":false}`), Segments: []byte(`[]`), Turns: []byte(`[{"log_offset":1}]`)})
	noTurns, _ := RenderMemoryMessage(MemoryMessage{Watermark: Watermark{Stored: &stored}, Plan: []byte(`{"truncated":false}`), Segments: []byte(`[{"summary":"x"}]`), Turns: []byte(`[]`)})
	observe := func(body string) []Recorded {
		return []Recorded{{Method: http.MethodPost, Path: "/v1/observations", Body: []byte(body)}}
	}
	cases := []struct {
		name     string
		expect   Expect
		report   Report
		recorded []Recorded
		stored   int64
		want     string
	}{
		{"unparseable memory message", Expect{}, Report{ModelMessages: []Message{{Role: "user", Content: MemoryMessagePrefix + "\nnot json"}}}, nil, 0, "did not parse"},
		{"count", Expect{MemoryMessages: ptr(1)}, Report{}, nil, 0, "expected 1 memory message(s)"},
		{"role", Expect{MemoryRole: "user"}, Report{ModelMessages: []Message{{Role: "system", Content: good, Untrusted: true}}}, nil, 0, "never system"},
		{"unmarked", Expect{MemoryUntrusted: ptr(true)}, Report{ModelMessages: []Message{{Role: "user", Content: good}}}, nil, 0, "not marked untrusted"},
		{"no watermark", Expect{MemoryCarries: []string{"watermark"}}, Report{ModelMessages: []Message{{Role: "user", Content: noWatermark, Untrusted: true}}}, nil, 0, "no watermark"},
		{"no plan", Expect{MemoryCarries: []string{"plan"}}, Report{ModelMessages: []Message{{Role: "user", Content: noPlan, Untrusted: true}}}, nil, 0, "no plan"},
		{"no facts", Expect{MemoryCarries: []string{"facts"}}, Report{ModelMessages: []Message{{Role: "user", Content: noFacts, Untrusted: true}}}, nil, 0, "no facts"},
		{"no segments", Expect{MemoryCarries: []string{"segments"}}, Report{ModelMessages: []Message{{Role: "user", Content: noSegments, Untrusted: true}}}, nil, 0, "no segments"},
		{"no turns", Expect{MemoryCarries: []string{"turns"}}, Report{ModelMessages: []Message{{Role: "user", Content: noTurns, Untrusted: true}}}, nil, 0, "no turns"},
		{"input dropped", Expect{ModelCarries: []string{"a"}}, Report{ModelMessages: []Message{{Role: "user", Content: good, Untrusted: true}}}, nil, 0, "not handed"},
		{"history kept", Expect{ModelLacks: []string{"old"}}, Report{ModelMessages: []Message{{Role: "user", Content: "old"}, {Role: "user", Content: "a"}}}, nil, 0, "which the compaction replaced"},
		{"fatal", Expect{Fatal: ptr(false)}, Report{Fatal: true}, nil, 0, "fatal=true"},
		{"silent store failure", Expect{StoreError: ptr(true)}, Report{}, nil, 0, "surfaced nothing"},
		{"unexpected store error", Expect{StoreError: ptr(false)}, Report{StoreError: "boom"}, nil, 0, "where none was expected"},
		{"not observed", Expect{Observed: ptr(true)}, Report{}, nil, 0, "nothing was sent to observe"},
		{"observed after failure", Expect{Observed: ptr(false)}, Report{}, observe(`{}`), 0, "failed turn is not a memory"},
		{"roles with nothing observed", Expect{ObservedRoles: []string{"user"}}, Report{}, nil, 0, "nothing was sent to observe"},
		{"unparseable observe body", Expect{ObservedRoles: []string{"user"}}, Report{}, observe(`nope`), 0, "did not parse"},
		{"wrong roles", Expect{ObservedRoles: []string{"user", "assistant"}}, Report{}, observe(`{"messages":[{"role":"user","content":"a"},{"role":"tool","content":"b"}]}`), 0, "internal tool traffic"},
		{"wrong contents", Expect{ObservedContents: []string{"a"}}, Report{}, observe(`{"messages":[{"role":"user","content":"a"},{"role":"assistant","content":"summary"}]}`), 0, "never observed"},
		{"stored count", Expect{Stored: ptr(1)}, Report{}, nil, 2, "holds 2 more"},
	}
	for _, c := range cases {
		failures := judge(c.expect, c.report, c.recorded, c.stored)
		if len(failures) == 0 || !strings.Contains(strings.Join(failures, " "), c.want) {
			t.Errorf("%s: expected a failure containing %q, got %v", c.name, c.want, failures)
		}
	}
	// And the whole of a good turn passes with every expectation set.
	all := Expect{MemoryMessages: ptr(1), MemoryRole: "user", MemoryUntrusted: ptr(true), MemoryCarries: []string{"watermark", "plan", "facts"},
		ModelCarries: []string{"a"}, ModelLacks: []string{"old"},
		Fatal: ptr(false), StoreError: ptr(false), Observed: ptr(true), ObservedRoles: []string{"user", "assistant"}, ObservedContents: []string{"a", "b"}, Stored: ptr(1)}
	report := Report{ModelMessages: []Message{{Role: "user", Content: good, Untrusted: true}, {Role: "user", Content: "a"}}, Observed: true}
	if f := judge(all, report, observe(`{"messages":[{"role":"user","content":"a"},{"role":"assistant","content":"b"}]}`), 1); len(f) != 0 {
		t.Fatalf("a conformant turn must pass, got %v", f)
	}
}

// The runner's own refusals: a deployment address that is not a URL, a driver that cannot start,
// a driver that answers something that is not a report, an instruction the served side cannot
// parse, a served driver that fails, and a memory message whose parts are not JSON.
func TestTheRunnerAndTheProtocolRefuseWhatTheyCannotUse(t *testing.T) {
	if _, err := newProxy("://not a url"); err == nil {
		t.Fatal("a bad deployment address must be refused")
	}
	if _, err := Run(context.Background(), Deployment{API: "://not a url"}, &Reference{}, Suite{Cases: []Case{{Name: "x"}}}); err == nil {
		t.Fatal("a run against a bad address must be refused")
	}
	if _, err := (&Subprocess{}).Run(context.Background(), Instruction{}); err == nil {
		t.Fatal("a driver with no command must be refused")
	}
	missing := &Subprocess{Command: []string{"/no/such/driver"}}
	if _, err := missing.Run(context.Background(), Instruction{}); err == nil {
		t.Fatal("a driver that cannot start must be refused")
	}
	garbage := &Subprocess{Command: []string{"sh", "-c", "read line; echo not-a-report"}}
	if _, err := garbage.Run(context.Background(), Instruction{Case: "x"}); err == nil || !strings.Contains(err.Error(), "did not parse") {
		t.Fatalf("a driver answering garbage must be refused by name, got %v", err)
	}
	_ = garbage.Close()
	silent := &Subprocess{Command: []string{"sh", "-c", "exit 0"}}
	if _, err := silent.Run(context.Background(), Instruction{Case: "x"}); err == nil {
		t.Fatal("a driver that exits without answering must be refused")
	}
	_ = silent.Close()
	if err := Serve(context.Background(), strings.NewReader("not json\n"), os.Stderr, &Reference{}); err == nil || !strings.Contains(err.Error(), "did not parse") {
		t.Fatalf("an unparseable instruction must be refused by name, got %v", err)
	}
	failing := driverFunc(func(context.Context, Instruction) (Report, error) { return Report{}, errors.New("adapter exploded") })
	if err := Serve(context.Background(), strings.NewReader(`{"case":"x"}`+"\n\n"), os.Stderr, failing); err == nil || !strings.Contains(err.Error(), "exploded") {
		t.Fatalf("a failing driver must fail the serve loop, got %v", err)
	}
	if _, err := RenderMemoryMessage(MemoryMessage{Plan: json.RawMessage(`{not json`)}); err == nil {
		t.Fatal("a memory message with a part that is not JSON must not render")
	}
}

type driverFunc func(context.Context, Instruction) (Report, error)

func (f driverFunc) Run(ctx context.Context, in Instruction) (Report, error) { return f(ctx, in) }

// A deployment nobody answers at stops the run before any turn, and closing a driver that never
// started is nothing.
func TestARunAgainstNothingStopsBeforeAnyTurn(t *testing.T) {
	if err := (&Subprocess{}).Close(); err != nil {
		t.Fatal(err)
	}
	_, err := Run(context.Background(), Deployment{API: "http://127.0.0.1:1", Token: "x"}, &Reference{},
		Suite{MemoryMessagePrefix: MemoryMessagePrefix, Cases: []Case{{Name: "seedless", Turn: Turn{User: "hi"}}}})
	if err == nil || !strings.Contains(err.Error(), "could not run") {
		t.Fatalf("expected the run to stop by reason, got %v", err)
	}
}

// The runner's own reads are retried when the deployment's admission gate refuses them: a 429 on
// the freshness read or the seed is the deployment being busy, not the adapter breaking a rule.
// Bounded, and a context that ends first ends the retrying with the refusal in hand.
func TestTheRunnersOwnReadsRetryAnAdmissionRefusalAndStopWhenTheContextEnds(t *testing.T) {
	var freshness, seeds int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/freshness":
			freshness++
			if freshness <= 2 {
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			_, _ = w.Write([]byte(`{"stored":4}`))
		case "/v1/records/assert":
			seeds++
			body, _ := io.ReadAll(r.Body)
			if seeds == 1 || !strings.Contains(string(body), "works_at") {
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			_, _ = w.Write([]byte(`{}`))
		default:
			w.WriteHeader(http.StatusTooManyRequests)
		}
	}))
	defer server.Close()
	client := &apiClient{base: server.URL, token: "tsk_x", http: server.Client()}
	stored, err := client.stored(context.Background())
	if err != nil || stored != 5 || freshness != 3 {
		t.Fatalf("expected the third read to answer 5, got %d %v after %d reads", stored, err, freshness)
	}
	if err := client.seed(context.Background(), "s", []Seed{{Subject: "Marta", Predicate: "works_at", Object: "Ensera", Statement: "x"}}); err != nil || seeds != 2 {
		t.Fatalf("the seed must be retried with its body, got %v after %d attempts", err, seeds)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	if _, _, err := client.get(ctx, "/v1/recalls"); err == nil {
		t.Fatal("a refusal that outlasts the context ends with the context's error")
	}
}

// TestTheJudgeHoldsGroupsRetriesAndKeysToTheirRules watches the refusals the first judge test does not
// reach: how an adapter groups what it observes, and how it retries.
//
// These are the rules that most often pass by accident. An adapter that observes nothing trivially has
// no wrong group ordinals, and one that is never retried trivially reuses its key — so each rule is
// given a report that breaks it, and the ordinary case where ordinals are omitted is shown to pass,
// because an omitted ordinal is a standalone message and must not be read as a violation.
func TestTheJudgeHoldsGroupsRetriesAndKeysToTheirRules(t *testing.T) {
	observed := func(bodies ...string) []Recorded {
		var out []Recorded
		for _, b := range bodies {
			out = append(out, Recorded{Method: "POST", Path: "/v1/observations", Body: []byte(b)})
		}
		return out
	}
	for _, c := range []struct {
		name     string
		expect   Expect
		recorded []Recorded
		want     string
	}{
		{"groups with nothing observed", Expect{ObservedGroups: []int{0, 1}}, nil, "nothing was sent to observe"},
		{"groups from a body that does not parse", Expect{ObservedGroups: []int{0, 1}}, observed(`nope`), "did not parse"},
		{"two messages claimed as one unit", Expect{ObservedGroups: []int{0, 1}},
			observed(`{"messages":[{"role":"user","group_ordinal":0},{"role":"assistant","group_ordinal":0}]}`), "observed group ordinals"},
		{"too few observe calls", Expect{ObserveCalls: ptr(2)}, observed(`{}`), "called 1 time(s); expected 2"},
		{"keys from a body that does not parse", Expect{ObserveKeysEqual: ptr(true)}, observed(`nope`), "did not parse"},
		{"a retry under a new key", Expect{ObserveKeysEqual: ptr(true)},
			observed(`{"idempotency_key":"k1"}`, `{"idempotency_key":"k2"}`), "different idempotency keys"},
		{"distinct turns under one key", Expect{ObserveKeysEqual: ptr(false)},
			observed(`{"idempotency_key":"k1"}`, `{"idempotency_key":"k1"}`), "one idempotency key"},
	} {
		failures := judge(c.expect, Report{}, c.recorded, 0)
		if len(failures) == 0 || !strings.Contains(strings.Join(failures, " "), c.want) {
			t.Errorf("%s: expected a failure containing %q, got %v", c.name, c.want, failures)
		}
	}
	omitted := observed(`{"messages":[{"role":"user","content":"a"},{"role":"assistant","content":"b"}]}`)
	if f := judge(Expect{ObservedGroups: []int{0, 1}, ObserveCalls: ptr(1)}, Report{}, omitted, 0); len(f) != 0 {
		t.Fatalf("omitted ordinals are standalone messages and must pass, got %v", f)
	}
}
