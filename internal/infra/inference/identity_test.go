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
