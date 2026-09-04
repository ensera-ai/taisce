package extract_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/taisce/taisce/internal/domain"
	"github.com/taisce/taisce/internal/extract"
)

// The model is stubbed HERE and only here, and it is not standing in for a database or a service.
//
// The property under test is what this package does with a model's output, so the model is the
// INPUT. A real one cannot be asked to paraphrase on command, invent a quote on command, and copy
// verbatim on command within one run — which is exactly the fixture set the span rule has to
// survive. Stubbing it is what makes those cases reachable at all.
type stubModel struct {
	proposals []extract.Proposal
	err       error
}

func (m stubModel) Propose(context.Context, domain.Message, domain.Ontology) ([]extract.Proposal, error) {
	return m.proposals, m.err
}

// The vocabulary is built by hand here rather than read from the database, because this package does
// not talk to one. What is asserted is that an unadmitted relation is refused — which needs a
// vocabulary with a hole in it, not the real one.
func vocabulary(t *testing.T) domain.Ontology {
	t.Helper()
	o, err := domain.NewOntology([]domain.Predicate{
		{Name: "lives_in", SemanticType: "identity", Cardinality: domain.CardinalityOne, ObjectKind: "place"},
		{Name: "works_at", SemanticType: "identity", Cardinality: domain.CardinalityMany, ObjectKind: "organisation"},
		{Name: "scheduled_for", SemanticType: "temporal", Cardinality: domain.CardinalityOne, ObjectKind: "time"},
	})
	if err != nil {
		t.Fatalf("vocabulary: %v", err)
	}
	return o
}

func message(content string) domain.Message {
	return domain.Message{
		Ordinal:    2,
		Role:       domain.RoleUser,
		Content:    content,
		OccurredAt: time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC),
	}
}

// ── #42's claim ───────────────────────────────────────────────────────────────────────────────
//
// Every returned claim's span, taken from the message content, equals its quote.
//
// This is the assertion the product's argument rests on. It is made over the fixture set that
// actually breaks a naive extractor: a verbatim copy, a whitespace-normalised copy, a paraphrase, an
// invention, and a quote of a relation the vocabulary does not admit.
func TestEveryReturnedSpanReproducesItsQuote(t *testing.T) {
	content := "I moved to Dublin\n  last month, and I now work at\tEnsera full time."

	for _, tc := range []struct {
		name      string
		quote     string
		predicate string
		claims    int
		reason    string
	}{
		{
			name:      "a verbatim quote is located exactly",
			quote:     "I moved to Dublin",
			predicate: "lives_in",
			claims:    1,
		},
		{
			name: "whitespace the model normalised still locates",
			// The message has a newline and two spaces where this has one space. The words are the
			// same words, so refusing this would discard a correct citation over formatting.
			quote:     "I moved to Dublin last month",
			predicate: "lives_in",
			claims:    1,
		},
		{
			name:      "a tab the model turned into a space still locates",
			quote:     "I now work at Ensera",
			predicate: "works_at",
			claims:    1,
		},
		{
			name: "a paraphrase is refused",
			// Every word of this appears in the message. None of it appears in this order, which is
			// the point: a paraphrase is the failure that looks most like success.
			quote:     "I moved to Ensera and work in Dublin",
			predicate: "works_at",
			claims:    0,
			reason:    domain.ReasonUnlocatableQuote,
		},
		{
			name:      "an invented quote is refused",
			quote:     "I have lived in Dublin since 2019",
			predicate: "lives_in",
			claims:    0,
			reason:    domain.ReasonUnlocatableQuote,
		},
		{
			name: "a relation outside the vocabulary is refused before the span is even considered",
			// The quote is verbatim and would locate. It is still refused, because admitting one
			// relation outside the set makes the set advisory.
			quote:     "I moved to Dublin",
			predicate: "relocated_to",
			claims:    0,
			reason:    domain.ReasonUnmappedRelation,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := extract.New(stubModel{proposals: []extract.Proposal{{
				Subject: "user", Predicate: tc.predicate, Object: "Dublin",
				Statement: "The user moved to Dublin.", Confidence: 0.9, Quote: tc.quote,
			}}}, vocabulary(t))

			got, err := e.Extract(context.Background(), message(content))
			if err != nil {
				t.Fatalf("extract: %v", err)
			}
			if len(got.Claims) != tc.claims {
				t.Fatalf("expected %d claims, got %d (%+v)", tc.claims, len(got.Claims), got)
			}
			for _, c := range got.Claims {
				// THE assertion. Not "the span looks plausible" — the span, taken from the message,
				// IS the quote.
				if content[c.ByteStart:c.ByteEnd] != c.Quote {
					t.Fatalf("span [%d:%d) gives %q but the quote is %q",
						c.ByteStart, c.ByteEnd, content[c.ByteStart:c.ByteEnd], c.Quote)
				}
				if c.ByteEnd <= c.ByteStart {
					t.Fatalf("span [%d:%d) is empty", c.ByteStart, c.ByteEnd)
				}
			}
			if tc.reason != "" {
				if len(got.Rejected) != 1 {
					t.Fatalf("expected one rejection, got %+v", got.Rejected)
				}
				if got.Rejected[0].Reason != tc.reason {
					t.Fatalf("rejected for %q, expected %q", got.Rejected[0].Reason, tc.reason)
				}
				// The model's own words are kept. A rejection recorded without them cannot tell
				// anybody whether the vocabulary is wrong or the model is.
				if got.Rejected[0].Quote != tc.quote {
					t.Fatalf("the rejection lost what the model said: %q", got.Rejected[0].Quote)
				}
			}
		})
	}
}

// The stored quote is the message's text, not the model's.
//
// When the model normalised whitespace the two differ, and storing the model's version would produce
// a quote its own span does not reproduce — a row that is self-inconsistent and looks fine.
func TestTheStoredQuoteIsTheMessagesTextNotTheModels(t *testing.T) {
	content := "I moved to Dublin\n  last month."
	e := extract.New(stubModel{proposals: []extract.Proposal{{
		Subject: "user", Predicate: "lives_in", Object: "Dublin",
		Quote: "moved to Dublin last month",
	}}}, vocabulary(t))

	got, err := e.Extract(context.Background(), message(content))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(got.Claims) != 1 {
		t.Fatalf("expected one claim, got %+v", got)
	}
	if want := "moved to Dublin\n  last month"; got.Claims[0].Quote != want {
		t.Fatalf("stored quote is %q; it must be the message's own bytes %q", got.Claims[0].Quote, want)
	}
}

// A span must be correct in bytes, not in characters.
//
// Everything downstream — the evidence columns, `substring(... FROM byte_start + 1 ...)` — indexes
// bytes. An off-by-a-rune span in text outside ASCII resolves to a truncated word, which reads as a
// bad extraction rather than as an indexing bug.
func TestASpanIsCorrectInBytesWhenTheTextIsNotASCII(t *testing.T) {
	content := "Tá mé i mo chónaí i mBaile Átha Cliath\nfaoi láthair."
	e := extract.New(stubModel{proposals: []extract.Proposal{{
		Subject: "user", Predicate: "lives_in", Object: "Baile Átha Cliath",
		Quote: "i mo chónaí i mBaile Átha Cliath faoi láthair",
	}}}, vocabulary(t))

	got, err := e.Extract(context.Background(), message(content))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(got.Claims) != 1 {
		t.Fatalf("expected one claim, got %+v", got)
	}
	c := got.Claims[0]
	if content[c.ByteStart:c.ByteEnd] != c.Quote {
		t.Fatalf("span [%d:%d) gives %q, quote is %q", c.ByteStart, c.ByteEnd, content[c.ByteStart:c.ByteEnd], c.Quote)
	}
	if want := "i mo chónaí i mBaile Átha Cliath\nfaoi láthair"; c.Quote != want {
		t.Fatalf("quote is %q, want %q", c.Quote, want)
	}
}

// A message that asserts nothing produces an empty result, not an error.
//
// Most messages assert nothing. A caller forced to distinguish "no claims" from "extraction failed"
// will end up treating failures as ordinary, which is how a silently broken extractor runs for a
// week.
func TestAMessageWithNothingAssertableIsAnEmptyResult(t *testing.T) {
	e := extract.New(stubModel{proposals: nil}, vocabulary(t))
	got, err := e.Extract(context.Background(), message("Thanks, that worked."))
	if err != nil {
		t.Fatalf("a message with no claims must not be an error: %v", err)
	}
	if len(got.Claims) != 0 || len(got.Rejected) != 0 {
		t.Fatalf("expected nothing, got %+v", got)
	}
}

// A model failure IS an error. It is the one thing that is not an ordinary empty result: silently
// returning no claims would make an outage indistinguishable from a quiet conversation, and the
// memory would simply stop forming.
func TestAModelFailureIsAnError(t *testing.T) {
	boom := errors.New("upstream refused")
	e := extract.New(stubModel{err: boom}, vocabulary(t))
	if _, err := e.Extract(context.Background(), message("I moved to Dublin.")); !errors.Is(err, boom) {
		t.Fatalf("a model failure must surface; got %v", err)
	}
}

// The vocabulary types the object end when the model volunteered nothing.
//
// `object_kind` is declared per relation precisely so that this is not a guess: the object of
// `scheduled_for` is a time whether or not the model said so.
func TestTheVocabularyTypesTheObjectWhenTheModelDidNot(t *testing.T) {
	content := "The review is scheduled for Friday."
	e := extract.New(stubModel{proposals: []extract.Proposal{{
		Subject: "the review", Predicate: "scheduled_for", Object: "Friday",
		Quote: "scheduled for Friday",
	}}}, vocabulary(t))

	got, err := e.Extract(context.Background(), message(content))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(got.Claims) != 1 {
		t.Fatalf("expected one claim, got %+v", got)
	}
	if got.Claims[0].ObjectType != "time" {
		t.Fatalf("object type is %q; the vocabulary says a scheduled_for object is a time", got.Claims[0].ObjectType)
	}
	if got.Claims[0].SemanticType != "temporal" {
		t.Fatalf("semantic type is %q, want temporal", got.Claims[0].SemanticType)
	}
}

// Case is not whitespace. A model that changed it retyped rather than copied, and a tolerance wide
// enough to accept this is wide enough to accept a sentence the model composed.
func TestAQuoteThatChangedCaseIsRefused(t *testing.T) {
	e := extract.New(stubModel{proposals: []extract.Proposal{{
		Subject: "user", Predicate: "lives_in", Object: "Dublin", Quote: "i moved to dublin",
	}}}, vocabulary(t))

	got, err := e.Extract(context.Background(), message("I moved to Dublin last month."))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(got.Claims) != 0 {
		t.Fatalf("a retyped quote was accepted: %+v", got.Claims)
	}
	if len(got.Rejected) != 1 || got.Rejected[0].Reason != domain.ReasonUnlocatableQuote {
		t.Fatalf("expected an unlocatable-quote rejection, got %+v", got.Rejected)
	}
}

// ── #74 ───────────────────────────────────────────────────────────────────────────────────────
//
// One message asserting the same relation twice is one fact, and the repeat is recorded rather than
// dropped.
//
// Nothing downstream collapses these. The fact table has no uniqueness past its primary key — it
// cannot have, because a superseded fact and its replacement share the triple — so a second copy
// would become a second fact, a second evidence row, and one of the rows a bundle is cut to.
func TestARelationAssertedTwiceInOneMessageIsOneClaim(t *testing.T) {
	content := "I work at Ensera. Yes, Ensera — I work at Ensera."
	result, err := extract.New(&stubModel{proposals: []extract.Proposal{
		{Subject: "I", Predicate: "works_at", Object: "Ensera", Quote: "I work at Ensera"},
		{Subject: "I", Predicate: "works_at", Object: "Ensera", Quote: "I work at Ensera."},
	}}, vocabulary(t)).Extract(context.Background(), message(content))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}

	if len(result.Claims) != 1 {
		t.Fatalf("got %d claims, wanted the relation once", len(result.Claims))
	}
	if len(result.Rejected) != 1 {
		t.Fatalf("got %d refusals, wanted the repeat written down", len(result.Rejected))
	}
	if result.Rejected[0].Reason != domain.ReasonDuplicateClaim {
		t.Fatalf("the repeat was refused as %q", result.Rejected[0].Reason)
	}
	// The first one survives, with its span intact — the refusal must not disturb what it follows.
	if got := content[result.Claims[0].ByteStart:result.Claims[0].ByteEnd]; got != result.Claims[0].Quote {
		t.Fatalf("span gives %q, quote is %q", got, result.Claims[0].Quote)
	}
}

// Identity is what identity is downstream. Entities resolve by normalised name, so two claims
// differing only in casing or spacing are one fact whether or not extraction notices — and if it
// does not, the second one is a duplicate row nobody asked for.
func TestARepeatIsFoundThroughTheSameNormalisationEntitiesUse(t *testing.T) {
	content := "I work at Ensera. I work at  ENSERA  as it happens."
	result, err := extract.New(&stubModel{proposals: []extract.Proposal{
		{Subject: "I", Predicate: "works_at", Object: "Ensera", Quote: "I work at Ensera"},
		{Subject: "i", Predicate: "works_at", Object: "  ENSERA  ", Quote: "I work at  ENSERA "},
	}}, vocabulary(t)).Extract(context.Background(), message(content))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(result.Claims) != 1 {
		t.Fatalf("got %d claims; casing and spacing do not make a second fact", len(result.Claims))
	}
}

// Two relations sharing a subject are not a repeat, and neither are two objects under one relation.
// A dedupe that swallowed either would be silently losing facts, which is worse than the defect.
func TestDifferentRelationsAndDifferentEndsAreNotRepeats(t *testing.T) {
	content := "I work at Ensera, I live in Dublin, and I work at Acme too."
	result, err := extract.New(&stubModel{proposals: []extract.Proposal{
		{Subject: "I", Predicate: "works_at", Object: "Ensera", Quote: "I work at Ensera"},
		{Subject: "I", Predicate: "lives_in", Object: "Dublin", Quote: "I live in Dublin"},
		{Subject: "I", Predicate: "works_at", Object: "Acme", Quote: "I work at Acme"},
	}}, vocabulary(t)).Extract(context.Background(), message(content))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(result.Claims) != 3 {
		t.Fatalf("got %d claims, wanted 3", len(result.Claims))
	}
	if len(result.Rejected) != 0 {
		t.Fatalf("got %d refusals, wanted none: %+v", len(result.Rejected), result.Rejected)
	}
}

// A repeat whose quote cannot be found is uncitable, which is the more specific defect. Order
// matters here: reporting it as a duplicate would hide that the model invented a quote.
func TestARepeatWithAnInventedQuoteIsRefusedAsUncitable(t *testing.T) {
	content := "I work at Ensera."
	result, err := extract.New(&stubModel{proposals: []extract.Proposal{
		{Subject: "I", Predicate: "works_at", Object: "Ensera", Quote: "I work at Ensera"},
		{Subject: "I", Predicate: "works_at", Object: "Ensera", Quote: "employed by Ensera"},
	}}, vocabulary(t)).Extract(context.Background(), message(content))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(result.Rejected) != 1 || result.Rejected[0].Reason != domain.ReasonUnlocatableQuote {
		t.Fatalf("wanted one uncitable refusal, got %+v", result.Rejected)
	}
}
