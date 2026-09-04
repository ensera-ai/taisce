// Package extract turns a message into claims that can be cited.
//
// The model is a port. What is in this package is everything that happens to a model's output before
// it is allowed to become a claim, and that is the part with a defensible correctness argument: a
// model asked for a quote will paraphrase it, normalise its whitespace, or invent it outright, and
// every one of those produces a citation that resolves to the wrong words while looking correct.
package extract

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/taisce/taisce/internal/domain"
)

// Proposal is what a model returns, before anything has been checked.
//
// It is deliberately not a domain.Claim. A Claim carries a byte span, and a span this package has
// not verified is the defect this package exists to prevent — so the model is not given a type it
// could put one in.
type Proposal struct {
	Subject     string
	Predicate   string
	Object      string
	Statement   string
	Confidence  float32
	SubjectType string
	ObjectType  string
	// Quote is what the model says it read. It is treated as a SEARCH TERM, never as evidence:
	// what is stored is the text found in the message, which is the only string a byte span can
	// honestly index.
	Quote string
}

// Model proposes claims for one message.
//
// The vocabulary is passed in rather than held by the implementation, because it is a per-tenant
// table and an implementation holding its own copy is a second definition of a closed set.
type Model interface {
	Propose(ctx context.Context, message domain.Message, vocabulary domain.Ontology) ([]Proposal, error)
}

// Result is everything one message produced.
type Result struct {
	Claims []domain.Claim
	// Rejected is not an error. Most messages contain no assertable claim at all, and a message
	// whose every proposal was rejected is the ordinary case rather than a failure.
	Rejected []domain.RejectedClaim
}

// Extractor applies the vocabulary and the span rule to a model's proposals.
type Extractor struct {
	model      Model
	vocabulary domain.Ontology
}

// New builds an extractor over a model and a tenant's vocabulary.
func New(model Model, vocabulary domain.Ontology) *Extractor {
	return &Extractor{model: model, vocabulary: vocabulary}
}

// Extract returns the claims one message supports.
//
// # The span is the whole point
//
// Every returned claim satisfies `message.Content[ByteStart:ByteEnd] == Quote`, and it satisfies it
// by construction rather than by the model's cooperation: the quote is located IN THE MESSAGE and
// the stored quote is the text that was found there. A model that paraphrased has produced a claim
// with no locatable quote, and that claim is not a weaker claim — it is an uncitable one, in a
// product whose argument is citation.
//
// # An empty result is not an error
//
// Most messages assert nothing. "Thanks, that worked" produces no claims, and a caller that has to
// distinguish that from a failure will end up treating failures as ordinary.
func (e *Extractor) Extract(ctx context.Context, message domain.Message) (Result, error) {
	proposals, err := e.model.Propose(ctx, message, e.vocabulary)
	if err != nil {
		return Result{}, err
	}

	var out Result
	for _, p := range proposals {
		predicate, admitted := e.vocabulary.Lookup(p.Predicate)
		if !admitted {
			out.Rejected = append(out.Rejected, reject(p, domain.ReasonUnmappedRelation, message.Ordinal))
			continue
		}

		start, end, found := Locate(message.Content, p.Quote)
		if !found {
			out.Rejected = append(out.Rejected, reject(p, domain.ReasonUnlocatableQuote, message.Ordinal))
			continue
		}

		objectType := p.ObjectType
		if objectType == "" {
			// The vocabulary says what the object end of this relation is. Using it means a
			// `scheduled_for` object is typed as a time even when the model volunteered nothing,
			// rather than defaulting to `thing` and losing the one piece of typing that was never
			// in doubt.
			objectType = predicate.ObjectKind
		}

		out.Claims = append(out.Claims, domain.Claim{
			Subject:   p.Subject,
			Predicate: p.Predicate,
			Object:    p.Object,
			Statement: p.Statement,
			// Reported by the model, and kept separate from salience, which is computed.
			Confidence: p.Confidence,
			// The text found in the message, NOT the text the model returned. They differ whenever
			// the model normalised whitespace, and storing the model's version would mean a stored
			// quote that its own span does not reproduce.
			Quote:         message.Content[start:end],
			ByteStart:     start,
			ByteEnd:       end,
			SourceOrdinal: message.Ordinal,
			SubjectType:   p.SubjectType,
			ObjectType:    objectType,
			SemanticType:  predicate.SemanticType,
			ValidFrom:     message.OccurredAt,
		})
	}
	return out, nil
}

func reject(p Proposal, reason string, ordinal int) domain.RejectedClaim {
	return domain.RejectedClaim{
		Predicate:     p.Predicate,
		Statement:     p.Statement,
		Quote:         p.Quote,
		Reason:        reason,
		SourceOrdinal: ordinal,
	}
}

// Locate finds a quote in a message and returns its byte span, or reports that it is not there.
//
// # Exact first, then whitespace-tolerant, and nothing else
//
// A model copying text out of a message routinely changes its whitespace: a line break inside a
// sentence comes back as a space, and indentation disappears. That difference carries no meaning —
// the words are the same words — so refusing it would discard correct citations for a formatting
// artefact.
//
// Nothing else is tolerated, and case in particular is not. A model that changed the case has
// retyped rather than copied, and once retyping is admitted there is no line left to draw: a
// tolerance wide enough to match "moved to dublin" against "moved to Dublin" is wide enough to
// match a sentence the model composed.
//
// # The span is mapped back to the ORIGINAL bytes
//
// Matching happens against a whitespace-normalised copy, but the span returned indexes the message
// as it was stored. A span into a normalised string that exists nowhere is precisely the failure
// this package exists to prevent, arrived at from a different direction.
//
// # The first occurrence wins
//
// A quote appearing twice in one message is the same words in both places, so a citation resolves to
// the same text either way; only the instance differs, and nothing reads the instance. Refusing
// repeats instead would discard a claim for being quoted about something mentioned twice.
func Locate(content, quote string) (start, end int, found bool) {
	if quote == "" {
		return 0, 0, false
	}
	if i := strings.Index(content, quote); i >= 0 {
		return i, i + len(quote), true
	}

	haystack := normalise(content)
	needle := normalise(quote)
	if needle.text == "" {
		// A quote of nothing but whitespace. There is no such evidence.
		return 0, 0, false
	}
	i := strings.Index(haystack.text, needle.text)
	if i < 0 {
		return 0, 0, false
	}
	return haystack.start[i], haystack.end[i+len(needle.text)-1], true
}

// normalised is a whitespace-collapsed copy of a string together with, for each of its bytes, where
// that byte came from in the original.
//
// Two parallel slices rather than one, because a match needs the START of its first byte and the END
// of its last, and for a multi-byte rune those are not one number apart.
type normalised struct {
	text  string
	start []int
	end   []int
}

// normalise collapses every run of whitespace to a single space and drops it at both ends.
//
// Runs collapse rather than disappear, because removing whitespace entirely would match "works at"
// against "worksat" and, worse, would join two words across a line break into one that appears in
// neither.
func normalise(s string) normalised {
	var b strings.Builder
	b.Grow(len(s))
	starts := make([]int, 0, len(s))
	ends := make([]int, 0, len(s))

	pendingSpace := false
	for i, r := range s {
		if unicode.IsSpace(r) {
			// Only after something has been written, so leading whitespace produces no space. A
			// run still pending when the string ends is never flushed, so trailing whitespace
			// produces none either.
			pendingSpace = b.Len() > 0
			continue
		}
		if pendingSpace {
			b.WriteByte(' ')
			// The collapsed space is attributed to the rune that follows it. Nothing reads a span
			// that begins or ends on it — a quote is normalised at both ends before matching — so
			// this only has to be inside the source, not exact.
			starts = append(starts, i)
			ends = append(ends, i)
			pendingSpace = false
		}
		before := b.Len()
		b.WriteRune(r)
		for j := before; j < b.Len(); j++ {
			starts = append(starts, i)
			ends = append(ends, i+utf8.RuneLen(r))
		}
	}
	return normalised{text: b.String(), start: starts, end: ends}
}
