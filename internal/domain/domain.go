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
	LogOffset  int64
	Scope      string
	OccurredAt time.Time
	IngestedAt time.Time
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
