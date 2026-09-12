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

	"github.com/ensera-ai/taisce/internal/domain"
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
			"a field is only ever added, and anything else is a new version.\n\n" +
			"Version 1 is frozen. The freeze is `internal/api/testdata/contract-v1.json`: every " +
			"operation, its method, path and success status, every request and response field with its " +
			"type, and every refusal code listed there is served by every later build of this version. " +
			"What may change: a response field may be added, a request field may be added if a request " +
			"without it is still accepted, a refusal code may be added, and an operation may be added. " +
			"Anything else is `/v2`, served beside `/v1`, and `/v1` stops only by a decision recorded in " +
			"the register, never by a patch.\n")

	b.WriteString("\nRequests must contain one complete UTF-8 JSON object followed only by whitespace. " +
		"Duplicate keys (including case aliases), unknown fields, invalid Unicode escapes and " +
		"nesting beyond 128 levels are refused with HTTP 400 and `invalid_body`. " +
		"The 1 MiB limit includes all bytes of the body, including trailing whitespace.\n")
	fmt.Fprintf(&b, "\nRecall questions are limited to %d UTF-8 bytes, %d distinct candidate names "+
		"and %d matching entity/name pairs. Exceeding a limit returns HTTP 400 and `invalid_question`, "+
		"with no partial bundle. The match count includes the selected speaker. Fact-budget cuts "+
		"remain successful responses with `truncated: true`.\n", domain.MaxRecallQuestionBytes, domain.MaxRecallTerms, domain.MaxRecallMatches)
	fmt.Fprintf(&b, "\nObservations admit at most %d messages, %d UTF-8 content bytes per message and %d "+
		"content bytes per turn. Exceeding these limits returns HTTP 400 and `invalid_turn` before storage. "+
		"Busy authentication or project/credential capacity returns HTTP 429 and `rate_limited`, with "+
		"`Retry-After: 1`. Retry with backoff and the same observation idempotency key; a refusal has no "+
		"stored receipt. Request concurrency limits are per API process; durable unfinished-turn limits "+
		"are shared by the instance and also return `rate_limited`. Existing keyed receipts can be replayed "+
		"at backlog capacity. Health checks remain outside request admission.\n",
		domain.MaxObservationMessages, domain.MaxMessageBytes, domain.MaxObservationBytes)

	b.WriteString("\nRecall `max_characters` must be positive and no larger than the server's configured budget. " +
		"Omission uses that budget. The count is Unicode code points in returned fact subjects, predicates, " +
		"objects, statements, source-role labels, quotes, contexts, paths and path predicates. Identifiers, " +
		"anchors, dates and JSON syntax are excluded. A first fact that cannot fit returns no facts, zero " +
		"characters and `truncated: true`. This is a content allowance, not a serialized-response or token limit.\n")
	b.WriteString("\nRecall `source_roles` defaults to `[\"user\"]`. An explicit nonempty list may contain " +
		"`user`, `assistant`, `system` and `tool`, without duplicates. Unknown roles, empty lists and invalid " +
		"budgets return HTTP 400 and `invalid_recall_controls`. There is no `document` role; fetched document " +
		"text uses its stored message role. Source selection preserves project, subject, erasure and temporal " +
		"filters. Hops are clamped to 1–2. Every successful response reports effective limits, roles and hops " +
		"in `controls`, including empty bundles.\n")

	b.WriteString("\nSubjects are optional project-authorized identity bookkeeping. Register with a required UUID " +
		"`idempotency_key`, optional exact `external_reference` (at most 1024 UTF-8 bytes) and `label` " +
		"(at most 256). Registration returns a random stable UUID to use as `data_subject_id`. Get accepts " +
		"exactly one of `id` and `external_reference`; list defaults to 20 and permits 1–100 entries, " +
		"with `after` taken from `next.id`. Unknown, foreign, erased and inactive unused subjects return " +
		"`404 not_found`. Invalid fields return `400 invalid_subject`.\n")
	b.WriteString("\nSubject updates require `id` and `expected_version` and replace the current reference and label; " +
		"omitted/empty metadata clears it. References are exact and case-sensitive, unique within a project; " +
		"there is no automatic normalization or merging. An immediate identical update retry is replayed; " +
		"stale competing versions, conflicting references and changed/erased registration keys return " +
		"`409 subject_conflict`. Original registration retries return current metadata. Use the stable UUID " +
		"for memory operations; rotating metadata never rewrites attribution. Read-only keys can get/list " +
		"but cannot register/update. Export/erasure include the registry and live retry metadata.\n")
	b.WriteString("\nA subject's `inactive_after` is stamped from project retention at registration. A null value " +
		"means retain until erasure. Once due, a mapping is unavailable and eligible for physical expiry " +
		"only when no attributed observation remains, including artifact sources. Updates do not extend " +
		"this deadline. Expiry/erasure clears mapping and retry payload metadata but retains a retry-key " +
		"digest tombstone. New collection requires a new registration key and receives a new subject ID. " +
		"Existing application-managed opaque IDs remain valid; the registry does not authenticate a human " +
		"or grant subject-specific permissions. See docs/19-subject-registry.md.\n")
	b.WriteString("\nRecord retraction accepts `records`, an atomic array of 1–20 objects with `id` and " +
		"`expected_version`. Versions come from inventory, history or citation inspection. Invalid requests " +
		"return `400 invalid_record_mutation`; any stale or already-withdrawn record returns `409 record_conflict`. " +
		"Unknown, foreign and erased records return the same `404 not_found`. Batches exceeding 128 supporting " +
		"evidence rows return `413 record_mutation_limit`. Any refusal leaves the whole batch unchanged.\n")
	b.WriteString("\nRetraction closes knowledge at the server time while retaining valid time and exact citations. " +
		"Current recall excludes the record; earlier knowledge remains inspectable. The response supplies " +
		"new versions and credential-attributed retraction metadata. Source-backed instructions prevent the " +
		"same structural claim from reappearing when its source message is re-extracted. A new independent " +
		"observation can assert the claim again. Retraction is not source erasure. After an uncertain response, " +
		"inspect the record instead of retrying with a newly fetched version.\n")
	b.WriteString("\nRecord correction accepts the same atomic `records` batch and support limits, adding " +
		"`object` (1–1024 UTF-8 bytes), `statement` (1–16384 UTF-8 bytes) and optional `valid_from` per item. " +
		"Blank, NUL-containing or invalid UTF-8 text is refused. Correction requires current known/valid state; " +
		"it preserves subject, relation and attribution, withdraws the original and creates a separate authored " +
		"source and replacement record. Omitted valid-from uses server time. Each result returns the original " +
		"and replacement IDs, source observation ID and new version. The original's retraction metadata links " +
		"to the replacement while its source survives. Curated evidence reports `curated/v1` and `authored_by`; " +
		"its stored claim replays without model extraction. A correction is an accountable assertion, not a " +
		"truth verification or permission to edit historical transcripts.\n")
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

	b.WriteString("\n## Management operations\n\n")
	b.WriteString("The management surface is served by the `manage` role on its own listener and reached " +
		"with an operator credential, which opens no memory route; a project credential is refused at this door " +
		"with the same answer a stranger gets. A request names the project it acts on. Every operation is on " +
		"the ledger with the operator credential as its principal. Refusal counts carry no words; the words " +
		"are read under a project credential through the memory routes.\n\n")
	b.WriteString("| Operation | Method | Path | On success |\n|---|---|---|---|\n")
	for _, o := range managementOperations {
		fmt.Fprintf(&b, "| `%s` | %s | `%s%s` | %d |\n", o.Name, o.Method, ManagementPrefix, o.Path, o.Success)
	}
	for _, o := range managementOperations {
		fmt.Fprintf(&b, "\n## `%s`\n\n%s `%s%s` → %d\n\n", o.Name, o.Method, ManagementPrefix, o.Path, o.Success)
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
	b.WriteString("\n`GET /ready` takes no credential and returns `200` with `{\"status\": \"ready\"}` or " +
		"`503` with `{\"status\": \"unavailable\"}`. Dependency checks have a one-second total budget; " +
		"only one runs at a time and results are cached for one second. No error, count or configuration is returned. " +
		"Serving readiness checks memory and registry access. `TAISCE_REQUIRE_FORMATION=true` additionally requires " +
		"a worker heartbeat within 330 seconds; the default permits intentionally API-only operation. " +
		"This proves responsiveness, not inference quality or completion of every project. Worker loopback probes " +
		"check their own processing progress and memory database access.\n")

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

// Field is one wire field as a client sees it: its JSON name and the type wireType names.
type Field struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// OperationShape is one operation of the surface with the shapes it takes and returns, the form
// the freeze is taken in and compared against.
type OperationShape struct {
	Name    string `json:"name"`
	Method  string `json:"method"`
	Path    string `json:"path"`
	Success int    `json:"success"`
	// NoBody marks an operation that takes none, which is different from one taking an object
	// with no fields: a client that sends a body to the first is refused.
	NoBody   bool    `json:"no_body,omitempty"`
	Request  []Field `json:"request,omitempty"`
	Response []Field `json:"response"`
}

// Frozen is the whole surface in the form that is compared across time: what a client of the
// version can rely on. It carries no prose, because prose is not something a test can hold to.
type Frozen struct {
	Version    string           `json:"version"`
	Operations []OperationShape `json:"operations"`
	// Management is the operator surface, frozen under the same rule: reached with an
	// operator credential under its own prefix, and never by a project credential.
	Management []OperationShape `json:"management,omitempty"`
	Codes      []string         `json:"codes"`
}

// Surface reports the served surface in the frozen form, from the same operation table and the
// same reflection the document is rendered from, so the two cannot disagree.
func Surface() Frozen {
	out := Frozen{Version: Version}
	for _, o := range operations {
		shape := OperationShape{Name: o.Name, Method: o.Method, Path: "/" + Version + o.Path, Success: o.Success}
		if o.request == nil {
			shape.NoBody = true
		} else {
			shape.Request = exported(shapeOf(reflect.TypeOf(o.request)))
		}
		shape.Response = exported(shapeOf(reflect.TypeOf(o.response)))
		out.Operations = append(out.Operations, shape)
	}
	for _, o := range managementOperations {
		shape := OperationShape{Name: o.Name, Method: o.Method, Path: ManagementPrefix + o.Path, Success: o.Success}
		if o.request == nil {
			shape.NoBody = true
		} else {
			shape.Request = exported(shapeOf(reflect.TypeOf(o.request)))
		}
		shape.Response = exported(shapeOf(reflect.TypeOf(o.response)))
		out.Management = append(out.Management, shape)
	}
	out.Codes = ErrorCodes()
	return out
}

func exported(fields []field) []Field {
	out := make([]Field, 0, len(fields))
	for _, f := range fields {
		out = append(out, Field{Name: f.name, Kind: f.kind})
	}
	return out
}

// FreezeJSON renders the surface as the snapshot a freeze commits: indented and in declaration
// order, so a diff against an earlier freeze reads as a list of what changed.
func FreezeJSON() ([]byte, error) {
	body, err := json.MarshalIndent(Surface(), "", "  ")
	if err != nil {
		return nil, err
	}
	return append(body, '\n'), nil
}

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
