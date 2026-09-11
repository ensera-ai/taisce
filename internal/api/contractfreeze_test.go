// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

// The freeze: what a client of version 1 can rely on, held to by a test rather than by review.
package api

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

const frozenPath = "testdata/contract-v1.json"

// Every operation, field and refusal code in the frozen snapshot is still served, with the same
// method, path, status and type. Additions pass: a new response field, a new request field, a new
// code, a new operation. A removal, a rename or a retyping fails, because a client written against
// the freeze would fail on it, and that is a new version rather than a change (D126).
//
// The document test beside this one catches every change so a reviewer sees it; this one says
// which changes are not allowed at all. It is the difference between "this was noticed" and
// "this cannot ship", and the second is what a freeze means.
func TestTheFrozenV1ContractIsStillServed(t *testing.T) {
	raw, err := os.ReadFile(frozenPath)
	if err != nil {
		t.Fatalf("the frozen contract is not there: %v", err)
	}
	var frozen Frozen
	if err := json.Unmarshal(raw, &frozen); err != nil {
		t.Fatalf("the frozen contract does not parse: %v", err)
	}
	if len(frozen.Operations) == 0 || len(frozen.Codes) == 0 {
		t.Fatal("an empty freeze holds nothing")
	}
	for _, broken := range freezeViolations(frozen, Surface()) {
		t.Error(broken)
	}
}

// The freeze refuses what it must: a doctored snapshot with an operation this surface does not
// serve, a moved route, a retyped and a missing field, and a code nobody declares, is named
// violation by violation, while an addition on the served side is not one.
func TestTheFreezeRefusesRemovalsRenamesAndRetypings(t *testing.T) {
	served := Surface()
	frozen := Surface()
	frozen.Operations[0].Method = "PUT"
	frozen.Operations[1].Request = append(frozen.Operations[1].Request, Field{Name: "vanished", Kind: "string"})
	frozen.Operations[1].Response[0].Kind = "integer, optional"
	frozen.Operations = append(frozen.Operations, OperationShape{Name: "memory.forget", Method: "POST", Path: "/v1/forget", Success: 200})
	frozen.Codes = append(frozen.Codes, "not_a_code")
	// The served side may have grown: an extra field and code on it are not violations.
	served.Operations[2].Response = append(served.Operations[2].Response, Field{Name: "added_later", Kind: "string"})
	served.Codes = append(served.Codes, "added_code")
	got := freezeViolations(frozen, served)
	want := []string{"route or status", `"vanished" was frozen and is gone`, "was frozen as integer, optional and is served as string", "memory.forget was frozen and is no longer served", `"not_a_code" was frozen`}
	if len(got) != len(want) {
		t.Fatalf("expected %d violations, got %d: %v", len(want), len(got), got)
	}
	for i, w := range want {
		if !strings.Contains(got[i], w) {
			t.Fatalf("violation %d: expected %q in %q", i, w, got[i])
		}
	}
	if v := freezeViolations(Surface(), served); len(v) != 0 {
		t.Fatalf("additions on the served side must not be violations, got %v", v)
	}
}

// freezeViolations lists every promise of the freeze the served surface no longer keeps.
func freezeViolations(frozen, served Frozen) []string {
	var out []string
	if frozen.Version != served.Version {
		return []string{fmt.Sprintf("the freeze is of %s and this serves %s", frozen.Version, served.Version)}
	}
	byName := map[string]OperationShape{}
	for _, o := range served.Operations {
		byName[o.Name] = o
	}
	for _, want := range frozen.Operations {
		got, ok := byName[want.Name]
		if !ok {
			out = append(out, fmt.Sprintf("operation %s was frozen and is no longer served: that is a new version, not a change", want.Name))
			continue
		}
		if got.Method != want.Method || got.Path != want.Path || got.Success != want.Success || got.NoBody != want.NoBody {
			out = append(out, fmt.Sprintf("operation %s changed its route or status: frozen %s %s → %d, served %s %s → %d", want.Name,
				want.Method, want.Path, want.Success, got.Method, got.Path, got.Success))
		}
		out = append(out, heldFields(want.Name+" request", want.Request, got.Request)...)
		out = append(out, heldFields(want.Name+" response", want.Response, got.Response)...)
	}
	managed := map[string]OperationShape{}
	for _, o := range served.Management {
		managed[o.Name] = o
	}
	for _, want := range frozen.Management {
		got, ok := managed[want.Name]
		if !ok {
			out = append(out, fmt.Sprintf("management operation %s was frozen and is no longer served: that is a new version, not a change", want.Name))
			continue
		}
		if got.Method != want.Method || got.Path != want.Path || got.Success != want.Success || got.NoBody != want.NoBody {
			out = append(out, fmt.Sprintf("management operation %s changed its route or status: frozen %s %s → %d, served %s %s → %d", want.Name,
				want.Method, want.Path, want.Success, got.Method, got.Path, got.Success))
		}
		out = append(out, heldFields(want.Name+" request", want.Request, got.Request)...)
		out = append(out, heldFields(want.Name+" response", want.Response, got.Response)...)
	}
	codes := map[string]bool{}
	for _, c := range served.Codes {
		codes[c] = true
	}
	for _, c := range frozen.Codes {
		if !codes[c] {
			out = append(out, fmt.Sprintf("refusal code %q was frozen and is no longer declared: a client branching on it falls through", c))
		}
	}
	return out
}

// heldFields reports every frozen field not served under the same name with the same type. A
// field that is present and typed the same passes even when its position moved; JSON does not
// carry order, so a client cannot depend on it.
func heldFields(where string, frozen, served []Field) []string {
	kinds := map[string]string{}
	for _, f := range served {
		kinds[f.Name] = f.Kind
	}
	var out []string
	for _, f := range frozen {
		kind, ok := kinds[f.Name]
		if !ok {
			out = append(out, fmt.Sprintf("%s: field %q was frozen and is gone", where, f.Name))
			continue
		}
		if kind != f.Kind {
			out = append(out, fmt.Sprintf("%s: field %q was frozen as %s and is served as %s", where, f.Name, f.Kind, kind))
		}
	}
	return out
}
