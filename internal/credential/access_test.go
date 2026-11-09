// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package credential_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/migrate"
)

func TestReadOnlyCredentialResolvesWithoutAcquiringWriteAuthority(t *testing.T) {
	ctx := context.Background()
	s := store(t)
	token, issued, err := s.IssueWithAccess(ctx, "readonly-grant", "alpha", credential.ReadOnly)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Resolve(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	if got != issued || got.Access != credential.ReadOnly || !strings.HasPrefix(token, credential.Prefix) {
		t.Fatal("read-only identity or authority changed")
	}
	// A holder cannot change an opaque token to acquire another grant.
	rewritten := token + "a"
	if _, err := s.Resolve(ctx, rewritten); !errors.Is(err, credential.ErrUnknown) {
		t.Fatal("tampered token resolved")
	}
	writer, grant, err := s.Issue(ctx, "default-writer", "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if grant.Access != credential.ReadWrite || !strings.HasPrefix(writer, credential.Prefix) {
		t.Fatal("default authority changed")
	}
	if token, _, err := s.IssueWithAccess(ctx, "unknown-access", "alpha", credential.Access("admin")); err == nil || token != "" {
		t.Fatal("unrecognized access minted a key")
	}
	if err := s.Revoke(ctx, issued.CredentialID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Resolve(ctx, token); !errors.Is(err, credential.ErrUnknown) {
		t.Fatal("revoked read-only key resolved")
	}
}

// An agent credential resolves as agent, and the registry itself holds the set: a row cannot be
// given an authority outside the three, and a row inserted without naming one is agent, the
// narrowest that still writes (D26). The column is the boundary; the Go check beside it is the
// second place an unknown value is stopped, not the only one.
func TestAnAgentCredentialResolvesAsAgentAndTheRegistryHoldsTheSetOfAuthorities(t *testing.T) {
	ctx := context.Background()
	pool := registry(t)
	s := credential.NewStore(pool, string(migrate.ControlSchema))
	token, issued, err := s.IssueWithAccess(ctx, "agent-grant", "alpha", credential.Agent)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Resolve(ctx, token)
	if err != nil || got.Access != credential.Agent {
		t.Fatalf("an agent key resolved as %+v, %v", got, err)
	}
	table := string(migrate.ControlSchema) + ".credential"
	if _, err := pool.Exec(ctx, `UPDATE `+table+` SET access = 'admin' WHERE credential_id = $1`, issued.CredentialID); err == nil {
		t.Fatal("the registry accepted an authority outside the set")
	}
	var fallback string
	if err := pool.QueryRow(ctx, `SELECT column_default FROM information_schema.columns
		WHERE table_schema = $1 AND table_name = 'credential' AND column_name = 'access'`, string(migrate.ControlSchema)).Scan(&fallback); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fallback, "'agent'") {
		t.Fatalf("a row inserted without an authority gets %s, not agent", fallback)
	}
	for _, a := range []credential.Access{credential.ReadOnly, credential.Agent, credential.ReadWrite} {
		if !a.Valid() {
			t.Fatalf("%q is one of the three and was refused", a)
		}
	}
	if credential.Access("Agent").Valid() || credential.Access("").Valid() {
		t.Fatal("an authority outside the set was valid")
	}
}
