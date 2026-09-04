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
