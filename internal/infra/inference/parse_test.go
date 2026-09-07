// Finding the reply inside what the model sent.
//
// Every case here was either measured against a real model or is the failure mode that being
// tolerant could introduce. The parse is the last thing between a provider and the write path, so
// what it accepts and what it refuses both matter — and the dangerous direction is not strictness,
// it is quietly reporting that a message asserted nothing.
package inference

import (
	"strings"
	"testing"
)

// The exact reply that made a correct extraction fail.
//
// Measured against a local model in JSON mode on the fixture "I moved to Dublin last month.": a
// fragment of the prompt's own shape emitted before a perfect document. It cost the turn, counted an
// attempt, and would have parked it.
func TestARepliedDocumentIsFoundBehindAPrefixTheModelInvented(t *testing.T) {
	const measured = `claims":{"claims": [{"subject": "I", "predicate": "lives_in", ` +
		`"object": "Dublin", "statement": "The speaker lives in Dublin.", "confidence": 0.95, ` +
		`"quote": "moved to Dublin", "subject_type": "person", "object_type": "place"}]}`

	proposals, err := parseProposals(measured)
	if err != nil {
		t.Fatalf("the measured reply was refused: %v", err)
	}
	if len(proposals) != 1 {
		t.Fatalf("got %d proposals, wanted the one in the reply", len(proposals))
	}
	if proposals[0].Predicate != "lives_in" || proposals[0].Quote != "moved to Dublin" {
		t.Fatalf("the claim did not survive being found: %+v", proposals[0])
	}
}

// A single claim emitted without its envelope must NOT be read as "nothing was asserted".
//
// This is the failure being tolerant could introduce, and it is the worst kind: silent. Decoding
// into a struct ignores unknown fields, so this object would parse as an envelope with no claims and
// the turn would be marked formed having produced nothing.
func TestAClaimWithoutItsEnvelopeIsAnErrorRatherThanAnEmptyResult(t *testing.T) {
	const bare = `{"subject": "I", "predicate": "lives_in", "object": "Dublin", "quote": "in Dublin"}`
	if _, err := parseProposals(bare); err == nil {
		t.Fatal("a bare claim was read as an empty result, which loses it silently")
	}
}

// An empty list means the message asserts nothing, which is the ordinary case and must stay
// distinguishable from a reply that could not be read.
func TestAnEmptyClaimListIsAResultAndNotAFailure(t *testing.T) {
	proposals, err := parseProposals(`{"claims": []}`)
	if err != nil {
		t.Fatalf("an empty result was treated as a failure: %v", err)
	}
	if len(proposals) != 0 {
		t.Fatalf("got %d proposals from an empty list", len(proposals))
	}
}

// Prose after the document does not fail the parse, because the decoder reads one value and stops.
func TestCommentaryAfterTheDocumentIsIgnored(t *testing.T) {
	proposals, err := parseProposals(
		`{"claims": [{"subject":"I","predicate":"works_at","object":"Ensera","quote":"work at Ensera"}]}` +
			"\n\nI hope this helps! Let me know if you would like anything else.")
	if err != nil {
		t.Fatalf("trailing commentary failed the parse: %v", err)
	}
	if len(proposals) != 1 {
		t.Fatalf("got %d proposals", len(proposals))
	}
}

// A fenced document still works, which is the case that was already handled and must not regress.
func TestAFencedDocumentIsStillRead(t *testing.T) {
	proposals, err := parseProposals("```json\n" +
		`{"claims": [{"subject":"I","predicate":"lives_in","object":"Dublin","quote":"in Dublin"}]}` +
		"\n```")
	if err != nil {
		t.Fatalf("a fenced reply was refused: %v", err)
	}
	if len(proposals) != 1 {
		t.Fatalf("got %d proposals", len(proposals))
	}
}

// A reply with no envelope anywhere is an error, not an empty result.
//
// This is the distinction the whole parse rests on: an empty result means "this message asserts
// nothing", which is true of most messages. Returning it for a broken reply would make a
// misconfigured model indistinguishable from a quiet conversation, and memory would stop forming
// with nothing reporting a problem.
func TestAReplyWithNoEnvelopeIsAnError(t *testing.T) {
	for name, reply := range map[string]string{
		"prose":            "I am sorry, I cannot help with that.",
		"wrong shape":      `{"claims": "The user works at Ensera"}`,
		"a list not a doc": `[{"predicate":"works_at"}]`,
		"empty":            "",
		"unclosed":         `{"claims": [`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseProposals(reply); err == nil {
				t.Fatalf("%q was accepted", reply)
			}
		})
	}
}

// The error carries the reply, bounded. It is stored on the observation where erasure reaches it,
// and a reply can contain the message that was sent to it.
func TestTheErrorQuotesTheReplyWithoutLettingItRunAway(t *testing.T) {
	long := strings.Repeat("not json ", 500)
	_, err := parseProposals(long)
	if err == nil {
		t.Fatal("nonsense was accepted")
	}
	if len(err.Error()) > 600 {
		t.Fatalf("the error is %d bytes; a remote system decides how much storage it uses", len(err.Error()))
	}
	if !strings.Contains(err.Error(), "not json") {
		t.Fatal("the error does not show what came back, so nobody can diagnose it")
	}
}
