// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package inference

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"net/url"
)

// Include request fencing, response parsing and prompt composition in the configuration identity.
// These are compiled sources, never paths read from the operator's filesystem at runtime.
//
//go:embed extractor.go
var extractionImplementation []byte

//go:embed prompt.go
var promptImplementation []byte

//go:embed reporter.go
var reportImplementation []byte

// ExtractionIdentity deliberately omits keys and URL userinfo/query/path. A path may contain a
// credential; even hashing it would turn provenance into retained secret-derived data. Model
// aliases and routing changes behind the same origin are not immutable weight identities.
func (m *Model) ExtractionIdentity() string {
	origin := ""
	if u, err := url.Parse(m.config.Endpoint); err == nil {
		origin = u.Scheme + "://" + u.Host
	}
	implementation := sha256.Sum256(extractionImplementation)
	composition := sha256.Sum256(promptImplementation)
	prompt := sha256.Sum256(extractionPromptFile)
	encoded, _ := json.Marshal(struct {
		Origin, Model                       string
		Implementation, Composition, Prompt [32]byte
	}{origin, m.config.Model, implementation, composition, prompt})
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

// ReportIdentity identifies what wrote a community report, on the same terms extraction is
// identified on: the origin, the model, the compiled code that composes the request and parses the
// reply, and the prompt file itself.
//
// # Why the prompt is hashed rather than versioned
//
// A version field in the YAML is a number somebody has to remember to change, and the edit that
// matters most — a small change of wording, made while tuning — is exactly the one where they will
// not. Hashing the compiled file makes the identity maintain itself: change the prompt at all and
// every report written under the old wording becomes distinguishable from one written under the new,
// which is what rule 14 requires of a prompt edit.
func (r *Reporter) ReportIdentity() string {
	origin := ""
	if u, err := url.Parse(r.config.Endpoint); err == nil {
		origin = u.Scheme + "://" + u.Host
	}
	implementation := sha256.Sum256(reportImplementation)
	composition := sha256.Sum256(promptImplementation)
	prompt := sha256.Sum256(reportPromptFile)
	encoded, _ := json.Marshal(struct {
		Origin, Model                       string
		Implementation, Composition, Prompt [32]byte
	}{origin, r.config.Model, implementation, composition, prompt})
	digest := sha256.Sum256(encoded)
	return "report/v1:" + hex.EncodeToString(digest[:])
}
