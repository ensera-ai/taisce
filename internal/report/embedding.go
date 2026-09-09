// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package report

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"
)

// EmbeddingSurfaceBudget keeps one aggregate report from consuming an unbounded provider request.
// The title and summary are mandatory; optional reasons and findings are included whole while space
// remains, in their persisted order.
const EmbeddingSurfaceBudget = 8000
const MaxEmbeddingSurfaceInputBytes = 1 << 20

var ErrInvalidEmbeddingSurface = errors.New("report embedding surface is invalid or exceeds its budget")

// EmbeddingSurface is the stable text used only by the community-report semantic index.
func EmbeddingSurface(title, summary, importanceReason string, findingsJSON []byte) (string, error) {
	if len(title)+len(summary)+len(importanceReason)+len(findingsJSON) > MaxEmbeddingSurfaceInputBytes ||
		!validEmbeddingText(title) || !validEmbeddingText(summary) || !validEmbeddingText(importanceReason) ||
		!utf8.Valid(findingsJSON) {
		return "", ErrInvalidEmbeddingSurface
	}
	title, summary, importanceReason = strings.TrimSpace(title), strings.TrimSpace(summary), strings.TrimSpace(importanceReason)
	if title == "" || summary == "" {
		return "", ErrInvalidEmbeddingSurface
	}
	base := title + "\n" + summary
	if len(base) > EmbeddingSurfaceBudget {
		return "", ErrInvalidEmbeddingSurface
	}
	var findings []Finding
	if err := json.Unmarshal(findingsJSON, &findings); err != nil {
		return "", ErrInvalidEmbeddingSurface
	}
	var b strings.Builder
	b.Grow(EmbeddingSurfaceBudget)
	b.WriteString(base)
	appendWhole := func(value string) {
		value = strings.TrimSpace(value)
		if value != "" && validEmbeddingText(value) && b.Len()+1+len(value) <= EmbeddingSurfaceBudget {
			b.WriteByte('\n')
			b.WriteString(value)
		}
	}
	appendWhole(importanceReason)
	for _, finding := range findings {
		appendWhole(finding.Summary)
		appendWhole(finding.Explanation)
	}
	return b.String(), nil
}

func validEmbeddingText(value string) bool {
	return utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}
