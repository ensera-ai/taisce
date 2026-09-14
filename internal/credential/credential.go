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
// Kind is which door a credential opens. A project credential opens one project's memory
// routes; an operator credential opens the management surface and no project's memory. Read from
// the registry at every request, never from the token.
type Kind string

const (
	KindProject  Kind = "project"
	KindOperator Kind = "operator"
)

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
	g, kind, err := s.resolve(ctx, token)
	if err != nil {
		return Grant{}, err
	}
	// The memory door opens for a project credential and nothing else. An operator credential is
	// refused here with the same answer a stranger gets: which door a token opens is not a fact the
	// holder of the wrong token is told.
	if kind != KindProject || g.Project == "" {
		return Grant{}, ErrUnknown
	}
	return g, nil
}

// ResolveOperator is the management door: it opens for an operator credential and refuses every
// other token, a project credential included, with the one answer a stranger gets. The grant
// carries no project, because an operator acts on the instance and names a project in the request.
func (s *Store) ResolveOperator(ctx context.Context, token string) (Grant, error) {
	g, kind, err := s.resolve(ctx, token)
	if err != nil {
		return Grant{}, err
	}
	if kind != KindOperator || g.Project != "" {
		return Grant{}, ErrUnknown
	}
	return g, nil
}

func (s *Store) resolve(ctx context.Context, token string) (Grant, Kind, error) {
	token = strings.TrimSpace(token)
	if token == "" || !strings.HasPrefix(token, Prefix) {
		// Refused without a query. A malformed token is not a database question, and answering it
		// with one is a free way for a stranger to make us work.
		return Grant{}, "", ErrUnknown
	}
	var g Grant
	var kind Kind
	err := s.registry.QueryRow(ctx, fmt.Sprintf(`
		SELECT credential_id::text, name, project, access, kind
		  FROM %s.credential
		 WHERE token_digest = $1
		   AND revoked_at IS NULL`, s.schema), Digest(token)).
		Scan(&g.CredentialID, &g.Name, &g.Project, &g.Access, &kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return Grant{}, "", ErrUnknown
	}
	if err != nil {
		return Grant{}, "", fmt.Errorf("resolve credential: %w", err)
	}
	// Authority comes from the protected registry, never from the token format or request body.
	if g.Access != ReadOnly && g.Access != ReadWrite {
		return Grant{}, "", ErrUnknown
	}
	return g, kind, nil
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
		INSERT INTO %s.credential (credential_id, token_digest, token_prefix, name, project, access, kind)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`, s.schema),
		g.CredentialID, Digest(token), token[:12], name, project, string(access), string(KindProject)); err != nil {
		return "", Grant{}, fmt.Errorf("issue credential: %w", err)
	}
	return token, g, nil
}

// IssueOperator mints an operator credential and returns the token ONCE. It names no project: the
// management surface acts on the instance, and a request names the project it acts on.
func (s *Store) IssueOperator(ctx context.Context, name string) (string, Grant, error) {
	if strings.TrimSpace(name) == "" {
		return "", Grant{}, fmt.Errorf("a credential needs a name, so a person can tell it from another")
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", Grant{}, fmt.Errorf("no randomness available to mint a credential: %w", err)
	}
	token := Prefix + base64.RawURLEncoding.EncodeToString(raw[:])
	g := Grant{CredentialID: uuid.New().String(), Name: name, Access: ReadWrite}
	if _, err := s.registry.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s.credential (credential_id, token_digest, token_prefix, name, project, access, kind)
		VALUES ($1, $2, $3, $4, '', $5, $6)`, s.schema),
		g.CredentialID, Digest(token), token[:12], name, string(ReadWrite), string(KindOperator)); err != nil {
		return "", Grant{}, fmt.Errorf("issue operator credential: %w", err)
	}
	return token, g, nil
}

// Listed is one credential as the registry describes it to an operator: never the token, never
// its digest.
type Listed struct {
	CredentialID string     `json:"id"`
	Name         string     `json:"name"`
	Kind         Kind       `json:"kind"`
	Project      string     `json:"project,omitempty"`
	Access       Access     `json:"access"`
	TokenPrefix  string     `json:"token_prefix"`
	CreatedAt    time.Time  `json:"created_at"`
	RevokedAt    *time.Time `json:"revoked_at,omitempty"`
}

// List describes the credentials of one project, or every operator credential when project is
// empty, newest first, revoked ones included so a revocation is visible as one.
func (s *Store) List(ctx context.Context, project string, limit int) ([]Listed, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.registry.Query(ctx, fmt.Sprintf(`
		SELECT credential_id::text, name, kind, project, access, token_prefix, created_at, revoked_at
		  FROM %s.credential
		 WHERE project = $1
		 ORDER BY created_at DESC, credential_id
		 LIMIT $2`, s.schema), project, limit)
	if err != nil {
		return nil, fmt.Errorf("list credentials: %w", err)
	}
	defer rows.Close()
	out := []Listed{}
	for rows.Next() {
		var c Listed
		if err := rows.Scan(&c.CredentialID, &c.Name, &c.Kind, &c.Project, &c.Access, &c.TokenPrefix, &c.CreatedAt, &c.RevokedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// RevokeInProject revokes a credential only if it is a live project credential of project.
//
// The scope is part of the UPDATE, not a lookup before it, so there is no moment between checking what
// a credential belongs to and revoking it. Every credential outside that scope gets ErrUnknown, the
// same answer as an identifier that names nothing:
//   - a credential of another project;
//   - an operator credential;
//   - a credential already revoked.
//
// It serves a surface whose page is one project, where the posted project is the scope the operator is
// acting in (#52). Revoke stays instance-wide for the CLI and the management API, whose callers name a
// credential rather than a page.
func (s *Store) RevokeInProject(ctx context.Context, credentialID, project string) error {
	if strings.TrimSpace(project) == "" {
		return ErrUnknown
	}
	tag, err := s.registry.Exec(ctx, fmt.Sprintf(`
		UPDATE %s.credential SET revoked_at = $2
		 WHERE credential_id = $1 AND revoked_at IS NULL AND kind = $3 AND project = $4`, s.schema),
		credentialID, time.Now().UTC(), string(KindProject), project)
	if err != nil {
		return fmt.Errorf("revoke credential: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrUnknown
	}
	return nil
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
