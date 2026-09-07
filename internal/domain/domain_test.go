package domain

import (
	"strings"
	"testing"
	"time"
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
