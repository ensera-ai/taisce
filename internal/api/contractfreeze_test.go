// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

// The freeze: what a client of version 1 can rely on, held to by a test rather than by review.
package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
)

const frozenPath = "testdata/contract-v1.json"

// Every operation, field and refusal code in the frozen snapshot is still served, with the same
// method, path, status and type. Additions pass: a new response field, a new request field, a new
// code, a new operation. A removal, a rename or a retyping fails, because a client written against
// the freeze would fail on it, and that is a new version rather than a change.
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
		out = append(out, heldKind(where+": field "+strconv.Quote(f.Name), f.Kind, kind)...)
	}
	return out
}

// heldKind compares one field's type, descending into an object rather than comparing its rendering.
//
// `wireType` expands a nested object inline, so a field ADDED inside one changes the enclosing
// field's rendered kind. Compared as strings that reads as a retyping — which is the one mistake the
// freeze must not make in this direction, because it refuses an addition the freeze explicitly
// permits, and a guard that refuses what it declares safe is one somebody works around: by
// contorting a design so a type stops being shared, or by re-freezing without reading the diff.
//
// So an object is compared member by member, at any depth. Everything that is not an object is
// compared exactly, and a removal or a retyping inside an object still fails — naming the path down
// to the field, so a reviewer is told where rather than handed two long shapes to diff by eye.
func heldKind(where, frozen, served string) []string {
	frozenBody, frozenSuffix, frozenObject := objectBody(frozen)
	servedBody, servedSuffix, servedObject := objectBody(served)
	if !frozenObject || !servedObject || frozenSuffix != servedSuffix {
		if frozen != served {
			return []string{fmt.Sprintf("%s was frozen as %s and is served as %s", where, frozen, served)}
		}
		return nil
	}
	return heldFields(where, membersOf(frozenBody), membersOf(servedBody))
}

// objectBody strips the braces from an object kind and returns whatever followed them.
//
// An optional object renders as `{...}, optional`, and optionality is part of the type: a field that
// stopped being optional is a retyping a client notices. So the suffix is returned rather than
// discarded, and the caller requires it to match before descending.
func objectBody(kind string) (body, suffix string, isObject bool) {
	if !strings.HasPrefix(kind, "{") {
		return "", "", false
	}
	depth := 0
	for i, r := range kind {
		switch r {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return kind[1:i], kind[i+1:], true
			}
		}
	}
	return "", "", false
}

// membersOf splits an object body into its fields.
//
// Two things make this more than splitting on commas. A nested object carries commas of its own, so
// the split is depth-aware. And a kind may itself contain a top-level comma — `integer, optional` is
// one type, not two — which no amount of depth tracking distinguishes.
//
// What distinguishes them is that a member always renders as `name: kind`. A segment with no `: ` is
// therefore the continuation of the member before it and is joined back onto its kind. Getting that
// wrong in the other direction would invent fields nobody serves and then report differences in them.
func membersOf(body string) []Field {
	var segments []string
	depth, start := 0, 0
	for i, r := range body {
		switch r {
		case '{':
			depth++
		case '}':
			depth--
		case ',':
			if depth == 0 {
				segments = append(segments, body[start:i])
				start = i + 1
			}
		}
	}
	segments = append(segments, body[start:])
	var out []Field
	for _, segment := range segments {
		segment = strings.TrimSpace(segment)
		if segment == "" {
			continue
		}
		name, kind, found := strings.Cut(segment, ": ")
		if !found {
			if len(out) > 0 {
				out[len(out)-1].Kind += ", " + segment
				continue
			}
			// Nothing to attach it to. Kept as its own entry rather than dropped, so an unparseable
			// shape is still compared instead of quietly freezing nothing.
			out = append(out, Field{Name: segment, Kind: segment})
			continue
		}
		out = append(out, Field{Name: name, Kind: kind})
	}
	return out
}

// ── The freeze descends into an object instead of comparing its rendering ─────────────────────
//
// `wireType` expands a nested object inline, so every change inside one shows up as a change to the
// enclosing field's rendered kind. Compared as text, an addition in there is indistinguishable from a
// retyping — and the freeze's rule is that the first passes and the second does not. This holds both
// halves, because getting the permissive half right by being permissive everywhere would be worse
// than the bug it replaced.
func TestTheFreezeReadsInsideAnObjectRatherThanComparingItsRendering(t *testing.T) {
	const nested = "{scope: string, stored: integer, optional, inner: {a: string, b: integer}}"
	for _, tc := range []struct {
		name, served string
		want         string
	}{
		{"a field added at the top of the object",
			"{scope: string, stored: integer, optional, inner: {a: string, b: integer}, extra: string}", ""},
		{"a field added inside a nested object",
			"{scope: string, stored: integer, optional, inner: {a: string, b: integer, c: boolean}}", ""},
		{"a field removed from the object",
			"{scope: string, inner: {a: string, b: integer}}", `"stored" was frozen and is gone`},
		{"a field removed from inside a nested object",
			"{scope: string, stored: integer, optional, inner: {a: string}}", `"b" was frozen and is gone`},
		{"a field retyped inside a nested object",
			"{scope: string, stored: integer, optional, inner: {a: string, b: string}}",
			`"b" was frozen as integer and is served as string`},
		{"a field that stopped being optional",
			"{scope: string, stored: integer, inner: {a: string, b: integer}}",
			`"stored" was frozen as integer, optional and is served as integer`},
		{"a nested object that became something else",
			"{scope: string, stored: integer, optional, inner: string}",
			`"inner" was frozen as {a: string, b: integer} and is served as string`},
		{"a nested object that became optional",
			"{scope: string, stored: integer, optional, inner: {a: string, b: integer}, optional}",
			`"inner" was frozen as {a: string, b: integer} and is served as {a: string, b: integer}, optional`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := heldKind("field", nested, tc.served)
			if tc.want == "" {
				if len(got) != 0 {
					t.Fatalf("an addition was refused: %v", got)
				}
				return
			}
			if len(got) != 1 || !strings.Contains(got[0], tc.want) {
				t.Fatalf("expected %q, got %v", tc.want, got)
			}
		})
	}

	// Two levels down, so the descent is not a single special case for one level of nesting.
	deep := heldKind("field", "{a: {b: {c: string, d: integer}}}", "{a: {b: {c: string}}}")
	if len(deep) != 1 || !strings.Contains(deep[0], `"d" was frozen and is gone`) {
		t.Fatalf("a removal two levels down was not caught: %v", deep)
	}
	// And the path says where, rather than handing a reviewer two long shapes to diff by eye.
	if !strings.Contains(deep[0], `"a"`) || !strings.Contains(deep[0], `"b"`) {
		t.Fatalf("the violation does not name the path to the field: %v", deep[0])
	}

	// A kind carrying its own top-level comma is one type, not two. Splitting it would invent a
	// field named "optional" and then report it missing.
	if v := heldKind("field", "{a: integer, optional}", "{a: integer, optional}"); len(v) != 0 {
		t.Fatalf("an unchanged optional member was reported as changed: %v", v)
	}
}

// FreezeJSON is what `make freeze-contract` commits, and its only caller is a script the build excludes
// (`//go:build ignore`), which is why nothing reached it. If it ever wrote something the freeze test
// could not read back, the next freeze would commit a file the release is then held to and cannot be
// checked against.
//
// So the property is the round trip: a freeze written today reads back through the shape the freeze
// test reads, today's surface keeps it, and two freezes of an unchanged surface are the same bytes —
// a diff between two freezes is then a list of what changed and never noise.
func TestAFreezeWrittenTodayIsOneTheCurrentSurfaceKeeps(t *testing.T) {
	first, err := FreezeJSON()
	if err != nil {
		t.Fatal(err)
	}
	second, err := FreezeJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("two freezes of one surface differ, so a diff between freezes would show noise")
	}
	if len(first) == 0 || first[len(first)-1] != '\n' {
		t.Fatal("a freeze does not end in a newline, so the committed file would carry a spurious diff")
	}
	if !bytes.Contains(first, []byte("\n  \"")) {
		t.Fatal("a freeze is not indented, so a diff between two of them would be one enormous line")
	}
	var parsed Frozen
	if err := json.Unmarshal(first, &parsed); err != nil {
		t.Fatalf("a freeze does not read back through the shape the freeze test reads: %v", err)
	}
	if violations := freezeViolations(parsed, Surface()); len(violations) != 0 {
		t.Fatalf("a freeze written now is broken by the surface now: %v", violations)
	}
	if len(parsed.Operations) != len(Surface().Operations) || len(parsed.Codes) != len(Surface().Codes) {
		t.Fatalf("the freeze holds %d operations and %d codes; the surface serves %d and %d",
			len(parsed.Operations), len(parsed.Codes), len(Surface().Operations), len(Surface().Codes))
	}
}
