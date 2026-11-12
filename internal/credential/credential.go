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
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
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

// Access is the memory authority stored with a project credential. The set is closed and ordered:
// each level holds everything the one before it does.
type Access string

const (
	// ReadOnly reads, recalls and exports, and changes nothing.
	ReadOnly Access = "read_only"
	// Agent is every memory operation except the two that destroy data irreversibly: erasing a
	// person and purging an entity (D26). It is what an agent process holds, and the default for a
	// credential issued without naming an authority.
	Agent Access = "agent"
	// ReadWrite is every memory operation, erasure included. It is what an application's own
	// erasure path holds, and what the first credential a project is bootstrapped with carries.
	ReadWrite Access = "read_write"
)

// Valid reports whether a is one of the three authorities. Anything else is refused, at issue and at
// every resolve, so an unknown value stored by hand fails closed rather than meaning something.
func (a Access) Valid() bool {
	return a == ReadOnly || a == Agent || a == ReadWrite
}

// Grant carries the authenticated project and memory access mode, never caller-supplied authority.
// Kind is which door a credential opens. A project credential opens one project's memory
// routes; an operator credential opens the management surface and no project's memory. Read from
// the registry at every request, never from the token.
type Kind string

const (
	KindProject  Kind = "project"
	KindOperator Kind = "operator"
	// KindObserver reads the instance's numbers and opens nothing else: not a management operation,
	// not the portal, not a memory route. It is what a monitoring system is given, so that the
	// system which scrapes does not have to hold the credential that can mint a key for any project.
	KindObserver Kind = "observer"
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

	live       atomic.Pointer[liveCredentials]
	refreshing atomic.Bool
	// retryAt is when a failed read of the set may be tried again, in nanoseconds. Without it a
	// registry that cannot be read would be asked again by the very next request, each one waiting
	// out the read's deadline on a connection the lookups need.
	retryAt atomic.Int64
}

// liveCredentials is the set of digests the registry held unrevoked when it was last read.
type liveCredentials struct {
	read  time.Time
	kinds map[[sha256.Size]byte]Kind
}

// knownFor is how long the set is used before it is read again.
//
// It bounds two things and neither is whether a credential works. A credential issued within the
// last few seconds is not yet in the set, so its lookup is paid for from the allowance strangers
// share; a credential revoked within the last few seconds is still in it, so its lookup — which
// refuses it — is not. Short enough that both windows are a moment, long enough that reading the
// set is not a cost every request pays.
const knownFor = 5 * time.Second

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

// WellFormed reports whether a presented value could be a token at all. What cannot be one is
// refused without a query and without spending anything, because there is nothing to look up.
func WellFormed(token string) bool {
	return strings.HasPrefix(strings.TrimSpace(token), Prefix)
}

// Known reports whether a token's digest is one this instance issued for the given door and had
// not revoked when the registry was last read, and returns the key that names it.
//
// # What it decides, and what it must never decide
//
// It decides which allowance pays for the lookup. It does not decide whether the credential is
// good: every admitted request is still resolved against the registry, so a revocation applies to
// the very next request exactly as before, and a credential minted a second ago works exactly as
// before. The set is allowed to be a few seconds stale for that reason and no other.
//
// # Why a set of digests rather than charging everybody alike
//
// Before a lookup, a stranger's random token and a customer's real one are both forty-seven
// characters with the right prefix. A limit that cannot tell them apart is spent by whoever sends
// most, and that is the stranger: one bucket, charged before anything was known about the request,
// let anybody who could reach the port refuse service to everybody who held a credential.
//
// The digest is what tells them apart without trusting the caller. It is unforgeable in the only
// sense that matters here — presenting a token whose SHA-256 is in the set means holding a token
// the instance issued. The stored prefix would be cheaper and is not used: it is shown in every
// credential list, so anyone who has seen one could ride the allowance meant for its holder.
//
// The key is the hex digest, never the token. It is used to count one credential's lookups in
// flight and is dropped when the count returns to zero.
//
// # What happens when the set cannot be read
//
// The previous set is kept. With no set at all every token is unknown, which is the behaviour this
// replaced: strangers and customers share one allowance. Failing to that side means a registry
// that cannot be read never widens what a stranger may do.
func (s *Store) Known(ctx context.Context, token string, kind Kind) (string, bool) {
	token = strings.TrimSpace(token)
	if !strings.HasPrefix(token, Prefix) {
		return "", false
	}
	live := s.live.Load()
	now := time.Now()
	if (live == nil || now.Sub(live.read) > knownFor) && now.UnixNano() >= s.retryAt.Load() &&
		s.refreshing.CompareAndSwap(false, true) {
		// One reader at a time. Everybody else uses the set already held, so a slow registry costs
		// one request a wait and the rest nothing — and after a failed read, nobody waits again
		// until the interval has passed.
		if fresh, err := s.readLive(ctx); err == nil {
			s.live.Store(fresh)
			live = fresh
		} else {
			s.retryAt.Store(now.Add(knownFor).UnixNano())
		}
		s.refreshing.Store(false)
	}
	if live == nil {
		return "", false
	}
	digest := sha256.Sum256([]byte(token))
	if live.kinds[digest] != kind {
		return "", false
	}
	return hex.EncodeToString(digest[:]), true
}

func (s *Store) readLive(ctx context.Context) (*liveCredentials, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	rows, err := s.registry.Query(ctx, fmt.Sprintf(
		`SELECT token_digest, kind FROM %s.credential WHERE revoked_at IS NULL`, s.schema))
	if err != nil {
		return nil, fmt.Errorf("read live credentials: %w", err)
	}
	defer rows.Close()
	out := &liveCredentials{read: time.Now(), kinds: map[[sha256.Size]byte]Kind{}}
	for rows.Next() {
		var digest []byte
		var kind Kind
		if err := rows.Scan(&digest, &kind); err != nil {
			return nil, fmt.Errorf("read live credentials: %w", err)
		}
		if len(digest) == sha256.Size {
			out.kinds[[sha256.Size]byte(digest)] = kind
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read live credentials: %w", err)
	}
	return out, nil
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
	if !g.Access.Valid() {
		return Grant{}, "", ErrUnknown
	}
	return g, kind, nil
}

// ResolveScraper is the metrics door: it opens for an observer credential and for an operator
// credential, and for nothing else.
//
// An operator is admitted because an operator may read everything on the surface this route is
// part of, and because deployments that scraped before the observer kind existed scrape with one.
// An observer is admitted here and nowhere else — every other door asks for its own kind and gives
// an observer the answer a stranger gets.
func (s *Store) ResolveScraper(ctx context.Context, token string) (Grant, error) {
	g, kind, err := s.resolve(ctx, token)
	if err != nil {
		return Grant{}, err
	}
	if (kind != KindOperator && kind != KindObserver) || g.Project != "" {
		return Grant{}, ErrUnknown
	}
	return g, nil
}

// IssueObserver mints a credential that reads the instance's numbers and nothing else, and returns
// the token once.
func (s *Store) IssueObserver(ctx context.Context, name string) (string, Grant, error) {
	return s.issueInstanceCredential(ctx, name, KindObserver, ReadOnly)
}

// Issue mints a credential for one project and returns the token ONCE.
//
// The token is not recoverable afterwards, by us or by anyone with the database: only its digest is
// stored. That is the property being bought, and it is why this returns the token rather than
// writing it anywhere.
//
// It mints read_write. Its caller is the bootstrap, minting the first credential a project has: its
// owner's, which the application's own erasure path holds. Every credential issued afterwards
// through the CLI, the management surface or the portal defaults to Agent instead (D26).
func (s *Store) Issue(ctx context.Context, name, project string) (string, Grant, error) {
	return s.IssueWithAccess(ctx, name, project, ReadWrite)
}

// IssueWithAccess creates an explicitly restricted project credential. Access is registry metadata;
// it is never taken from an HTTP request using that credential.
func (s *Store) IssueWithAccess(ctx context.Context, name, project string, access Access) (string, Grant, error) {
	if !access.Valid() {
		return "", Grant{}, fmt.Errorf("credential access must be read_only, agent or read_write")
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
	return s.issueInstanceCredential(ctx, name, KindOperator, ReadWrite)
}

// issueInstanceCredential mints a credential that names no project: an operator's or an observer's.
//
// The access column is not what bounds either — the kind is, at the door — but it is stored with
// the least authority the kind could mean, so a row read by something that looks only at access
// does not find `read_write` on a credential issued to scrape.
func (s *Store) issueInstanceCredential(ctx context.Context, name string, kind Kind, access Access) (string, Grant, error) {
	if strings.TrimSpace(name) == "" {
		return "", Grant{}, fmt.Errorf("a credential needs a name, so a person can tell it from another")
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", Grant{}, fmt.Errorf("no randomness available to mint a credential: %w", err)
	}
	token := Prefix + base64.RawURLEncoding.EncodeToString(raw[:])
	g := Grant{CredentialID: uuid.New().String(), Name: name, Access: access}
	if _, err := s.registry.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s.credential (credential_id, token_digest, token_prefix, name, project, access, kind)
		VALUES ($1, $2, $3, $4, '', $5, $6)`, s.schema),
		g.CredentialID, Digest(token), token[:12], name, string(access), string(kind)); err != nil {
		return "", Grant{}, fmt.Errorf("issue %s credential: %w", kind, err)
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
