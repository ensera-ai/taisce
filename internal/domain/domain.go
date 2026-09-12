// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

// Package domain holds the vocabulary the read and write paths share.
//
// No I/O, no clock, no database. A type here is a thing the product talks about, and the rule for
// admitting one is that both sides of the system need to name it — anything only one side needs
// belongs to that side.
package domain

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Role is who spoke. The set is closed.
//
// # Why this is a type and not a string
//
// The role policy — an assistant's claim never becomes a user's fact — is enforced per message, so
// the role is load-bearing rather than descriptive. A string typo produces a message that stores
// fine and is silently exempt from the policy, because no arm of the switch matches it.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
	RoleTool      Role = "tool"
)

// Valid reports whether r is a role the system knows.
//
// Go has no exhaustiveness check on a switch over a named string type, so this plus the database
// CHECK are the two places an unknown role is stopped. Both exist deliberately: the constraint is
// what makes it impossible to store, and this is what makes it a clear error rather than a
// constraint violation surfacing from three layers down.
func (r Role) Valid() bool {
	switch r {
	case RoleUser, RoleAssistant, RoleSystem, RoleTool:
		return true
	}
	return false
}

// SpeaksForPrincipal reports whether a claim in a message with this role may become a fact about
// the principal.
//
// Only the user. An assistant sentence is a model's output, and storing it as something the person
// said is how a hallucination becomes a memory that outlives the conversation that produced it — at
// which point nothing distinguishes it from something they actually told us.
//
// A tool result is not the principal speaking either. It is data the agent fetched, and whatever it
// says is a claim by whatever produced it.
func (r Role) SpeaksForPrincipal() bool { return r == RoleUser }

// Message is one message in a turn.
type Message struct {
	// Ordinal is position in the turn as the caller sent it. The caller's order is authoritative:
	// it is the order the model saw, and a memory that reorders a conversation has changed what
	// happened.
	Ordinal int
	Role    Role
	Content string
	// GroupOrdinal marks an atomic unit. An assistant message carrying tool calls and the tool
	// results answering them share one value, because summarising half of that pair produces a
	// message list the model rejects outright.
	//
	// Zero is not special; it is simply the first group.
	GroupOrdinal int
	OccurredAt   time.Time
}

// Turn is what a caller observes: an ordered set of messages with their speakers.
//
// There is no content-only alternative to this shape, and that is the point. A surface accepting an
// unattributed blob makes the role policy unenforceable for everything written through it, and that
// surface is invariably the convenient one everybody reaches for.
type Turn struct {
	Scope         string
	DataSubjectID string
	Messages      []Message
	// OccurredAt is when the turn happened, distinct from when we were told. A backfill has an old
	// OccurredAt and a new ingestion time, and conflating them makes a year of imported history look
	// like it all happened at import.
	OccurredAt time.Time
}

// Validate reports why a turn cannot be stored, or nil.
//
// Called before anything is written, so a rejected turn leaves nothing behind. Scalar fields also
// have database constraints; group ordering spans rows and is checked here for every append path.
func (t Turn) Validate() error {
	if err := ValidateMessageCount(len(t.Messages)); err != nil {
		return err
	}
	if strings.TrimSpace(t.Scope) == "" {
		return fmt.Errorf("turn has no scope")
	}
	if len(t.Messages) == 0 {
		return fmt.Errorf("turn has no messages")
	}
	seen := make(map[int]bool, len(t.Messages))
	contentBytes := 0
	for i, m := range t.Messages {
		if len(m.Content) > MaxMessageBytes {
			return ErrMessageSize
		}
		if len(m.Content) > MaxObservationBytes-contentBytes {
			return ErrObservationSize
		}
		contentBytes += len(m.Content)
		if !m.Role.Valid() {
			return fmt.Errorf("message %d has role %q, which is not one of user, assistant, system, tool", i, m.Role)
		}
		if strings.TrimSpace(m.Content) == "" {
			return fmt.Errorf("message %d (%s) has no content", i, m.Role)
		}
		if m.Ordinal < 0 {
			return fmt.Errorf("message %d has ordinal %d", i, m.Ordinal)
		}
		if seen[m.Ordinal] {
			// Two messages at one position means the caller's order is not a total order, and
			// something downstream would have to break the tie arbitrarily — differently each time.
			return fmt.Errorf("two messages share ordinal %d", m.Ordinal)
		}
		seen[m.Ordinal] = true
		if m.GroupOrdinal < 0 {
			return fmt.Errorf("message %d has group ordinal %d", i, m.GroupOrdinal)
		}
	}
	// Callers may supply messages out of ordinal order. Validate the logical sequence without
	// changing their slice; a group cannot disappear and resume later or refer to a missing group.
	ordered := slices.Clone(t.Messages)
	slices.SortFunc(ordered, func(a, b Message) int { return cmp.Compare(a.Ordinal, b.Ordinal) })
	previous := 0
	for _, m := range ordered {
		if m.GroupOrdinal != previous && m.GroupOrdinal != previous+1 || m.Ordinal == ordered[0].Ordinal && m.GroupOrdinal != 0 {
			return fmt.Errorf("message %d has a noncontiguous group ordinal %d", m.Ordinal, m.GroupOrdinal)
		}
		previous = m.GroupOrdinal
	}
	return nil
}

// Observation is a stored turn, with the log position that makes freshness answerable.
type Observation struct {
	ID string
	// LogOffset is contiguous within the scope. The watermark is the highest offset with no gap
	// below it, which only means something if offsets are dense per scope.
	LogOffset int64
	Scope     string
	// DataSubjectID is who the turn is about, and it travels with the observation because every
	// projection derived from it inherits the same governance. Carried here rather than looked up
	// per projection: a lookup is a way to forget.
	DataSubjectID string
	OccurredAt    time.Time
	IngestedAt    time.Time
}

// Freshness is what a caller checks to know whether a recall would see what they just wrote.
//
// Two numbers because they answer two questions. Stored is what an append confirms, and the caller
// already knows it. Formed is what a recall depends on, and it is the one being asked about.
type Freshness struct {
	Scope string
	// Stored is the highest offset ever appended in this scope. HasStored distinguishes
	// an empty project from its first observation at offset zero, even after erasure.
	Stored    int64
	HasStored bool
	// Formed is the highest offset whose turn has been through extraction, with nothing unformed
	// below it. Absent until the scope's first turn forms — and absent is not zero, since zero is a
	// real offset belonging to the first turn.
	Formed    int64
	HasFormed bool
	// Parked is how many turns the driver gave up on. It travels with the two numbers because it is
	// the exception to the second one: the watermark advances past a parked turn so that one turn
	// nobody can form does not freeze a scope's memory, which makes Formed mean "formed, except
	// these". That is only acceptable while it is visible, so it is reported rather than inferred,
	// and a non-zero value is something to act on.
	Parked int
	// Rebuilding is present only while an operator is reinterpreting this project's facts, and its
	// presence is the whole of the message: a rebuild publishes source by source, so a project with
	// one in flight holds the old extractor's reading of the sources it has not reached and the new
	// one's of the sources it has. A bundle taken then is reproducible from neither extractor alone.
	//
	// It travels with Parked for the same reason Parked travels with Formed. A partial state is
	// acceptable while it is visible, and this one was visible nowhere.
	Rebuilding *Rebuild
}

// Rebuild is what a caller needs to decide whether to trust a bundle taken during one: how far the
// reinterpretation has reached, how far it intends to reach, and what it has acknowledged.
//
// Offsets and counts only. The job record holds no source identifier, subject or message text, and
// nothing here adds one — a project credential learns from this exactly what Stored and Formed
// already tell it.
type Rebuild struct {
	// ReinterpretedThrough is the log offset below which every eligible source now carries the new
	// extractor's reading. ReinterpretingThrough is where the job stops: appends after it were
	// excluded when the job started and are not part of this reinterpretation at all.
	ReinterpretedThrough  int64
	ReinterpretingThrough int64
	// Acknowledged and Skipped describe sources the job has finished with, not a frozen total. A
	// source erased before the job reached it never enters either, so these cannot be subtracted
	// from anything to produce a remainder.
	Acknowledged int64
	Skipped      int64
}

// ParkedTurn is a turn the driver stopped trying to form, and why.
//
// It is a decision rather than a loss: the observation and its messages are untouched, so unparking
// is one update and re-forming it is a rebuild. The reason is here so that the exception to the
// freshness watermark can be read rather than only counted.
type ParkedTurn struct {
	ObservationID string
	LogOffset     int64
	Attempts      int
	// Reason is the last failure, truncated. It comes from a provider and is treated as text
	// somebody else wrote.
	Reason   string
	ParkedAt time.Time
}

// Claim is an extracted triple with the evidence for it, before it has been resolved or stored.
//
// It is a CLAIM rather than a fact because nothing has yet decided whether it may be believed: the
// role policy has not been applied and the entity ends are still strings.
type Claim struct {
	Subject   string
	Predicate string
	Object    string
	// Statement is the claim as a sentence, for a reader. The triple is for the machine and reads
	// badly to a person; a bundle that shows `user / lives_in / Dublin` makes the caller reassemble
	// English that the extractor already had.
	Statement string
	// Confidence is what the extractor asserted, distinct from salience, which is computed. Merged
	// into one number, a weak extraction mentioned often is indistinguishable from a certain one.
	Confidence float32
	// Quote is the verbatim span that produced this, and Start/End index into the CONTENT OF ONE
	// MESSAGE — never into the turn. A span against a concatenation that exists nowhere resolves to
	// the wrong words while looking correct.
	Quote         string
	ByteStart     int
	ByteEnd       int
	SourceOrdinal int
	SubjectType   string
	ObjectType    string
	SemanticType  string
	// Cardinality comes from the vocabulary and travels with the claim so the write path can tell a
	// relation that supersedes from one that accumulates. Carried rather than looked up at insert
	// time: a lookup is somewhere to forget, and this decides whether an earlier fact is closed.
	Cardinality Cardinality
	ValidFrom   time.Time
}

// Evidence is the stored receipt for a fact: which message, which bytes, which extractor.
type Evidence struct {
	SourceObservationID string
	SourceOrdinal       int
	Quote               string
	ByteStart           int
	ByteEnd             int
	ExtractorVersion    string
	// Context is the words around the quote. It is what makes the receipt legible rather than only
	// verifiable, and it is empty when the message it would come from is gone.
	Context Context
}

// ── What a citation carries ───────────────────────────────────────────────────────────────────
//
// A quote and a byte span are exactly enough to verify a fact and not enough to understand it. A
// span reading "Dublin" proves the word was in the message and says nothing about whether the
// sentence asserted it, hedged it or denied it — so a reader who wants to check a fact has to go and
// fetch the message, and there is no path that returns one.
//
// The words either side are already stored, already scoped and already erased with the message they
// belong to. Returning them is not a retrieval decision: nothing ranks, nothing is searched, and no
// message becomes reachable that was not already reachable through the fact it produced. It is the
// difference between a receipt and a legible receipt.

// Fanout is the most relations followed out of any one entity, per direction, on a single hop.
//
// # Why there is a cap at all
//
// A second hop multiplies. An anchor with ten thousand facts reaches ten thousand entities, and
// expanding each of those is the query that takes the read path down — which is the first bottleneck
// a traversal has, and it is a property of hub entities rather than of corpus size. The cap makes the
// work bounded by depth rather than by whatever the busiest entity in the project happens to be.
//
// # Why this number, and what it costs
//
// Sixty-four either side of an entity, so one hop from one anchor reads at most 128 rows and two hops
// at most about sixteen thousand — a bounded query on any corpus. What it costs is completeness at a
// hub: a fact reachable only through the hundredth relation of a busy entity is not reached. That is
// an arbitrary cut like the budget's, and the bundle says when it was made rather than pretending the
// neighbourhood was smaller than it was.
//
// It is a constant because nothing has asked to vary it and because the number that would justify
// changing it is a measurement nobody has taken yet — the corpus sizes in the scaling work are where
// it comes from, not from a preference.
const Fanout = 64

// ContextWindow is how many bytes either side of a quote a citation carries.
//
// # Why a window rather than the message
//
// The whole message is the obvious answer and it is the wrong one twice over. A bundle whose facts
// each carry a paragraph is passages with facts attached, which inverts the product — and it would
// smuggle in passage retrieval through the citation, where it arrives with no measurement behind it
// and no way for a caller to ask for it or not. The window is bounded, so what a fact costs does not
// depend on how long the message it came from was.
//
// # Why this number
//
// Around 160 bytes is a sentence or two of English either side: enough to see the clause the quote
// sits in and the one before it, which is what tells a reader whether the sentence asserted the
// thing. It is a constant rather than a setting because nothing has asked to vary it, and a setting
// is a default nobody tunes plus a branch nobody exercises. What a caller does control is the
// character budget, which counts this — so a caller who cannot afford context gets fewer facts
// rather than a knob to find.
const ContextWindow = 160

// Context is the words around a quote, taken from the message the quote indexes into.
type Context struct {
	// Text is the window, cut at word boundaries. Empty when the message is gone.
	Text string
	// ByteStart is where Text begins in the message, so a reader can place the quote inside it:
	// the quote starts at Evidence.ByteStart - ByteStart of Text.
	ByteStart int
	// Complete says the window IS the whole message, so a reader knows whether anything was left
	// out. Without it, a window that happens to fit and one that was cut are indistinguishable —
	// and a reader who assumes the first has read a sentence that continues.
	Complete bool
}

// RawWindow is a window cut around a quote before it has been made readable.
//
// It arrives as bytes because the cut is made by byte arithmetic in the database — the span is bytes,
// and cutting there is what keeps a hundred-kilobyte message from crossing the wire to produce three
// hundred bytes of context. Bytes cut at arbitrary offsets split runes and words, which is what
// Legible repairs.
type RawWindow struct {
	// Bytes is the window as cut, Start is where it begins in the message.
	Bytes []byte
	Start int
	// QuoteStart and QuoteEnd are the span, in the message's coordinates rather than the window's.
	QuoteStart int
	QuoteEnd   int
	// MessageBytes is the length of the whole message, which is the only way to know whether the
	// window reached its end.
	MessageBytes int
}

// Legible turns a raw window into context a person can read.
//
// Two repairs, both only where the cut was made. A cut edge lands mid-rune, which would put a
// replacement character in a receipt; and it lands mid-word, which reads as a typo in the stored
// text rather than as an elision. Neither repair touches the quote: the trimming is bounded to the
// bytes before the span and the bytes after it, so the words that were cited always survive.
//
// An edge that was NOT cut is left exactly as it is. The first word of a message is not trimmed to
// look like an elision, which is what makes Complete worth reporting.
func (w RawWindow) Legible() Context {
	qs, qe := w.QuoteStart-w.Start, w.QuoteEnd-w.Start
	if len(w.Bytes) == 0 || qs < 0 || qe > len(w.Bytes) || qs > qe {
		// The window does not contain the quote it was cut around, so nothing here can be shown as
		// the context for it. Returning empty is the honest answer; returning the bytes would be a
		// receipt pointing at the wrong words while looking correct.
		return Context{}
	}

	lo, hi := 0, len(w.Bytes)
	elidedBefore := w.Start > 0
	elidedAfter := w.Start+len(w.Bytes) < w.MessageBytes
	if elidedBefore {
		lo = wordStartIn(w.Bytes[:qs])
	}
	if elidedAfter {
		hi = qe + wordEndIn(w.Bytes[qe:])
	}
	return Context{
		Text:      string(w.Bytes[lo:hi]),
		ByteStart: w.Start + lo,
		Complete:  !elidedBefore && !elidedAfter,
	}
}

// wordStartIn returns where the first whole word begins in a prefix whose start was cut.
//
// A prefix with no whitespace at all is one long word the cut landed inside, and dropping it would
// throw away every byte of context there is. So the fallback keeps it, minus a rune the cut split.
func wordStartIn(prefix []byte) int {
	i := bytes.IndexFunc(prefix, unicode.IsSpace)
	if i < 0 {
		return firstWholeRune(prefix)
	}
	return len(prefix) - len(bytes.TrimLeftFunc(prefix[i:], unicode.IsSpace))
}

// wordEndIn returns where the last whole word ends in a suffix whose end was cut.
func wordEndIn(suffix []byte) int {
	i := bytes.LastIndexFunc(suffix, unicode.IsSpace)
	if i < 0 {
		return lastWholeRune(suffix)
	}
	return len(bytes.TrimRightFunc(suffix[:i], unicode.IsSpace))
}

// firstWholeRune and lastWholeRune drop the bytes of a rune the cut split.
//
// A genuine U+FFFD in the stored text decodes as a three-byte error and is kept; only the one-byte
// decode failure — which is what a split rune produces — is dropped, and at most one rune's worth,
// so text that is not UTF-8 at all is returned as it is rather than eaten.
func firstWholeRune(b []byte) int {
	start := 0
	for i := 0; i < utf8.UTFMax && start < len(b); i++ {
		r, size := utf8.DecodeRune(b[start:])
		if r != utf8.RuneError || size > 1 {
			break
		}
		start += size
	}
	return start
}

func lastWholeRune(b []byte) int {
	end := len(b)
	for i := 0; i < utf8.UTFMax && end > 0; i++ {
		r, size := utf8.DecodeLastRune(b[:end])
		if r != utf8.RuneError || size > 1 {
			break
		}
		end -= size
	}
	return end
}

// Entity is a thing facts are about.
//
// Identity is (scope, NormalizedName) and deliberately not the type: with the type in the key, one
// thing extracted once as a place and once as a thing becomes two nodes holding two halves of what
// is known about it — a silent under-merge on every hop, invisible because both rows look correct.
type Entity struct {
	ID             string
	Scope          string
	CanonicalName  string
	NormalizedName string
	Type           string
}

// NormalizeName is the resolution key: lowercase, trim, collapse internal whitespace.
//
// Deliberately nothing cleverer. Merging "the Dublin office" with "our Dublin site" needs either a
// similarity threshold nobody can justify or a model call per entity. Over-merging puts two people's
// facts on one node, which is a governance failure; under-merging costs recall on a hop. That
// asymmetry sets the default, and meaning arrives later as a measurable surface rather than as a
// guess applied irreversibly at write time.
func NormalizeName(name string) string {
	return strings.ToLower(strings.Join(strings.Fields(name), " "))
}

// ── The relation vocabulary ───────────────────────────────────────────────────────────────────

// Cardinality is how many facts with one subject and one predicate may be currently valid at once.
//
// It is declared when a predicate is defined, because by the time supersession runs all that is
// visible is two rows and the question is no longer answerable from them.
type Cardinality string

const (
	// CardinalityOne means a new fact closes the previous one: a person lives in one place, and
	// recording the second without closing the first stores a contradiction as two beliefs.
	CardinalityOne Cardinality = "one"
	// CardinalityMany means several coexist, and only a repetition of the same object is a
	// duplicate.
	CardinalityMany Cardinality = "many"
)

// Predicate is one relation in the closed vocabulary.
//
// The authority is the `predicate` table, which `fact.predicate` references — so this is a copy of
// what the database already enforces, held in memory because the extractor has to put the vocabulary
// in a prompt and cannot issue a query per candidate relation.
type Predicate struct {
	Name         string
	SemanticType string
	Cardinality  Cardinality
	// ObjectKind is what the object end should be: person, organisation, place, time, value or
	// thing. What an extractor is told to look for, and what an implausible extraction is judged
	// against — a `time` object that came back as somebody's name is a bad extraction, not a fact.
	ObjectKind  string
	Description string
	// Event marks a relation whose natural tense is the past and which stays true once it has
	// happened: "voters adopted an amendment" is complete and remains a fact. For a state relation
	// the past tense means the relation ended, and admitting it would write a current fact from an
	// ended one; for an event it means the event occurred, which is the only way an event is ever
	// reported. The extractor admits past tense on events and refuses it on states.
	Event bool
}

// Ontology is the vocabulary, in the order the database holds it.
//
// There is no constructor from a Go literal, deliberately. Two definitions of a closed set is two
// places for it to drift, and the one that would drift is whichever is not enforced by a foreign
// key.
type Ontology struct {
	ordered []Predicate
	byName  map[string]Predicate
}

// NewOntology builds the vocabulary from rows read out of the predicate table.
func NewOntology(predicates []Predicate) (Ontology, error) {
	if len(predicates) == 0 {
		// An empty vocabulary admits nothing, so every extraction would be refused and the failure
		// would look like a bad model rather than an unmigrated tenant.
		return Ontology{}, fmt.Errorf("the relation vocabulary is empty")
	}
	byName := make(map[string]Predicate, len(predicates))
	for _, p := range predicates {
		if _, dup := byName[p.Name]; dup {
			return Ontology{}, fmt.Errorf("relation %q appears twice", p.Name)
		}
		byName[p.Name] = p
	}
	return Ontology{ordered: predicates, byName: byName}, nil
}

// Lookup returns the predicate a relation names, and whether the vocabulary admits it.
func (o Ontology) Lookup(name string) (Predicate, bool) {
	p, ok := o.byName[name]
	return p, ok
}

// All returns the vocabulary in a stable order, which is what a prompt is built from. Stable so that
// two extractions of the same message are given the same list — a prompt that reorders is a prompt
// that produces different output for the same input, and extraction quality becomes unmeasurable.
func (o Ontology) All() []Predicate { return o.ordered }

// Len reports how many relations the vocabulary admits.
func (o Ontology) Len() int { return len(o.ordered) }

// ── What extraction refused ───────────────────────────────────────────────────────────────────

// Why a proposed claim did not become a fact. The set is closed by a check constraint in the schema,
// so a reason no reader knows about cannot be written — and each member has to mean a genuinely
// different thing, because a record whose reasons overlap cannot be counted.
const (
	// ReasonUnmappedRelation — the relation is not in the vocabulary. The claim may well be true;
	// there is simply no edge type for it, and admitting one relation outside the set makes the set
	// advisory.
	ReasonUnmappedRelation = "unmapped_relation"
	// ReasonUnlocatableQuote — the quote is not in the message. Usually a paraphrase, occasionally
	// an invention. Either way there is nothing to cite.
	ReasonUnlocatableQuote = "unlocatable_quote"
	// ReasonDuplicateClaim — the same relation between the same two ends, already produced by this
	// message. The claim is not wrong and not uncitable; it is already recorded, and admitting it
	// again would spend one of a bundle's rows on a repeat of a fact the caller already has.
	ReasonDuplicateClaim = "duplicate_claim"
	// ReasonNotAsserted — the message contains the words but does not assert the relation. A denial,
	// a hedge, a hypothetical, or somebody else's statement being reported.
	//
	// It is the only refusal whose alternative is writing a FALSE fact rather than losing a true
	// one. The other three are absences, and an absence is visible: a question gets a thin answer.
	// This one would be a presence — cited, current, and indistinguishable from a correct fact by
	// every mechanism downstream.
	ReasonNotAsserted = "not_asserted"
	// ReasonUnresolvableSubject — the subject names nothing the message establishes. A pronoun or a
	// demonstrative whose referent is outside the message.
	//
	// Measured: `Thanks, that worked perfectly.` produced `that has_status worked perfectly`, with a
	// verbatim quote and an exact span. Every check in the path is about form, and this one is about
	// whether there is anything to be about.
	ReasonUnresolvableSubject = "unresolvable_subject"
	// ReasonNotCurrent — the message asserts the relation and puts it in the past. "I used to live
	// in Amman."
	//
	// Refused rather than stored, because storing it means inventing an interval. What the sentence
	// licenses is that the relation ended somewhere at or before this message; it says nothing about
	// when it began, and `valid` cannot be left unbounded below — every read is containment, so an
	// unbounded start answers "did they live there in 1990" with yes, on evidence nobody has.
	//
	// The second refusal whose alternative is a FALSE fact rather than a missing one, and the worse
	// of the two: a denial at least reads as absent, while this would come back as the answer to
	// "where do they live" with a verbatim quote behind it.
	ReasonNotCurrent = "not_current"
	// ReasonConflictingValue — the message asserts a second current value for a single-cardinality
	// relation it has already given a value in this same message, and the two share one occurrence
	// time, so neither can supersede the other. "X was appointed director. Y is the director."
	//
	// The exclusion constraint refuses the second row; that is G4 working. What makes this a refusal
	// rather than a failure is that the claim was genuinely made and cannot be stored beside its
	// rival, which is exactly what a duplicate or a paraphrase is: an outcome with a reason, not a
	// transport error to retry six times and park. The first value in message order wins.
	ReasonConflictingValue = "conflicting_value"
	// ReasonNotSpokenByPrincipal — the claim speaks in the first person, and the message it came
	// from was not the principal speaking. An assistant's "so you work at Ensera" is the assistant's
	// inference, not the person's statement, and a fetched document's "I" is somebody else's.
	//
	// Recorded as a row rather than only counted, because a corpus of documents refuses many of these
	// and an operator reading the refusals should see them beside the others. The refusal is about the
	// speaker reference, never about the role alone: the same message's claims about named third
	// parties are admitted under the message's own role, which recall separates from user statements.
	ReasonNotSpokenByPrincipal = "not_spoken_by_principal"
	// ReasonEntityNameLimit — one end of the claim carries a name the store cannot keep: longer than
	// the bound, or one spelling too many for a single entity.
	//
	// A refusal rather than a failed turn, because nothing about it is transient. Retrying sends the
	// same name to the same bound six times and parks a turn whose other claims were ordinary. The
	// claim is lost and said to be lost, which is the outcome every other member of this set
	// describes.
	ReasonEntityNameLimit = "entity_name_limit"
)

// Vocabulary is the closed sets an extractor is given, read from the tables that enforce them.
//
// Two definitions of a closed set is two places for it to drift, and the one that drifts is whichever
// is not enforced — so both of these come from the database rather than from a literal here.
type Vocabulary struct {
	Ontology Ontology
	// Unresolvable holds terms that cannot be a subject, normalised the way entity names are, so the
	// comparison is the one the resolver would have made.
	Unresolvable map[string]struct{}
}

// CanBeSubject reports whether a name could resolve to something the message established.
func (v Vocabulary) CanBeSubject(name string) bool {
	_, unresolvable := v.Unresolvable[NormalizeName(name)]
	return !unresolvable
}

// Polarity is what a claim's own message does with it.
//
// Reported by the extractor's model, and treated as a gate rather than as data: anything that is not
// a plain present assertion is refused and written down. That is deliberately a smaller promise than
// storing the distinction — a fact carrying "this was denied" is one every read path has to remember
// to filter, and the one that forgets returns the negation as an assertion.
type Polarity string

const (
	// PolarityAsserted — the message states the relation as being so.
	PolarityAsserted Polarity = "asserted"
	// PolarityDenied — the message denies it. "I do not live in London."
	PolarityDenied Polarity = "denied"
	// PolarityUncertain — the message hedges. "I think John works at Acme."
	PolarityUncertain Polarity = "uncertain"
	// PolarityHypothetical — the message supposes or intends it. "If I moved to Amman…"
	PolarityHypothetical Polarity = "hypothetical"
	// PolarityReported — somebody else said it. `John said: "I live in London."`
	PolarityReported Polarity = "reported"
)

// Asserts reports whether a claim with this polarity may become a fact.
//
// Written as an allowlist of one rather than a denylist of four, because the failure mode of a
// denylist is a value nobody anticipated passing through — and the values here come from a model,
// which is precisely where an unanticipated value comes from.
func (p Polarity) Asserts() bool { return p == PolarityAsserted }

// Tense is when the message puts the relation. The set is closed, and only one value becomes a fact.
//
// # Why this is separate from polarity
//
// They are independent and both have to be right. "I do not live in London" is asserted-about-nothing;
// "I used to live in Amman" is asserted, positively, about a period that is over. A single field
// mixing them would make a model choose between two true answers, and the one it dropped would be the
// one nobody was checking.
type Tense string

const (
	// TensePresent — the relation holds as the message is written. "I live in Dublin."
	TensePresent Tense = "present"
	// TensePast — it held and does not now. "I used to work at Acme."
	TensePast Tense = "past"
	// TenseFuture — it is expected to hold and does not yet. "I am moving to Berlin in March."
	//
	// A stated plan is a present-tense intention rather than a future-tense relation: what holds now
	// is the intending. This value is for a model that reports the relation itself as future, and it
	// is refused for the same reason the past is — the date it would start is one nobody has.
	TenseFuture Tense = "future"
)

// IsCurrent reports whether a relation with this tense may become a currently-valid fact.
//
// An allowlist of one, for the reason the polarity allowlist is one long: the values arrive from a
// model, which is exactly where an unanticipated value comes from, and the failure of a denylist is
// the value nobody anticipated passing through. An omitted tense is not "present" — it is a model
// that did not answer, and a fact nobody vouched for is the thing this refuses to write.
func (t Tense) IsCurrent() bool { return t == TensePresent }

// RejectedClaim is a proposal that did not become a fact, kept with the words that were proposed.
//
// Kept rather than dropped because both refusals are silent and both are claims about quality that
// nobody can check without the text. A rising unmapped rate is the evidence that the vocabulary is
// wrong. A rising unlocatable rate is the evidence that the model is paraphrasing — which is
// invisible from the claims that survived, because the survivors all look perfect.
//
// It carries somebody's verbatim words, so it is a projection like any other and erasure covers it.
type RejectedClaim struct {
	Predicate     string
	Statement     string
	Quote         string
	Reason        string
	SourceOrdinal int
}

// ── The read side ─────────────────────────────────────────────────────────────────────────────

// Anchor is an entity a question resolved to, with the words that resolved to it.
//
// The matched text is carried because an anchor a caller did not expect is the first thing to look
// at when a bundle is wrong, and without it the only way to find out why an entity was chosen is to
// re-run the resolution by hand.
type Anchor struct {
	Entity  Entity
	Matched string
}

// DegradedCitationContext says at least one returned fact came back without the words around its
// quote, because the message those words are in is gone.
//
// A named constant rather than a string at the point it is set, so the reason a caller matches on is
// the reason the code produces. The fact itself is unaffected — its quote and span are what they
// were — and it is reported because a citation that is suddenly only a span looks like a product
// that stopped explaining itself rather than like an erasure that did its job.
const DegradedCitationContext = "citation_context"

// The three composed surfaces a recall consults after exact anchoring. Each is named here so
// a bundle can say which one did not run: because the deployment has no embedding revision, because
// the caller left it out of `surfaces`, or because the surface's own retriever refused.
const (
	DegradedSemanticAnchors = "semantic_anchors"
	DegradedReports         = "reports"
	// DegradedReportsWithheld says reports were not consulted because the recall was narrowed to one
	// person. A report summarises a community of entities across everybody the project holds and
	// records nobody's words as its source, so it cannot honestly be narrowed to one of them.
	DegradedReportsWithheld = "reports_withheld_for_subject"
	DegradedPassages        = "passages"
)

// ReportHit is a community report reached by theme rather than by anchor: the written summary of a
// group of entities the graph found densely connected, carrying the observations it was registered
// against so a caller can cite the words behind it rather than trust the paragraph.
type ReportHit struct {
	CommunityID string
	ReportID    string
	Title       string
	Summary     string
	Importance  float32
	Similarity  float64
	Level       int
	// Parent is the community this report was reached from when it is a child substituted into
	// the answer, and empty for a report the question matched directly.
	Parent  string
	Sources []string
}

// Passage is a source message reached by similarity. It is evidence and never a fact: it carries
// the role that said it, its identity and offsets, and a bounded quote, and nothing downstream may
// treat it as an assertion about anything.
type Passage struct {
	ChunkID    string
	SourceID   string
	Ordinal    int
	Role       string
	Quote      string
	Similarity float64
	OccurredAt time.Time
	Complete   bool
}

// AsOf is the pair of instants a bitemporal read is taken at.
//
// # Why two times and not one
//
// A fact has two independent histories: when it was true of the world, and when this system believed
// it. They come apart on every correction — we learn on Friday that somebody moved in March — and a
// memory that keeps only one of them cannot answer why it said what it said last week. So a read
// names both: what was true at Valid, as this system understood things at Known.
//
// # Why the zero value is now
//
// The overwhelmingly common read is the current one, and a caller that has said nothing about time
// is asking about now. A zero AsOf is that read, and it is the one the ordinary recall issues.
type AsOf struct {
	// Valid is the instant the world is asked about. Zero means now.
	Valid time.Time
	// Known is the instant this system's belief is asked about — facts recorded after it are not
	// returned, whatever we now know. Zero means now.
	Known time.Time
}

// HasValid and HasKnown say which halves the caller named. An unnamed half is NOT a timestamp of
// now: it is the open interval, which is a different thing and the only correct one.
//
// Substituting this process's clock for "now" was tried and is wrong. The database stamps `known`
// with its own clock, and the two are not the same clock — measured 82ms apart between a server
// process and a database container on one machine. A fact recorded a moment ago then falls outside
// `known @> ourNow`, so a caller writes something and a read that named only a validity time cannot
// see it. The open interval asks the question the caller meant — what we believe now — and involves
// no clock at all.
func (a AsOf) HasValid() bool { return !a.Valid.IsZero() }
func (a AsOf) HasKnown() bool { return !a.Known.IsZero() }

// IsNow reports whether this is the ordinary current read.
func (a AsOf) IsNow() bool { return !a.HasValid() && !a.HasKnown() }

// CitedFact is a fact with the receipt for it. There is no uncited variant, and that is deliberate:
// a fact whose evidence cannot be produced is not something this product returns.
type CitedFact struct {
	ID         string
	Scope      string
	Subject    string
	Predicate  string
	Object     string
	Statement  string
	Confidence float32
	// ValidFrom and ValidUntil are when this was true of the world. ValidUntil is nil for a fact
	// nothing has superseded, and a time for one that has been — which is what makes a historical
	// read legible: a caller asking about March gets the fact that held in March along with the
	// date it stopped holding, rather than a fact that looks current.
	ValidFrom  time.Time
	ValidUntil *time.Time
	Evidence   Evidence
	// Hops is how many relations away from the question this fact was found. One is directly about
	// something the question named.
	Hops int
	// Path and Via are the chain that reached it, and they are the reason a multi-hop answer can be
	// believed: Marta → reports_to → Hamza → works_at → Ensera is something a reader can check,
	// where the same fact with a distance score attached is something they have to trust.
	//
	// Path holds the entity names in order and Via the relations between them, so Path has exactly
	// one more member than Via. Both are empty for a one-hop fact, where the chain would be a single
	// name and no relations.
	//
	// An entity erased since the fact was written appears as an empty name rather than removing a
	// step, because a chain with a step missing reads as a shorter chain, which is a different claim.
	Path []string
	Via  []string
	// AnchoredOn is the entity that brought this fact into the bundle. A fact can name two anchors;
	// this is the one it was reached through, which is what makes the path a caller can inspect.
	AnchoredOn string
	// CoDerivedWith names the other facts in this bundle that came from the SAME message and are
	// about the SAME subject.
	//
	// # What this is for
	//
	// One sentence can produce several facts at different granularities — a preference stated
	// broadly and the same preference stated as a rule. They are different triples, so nothing can
	// collapse them: no key to be unique on, and no threshold that separates a restatement from a
	// genuinely different claim. Measured elsewhere on an embedder's own held-out set, near misses
	// reached higher similarity than true pairs began at, so the obvious automatic answer does not
	// work and a word-overlap rule is a guess wearing a mechanism's clothes.
	//
	// What CAN be established, exactly and without a threshold, is that two facts are not
	// independent: they came from one message about one subject. That is the harm — a reader seeing
	// two rows and weighing them as two sources, when one person said one thing once.
	//
	// So nothing is discarded and nothing is merged. The relationship is reported, and what a caller
	// does with it is theirs. It does not claim the facts say the same thing, because they may not.
	CoDerivedWith []string
	// SourceRole is the role of the message this fact was extracted from.
	//
	// It travels with the fact to the caller, always, because a fact shown without saying it came
	// from a fetched page asks the reader to trust our sources as if they were their user's words.
	// A recall returns only what the principal said unless the caller asks for more — and a caller
	// who asks is told which is which rather than given a merged list.
	SourceRole string
}

// Bundle is what a recall returns.
type Bundle struct {
	Anchors []Anchor
	Facts   []CitedFact
	// Reports and Passages are the composed surfaces: themes the graph already holds, and
	// source words by similarity. Both are charged to the same budget as the facts and both are
	// labelled for what they are; neither is a fact.
	Reports  []ReportHit
	Passages []Passage
	// Truncated says the limit cut the result. It matters because the order carries no judgment:
	// nothing here has ranked the facts, so a truncated bundle is missing arbitrary members rather
	// than the least relevant ones, and a caller that does not know that will read the cut as a
	// verdict.
	Truncated bool
	// Characters is what the returned facts cost, in the unit the cut was made in.
	//
	// Reported so a caller can see how much of their budget was used, and so a caller who asked for
	// more than they can afford finds out from the answer rather than from their model.
	Characters int
	// Degraded names the parts of the retrieval that did not run.
	//
	// Empty today because the path either works or errors: anchors and facts are both required, and
	// a failure in either is a failure of the whole. It is here before it is needed because it
	// cannot be added afterwards — the moment anything in the path can fail on its own, a bundle
	// that silently comes back smaller is indistinguishable from a scope with less in it, and a
	// caller reads a dead dependency as an empty memory. Adding a field to a published contract is a
	// version; adding it now is free.
	//
	// The reason a degraded path returns a smaller bundle rather than an error: memory enhances a
	// turn and is not a precondition for one. An agent handed less keeps working; an agent handed an
	// error stops.
	Degraded []string
	// Reach says how the bundle was arrived at: what the question offered, what of it was found, and
	// where the facts came from.
	//
	// It exists because an empty bundle and a full one can both be the wrong answer for reasons a
	// caller cannot see. Asked something the memory holds nothing about, retrieval returns whatever
	// it can reach — every mechanism that orders candidates says which are BEST and none says
	// whether any is GOOD. Entity anchoring makes the worst version of that impossible: a question
	// naming nothing known reaches nothing. What it does not prevent is a question that names
	// something known and then asks about something else, which comes back full and plausible.
	//
	// So the bundle reports the shape it was built from rather than a verdict on it. See Reach.
	Reach Reach
}

// Reach is what a recall had to work with.
//
// # Why this and not a relevance score
//
// The obvious answer is a verdict — strong, weak, none — derived from how the top candidate compares
// to the rest. That needs candidates to be scored, and nothing here scores them: recall returns facts
// in a deterministic order that carries no judgement, because a ranking stage introduced before the
// plain path is measured makes it impossible to say afterwards which stage is doing the work and
// which is doing harm.
//
// A verdict is a ranking judgement wearing a different name, and it would arrive with the same
// problem: a threshold nobody can justify. And an absolute similarity floor is measured not to work —
// on an embedder's own held-out set, near misses reached higher similarity than true pairs began at,
// so no cutoff separates them.
//
// What is exact and needs no threshold is the SHAPE: how many candidate names the question offered,
// how many were found in memory, and how the facts distribute across them. A question that offered
// six names, matched one, and drew every fact from it is recognisably a question that named something
// and asked about something else — and a caller can see that without anybody choosing a number.
type Reach struct {
	// Terms is how many candidate names the question offered.
	Terms int
	// Anchored is how many of them were found. Zero means the question named nothing this memory
	// holds, which is a different answer from holding the thing and knowing nothing about it — and
	// the two are indistinguishable in an empty bundle unless this says so.
	Anchored int
	// FactsPerAnchor is how many facts each anchor contributed, keyed by entity id. Everything
	// arriving from one anchor when several matched is the signature worth seeing.
	FactsPerAnchor map[string]int
}

// NamedNothingKnown reports that the question offered candidates and none of them are in this memory.
//
// Worth its own answer: "we have never heard of what you asked about" and "we know that thing and
// have nothing to say about it" are different, and a caller that cannot tell them apart cannot decide
// whether to write something or ask differently.
func (r Reach) NamedNothingKnown() bool { return r.Terms > 0 && r.Anchored == 0 }

// ── Erasure ───────────────────────────────────────────────────────────────────────────────────

// Erasure is the receipt for a deletion: what was asked, what went, and what survived.
//
// The residual is the product. An erasure that reports "done" is a claim; one that reports how many
// rows matching the same predicate are still there, counted after the delete in the same
// transaction, is a measurement — and it is the measurement a data protection officer is actually
// asking for.
type Erasure struct {
	RequestID string
	Scope     string
	// DataSubjectID names the person an erasure was for; SourceObservationIDs names the observations
	// one was for instead. Exactly one is set: a receipt says what was asked, and it was one thing.
	DataSubjectID        string
	SourceObservationIDs []string
	Reason               string
	CompletedAt          time.Time
	// Deleted and Residual are per projection kind, and both are needed. Deleted alone cannot
	// distinguish a thorough erasure from one that found nothing. Residual alone cannot distinguish
	// a clean sweep from a subject who was never here.
	Deleted  map[string]int
	Residual map[string]int
}

// Clean reports whether nothing that should have gone survived.
func (e Erasure) Clean() bool {
	for _, n := range e.Residual {
		if n != 0 {
			return false
		}
	}
	return true
}

// ── The audit ledger ──────────────────────────────────────────────────────────────────────────

// Operations the ledger records. Closed, because an operation nobody has heard of cannot be counted,
// and the point of the ledger is that the set of things which happen is knowable.
const (
	AuditEmbeddingStart        = "embedding.start"
	AuditEmbeddingBuild        = "embedding.build"
	AuditEmbeddingActivate     = "embedding.activate"
	AuditEmbeddingCancel       = "embedding.cancel"
	AuditEmbeddingPrune        = "embedding.prune"
	AuditEmbeddingRepair       = "embedding.repair"
	AuditEmbeddingSearch       = "embedding.search"
	AuditEntityEmbeddingSearch = "entity_embedding.search"
	AuditReportEmbeddingSearch = "report_embedding.search"
	AuditObserve               = "observe"
	AuditRecall                = "recall"
	AuditErase                 = "erase"
	AuditFreshness             = "freshness"
	AuditExport                = "export"
	AuditProjectCreate         = "project.create"
	AuditProjectSuspend        = "project.suspend"
	AuditProjectResume         = "project.resume"
	AuditCredentialIssue       = "credential.issue"
	AuditCredentialRevoke      = "credential.revoke"
	AuditFormationUnpark       = "formation.unpark"
	AuditFormationRecover      = "formation.recover"
	AuditFormationRebuild      = "formation.rebuild"
	AuditRebuildStart          = "formation.rebuild.start"
	AuditRebuildCancel         = "formation.rebuild.cancel"
	AuditCitationResolve       = "citation.resolve"
	AuditMessageGet            = "message.get"
	AuditRecordList            = "record.list"
	AuditEntityList            = "entity.list"
	AuditEntityGet             = "entity.get"
	// AuditEntityPurge is withdrawing an entity the extractor should not have made. A write, and
	// recorded as one: it removes claims other people's turns supported.
	AuditEntityPurge = "entity.purge"
	// Notifications. Nominating a destination is the widest egress in the product being
	// chosen, so it is on the ledger like every other write.
	AuditNotificationRegister   = "notification.register"
	AuditNotificationList       = "notification.list"
	AuditNotificationDisable    = "notification.disable"
	AuditNotificationDeliveries = "notification.deliveries"
	AuditContextAssemble        = "context.assemble"
	AuditRecordHistory          = "record.history"
	AuditRecordRetract          = "record.retract"
	AuditRecordCorrect          = "record.correct"
	AuditRecordAssert           = "record.assert"

	// Feedback is a report about a record that asserts nothing. It has its own three names rather
	// than borrowing the record operations' because the ledger has to be able to answer "who
	// decided this" separately from "who complained about it" — and under one name, a promotion and
	// the complaint that prompted it would be indistinguishable afterwards.
	AuditFeedbackRecord    = "feedback.record"
	AuditFeedbackList      = "feedback.list"
	AuditFeedbackPromote   = "feedback.promote"
	AuditArtifactPut       = "artifact.put"
	AuditArtifactGet       = "artifact.get"
	AuditArtifactList      = "artifact.list"
	AuditArtifactDelete    = "artifact.delete"
	AuditArtifactConfigure = "artifact.configure"
	// The management surface: what an operator does to the instance, on the ledger with the
	// operator credential as its principal.
	AuditProjectList     = "project.list"
	AuditCredentialList  = "credential.list"
	AuditRefusalSummary  = "refusal.summary"
	AuditErasureList     = "erasure.list"
	AuditFormationStatus = "formation.status"
	AuditFormationParked = "formation.parked"
	AuditAuditSeal       = "audit.seal"
	AuditAuditVerify     = "audit.verify"
	// AuditAuditActivity is the console reading the ledger's own counts for a window: how many
	// operations, of which kinds, allowed or refused. A read of the ledger is on the ledger like any
	// other read an operator makes.
	AuditAuditActivity   = "audit.activity"
	AuditSubjectRegister = "subject.register"
	AuditSubjectGet      = "subject.get"
	AuditSubjectList     = "subject.list"
	AuditSubjectUpdate   = "subject.update"
	// AuditRetentionSweep is the deployment expiring memory on its own schedule. Its principal is
	// the system: nobody asked for it, the policy did, and the receipt says what went.
	AuditRetentionSweep = "retention.sweep"
	// AuditProjectRetention is an operator setting how long a project keeps what it is told, which
	// is a governance decision and belongs on the ledger beside suspension.
	AuditProjectRetention = "project.retention"
	// AuditAuthenticate records a credential being refused. A successful authentication is recorded
	// as the operation it went on to perform, because a row for every accepted request twice over
	// would double the ledger and say nothing the operation's own row does not.
	AuditAuthenticate = "authenticate"
)

// RecordableOperation says whether the ledger can name an operation.
//
// Exported because the wire surface asks it of every route it declares, at startup, before it will
// serve any of them. An operation the ledger cannot name is one that would either be served without
// a row or refused at the moment somebody used it, and both are worse than refusing to start.
//
// This is the one definition. Validate calls it too, so a name added to the ledger's vocabulary
// becomes servable and a name removed stops being servable, without a second list agreeing to it.
func RecordableOperation(name string) bool {
	switch name {
	case AuditEmbeddingStart, AuditEmbeddingBuild, AuditEmbeddingActivate, AuditEmbeddingCancel, AuditEmbeddingPrune, AuditEmbeddingRepair, AuditEmbeddingSearch, AuditEntityEmbeddingSearch, AuditReportEmbeddingSearch:
		return true
	case AuditEntityList, AuditEntityGet, AuditMessageGet, AuditContextAssemble:
		return true
	case AuditNotificationList, AuditNotificationDeliveries:
		return true
	case AuditRebuildStart, AuditRebuildCancel:
		return true
	case AuditProjectList, AuditCredentialList, AuditRefusalSummary, AuditErasureList, AuditFormationStatus, AuditFormationParked, AuditAuditSeal, AuditAuditVerify, AuditAuditActivity:
		return true
	case AuditRetentionSweep, AuditProjectRetention:
		return true
	case AuditObserve, AuditRecall, AuditErase, AuditFreshness, AuditExport,
		AuditProjectCreate, AuditProjectSuspend, AuditProjectResume,
		AuditEntityPurge, AuditNotificationRegister, AuditNotificationDisable, AuditCredentialIssue, AuditCredentialRevoke, AuditAuthenticate, AuditFormationUnpark, AuditFormationRecover, AuditFormationRebuild, AuditCitationResolve, AuditRecordList, AuditRecordHistory, AuditRecordRetract, AuditRecordCorrect, AuditRecordAssert, AuditFeedbackRecord, AuditFeedbackList, AuditFeedbackPromote, AuditArtifactPut, AuditArtifactGet, AuditArtifactList, AuditArtifactDelete, AuditArtifactConfigure, AuditSubjectRegister, AuditSubjectGet, AuditSubjectList, AuditSubjectUpdate:
		return true
	}
	return false
}

// Who performed an operation.
const (
	PrincipalCredential = "credential"
	PrincipalOperator   = "operator"
	// PrincipalSystem is the driver, the retention sweep, anything the instance does for itself. It
	// is distinguished so that "nobody asked for this" is visible rather than looking like an
	// operator acting at three in the morning.
	PrincipalSystem = "system"
)

// Whether it was allowed. A refused operation is the more interesting row: a repeated refusal is what
// an attack looks like from inside the ledger.
const (
	OutcomeAllowed = "allowed"
	OutcomeRefused = "refused"
)

// AuditEntry is one thing that happened.
//
// It holds no data subject, no question, no quote and no content — deliberately, and that is what
// makes the ledger append-only without conflicting with erasure. An audit trail an erasure can delete
// is not one; an audit trail an erasure cannot touch is personal data surviving an erasure. Recording
// nothing that could be asked about avoids both.
//
// The cost: a person can be told that a recall happened against a project, by which credential, and
// when — never which of their memories it read.
type AuditEntry struct {
	Operation string
	// Principal is an identifier that resolves in the registry — a credential id or a session id.
	// Never a name or an address, because those are personal data and this record holds none.
	Principal     string
	PrincipalKind string
	// Project is empty for instance-level operations.
	Project string
	// Magnitude is how much the operation touched: facts returned, rows deleted, messages appended.
	// A number rather than a description, because a description summarises content.
	Magnitude  int
	Outcome    string
	OccurredAt time.Time
}

// Validate refuses an entry that could not be counted.
//
// Checked before the insert as well as by the schema, so that the caller gets the reason rather than
// a constraint violation four frames down naming a column.
func (e AuditEntry) Validate() error {
	if !RecordableOperation(e.Operation) {
		return fmt.Errorf("audit entry names operation %q, which is not one the ledger records", e.Operation)
	}
	switch e.PrincipalKind {
	case PrincipalCredential, PrincipalOperator, PrincipalSystem:
	default:
		return fmt.Errorf("audit entry names principal kind %q", e.PrincipalKind)
	}
	if e.Outcome != OutcomeAllowed && e.Outcome != OutcomeRefused {
		return fmt.Errorf("audit entry has outcome %q", e.Outcome)
	}
	if strings.TrimSpace(e.Principal) == "" {
		// An unattributed row is the one thing this table cannot hold: the whole point is that every
		// operation has somebody behind it.
		return fmt.Errorf("audit entry has no principal, and an unattributed operation is what this record exists to prevent")
	}
	if e.Magnitude < 0 {
		return fmt.Errorf("audit entry has a negative magnitude")
	}
	return nil
}

// Export is everything held about one person in one project.
//
// The other half of the pair erasure belongs to: a system that can prove what it deleted and cannot
// produce what it holds has answered half the question, and the missing half is the one asked first.
//
// Sections are keyed by projection kind, plus the messages every projection derives from. Rows are
// raw JSON rather than typed structures on purpose: an export has to include a column added by a
// migration without anybody remembering to add it here, and a typed shape is exactly the place that
// would be forgotten.
type Export struct {
	Scope string
	// DataSubjectID names the person an export was for; SourceObservationIDs names the turns one was
	// for instead. Exactly one is set, as on an erasure receipt: an export says what was asked.
	DataSubjectID        string
	SourceObservationIDs []string
	// Sections is empty-not-absent per kind: "we hold none of this" and "we did not look" are
	// different statements, and only one of them is true.
	Sections map[string][]json.RawMessage
}

// Rows counts what an export contains, per section.
//
// The number an export is checked against: an erasure receipt reports what it deleted per kind, and
// the two walks agreeing is what says neither has forgotten a projection.
func (e Export) Rows() map[string]int {
	out := make(map[string]int, len(e.Sections))
	for kind, rows := range e.Sections {
		out[kind] = len(rows)
	}
	return out
}

// RetentionSweep is what a retention run removed.
//
// A receipt rather than a log line, for the same reason an erasure produces one: a sweep that deletes
// quietly is indistinguishable from data loss, and "memory disappeared" has to be answerable rather
// than alarming.
type RetentionSweep struct {
	// ID is the receipt this sweep wrote, empty when it deleted nothing and wrote none.
	ID    string
	Scope string
	// Observations is how many turns expired. Deleted is what went per projection kind, which is the
	// same shape an erasure receipt reports so the two can be read the same way.
	Observations int
	Deleted      map[string]int
	SweptAt      time.Time
}

// Empty reports whether the sweep found nothing to do, which is the ordinary case.
func (s RetentionSweep) Empty() bool {
	if s.Observations != 0 {
		return false
	}
	for _, count := range s.Deleted {
		if count != 0 {
			return false
		}
	}
	return true
}

// AuditSeal is a chained digest over a range of ledger entries.
type AuditSeal struct {
	From, To int64
	Entries  int
	Digest   []byte
}

// AuditVerification is what an operator's own check found.
//
// It reports what is NOT covered as well as what is, because a verification that says only "valid"
// while a window of recent entries is unsealed is a reassurance rather than a check.
type AuditVerification struct {
	Valid   bool
	Seals   int
	Entries int
	// Unsealed is how many entries were written since the last seal. They are not covered by
	// anything, and the number is the size of the window somebody could have written into.
	Unsealed int
	// Head is the digest at the end of the chain. Worth recording somewhere the database cannot
	// reach: a rebuilt ledger cannot produce a head that agrees with one written down earlier.
	Head []byte
	// Failure says which seal disagreed and how — an altered entry and a re-linked chain are
	// different accusations, and a verifier that says only "invalid" tells nobody what happened.
	Failure string
}
