// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package inference

import (
	"errors"
	"fmt"
	"math"
	"time"
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

// Purposes is the set of generation purposes, for whatever has to enumerate it.
func Purposes() []Purpose {
	return []Purpose{PurposeExtraction, PurposeReport, PurposeCompaction}
}

// PurposeEmbedding is the one model call that is not a generation: a vector for a batch of texts.
// It has no prompt, no reply to cut and no usage this counts, so it is not among Purposes. It does
// take time, on the read path among other places, so it is among TimedPurposes.
const PurposeEmbedding Purpose = "embedding"

// TimedPurposes is every purpose a model call is timed under: the generation purposes and embedding.
func TimedPurposes() []Purpose {
	return append(Purposes(), PurposeEmbedding)
}

// clock starts timing one model call and returns what stops it.
//
// Started where the request leaves and stopped when the function that made it returns, so the time
// is the provider's plus the network's, which is the thing an operator cannot see from a turn's
// total: a turn makes several calls, and a recall that waited on an embedding shows only as a slow
// recall.
//
// The stop is told whether the provider answered — a reply arrived with a success status and could
// be decoded. The two are kept apart because they are different populations: a refused connection
// fails in a millisecond and a timeout fails at the client's deadline, and either, averaged in with
// the answers, would describe a model that does not exist. A reply that answered and was then
// refused for what it said — cut at the ceiling, or unparseable as proposals — still answered; how
// often that happens is counted where the refusal is.
func (c Config) clock(purpose Purpose) func(answered *bool) {
	if c.Timed == nil {
		return func(*bool) {}
	}
	started := time.Now()
	return func(answered *bool) { c.Timed(purpose, *answered, time.Since(started)) }
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

// replyAllowance is how much a prompt lets the model write back: a base, and an amount for each
// byte of what the model was given to read.
//
// # Why it is relative to what was sent
//
// A ceiling exists because the text sent to a model is somebody else's, and "write ten thousand
// words" in a message is an instruction the model may follow at the operator's expense. A fixed
// number cannot be that ceiling. What a reply legitimately needs grows with what it was given: a
// message that asserts two hundred and forty things gets two hundred and forty proposals back, each
// quoting its words. Measured against one provider, that was 24,753 tokens for a message of seven
// kilobytes — and a fixed ceiling low enough to bound a hostile short message would have refused
// that reply whole, and the turn with it.
//
// So the allowance follows the input. A short message that asks for a long answer is held to a
// short one. A long message is allowed a long reply, and whoever sent it paid for every byte the
// model read on the way in: what they can make an operator spend is bounded by what they sent.
//
// # Why it lives in the prompt file
//
// How much a prompt writes back is a property of the prompt — a reworded task that asks for an
// explanation with each proposal writes more. The numbers are reviewed with the wording they
// belong to, and a prompt file that states none does not load.
//
// # What it does not suit
//
// A model that reasons before it answers spends this allowance on the reasoning. For such a model
// the operator states a ceiling of their own (EnvMaxOutputTokens), which replaces this one.
type replyAllowance struct {
	Base         int     `yaml:"base"`
	PerInputByte float64 `yaml:"per_input_byte"`
}

// stated refuses a prompt file that leaves a reply unbounded.
func (a replyAllowance) stated() error {
	if a.Base < 1 || a.PerInputByte <= 0 {
		return fmt.Errorf("model_contract.reply_tokens must state a base of at least 1 and a positive per_input_byte; " +
			"a prompt that states no ceiling leaves what a reply costs to the provider")
	}
	return nil
}

// ceilingFor is the ceiling stated on one request: the operator's when they set one, and otherwise
// what the prompt allows a reply to input of this size.
func (c Config) ceilingFor(allowance replyAllowance, inputBytes int) int {
	if c.MaxOutputTokens > 0 {
		return c.MaxOutputTokens
	}
	allowed := float64(allowance.Base) + math.Ceil(allowance.PerInputByte*float64(inputBytes))
	return int(math.Min(allowed, MaxOutputTokensLimit))
}

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
		switch {
		case call.Reported && call.CompletionTokens < int64(ceiling):
			// Stopped short of what this request allowed, so the limit that cut it is not ours.
			return "", fmt.Errorf("%s: %w after %d tokens, by the provider's own limit and below the %d this request allowed; nothing from it was used",
				purpose, ErrReplyCut, call.CompletionTokens, ceiling)
		case c.MaxOutputTokens > 0:
			return "", fmt.Errorf("%s: %w at the %d tokens %s allows; nothing from it was used",
				purpose, ErrReplyCut, ceiling, EnvMaxOutputTokens)
		default:
			return "", fmt.Errorf("%s: %w at the %d tokens this prompt allows a reply to what it was given; nothing from it was used. "+
				"A model that reasons before it answers needs more: %s states a ceiling in its place",
				purpose, ErrReplyCut, ceiling, EnvMaxOutputTokens)
		}
	}
	return reply.Choices[0].Message.Content, nil
}
