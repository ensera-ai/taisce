// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

// The contract document, rendered from the surface it describes.
//
// # Why this is generated and still committed
//
// A hand-written contract is a second statement of the same thing, and the two diverge the first
// time a field is added by somebody who did not know the document existed — silently, because
// nothing fails. Generating it removes that.
//
// But generating it into a build directory would remove the thing worth having: the document is in
// git so a change to it shows up in a diff, and a breaking change to a contract external code
// depends on is something a reviewer has to see before it ships, not after somebody's build breaks.
// So the file is committed and a test re-renders it and fails on any difference, which makes
// "regenerate the contract" a step somebody takes deliberately.
//
// # What this package is allowed to decide here
//
// How the surface is described, and nothing about what it is. Every fact in the output comes from
// the operation table or from reflection over the request and response types. There is no prose
// about behaviour, because prose about behaviour is a claim nothing checks — that lives in the
// goals, the register, and the doc comments beside the code that holds it.
package api

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// Contract renders the public description of this surface.
//
// Deterministic: the same surface produces byte-identical output on every run and on every machine,
// because a generated file that reorders itself makes every diff unreadable and teaches reviewers to
// skim the one that matters. Operations come out in declaration order and types in field order,
// both of which are choices a person made and can see.
func Contract() string {
	var b strings.Builder

	fmt.Fprintf(&b, "# The %s contract\n\n", Version)
	b.WriteString(
		"Generated from the surface it describes. Do not edit: run `make contract`.\n\n" +
			"Every operation is reached over HTTP with a bearer credential, and the credential " +
			"decides which project the operation acts on — a request never names one.\n\n" +
			"The version is in the path. It is permanent from the day the first client ships: " +
			"a field is only ever added, and anything else is a new version.\n")

	b.WriteString("\n## Operations\n\n")
	b.WriteString("| Operation | Method | Path | On success |\n|---|---|---|---|\n")
	for _, o := range operations {
		fmt.Fprintf(&b, "| `%s` | %s | `/%s%s` | %d |\n", o.Name, o.Method, Version, o.Path, o.Success)
	}
	b.WriteString(
		"\nThe operation name is the same identifier the audit ledger records, so an entry in an " +
			"operator's ledger and a call in a client name the same thing.\n")

	for _, o := range operations {
		fmt.Fprintf(&b, "\n## `%s`\n\n%s `/%s%s` → %d\n\n", o.Name, o.Method, Version, o.Path, o.Success)
		if o.request == nil {
			b.WriteString("Takes no body.\n\n")
		} else {
			b.WriteString("### Request\n\n")
			writeShape(&b, o.request)
		}
		b.WriteString("### Response\n\n")
		writeShape(&b, o.response)
	}

	b.WriteString("\n## Refusals\n\n")
	b.WriteString(
		"A refusal is a JSON body of the shape `{\"error\": {\"code\": \"…\", \"message\": \"…\"}}`. " +
			"Branch on the code; the message is for a person reading a log and may change.\n\n" +
			"Any operation may answer with any of these.\n\n")
	b.WriteString("| Code |\n|---|\n")
	for _, c := range errorCodes {
		fmt.Fprintf(&b, "| `%s` |\n", c)
	}

	b.WriteString("\n## Liveness\n\n")
	b.WriteString("`GET /health` answers `200` with `{\"status\": \"ok\"}`. " +
		"It takes no credential and reports nothing about the instance.\n")

	return b.String()
}

// writeShape renders a struct's JSON shape: the field names on the wire, and their types.
//
// Names and types rather than a description of meaning. A widened response is as breaking as a
// narrowed one — a client that unmarshals strictly fails on a field nobody told it about — so what
// this has to capture is the exact set of names, and it is generated so it cannot fall behind.
func writeShape(b *strings.Builder, v any) {
	b.WriteString("| Field | Type |\n|---|---|\n")
	for _, f := range shapeOf(reflect.TypeOf(v)) {
		fmt.Fprintf(b, "| `%s` | %s |\n", f.name, f.kind)
	}
	b.WriteString("\n")
}

type field struct{ name, kind string }

// shapeOf walks a struct in field order and reports what a client sees.
//
// Embedded structs are flattened the way encoding/json flattens them, so the document says what is
// on the wire rather than how the Go types are arranged — a client cannot see the difference and
// should not be told about it.
func shapeOf(t reflect.Type) []field {
	var out []field
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if tag == "-" {
			continue
		}
		if tag == "" && f.Anonymous && f.Type.Kind() == reflect.Struct {
			out = append(out, shapeOf(f.Type)...)
			continue
		}
		name := tag
		if name == "" {
			name = f.Name
		}
		out = append(out, field{name: name, kind: wireType(f.Type)})
	}
	return out
}

// wireType names a Go type as a client sees it after JSON.
//
// A pointer is "optional" rather than "a pointer", because that is the only thing a pointer means
// once it has been through encoding/json: the field may be absent or null. Every nested object is
// expanded inline, so the document has no type names a reader has to look up somewhere else.
func wireType(t reflect.Type) string {
	// A raw message is whatever the projection holds. Saying more would be a claim the exporter
	// deliberately does not make: it returns raw rows so a column a migration added arrives without
	// anybody here remembering to list it.
	if t == reflect.TypeOf(json.RawMessage(nil)) {
		return "opaque JSON"
	}
	switch t.Kind() {
	case reflect.Pointer:
		return wireType(t.Elem()) + ", optional"
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 {
			return "string"
		}
		return "array of " + wireType(t.Elem())
	case reflect.Map:
		return "object of " + wireType(t.Elem())
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return "integer"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Struct:
		// time.Time is a struct and is not one on the wire.
		if t.PkgPath() == "time" && t.Name() == "Time" {
			return "timestamp (RFC 3339)"
		}
		parts := make([]string, 0, t.NumField())
		for _, f := range shapeOf(t) {
			parts = append(parts, f.name+": "+f.kind)
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	return t.Kind().String()
}

// ErrorCodes is the refusal vocabulary, for anything that has to enumerate it rather than remember
// it — the same reason Operations exists.
//
// Sorted, because it is used to answer "is this code one of ours" rather than to render anything;
// the contract document publishes them in declaration order, which is the order a person chose.
func ErrorCodes() []string {
	out := make([]string, 0, len(errorCodes))
	for _, c := range errorCodes {
		out = append(out, string(c))
	}
	sort.Strings(out)
	return out
}
