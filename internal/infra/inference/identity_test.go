// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package inference

import "testing"

func TestExtractionIdentityChangesWithModelOriginPromptAndImplementationButNeverKeys(t *testing.T) {
	base := Config{Endpoint: "https://models.example/v1", Model: "model-a", APIKey: "secret-one"}
	want := NewModel(base).ExtractionIdentity()
	for _, config := range []Config{
		{Endpoint: base.Endpoint, Model: base.Model, APIKey: "secret-two"},
		{Endpoint: "https://user:password@models.example/secret-route?api_key=secret#secret", Model: base.Model},
		{Endpoint: base.Endpoint, Model: base.Model, EmbeddingModel: "other-embedding", EmbeddingAPIKey: "different"},
	} {
		if got := NewModel(config).ExtractionIdentity(); got != want {
			t.Fatal("identity retained credentials or unrelated settings")
		}
	}
	for _, config := range []Config{
		{Endpoint: base.Endpoint, Model: "model-b"},
		{Endpoint: "https://elsewhere.example/v1", Model: base.Model},
	} {
		if NewModel(config).ExtractionIdentity() == want {
			t.Fatal("changed inference configuration kept its stamp")
		}
	}
	for _, input := range []*[]byte{&extractionPromptFile, &extractionImplementation, &promptImplementation} {
		old := *input
		*input = append(append([]byte{}, old...), byte('x'))
		got := NewModel(base).ExtractionIdentity()
		*input = old
		if got == want {
			t.Fatal("changed compiled extraction rules kept their stamp")
		}
	}
	if got := NewModel(Config{Endpoint: "%invalid", Model: "a"}).ExtractionIdentity(); len(got) != 64 {
		t.Fatal("invalid endpoint produced an invalid identity")
	}
}

// A report's identity moves with the model, the origin, the prompt and the compiled code that
// composes and parses — and with nothing else. A credential must never be part of it: hashing one
// would turn provenance into retained secret-derived data.
func TestReportIdentityChangesWithModelOriginPromptAndImplementationButNeverKeys(t *testing.T) {
	base := Config{Endpoint: "https://models.example/v1", Model: "model-a", APIKey: "secret-one"}
	want := NewReporter(base).ReportIdentity()

	for _, config := range []Config{
		{Endpoint: base.Endpoint, Model: base.Model, APIKey: "secret-two"},
		{Endpoint: "https://user:password@models.example/secret-route?api_key=secret#secret", Model: base.Model},
		{Endpoint: base.Endpoint, Model: base.Model, EmbeddingModel: "other-embedding", EmbeddingAPIKey: "different"},
	} {
		if got := NewReporter(config).ReportIdentity(); got != want {
			t.Fatal("a report identity retained credentials or unrelated settings")
		}
	}
	for _, config := range []Config{
		{Endpoint: base.Endpoint, Model: "model-b"},
		{Endpoint: "https://elsewhere.example/v1", Model: base.Model},
	} {
		if NewReporter(config).ReportIdentity() == want {
			t.Fatal("a changed model or origin kept its stamp")
		}
	}
	// The whole point: edit the prompt at all and every report written under the old wording becomes
	// distinguishable from one written under the new.
	for _, input := range []*[]byte{&reportPromptFile, &reportImplementation, &promptImplementation} {
		old := *input
		*input = append(append([]byte{}, old...), byte('x'))
		got := NewReporter(base).ReportIdentity()
		*input = old
		if got == want {
			t.Fatal("a changed prompt or changed compiled rules kept their stamp")
		}
	}
	// And it is never the same as extraction's, so one cannot be mistaken for the other.
	if want == NewModel(base).ExtractionIdentity() {
		t.Fatal("a report identity and an extraction identity collided")
	}
	if got := NewReporter(Config{Endpoint: "%invalid", Model: "a"}).ReportIdentity(); len(got) != len(want) {
		t.Fatal("an invalid endpoint produced an invalid identity")
	}
}
