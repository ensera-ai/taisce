// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package extract

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// Hash the compiled admission implementation as well as configuration. A parser/rule change must
// not depend on someone remembering to bump a decorative version constant.
//
//go:embed extract.go
var admissionImplementation []byte

// ExtractionIdentity is optional for in-process proposers whose model provenance is unknown.
// The shipped HTTP proposer implements it; an unidentified proposer retains the explicit legacy
// code label and cannot be used as evidence that two model configurations are the same.
type IdentifiedModel interface{ ExtractionIdentity() string }

// PipelineVersion identifies extraction configuration, not provider weights or a whole graph
// generation. Vocabulary order is significant because the model sees it in that order; refusal
// terms are a set, so insertion order must not make two equivalent configurations disagree.
func (e *Extractor) PipelineVersion() string {
	m, ok := e.model.(IdentifiedModel)
	if !ok || m.ExtractionIdentity() == "" {
		return "extract/v1"
	}
	terms := make([]string, 0, len(e.unresolvable))
	for term := range e.unresolvable {
		terms = append(terms, term)
	}
	sort.Strings(terms)
	rules := sha256.Sum256(admissionImplementation)
	encoded, _ := json.Marshal(struct {
		Model      string
		Rules      [32]byte
		Vocabulary any
		Refused    []string
	}{m.ExtractionIdentity(), rules, e.vocabulary.All(), terms})
	digest := sha256.Sum256(encoded)
	return "extract/v2:" + hex.EncodeToString(digest[:])
}
