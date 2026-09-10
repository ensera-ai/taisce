// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

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

	"github.com/ensera-ai/taisce/internal/domain"
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
	// Polarity is what the message does with the relation: states it, denies it, hedges it, supposes
	// it, or reports somebody else saying it. Only the first becomes a fact.
	//
	// An empty value is not "asserted". It means the model did not answer, and a claim nobody has
	// decided the polarity of is refused — see the reasoning at the refusal site.
	Polarity domain.Polarity
	// Tense is when the message puts the relation: now, over, or not yet. Only the first becomes a
	// fact, and an empty value is refused for the same reason an empty polarity is.
	//
	// Separate from polarity because they are independent and both have to be right: "I used to live
	// in Amman" is asserted, positively, about a period that is over.
	Tense domain.Tense
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
	// unresolvable are terms that cannot be a subject, from the table that holds them. Empty is
	// valid and means the check does nothing — a deployment that has not seeded the table gets the
	// behaviour it had before, rather than a refusal it cannot explain.
	unresolvable map[string]struct{}
}

// New builds an extractor over a model and a tenant's vocabulary.
func New(model Model, vocabulary domain.Ontology) *Extractor {
	return &Extractor{model: model, vocabulary: vocabulary}
}

// NewWith builds an extractor over both closed sets: the relations it may admit, and the terms that
// cannot be a subject.
func NewWith(model Model, v domain.Vocabulary) *Extractor {
	return &Extractor{model: model, vocabulary: v.Ontology, unresolvable: v.Unresolvable}
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
	// What this message has already produced. A model asked about one message can report the same
	// relation twice — once from each sentence that states it, or simply twice — and each copy would
	// become its own fact, its own evidence row and its own place in a bundle that is cut in rows.
	//
	// The key is what identity is made of downstream: the relation, and the two ends normalised the
	// way entity resolution normalises them. Two claims differing only in the casing of a name
	// resolve to one entity pair, so they are one fact whether or not this notices.
	//
	// Scoped to one message, deliberately. The same relation asserted in two different messages is
	// two people, or one person twice, saying a thing — which is corroboration rather than
	// repetition, and reconciling it is supersession's problem, not extraction's.
	seen := make(map[string]struct{}, len(proposals))

	for _, p := range proposals {
		predicate, admitted := e.vocabulary.Lookup(p.Predicate)
		if !admitted {
			out.Rejected = append(out.Rejected, reject(p, domain.ReasonUnmappedRelation, message.Ordinal))
			continue
		}

		// A subject that names nothing the message established.
		//
		// Before the quote is located, because a pronoun subject is wrong regardless of whether its
		// quote can be found — and reporting it as uncitable would send whoever reads the refusals
		// looking at the model's quoting when the problem is that there is nothing to be about.
		if _, blank := e.unresolvable[domain.NormalizeName(p.Subject)]; blank {
			out.Rejected = append(out.Rejected, reject(p, domain.ReasonUnresolvableSubject, message.Ordinal))
			continue
		}

		start, end, found := Locate(message.Content, p.Quote)
		if !found {
			out.Rejected = append(out.Rejected, reject(p, domain.ReasonUnlocatableQuote, message.Ordinal))
			continue
		}

		// Before the duplicate check, because a claim the message never asserted is not a repeat of
		// anything — recording it as a duplicate would put the wrong reason on the row and make the
		// counts that drive different responses agree when they should not.
		//
		// # Why an empty polarity is refused rather than assumed
		//
		// Rule 8: every default is chosen for what happens when something upstream forgets. The
		// thing upstream here is a model, and the forgetting is routine — a field omitted, a value
		// spelled differently, a reply shaped by a provider that ignores the request. Treating
		// absence as "asserted" would mean every one of those writes a fact nobody vouched for, on
		// the path where a false fact is indistinguishable from a true one.
		//
		// So the allowlist is one value long. Anything else, including nothing, is written down and
		// not admitted.
		if !p.Polarity.Asserts() {
			out.Rejected = append(out.Rejected, reject(p, domain.ReasonNotAsserted, message.Ordinal))
			continue
		}

		// And then when the message puts it. A past tense passes every check above: the relation is
		// admitted, the subject is real, the quote is verbatim, and the polarity is asserted —
		// because "I used to live in Amman" does assert the relation. What it also says is that the
		// relation is over, and a fact written from it is the answer to "where do they live" with a
		// verbatim quote behind it.
		//
		// Refused rather than stored with an interval, because only half the interval is knowable:
		// the sentence says it ended at or before this message and says nothing about when it began.
		// Every read is a containment test, so an unbounded start answers a question about 1990 with
		// yes, and a bounded one is a date this system invented.
		//
		// After polarity, so a denied past tense is recorded as the denial it is. The counts drive
		// different responses — a rising not_asserted is a model reading modality badly, a rising
		// not_current is a corpus about the past — and a refusal filed under the wrong reason makes
		// them agree when they should not.
		//
		// Except for an event. A relation the vocabulary marks as an event is reported in the past
		// tense because that is when events are reported, and it stays true once it has happened:
		// "voters adopted an amendment" is complete and remains a fact. The validity start is when
		// it was reported; when the message gives the event's own time, `occurred_on` carries it.
		if !p.Tense.IsCurrent() && !(predicate.Event && p.Tense == domain.TensePast) {
			out.Rejected = append(out.Rejected, reject(p, domain.ReasonNotCurrent, message.Ordinal))
			continue
		}

		// After the quote is located, not before: a repeat whose quote cannot be found is
		// uncitable, which is the more specific defect and the one worth recording.
		identity := p.Predicate + "\x00" + domain.NormalizeName(p.Subject) + "\x00" + domain.NormalizeName(p.Object)
		if _, already := seen[identity]; already {
			out.Rejected = append(out.Rejected, reject(p, domain.ReasonDuplicateClaim, message.Ordinal))
			continue
		}
		seen[identity] = struct{}{}

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
			Cardinality:   predicate.Cardinality,
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
