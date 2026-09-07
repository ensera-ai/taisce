// What a credential resolves to, and what cannot reach it.
//
// Two claims live here. The first is behavioural — a token resolves to a project set and to nothing
// else, and every way of presenting a bad one gets the same answer. The second is structural: the
// memory service's own database identity has no privilege over this table, so a memory query cannot
// read a key even if one were written that tried.
//
// The second is asserted through the data-plane login rather than through Go, because a boundary
// expressed in code is a convention and one expressed in a grant is a boundary.
package credential_test

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/migrate"
)

const dataPassword = "credential-test-data-password"

func registry(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TAISCE_TEST_DSN")
	if dsn == "" {
		t.Skip("TAISCE_TEST_DSN is not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.EstablishPlanes(context.Background(), pool, "credential-test-control", dataPassword); err != nil {
		t.Fatalf("establish planes: %v", err)
	}
	return pool
}

func store(t *testing.T) *credential.Store {
	t.Helper()
	return credential.NewStore(registry(t), string(migrate.ControlSchema))
}

// A credential resolves to the projects it was granted, and carries nothing else.
func TestACredentialResolvesToItsProjectsAndNothingElse(t *testing.T) {
	ctx := context.Background()
	s := store(t)

	token, issued, err := s.Issue(ctx, "resolves-to-projects", []string{"alpha", "beta"})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	grant, err := s.Resolve(ctx, token)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if grant.CredentialID != issued.CredentialID {
		t.Fatalf("resolved to a different credential: %s vs %s", grant.CredentialID, issued.CredentialID)
	}
	if !grant.Permits("alpha") || !grant.Permits("beta") {
		t.Fatalf("a granted project is not permitted: %v", grant.Scopes)
	}
	if grant.Permits("gamma") {
		t.Fatal("a project that was never granted is permitted")
	}
}

// An empty grant is a valid state and it reaches nothing.
//
// This is the state a credential lands in when its grants are removed, and the wrong behaviour —
// treating empty as unrestricted — is the failure the permission exists to prevent.
func TestACredentialWithNoProjectsReachesNothing(t *testing.T) {
	ctx := context.Background()
	s := store(t)

	token, _, err := s.Issue(ctx, "reaches-nothing", nil)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	grant, err := s.Resolve(ctx, token)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(grant.Scopes) != 0 {
		t.Fatalf("an empty grant acquired scopes: %v", grant.Scopes)
	}
	if grant.Permits("anything") {
		t.Fatal("an empty grant permitted a project")
	}
}

// Absent, malformed, unknown and revoked are one answer, because telling them apart tells a stranger
// whether a token they hold is real.
func TestEveryWayOfFailingToResolveGivesTheSameAnswer(t *testing.T) {
	ctx := context.Background()
	s := store(t)

	revoked, issued, err := s.Issue(ctx, "to-be-revoked", []string{"alpha"})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if err := s.Revoke(ctx, issued.CredentialID); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	for name, token := range map[string]string{
		"empty":       "",
		"not ours":    "some-other-systems-token",
		"right shape": credential.Prefix + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"revoked":     revoked,
		"whitespace":  "   ",
		"prefix only": credential.Prefix,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := s.Resolve(ctx, token); !errors.Is(err, credential.ErrUnknown) {
				t.Fatalf("got %v, wanted the one indistinguishable refusal", err)
			}
		})
	}
}

// The token is returned once and is not recoverable from what is stored.
func TestTheTokenIsNotRecoverableFromTheDatabase(t *testing.T) {
	ctx := context.Background()
	pool := registry(t)
	s := credential.NewStore(pool, string(migrate.ControlSchema))

	token, issued, err := s.Issue(ctx, "not-recoverable", []string{"alpha"})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	var prefix string
	var digest []byte
	if err := pool.QueryRow(ctx,
		`SELECT token_prefix, token_digest FROM `+string(migrate.ControlSchema)+
			`.credential WHERE credential_id = $1`, issued.CredentialID).Scan(&prefix, &digest); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if strings.Contains(token, string(digest)) || string(digest) == token {
		t.Fatal("the stored digest is the token")
	}
	if !strings.HasPrefix(token, prefix) {
		t.Fatalf("the stored prefix %q does not name the token", prefix)
	}
	if len(prefix) >= len(token) {
		t.Fatal("the stored prefix is the whole token, which makes storing a digest pointless")
	}
}

// ── The structural claim ──────────────────────────────────────────────────────────────────────
//
// The memory service cannot read a credential.
//
// Asserted through the memory service's own database login. A rule expressed in Go is one every
// future query has to remember; expressed as an absent grant it is a rule no statement can break —
// including a statement written during an incident by somebody in a hurry.
func TestTheMemoryServiceCannotReadACredential(t *testing.T) {
	ctx := context.Background()
	s := store(t)
	if _, _, err := s.Issue(ctx, "unreachable-by-memory", []string{"alpha"}); err != nil {
		t.Fatalf("issue: %v", err)
	}

	dsn, err := url.Parse(os.Getenv("TAISCE_TEST_DSN"))
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	dsn.User = url.UserPassword(migrate.DataRole, dataPassword)
	memory, err := pgxpool.New(ctx, dsn.String())
	if err != nil {
		t.Fatalf("connect as %s: %v", migrate.DataRole, err)
	}
	defer memory.Close()

	var n int
	err = memory.QueryRow(ctx,
		`SELECT count(*) FROM `+string(migrate.ControlSchema)+`.credential`).Scan(&n)
	if err == nil {
		t.Fatalf("the memory identity read %d credentials; the grant is not doing its job", n)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "permission denied") {
		t.Fatalf("refused for the wrong reason, which may not survive a schema change: %v", err)
	}
}

// Revoking something that is not there, or is already revoked, is refused with the same answer as
// resolving one. A caller cannot learn whether a credential id exists by trying to revoke it.
func TestRevokingAnUnknownOrAlreadyRevokedCredentialIsRefused(t *testing.T) {
	ctx := context.Background()
	s := store(t)

	if err := s.Revoke(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, credential.ErrUnknown) {
		t.Fatalf("revoking an unknown credential gave %v", err)
	}

	_, issued, err := s.Issue(ctx, "revoked-twice", []string{"alpha"})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if err := s.Revoke(ctx, issued.CredentialID); err != nil {
		t.Fatalf("first revoke: %v", err)
	}
	if err := s.Revoke(ctx, issued.CredentialID); !errors.Is(err, credential.ErrUnknown) {
		t.Fatalf("revoking twice gave %v", err)
	}
}

// A credential has to be nameable, because a list of anonymous keys is a list nobody can safely
// revoke from.
func TestACredentialWithoutANameIsRefused(t *testing.T) {
	ctx := context.Background()
	s := store(t)
	if _, _, err := s.Issue(ctx, "   ", []string{"alpha"}); err == nil {
		t.Fatal("issued a credential with no name")
	}
}
