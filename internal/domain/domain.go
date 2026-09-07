// Package domain holds the vocabulary the read and write paths share.
//
// No I/O, no clock, no database. A type here is a thing the product talks about, and the rule for
// admitting one is that both sides of the system need to name it — anything only one side needs
// belongs to that side.
package domain

import (
	"fmt"
	"strings"
	"time"
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
// Called before anything is written, so a rejected turn leaves nothing behind. Every rule here is
// also a database constraint; this exists so the caller gets a sentence rather than a
// constraint-violation error naming a table they have never heard of.
func (t Turn) Validate() error {
	if strings.TrimSpace(t.Scope) == "" {
		return fmt.Errorf("turn has no scope")
	}
	if len(t.Messages) == 0 {
		return fmt.Errorf("turn has no messages")
	}
	seen := make(map[int]bool, len(t.Messages))
	for i, m := range t.Messages {
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
	// Stored is the highest offset appended in this scope.
	Stored int64
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
	ValidFrom     time.Time
}

// Evidence is the stored receipt for a fact: which message, which bytes, which extractor.
type Evidence struct {
	SourceObservationID string
	SourceOrdinal       int
	Quote               string
	ByteStart           int
	ByteEnd             int
	ExtractorVersion    string
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
)

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
	ValidFrom  time.Time
	Evidence   Evidence
	// AnchoredOn is the entity that brought this fact into the bundle. A fact can name two anchors;
	// this is the one it was reached through, which is what makes the path a caller can inspect.
	AnchoredOn string
}

// Bundle is what a recall returns.
type Bundle struct {
	Anchors []Anchor
	Facts   []CitedFact
	// Truncated says the limit cut the result. It matters because the order carries no judgment:
	// nothing here has ranked the facts, so a truncated bundle is missing arbitrary members rather
	// than the least relevant ones, and a caller that does not know that will read the cut as a
	// verdict.
	Truncated bool
}

// ── Erasure ───────────────────────────────────────────────────────────────────────────────────

// Erasure is the receipt for a deletion: what was asked, what went, and what survived.
//
// The residual is the product. An erasure that reports "done" is a claim; one that reports how many
// rows matching the same predicate are still there, counted after the delete in the same
// transaction, is a measurement — and it is the measurement a data protection officer is actually
// asking for.
type Erasure struct {
	RequestID     string
	Scope         string
	DataSubjectID string
	Reason        string
	CompletedAt   time.Time
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
