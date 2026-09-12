package domain

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// The role policy in one assertion. Everything downstream depends on this being the only role that
// speaks for the principal.
func TestOnlyTheUserSpeaksForThePrincipal(t *testing.T) {
	for role, want := range map[Role]bool{
		RoleUser:      true,
		RoleAssistant: false,
		RoleSystem:    false,
		RoleTool:      false,
		Role("admin"): false, // unknown roles must not acquire the privilege by default
	} {
		if got := role.SpeaksForPrincipal(); got != want {
			t.Errorf("role %q speaks for principal = %v, want %v", role, got, want)
		}
	}
}

func TestOnlyTheClosedSetIsValid(t *testing.T) {
	for _, r := range []Role{RoleUser, RoleAssistant, RoleSystem, RoleTool} {
		if !r.Valid() {
			t.Errorf("%q should be valid", r)
		}
	}
	for _, r := range []Role{"", "User", "USER", "developer", "function"} {
		if Role(r).Valid() {
			t.Errorf("%q should not be valid", r)
		}
	}
}

func TestATurnWithoutASpeakerIsRefused(t *testing.T) {
	now := time.Now()
	for name, turn := range map[string]Turn{
		"no scope":       {Messages: []Message{{Role: RoleUser, Content: "hi", OccurredAt: now}}},
		"no messages":    {Scope: "p1"},
		"blank role":     {Scope: "p1", Messages: []Message{{Content: "hi", OccurredAt: now}}},
		"unknown role":   {Scope: "p1", Messages: []Message{{Role: "developer", Content: "hi", OccurredAt: now}}},
		"empty content":  {Scope: "p1", Messages: []Message{{Role: RoleUser, Content: "   ", OccurredAt: now}}},
		"negative order": {Scope: "p1", Messages: []Message{{Role: RoleUser, Content: "hi", Ordinal: -1, OccurredAt: now}}},
		"duplicate order": {Scope: "p1", Messages: []Message{
			{Ordinal: 0, Role: RoleUser, Content: "a", OccurredAt: now},
			{Ordinal: 0, Role: RoleAssistant, Content: "b", OccurredAt: now},
		}},
	} {
		if err := turn.Validate(); err == nil {
			t.Errorf("%s: should have been refused", name)
		}
	}

	valid := Turn{Scope: "p1", Messages: []Message{
		{Ordinal: 0, Role: RoleUser, Content: "where is the office", OccurredAt: now},
		{Ordinal: 1, Role: RoleAssistant, Content: "Dublin", OccurredAt: now},
	}}
	if err := valid.Validate(); err != nil {
		t.Fatalf("a valid turn was refused: %v", err)
	}
}

// The rejection has to name the offending message, because a turn is a batch and "invalid turn"
// leaves the caller diffing their own payload.
func TestARejectionSaysWhichMessage(t *testing.T) {
	err := Turn{Scope: "p1", Messages: []Message{
		{Ordinal: 0, Role: RoleUser, Content: "fine", OccurredAt: time.Now()},
		{Ordinal: 1, Role: "nonsense", Content: "also fine", OccurredAt: time.Now()},
	}}.Validate()
	if err == nil {
		t.Fatal("expected a rejection")
	}
	if !strings.Contains(err.Error(), "message 1") || !strings.Contains(err.Error(), "nonsense") {
		t.Fatalf("error should name the message and the bad role, got: %v", err)
	}
}

func TestNormalizationMergesCasingAndWhitespaceAndNothingElse(t *testing.T) {
	same := []string{"Dublin Office", "dublin office", "  DUBLIN   OFFICE  ", "Dublin\tOffice"}
	want := NormalizeName(same[0])
	for _, s := range same[1:] {
		if got := NormalizeName(s); got != want {
			t.Errorf("NormalizeName(%q) = %q, want %q", s, got, want)
		}
	}

	// The deliberate limit, asserted so nobody "fixes" it without reading D6. Merging these needs a
	// threshold nobody can justify, and over-merging is a governance failure.
	if NormalizeName("Dublin office") == NormalizeName("our Dublin site") {
		t.Fatal("normalization must not merge different phrases; meaning is a separate surface")
	}
}

func TestOntologyRejectsEmptyAndDuplicateRelations(t *testing.T) {
	if _, err := NewOntology(nil); err == nil {
		t.Fatal("empty ontology was accepted")
	}
	p := Predicate{Name: "works_at", Cardinality: CardinalityMany}
	if _, err := NewOntology([]Predicate{p, p}); err == nil {
		t.Fatal("duplicate relation was accepted")
	}
}

func TestErasureCleanReportsResidualRows(t *testing.T) {
	if !(Erasure{Residual: map[string]int{"fact": 0, "entity": 0}}).Clean() {
		t.Fatal("zero residual rows were reported as dirty")
	}
	if (Erasure{Residual: map[string]int{"fact": 1}}).Clean() {
		t.Fatal("residual rows were reported as clean")
	}
}

// ── #86: what the ledger will not record ──────────────────────────────────────────────────────
//
// Checked before the insert as well as by the schema, so a caller gets the reason rather than a
// constraint violation four frames down naming a column.

func TestAnAuditEntryThatCouldNotBeCountedIsRefused(t *testing.T) {
	valid := AuditEntry{
		Operation:     AuditRecall,
		Principal:     "cred-1",
		PrincipalKind: PrincipalCredential,
		Outcome:       OutcomeAllowed,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("a valid entry was refused: %v", err)
	}

	for name, mutate := range map[string]func(*AuditEntry){
		"an operation nobody has heard of": func(e *AuditEntry) { e.Operation = "exfiltrate" },
		"no operation at all":              func(e *AuditEntry) { e.Operation = "" },
		"a principal kind that is not one": func(e *AuditEntry) { e.PrincipalKind = "ghost" },
		"no principal kind":                func(e *AuditEntry) { e.PrincipalKind = "" },
		"an outcome that is neither":       func(e *AuditEntry) { e.Outcome = "maybe" },
		"no outcome":                       func(e *AuditEntry) { e.Outcome = "" },
		"no principal":                     func(e *AuditEntry) { e.Principal = "" },
		"a whitespace principal":           func(e *AuditEntry) { e.Principal = "   " },
		"a negative magnitude":             func(e *AuditEntry) { e.Magnitude = -1 },
	} {
		t.Run(name, func(t *testing.T) {
			e := valid
			mutate(&e)
			if err := e.Validate(); err == nil {
				t.Fatalf("%s was accepted", name)
			}
		})
	}
}

// An unattributed operation is the one thing the ledger cannot hold, so the refusal says so rather
// than naming a column.
func TestAnUnattributedOperationIsRefusedWithTheReason(t *testing.T) {
	e := AuditEntry{
		Operation:     AuditObserve,
		PrincipalKind: PrincipalCredential,
		Outcome:       OutcomeAllowed,
	}
	err := e.Validate()
	if err == nil {
		t.Fatal("an operation with nobody behind it was accepted")
	}
	if !strings.Contains(err.Error(), "unattributed") {
		t.Fatalf("the refusal does not say what is wrong: %v", err)
	}
}

// Every operation the API can perform is one the ledger knows about.
//
// The failure this prevents: a handler added later records an operation the ledger refuses, the
// append fails, it is swallowed by design — and the operation happens with no row at all.
func TestEveryRecordedOperationIsOneTheLedgerAccepts(t *testing.T) {
	for _, op := range []string{
		AuditObserve, AuditRecall, AuditErase, AuditFreshness,
		AuditProjectCreate, AuditProjectSuspend, AuditProjectResume,
		AuditCredentialIssue, AuditCredentialRevoke, AuditAuthenticate,
	} {
		e := AuditEntry{
			Operation: op, Principal: "p", PrincipalKind: PrincipalSystem,
			Outcome: OutcomeRefused,
		}
		if err := e.Validate(); err != nil {
			t.Fatalf("the ledger refuses %q, which the code records: %v", op, err)
		}
	}
	for _, kind := range []string{PrincipalCredential, PrincipalOperator, PrincipalSystem} {
		e := AuditEntry{
			Operation: AuditRecall, Principal: "p", PrincipalKind: kind,
			Outcome: OutcomeAllowed,
		}
		if err := e.Validate(); err != nil {
			t.Fatalf("the ledger refuses principal kind %q: %v", kind, err)
		}
	}
}

// ── #60: a citation is legible, and says how much of the message it is showing ─────────────────

// cut is the window the database takes, in Go, so these cases can be written against the same
// arithmetic without a database. The pg tests assert that the SQL cuts the same bytes.
func cut(message string, quoteStart, quoteEnd int) RawWindow {
	start := quoteStart - ContextWindow
	if start < 0 {
		start = 0
	}
	end := quoteEnd + ContextWindow
	if end > len(message) {
		end = len(message)
	}
	return RawWindow{
		Bytes:        []byte(message[start:end]),
		Start:        start,
		QuoteStart:   quoteStart,
		QuoteEnd:     quoteEnd,
		MessageBytes: len(message),
	}
}

// The property the whole feature rests on: whatever the window did at its edges, the cited words are
// inside what comes back, at the offset the citation says they are.
func TestTheContextAlwaysContainsTheWordsItIsContextFor(t *testing.T) {
	long := strings.Repeat("the migration went out on friday and nothing broke. ", 20)
	for name, msg := range map[string]string{
		"short message":       "I live in Dublin.",
		"quote at the head":   "Dublin is where I live. " + long,
		"quote at the tail":   long + " and I live in Dublin",
		"quote in the middle": long + "I live in Dublin, which is where the office is. " + long,
	} {
		t.Run(name, func(t *testing.T) {
			start := strings.Index(msg, "Dublin")
			end := start + len("Dublin")
			got := cut(msg, start, end).Legible()
			if got.Text == "" {
				t.Fatal("no context came back at all")
			}
			at := start - got.ByteStart
			if at < 0 || at+len("Dublin") > len(got.Text) {
				t.Fatalf("the quote is not inside its own context: offset %d in %d bytes", at, len(got.Text))
			}
			if got.Text[at:at+len("Dublin")] != "Dublin" {
				t.Fatalf("context_start does not locate the quote: got %q", got.Text[at:])
			}
			if !strings.HasPrefix(msg[got.ByteStart:], got.Text) {
				t.Fatal("the context is not the message's own bytes")
			}
		})
	}
}

// Complete is the difference between "this is the message" and "this is part of it". A reader who
// cannot tell them apart reads a sentence that continues as one that stopped.
func TestContextSaysWhetherItIsTheWholeMessage(t *testing.T) {
	whole := "I live in Dublin."
	start := strings.Index(whole, "Dublin")
	got := cut(whole, start, start+6).Legible()
	if !got.Complete || got.Text != whole || got.ByteStart != 0 {
		t.Fatalf("a message shorter than the window came back cut: %+v", got)
	}

	long := strings.Repeat("a sentence that is long enough to be elided. ", 40)
	cutStart := strings.Index(long+"Dublin"+long, "Dublin")
	partial := cut(long+"Dublin"+long, cutStart, cutStart+6).Legible()
	if partial.Complete {
		t.Fatal("a window cut out of a longer message claimed to be the whole message")
	}
	if len(partial.Text) > 2*ContextWindow+len("Dublin") {
		t.Fatalf("the window is not bounded: %d bytes", len(partial.Text))
	}
}

// A cut edge lands mid-word and mid-rune. Neither belongs in a receipt: one reads as a typo in the
// stored text, the other as a replacement character.
func TestACutEdgeIsRepairedAndOnlyTheCutEdge(t *testing.T) {
	// Every character here is multi-byte, so an arbitrary byte cut splits a rune almost always.
	filler := strings.Repeat("naïve café résumé — ", 30)
	msg := filler + "Dublin" + filler
	start := strings.Index(msg, "Dublin")

	got := cut(msg, start, start+6).Legible()
	if strings.ContainsRune(got.Text, utf8.RuneError) {
		t.Fatalf("a split rune reached the caller: %q", got.Text)
	}
	if !utf8.ValidString(got.Text) {
		t.Fatalf("the context is not valid UTF-8: %q", got.Text)
	}
	if strings.HasPrefix(got.Text, " ") || strings.HasSuffix(got.Text, " ") {
		t.Fatalf("the window was not trimmed to a word: %q", got.Text)
	}

	// The edge that was NOT cut is left alone, which is what makes Complete worth reporting: the
	// first word of a message is not trimmed to look like an elision.
	head := "Dublin " + filler
	atHead := cut(head, 0, 6).Legible()
	if !strings.HasPrefix(atHead.Text, "Dublin") || atHead.ByteStart != 0 {
		t.Fatalf("the head of a message was trimmed as though it had been cut: %+v", atHead)
	}
}

// One long word with no whitespace in it — a URL, a stack frame, a base64 blob. Snapping to a word
// boundary would throw away every byte of context there is, so the fallback keeps it.
func TestAWindowWithNoWordBoundaryKeepsWhatItHas(t *testing.T) {
	blob := strings.Repeat("x", 400)
	msg := blob + "Dublin" + blob
	start := strings.Index(msg, "Dublin")
	got := cut(msg, start, start+6).Legible()
	if !strings.Contains(got.Text, "Dublin") {
		t.Fatalf("the quote was trimmed away: %q", got.Text)
	}
	if len(got.Text) < ContextWindow {
		t.Fatalf("a window with no whitespace was discarded rather than kept: %d bytes", len(got.Text))
	}
}

// A window that does not hold its own quote is not context for it, and returning the bytes anyway
// would be a receipt pointing at the wrong words while looking correct.
func TestAWindowThatDoesNotHoldItsQuoteReturnsNothing(t *testing.T) {
	for name, w := range map[string]RawWindow{
		"no bytes":           {Bytes: nil, QuoteStart: 0, QuoteEnd: 0},
		"quote before it":    {Bytes: []byte("in Dublin"), Start: 100, QuoteStart: 3, QuoteEnd: 9, MessageBytes: 200},
		"quote past its end": {Bytes: []byte("in Dublin"), Start: 0, QuoteStart: 3, QuoteEnd: 400, MessageBytes: 400},
		"end before start":   {Bytes: []byte("in Dublin"), Start: 0, QuoteStart: 9, QuoteEnd: 3, MessageBytes: 9},
	} {
		if got := w.Legible(); got.Text != "" {
			t.Errorf("%s: returned %q as context", name, got.Text)
		}
	}
}
