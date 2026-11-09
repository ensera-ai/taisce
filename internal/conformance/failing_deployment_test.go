// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package conformance

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// scripted is a deployment that answers each path with a fixed status and body, so the reference
// driver and the runner can be watched meeting each way a deployment fails: down, refusing, or
// answering something that is not the contract. The behaviour against a working deployment is held
// by the suite's own runs against a live one.
func scripted(t *testing.T, answers map[string][2]string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		answer, ok := answers[r.URL.Path]
		if !ok {
			answer = [2]string{"404", `{"error":{"code":"not_found"}}`}
		}
		status := map[string]int{"200": 200, "201": 201, "404": 404, "500": 500, "503": 503}[answer[0]]
		w.WriteHeader(status)
		_, _ = w.Write([]byte(answer[1]))
	}))
	t.Cleanup(server.Close)
	return server.URL
}

const fresh = `{"stored":3,"formed":3,"parked":0}`

// Whatever the deployment does to recall or to the context, the reference adapter runs the turn
// without memory and without raising it; whatever it does to the store, the failure is surfaced.
// That is the contract every adapter is held to, watched here against each failure in turn.
func TestTheReferenceAdapterSurvivesEveryRecallFailureAndSurfacesEveryStoreFailure(t *testing.T) {
	turn := Turn{User: "Where does Marta work?", Assistant: "At Ensera.", Compact: true,
		History: []Message{{Role: "user", Content: "earlier"}, {Role: "assistant", Content: "noted"}}}
	for name, answers := range map[string]map[string][2]string{
		"freshness refused": {"/v1/freshness": {"503", "{}"}, "/v1/observations": {"201", `{"id":"x"}`}},
		"freshness garbled": {"/v1/freshness": {"200", "not json"}, "/v1/observations": {"201", `{"id":"x"}`}},
		"recall refused":    {"/v1/freshness": {"200", fresh}, "/v1/recalls": {"500", "{}"}, "/v1/observations": {"201", `{"id":"x"}`}},
		"recall garbled":    {"/v1/freshness": {"200", fresh}, "/v1/recalls": {"200", "["}, "/v1/observations": {"201", `{"id":"x"}`}},
		"context garbled":   {"/v1/freshness": {"200", fresh}, "/v1/recalls": {"200", `{"facts":[]}`}, "/v1/contexts": {"200", "{"}, "/v1/observations": {"201", `{"id":"x"}`}},
		"context refused":   {"/v1/freshness": {"200", fresh}, "/v1/recalls": {"200", `{"facts":[]}`}, "/v1/contexts": {"500", "{}"}, "/v1/observations": {"201", `{"id":"x"}`}},
	} {
		report, err := (&Reference{}).Run(context.Background(), Instruction{Case: "c", API: scripted(t, answers), Token: "t", DataSubjectID: "s", Turn: turn})
		if err != nil || report.Fatal || !report.Observed || report.StoreError != "" {
			t.Errorf("%s: the turn did not go on without memory: %+v %v", name, report, err)
		}
		for _, m := range report.ModelMessages {
			if strings.HasPrefix(m.Content, MemoryMessagePrefix) {
				t.Errorf("%s: memory was injected from a failed read", name)
			}
		}
	}
	report, err := (&Reference{}).Run(context.Background(), Instruction{Case: "c", API: "http://127.0.0.1:1", Token: "t", DataSubjectID: "s", Turn: Turn{User: "hi", Assistant: "hello"}})
	if err != nil || report.Fatal || report.StoreError == "" {
		t.Errorf("an unreachable deployment: the store failure was not surfaced: %+v %v", report, err)
	}
}

// The runner reads the deployment itself, never through the proxy, and a deployment that refuses or
// garbles those reads stops the run with an error naming the case, rather than producing verdicts
// computed from nothing.
func TestTheRunnerStopsWhenItsOwnReadsOfTheDeploymentFail(t *testing.T) {
	seeded := Case{Name: "seeded", Seed: []Seed{{Subject: "Marta", Predicate: "works_at", Object: "Ensera", Statement: "Marta works at Ensera."}},
		Turn: Turn{User: "hi", Assistant: "hello"}}
	erased := Case{Name: "erased", Erase: true, Turn: Turn{User: "hi", Assistant: "hello"}, Expect: Expect{Erased: ptr(true)}}
	for name, c := range map[string]struct {
		answers map[string][2]string
		c       Case
		reason  string
	}{
		"seeding refused":   {map[string][2]string{"/v1/records/assert": {"500", "{}"}}, seeded, "seeding refused"},
		"freshness refused": {map[string][2]string{"/v1/freshness": {"503", "{}"}}, erased, "freshness answered 503"},
		"freshness garbled": {map[string][2]string{"/v1/freshness": {"200", "nope"}}, erased, "invalid"},
		"export refused":    {map[string][2]string{"/v1/freshness": {"200", fresh}, "/v1/observations": {"201", `{"id":"x"}`}, "/v1/erasures": {"200", `{"clean":true,"deleted":{"message":1}}`}, "/v1/exports": {"500", "{}"}}, erased, "export answered 500"},
		"export garbled":    {map[string][2]string{"/v1/freshness": {"200", fresh}, "/v1/observations": {"201", `{"id":"x"}`}, "/v1/erasures": {"200", `{"clean":true,"deleted":{"message":1}}`}, "/v1/exports": {"200", "{"}}, erased, "unexpected end"},
	} {
		_, err := Run(context.Background(), Deployment{API: scripted(t, c.answers), Token: "t"}, &Reference{}, Suite{Cases: []Case{c.c}})
		if err == nil || !strings.Contains(err.Error(), "could not run") || !strings.Contains(err.Error(), c.reason) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// A deployment that has stored nothing answers no watermark, and that is zero, not a failure.
	stored, err := (&apiClient{base: scripted(t, map[string][2]string{"/v1/freshness": {"200", `{"stored":null}`}}), token: "t", http: http.DefaultClient}).stored(context.Background())
	if err != nil || stored != 0 {
		t.Fatalf("an empty deployment read as %d, %v", stored, err)
	}
}
