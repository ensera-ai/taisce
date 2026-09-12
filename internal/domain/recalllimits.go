// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package domain

import "errors"

// Recall limits protect question expansion, database parameters and traversal seeds. They are
// admission ceilings, not relevance thresholds. A refused question produces no partial bundle.
const (
	MaxRecallQuestionBytes = 8192
	MaxRecallTerms         = 512
	MaxRecallTermBytes     = 4 * MaxRecallQuestionBytes
	MaxRecallMatches       = 256
)

var (
	ErrRecallQuestionSize = errors.New("question exceeds 8192 UTF-8 bytes")
	ErrRecallTerms        = errors.New("question exceeds the candidate-name limit; use a shorter question")
	ErrRecallMatches      = errors.New("question matches too many names; narrow the question or select a data subject")
)

// ValidateRecallTerms also protects direct store callers that bypass question expansion.
func ValidateRecallTerms(terms []string) error {
	if len(terms) > MaxRecallTerms {
		return ErrRecallTerms
	}
	bytes := 0
	for _, term := range terms {
		if len(term) > MaxRecallTermBytes-bytes {
			return ErrRecallTerms
		}
		bytes += len(term)
	}
	return nil
}
