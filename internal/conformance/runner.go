// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package conformance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Driver is the adapter under test, however it is reached: in this process for the reference,
// over a subprocess for an adapter in another language.
type Driver interface {
	Run(ctx context.Context, instruction Instruction) (Report, error)
}

// Deployment is the live deployment the suite runs against, reached with a write-enabled
// credential for one project. Every case uses a data subject of its own, so cases isolate
// without the runner reaching into the database.
type Deployment struct {
	API   string
	Token string
}

// Result is one case's verdict with every failure named.
type Result struct {
	Case     string   `json:"case"`
	Rule     string   `json:"rule"`
	Passed   bool     `json:"passed"`
	Failures []string `json:"failures,omitempty"`
}

// Load reads a suite file.
func Load(path string) (Suite, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Suite{}, err
	}
	var s Suite
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&s); err != nil {
		return Suite{}, fmt.Errorf("%s: %w", path, err)
	}
	if s.MemoryMessagePrefix != MemoryMessagePrefix {
		return Suite{}, fmt.Errorf("%s: the suite's memory message prefix %q is not this runner's %q", path, s.MemoryMessagePrefix, MemoryMessagePrefix)
	}
	if len(s.Cases) == 0 {
		return Suite{}, fmt.Errorf("%s: no cases", path)
	}
	return s, nil
}

// Run executes every case against the deployment through the driver and returns a verdict per
// case. It returns an error only when the suite could not run at all; a failing case is a result.
func Run(ctx context.Context, deployment Deployment, driver Driver, suite Suite) ([]Result, error) {
	p, err := newProxy(deployment.API)
	if err != nil {
		return nil, err
	}
	defer p.Close()
	client := &apiClient{base: strings.TrimRight(deployment.API, "/"), token: deployment.Token, http: &http.Client{Timeout: 30 * time.Second}}
	var results []Result
	for _, c := range suite.Cases {
		r, err := runCase(ctx, client, p, driver, c)
		if err != nil {
			return results, fmt.Errorf("case %s could not run: %w", c.Name, err)
		}
		results = append(results, r)
	}
	return results, nil
}

func runCase(ctx context.Context, client *apiClient, p *proxy, driver Driver, c Case) (Result, error) {
	result := Result{Case: c.Name, Rule: c.Rule}
	subject := uuid.NewString()
	if err := client.seed(ctx, subject, c.Seed); err != nil {
		return result, err
	}
	before, err := client.stored(ctx)
	if err != nil {
		return result, err
	}
	p.begin(c.Faults, c.Context)
	// A repeated turn is the same instruction sent again, as a retry would be: the driver is not
	// told it is a repeat, because an application retrying is not told either.
	var report Report
	for i := 0; i < max(1, c.Turn.Repeat); i++ {
		if i > 0 && c.Turn.RepeatAfterMS > 0 {
			select {
			case <-time.After(time.Duration(c.Turn.RepeatAfterMS) * time.Millisecond):
			case <-ctx.Done():
				return result, ctx.Err()
			}
		}
		report, err = driver.Run(ctx, Instruction{Case: c.Name, API: p.URL(), Token: client.token, DataSubjectID: subject, Turn: c.Turn})
		if err != nil {
			return result, fmt.Errorf("driver: %w", err)
		}
	}
	recorded := p.recorded()
	after, err := client.stored(ctx)
	if err != nil {
		return result, err
	}
	result.Failures = judge(c.Expect, report, recorded, after-before)
	result.Passed = len(result.Failures) == 0
	return result, nil
}

// judge is the whole of what the suite asserts, in one place, so a reader can see every rule
// beside the field that holds it.
func judge(expect Expect, report Report, recorded []Recorded, stored int64) []string {
	var failures []string
	fail := func(format string, args ...any) { failures = append(failures, fmt.Sprintf(format, args...)) }

	var memory []Message
	var parsed []MemoryMessage
	for _, m := range report.ModelMessages {
		mm, err := ParseMemoryMessage(m.Content)
		if errors.Is(err, ErrNotAMemoryMessage) && !strings.HasPrefix(m.Content, MemoryMessagePrefix) {
			continue
		}
		if err != nil {
			fail("a memory message did not parse: %v", err)
			continue
		}
		memory = append(memory, m)
		parsed = append(parsed, mm)
	}
	if expect.MemoryMessages != nil && len(memory) != *expect.MemoryMessages {
		fail("expected %d memory message(s) handed to the model, got %d", *expect.MemoryMessages, len(memory))
	}
	for i, m := range memory {
		if expect.MemoryRole != "" && m.Role != expect.MemoryRole {
			fail("memory message %d has role %q; it must be %q, never system, never assistant", i, m.Role, expect.MemoryRole)
		}
		if expect.MemoryUntrusted != nil && m.Untrusted != *expect.MemoryUntrusted {
			fail("memory message %d is not marked untrusted", i)
		}
		for _, carries := range expect.MemoryCarries {
			switch carries {
			case "watermark":
				if parsed[i].Watermark.Stored == nil {
					fail("memory message %d carries no watermark", i)
				}
			case "plan":
				if len(parsed[i].Plan) == 0 || string(parsed[i].Plan) == "null" {
					fail("memory message %d carries no plan", i)
				}
			case "facts":
				var facts []json.RawMessage
				if json.Unmarshal(parsed[i].Facts, &facts) != nil || len(facts) == 0 {
					fail("memory message %d carries no facts although memory holds some", i)
				}
			case "segments":
				var segments []json.RawMessage
				if json.Unmarshal(parsed[i].Segments, &segments) != nil || len(segments) == 0 {
					fail("memory message %d carries no segments although the context holds some: a compaction is the deployment's answer handed over unchanged", i)
				}
			case "turns":
				var turns []json.RawMessage
				if json.Unmarshal(parsed[i].Turns, &turns) != nil || len(turns) == 0 {
					fail("memory message %d carries no turns although the context holds some", i)
				}
			}
		}
	}
	// What reached the model as its own message, and what must not have.
	var plain []string
	for _, m := range report.ModelMessages {
		if !strings.HasPrefix(m.Content, MemoryMessagePrefix) {
			plain = append(plain, m.Content)
		}
	}
	for _, text := range expect.ModelCarries {
		if !slices.Contains(plain, text) {
			fail("the model was not handed %q as a message of its own: a compaction keeps what the deployment did not replace", text)
		}
	}
	for _, text := range expect.ModelLacks {
		if slices.Contains(plain, text) {
			fail("the model was handed %q, which the compaction replaced: history the deployment already holds is not sent twice", text)
		}
	}
	if expect.Fatal != nil && report.Fatal != *expect.Fatal {
		fail("the turn was fatal=%v; expected %v", report.Fatal, *expect.Fatal)
	}
	if expect.StoreError != nil {
		if *expect.StoreError && report.StoreError == "" {
			fail("storing failed and the adapter surfaced nothing: a lost turn is invisible until a subject access request asks for it")
		}
		if !*expect.StoreError && report.StoreError != "" {
			fail("the adapter reported a store error where none was expected: %s", report.StoreError)
		}
	}
	var observes []Recorded
	for _, r := range recorded {
		if r.Method == http.MethodPost && strings.HasSuffix(r.Path, "/observations") {
			observes = append(observes, r)
		}
	}
	if expect.Observed != nil {
		if *expect.Observed && len(observes) == 0 {
			fail("nothing was sent to observe")
		}
		if !*expect.Observed && len(observes) > 0 {
			fail("observe was called %d time(s) after a failed turn: a failed turn is not a memory", len(observes))
		}
	}
	if len(expect.ObservedGroups) > 0 {
		if len(observes) == 0 {
			fail("nothing was sent to observe")
		} else {
			var body struct {
				Messages []struct {
					Group *int `json:"group_ordinal"`
				} `json:"messages"`
			}
			if err := json.Unmarshal(observes[len(observes)-1].Body, &body); err != nil {
				fail("the observe body did not parse: %v", err)
			} else {
				groups := make([]int, 0, len(body.Messages))
				for _, m := range body.Messages {
					// An omitted ordinal is a standalone message, which the deployment reads as its
					// own position; the suite compares what the adapter meant, not how it spelled it.
					if m.Group == nil {
						groups = append(groups, len(groups))
						continue
					}
					groups = append(groups, *m.Group)
				}
				if !slices.Equal(groups, expect.ObservedGroups) {
					fail("observed group ordinals %v; expected %v: messages that are not one atomic unit must not be observed as one", groups, expect.ObservedGroups)
				}
			}
		}
	}
	if len(expect.ObservedRoles) > 0 || len(expect.ObservedContents) > 0 {
		if len(observes) == 0 {
			fail("nothing was sent to observe")
		} else {
			var body struct {
				Messages []struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"messages"`
			}
			if err := json.Unmarshal(observes[len(observes)-1].Body, &body); err != nil {
				fail("the observe body did not parse: %v", err)
			}
			var roles, contents []string
			for _, m := range body.Messages {
				roles = append(roles, m.Role)
				contents = append(contents, m.Content)
			}
			if len(expect.ObservedRoles) > 0 && !slices.Equal(roles, expect.ObservedRoles) {
				fail("observed roles %v; expected %v: internal tool traffic must not become memory by default", roles, expect.ObservedRoles)
			}
			if len(expect.ObservedContents) > 0 && !slices.Equal(contents, expect.ObservedContents) {
				fail("observed %d message(s) %q; expected exactly %q: a synthetic or injected message is never observed as though a person said it", len(contents), contents, expect.ObservedContents)
			}
		}
	}
	if expect.ObserveCalls != nil && len(observes) != *expect.ObserveCalls {
		fail("observe was called %d time(s); expected %d: a retried turn is sent again, and sent with the same key", len(observes), *expect.ObserveCalls)
	}
	if expect.ObserveKeysEqual != nil {
		keys := map[string]bool{}
		for _, o := range observes {
			var body struct {
				Key string `json:"idempotency_key"`
			}
			if err := json.Unmarshal(o.Body, &body); err != nil {
				fail("the observe body did not parse: %v", err)
				continue
			}
			keys[body.Key] = true
		}
		if *expect.ObserveKeysEqual && len(keys) > 1 {
			fail("a retried turn was sent with %d different idempotency keys; a retry carries the key of the turn it retries, or the deployment stores the same turn twice", len(keys))
		}
		if !*expect.ObserveKeysEqual && len(keys) < len(observes) {
			fail("distinct turns were sent with one idempotency key; the deployment would keep only the first")
		}
	}
	if expect.Stored != nil && stored != int64(*expect.Stored) {
		fail("the deployment holds %d more observation(s) after the turn; expected %d", stored, *expect.Stored)
	}
	return failures
}

// apiClient is the runner's own view of the deployment, never through the proxy.
type apiClient struct {
	base  string
	token string
	http  *http.Client
}

// seed asserts the case's facts under its subject, dated in the past so recall as of now finds
// them, through the authored-assertion operation: curated evidence that replays with no model.
func (c *apiClient) seed(ctx context.Context, subject string, seeds []Seed) error {
	if len(seeds) == 0 {
		return nil
	}
	records := make([]map[string]any, 0, len(seeds))
	for _, s := range seeds {
		records = append(records, map[string]any{
			"idempotency_key": uuid.NewString(), "data_subject_id": subject,
			"subject": s.Subject, "predicate": s.Predicate, "object": s.Object, "statement": s.Statement,
			"valid_from": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
		})
	}
	status, raw, err := c.post(ctx, "/v1/records/assert", map[string]any{"records": records})
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("seeding refused (%d): %s", status, raw)
	}
	return nil
}

// stored reads how many observations the project holds, as the freshness watermark's stored
// offset plus one, or zero before the first; the difference across a turn is what the store
// rules are judged by, and it needs no database.
func (c *apiClient) stored(ctx context.Context) (int64, error) {
	status, raw, err := c.get(ctx, "/v1/freshness")
	if err != nil {
		return 0, err
	}
	if status != http.StatusOK {
		return 0, fmt.Errorf("freshness answered %d: %s", status, raw)
	}
	var f struct {
		Stored *int64 `json:"stored"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return 0, err
	}
	if f.Stored == nil {
		return 0, nil
	}
	return *f.Stored + 1, nil
}

func (c *apiClient) post(ctx context.Context, path string, body any) (int, []byte, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(encoded))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(req)
}

func (c *apiClient) get(ctx context.Context, path string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return 0, nil, err
	}
	return c.do(req)
}

// do sends one of the runner's own requests. A 429 here is the deployment's admission gate
// refusing the runner's bookkeeping, not the adapter under test, and the contract tells every
// client what to do with it: retry with backoff. Bounded, so a deployment that refuses everything
// still ends the run with the refusal in hand rather than a hang.
func (c *apiClient) do(req *http.Request) (int, []byte, error) {
	req.Header.Set("Authorization", "Bearer "+c.token)
	for attempt := 0; ; attempt++ {
		if attempt > 0 && req.GetBody != nil {
			body, err := req.GetBody()
			if err != nil {
				return 0, nil, err
			}
			req.Body = body
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return 0, nil, err
		}
		raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		_ = resp.Body.Close()
		if err != nil {
			return 0, nil, err
		}
		if resp.StatusCode != http.StatusTooManyRequests || attempt >= 6 {
			return resp.StatusCode, raw, nil
		}
		select {
		case <-req.Context().Done():
			return resp.StatusCode, raw, req.Context().Err()
		case <-time.After(time.Duration(250<<attempt) * time.Millisecond):
		}
	}
}
