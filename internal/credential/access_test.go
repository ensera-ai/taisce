// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package credential_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/credential"
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
