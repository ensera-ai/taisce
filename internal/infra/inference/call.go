// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package inference

import (
	"errors"
	"fmt"
)

// What bounds a generation call, and what is known about one afterwards.
//
// # What this is allowed to decide, and what it must not
//
// It decides whether a reply that was cut off may be used, and reports what a call cost. It does not
// decide what a reply means: parsing, the vocabulary and every refusal stay with
// the caller that asked.

// Purpose is why a generation model was called. A closed set: each has a prompt file of its own and
// a count kept under its name.
type Purpose string

const (
	PurposeExtraction Purpose = "extraction"
	PurposeReport     Purpose = "report"
	PurposeCompaction Purpose = "compaction"
)

// Purposes is the set, for whatever has to enumerate it.
func Purposes() []Purpose {
	return []Purpose{PurposeExtraction, PurposeReport, PurposeCompaction}
}

// Call is what one generation call cost, as far as the provider said.
type Call struct {
	Purpose Purpose
	// Reported is whether the provider sent a usage block. Without one the two counts are not zero,
	// they are unknown, and a reader adding them up must be able to tell the difference.
	Reported         bool
	PromptTokens     int64
	CompletionTokens int64
	// Cut is whether the reply stopped because it reached the ceiling.
	Cut bool
}

// ErrReplyCut is returned when a reply reached a ceiling — the operator's, or the provider's own —
// and was cut off.
//
// An error rather than whatever could be parsed from the fragment. A cut reply is a JSON document
// that stops in the middle: sometimes that fails to parse, and sometimes the cut lands where the
// fragment is still well-formed and holds the first half of what the model meant to say. Using it
// would store half of a message's claims and mark the message formed, and nothing afterwards could
// tell that from a message that asserted only half as much.
var ErrReplyCut = errors.New("the model's reply reached its ceiling and was cut")

// MaxOutputTokensLimit is the largest ceiling an operator may set. It is a bound on a typing
// mistake, not a recommendation: no provider this was read against accepts more.
const MaxOutputTokensLimit = 1_000_000

// accept takes a decoded reply and returns its text, or refuses it.
//
// The one place the three callers share, so that what a cut reply is and how a call is counted are
// stated once. The count is reported before the refusal: a reply that was cut was still paid for,
// and a ceiling that hid its own cost would make raising it look free.
func (c Config) accept(purpose Purpose, reply chatResponse, ceiling int) (string, error) {
	call := Call{Purpose: purpose}
	if reply.Usage != nil {
		call.Reported = true
		call.PromptTokens = reply.Usage.PromptTokens
		call.CompletionTokens = reply.Usage.CompletionTokens
	}
	cut := len(reply.Choices) > 0 && reply.Choices[0].FinishReason == "length"
	call.Cut = cut
	if c.Metered != nil {
		c.Metered(call)
	}
	if len(reply.Choices) == 0 {
		return "", fmt.Errorf("the model returned no reply")
	}
	if cut {
		if ceiling > 0 {
			return "", fmt.Errorf("%s: %w at the %d tokens %s allows; nothing from it was used",
				purpose, ErrReplyCut, ceiling, EnvMaxOutputTokens)
		}
		return "", fmt.Errorf("%s: %w by the provider's own limit; nothing from it was used. "+
			"%s states a ceiling where the provider's default is too low", purpose, ErrReplyCut, EnvMaxOutputTokens)
	}
	return reply.Choices[0].Message.Content, nil
}
