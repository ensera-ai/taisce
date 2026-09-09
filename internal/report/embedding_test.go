// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package report_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/report"
)

func TestReportEmbeddingSurfaceIsBoundedAndDeterministic(t *testing.T) {
	findings, _ := json.Marshal([]report.Finding{{Summary: "Hiring", Explanation: "The team is expanding."}})
	first, err := report.EmbeddingSurface("Growth plan", "Several signals describe expansion.", "High operational impact.", findings)
	if err != nil {
		t.Fatal(err)
	}
	second, err := report.EmbeddingSurface(" Growth plan ", "Several signals describe expansion.", "High operational impact.", findings)
	if err != nil || first != second || !strings.Contains(first, "The team is expanding.") || len(first) > report.EmbeddingSurfaceBudget {
		t.Fatalf("unstable report surface: %q %v", second, err)
	}
}

func TestReportEmbeddingSurfaceRefusesInvalidOrOversizedRequiredText(t *testing.T) {
	for _, input := range []struct{ title, summary string }{
		{"", "summary"},
		{"title", " "},
		{"bad\x00title", "summary"},
		{strings.Repeat("x", report.EmbeddingSurfaceBudget), "summary"},
	} {
		if _, err := report.EmbeddingSurface(input.title, input.summary, "", []byte("[]")); err == nil {
			t.Fatalf("accepted invalid surface: %#v", input)
		}
	}
	if _, err := report.EmbeddingSurface("title", "summary", "", []byte("{")); err == nil {
		t.Fatal("accepted malformed findings")
	}
}

func TestReportEmbeddingSurfaceKeepsOptionalPartsWhole(t *testing.T) {
	findings, _ := json.Marshal([]report.Finding{
		{Summary: "small", Explanation: strings.Repeat("z", report.EmbeddingSurfaceBudget)},
		{Summary: "later", Explanation: "fits"},
	})
	got, err := report.EmbeddingSurface("title", "summary", "", findings)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, strings.Repeat("z", 32)) || !strings.Contains(got, "later\nfits") {
		t.Fatalf("optional findings were truncated or blocked later content: %q", got)
	}
}
