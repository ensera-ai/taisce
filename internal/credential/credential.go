// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

// Package credential resolves what a caller presented into what they may reach.
//
// It is a separate package from the HTTP surface because the answer it gives is a security decision
// and the surface is transport. It talks to the REGISTRY connection, never the memory one — the two
// database identities exist so that a memory query cannot read a key, and a package that held both
// pools would be the place that difference stopped mattering.
package credential

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Prefix marks a Taisce token in a log, a secret scanner or a pasted snippet. It is stored in the
// clear alongside the digest so a person can tell two credentials apart in a list.
const Prefix = "tsk_"

// ErrUnknown is returned for a credential that does not resolve — absent, malformed, unknown or
// revoked, deliberately without distinguishing them.
//
// The caller of this package turns it into one status. Telling an unauthenticated stranger which of
// those it was tells them whether a token they hold is real, which is the one fact they cannot
// otherwise get.
var ErrUnknown = errors.New("credential does not resolve")

// Access is the memory authority stored with a project credential.
type Access string

const (
	ReadOnly  Access = "read_only"
	ReadWrite Access = "read_write"
)

// Grant carries the authenticated project and memory access mode, never caller-supplied authority.
type Grant struct {
	Access       Access
	CredentialID string
	Name         string
	// Project is the one project this credential reaches. A caller never names a project — the
	// credential decides it — so this is not a set to be checked against a request but the answer
	// to which project an operation acts on.
	//
	// Empty is not a valid resolved grant: Resolve refuses a credential with no project rather than
	// returning one that reaches nothing, because a token that authenticates and then does nothing
	// is a support case, not a security posture.
	Project string
}

// Store resolves and issues credentials against the registry schema.
type Store struct {
	registry *pgxpool.Pool
	schema   string
}

// NewStore takes the REGISTRY pool. Handing it the memory pool would silently work — both reach the
// same database — and would defeat the grant that makes the boundary real, so the parameter is named
// for the only pool that is correct.
func NewStore(registry *pgxpool.Pool, schema string) *Store {
	return &Store{registry: registry, schema: schema}
}

// Digest is the stored form of a token.
//
// SHA-256 rather than a password hash. A token is 256 bits of random, so the guessing attack a slow
// hash defends against does not exist here; what a digest defends against is a read of the table
// that should never have happened, and it does that just as well while costing one hash on a path
// every request pays.
func Digest(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// Resolve turns a presented token into a grant.
func (s *Store) Resolve(ctx context.Context, token string) (Grant, error) {
	token = strings.TrimSpace(token)
	if token == "" || !strings.HasPrefix(token, Prefix) {
		// Refused without a query. A malformed token is not a database question, and answering it
		// with one is a free way for a stranger to make us work.
		return Grant{}, ErrUnknown
	}

	var g Grant
	err := s.registry.QueryRow(ctx, fmt.Sprintf(`
		SELECT credential_id::text, name, project, access
		  FROM %s.credential
		 WHERE token_digest = $1
		   AND revoked_at IS NULL`, s.schema), Digest(token)).
		Scan(&g.CredentialID, &g.Name, &g.Project, &g.Access)
	if errors.Is(err, pgx.ErrNoRows) {
		return Grant{}, ErrUnknown
	}
	if err != nil {
		return Grant{}, fmt.Errorf("resolve credential: %w", err)
	}
	// Authority comes from the protected registry, never from the token format or request body.
	if g.Access != ReadOnly && g.Access != ReadWrite {
		return Grant{}, ErrUnknown
	}
	return g, nil
}

// Issue mints a credential for one project and returns the token ONCE.
//
// The token is not recoverable afterwards, by us or by anyone with the database: only its digest is
// stored. That is the property being bought, and it is why this returns the token rather than
// writing it anywhere.
func (s *Store) Issue(ctx context.Context, name, project string) (string, Grant, error) {
	return s.IssueWithAccess(ctx, name, project, ReadWrite)
}

// IssueWithAccess creates an explicitly restricted project credential. Access is registry metadata;
// it is never taken from an HTTP request using that credential.
func (s *Store) IssueWithAccess(ctx context.Context, name, project string, access Access) (string, Grant, error) {
	if access != ReadOnly && access != ReadWrite {
		return "", Grant{}, fmt.Errorf("credential access must be read_only or read_write")
	}
	if strings.TrimSpace(name) == "" {
		return "", Grant{}, fmt.Errorf("a credential needs a name, so a person can tell it from another")
	}
	if strings.TrimSpace(project) == "" {
		// A credential with no project reaches nothing and authenticates successfully, which is the
		// worst combination: it looks like a working key to whoever holds it.
		return "", Grant{}, fmt.Errorf("a credential is attached to a project")
	}

	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		// The token's only property is that it cannot be guessed. Continuing with degraded
		// randomness would produce a credential that looks identical and is not.
		return "", Grant{}, fmt.Errorf("no randomness available to mint a credential: %w", err)
	}
	token := Prefix + base64.RawURLEncoding.EncodeToString(raw[:])

	g := Grant{CredentialID: uuid.New().String(), Name: name, Project: project, Access: access}
	if _, err := s.registry.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s.credential (credential_id, token_digest, token_prefix, name, project, access)
		VALUES ($1, $2, $3, $4, $5, $6)`, s.schema),
		g.CredentialID, Digest(token), token[:12], name, project, string(access)); err != nil {
		return "", Grant{}, fmt.Errorf("issue credential: %w", err)
	}
	return token, g, nil
}

// Revoke stops a credential resolving, keeping the row so whatever recorded its use can still name
// it.
func (s *Store) Revoke(ctx context.Context, credentialID string) error {
	tag, err := s.registry.Exec(ctx, fmt.Sprintf(`
		UPDATE %s.credential SET revoked_at = $2
		 WHERE credential_id = $1 AND revoked_at IS NULL`, s.schema),
		credentialID, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("revoke credential: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrUnknown
	}
	return nil
}
