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
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/google/uuid"
)

// The two plane roles are database-global, and every package's tests share one database. Establishing
// them with a different password rotates it for whoever else is mid-run, so the value is the same
// everywhere — a fact worth stating because the symptom is an unrelated package failing on
// authentication and looking like a bug in its own code.
const dataPassword = "taisce-test-data"

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
	if err := migrate.EstablishPlanes(context.Background(), pool, "taisce-test-control", dataPassword); err != nil {
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

	token, issued, err := s.Issue(ctx, "resolves-to-projects", "alpha")
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
	if grant.Project != "alpha" {
		t.Fatalf("the credential resolved to project %q", grant.Project)
	}
}

// A credential with no project is refused at minting rather than issued and useless.
//
// The failure being prevented is a token that authenticates successfully and reaches nothing, which
// looks like a working key to whoever holds it and like a permissions bug to whoever debugs it.
func TestACredentialWithoutAProjectIsRefused(t *testing.T) {
	ctx := context.Background()
	s := store(t)

	if _, _, err := s.Issue(ctx, "reaches-nothing", ""); err == nil {
		t.Fatal("minted a credential attached to no project")
	}
	if _, _, err := s.Issue(ctx, "reaches-nothing", "   "); err == nil {
		t.Fatal("whitespace was accepted as a project")
	}
}

// Absent, malformed, unknown and revoked are one answer, because telling them apart tells a stranger
// whether a token they hold is real.
func TestEveryWayOfFailingToResolveGivesTheSameAnswer(t *testing.T) {
	ctx := context.Background()
	s := store(t)

	revoked, issued, err := s.Issue(ctx, "to-be-revoked", "alpha")
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

	token, issued, err := s.Issue(ctx, "not-recoverable", "alpha")
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
	if _, _, err := s.Issue(ctx, "unreachable-by-memory", "alpha"); err != nil {
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

	_, issued, err := s.Issue(ctx, "revoked-twice", "alpha")
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

// ── #52 ──────────────────────────────────────────────────────────────────────────────────────
//
// RevokeInProject revokes only a live project credential of the project it is given. Everything
// outside that scope gets the same ErrUnknown as an identifier that names nothing, and stays live;
// that the refused credentials still resolve is the half of the claim a count of rows would miss.
func TestRevokingInAProjectTouchesOnlyThatProjectsLiveCredentials(t *testing.T) {
	ctx := context.Background()
	s := store(t)
	suffix := uuid.NewString()[:8]
	alpha, beta := "alpha_"+suffix, "beta_"+suffix

	tokenBeta, ofBeta, err := s.Issue(ctx, "of-beta", beta)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	tokenOperator, operator, err := s.IssueOperator(ctx, "operator-"+suffix)
	if err != nil {
		t.Fatalf("issue operator: %v", err)
	}
	tokenAlpha, ofAlpha, err := s.Issue(ctx, "of-alpha", alpha)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	for what, c := range map[string]struct{ id, project string }{
		"another project's credential":       {ofBeta.CredentialID, alpha},
		"an operator credential":             {operator.CredentialID, alpha},
		"an operator credential, no project": {operator.CredentialID, ""},
		"an identifier that names nothing":   {uuid.NewString(), alpha},
	} {
		if err := s.RevokeInProject(ctx, c.id, c.project); !errors.Is(err, credential.ErrUnknown) {
			t.Fatalf("revoking %s gave %v, want ErrUnknown", what, err)
		}
	}
	if _, err := s.Resolve(ctx, tokenBeta); err != nil {
		t.Fatalf("another project's credential stopped resolving after a refused revoke: %v", err)
	}
	if _, err := s.ResolveOperator(ctx, tokenOperator); err != nil {
		t.Fatalf("the operator credential stopped resolving after a refused revoke: %v", err)
	}

	if err := s.RevokeInProject(ctx, ofAlpha.CredentialID, alpha); err != nil {
		t.Fatalf("revoking the project's own credential: %v", err)
	}
	if _, err := s.Resolve(ctx, tokenAlpha); !errors.Is(err, credential.ErrUnknown) {
		t.Fatalf("a revoked credential resolved: %v", err)
	}
	if err := s.RevokeInProject(ctx, ofAlpha.CredentialID, alpha); !errors.Is(err, credential.ErrUnknown) {
		t.Fatalf("revoking it twice gave %v, want ErrUnknown", err)
	}
}

// A credential has to be nameable, because a list of anonymous keys is a list nobody can safely
// revoke from.
func TestACredentialWithoutANameIsRefused(t *testing.T) {
	ctx := context.Background()
	s := store(t)
	if _, _, err := s.Issue(ctx, "   ", "alpha"); err == nil {
		t.Fatal("issued a credential with no name")
	}
}

// A credential is one of two kinds, and each kind opens one door: the memory resolver
// refuses an operator credential and the operator resolver refuses a project credential, both
// with the answer a stranger gets; an operator credential names no project; and the registry
// refuses a row that is both or neither.
func TestACredentialOpensOneDoorByItsKindAndTheRegistryHoldsTheKind(t *testing.T) {
	ctx := context.Background()
	pool := registry(t)
	s := credential.NewStore(pool, string(migrate.ControlSchema))
	operatorToken, operator, err := s.IssueOperator(ctx, "ops")
	if err != nil || operator.Project != "" || operator.CredentialID == "" {
		t.Fatalf("an operator credential names no project, got %+v %v", operator, err)
	}
	project := "kinds_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	projectToken, _, err := s.Issue(ctx, "app", project)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Resolve(ctx, operatorToken); !errors.Is(err, credential.ErrUnknown) {
		t.Fatalf("the memory door must refuse an operator credential as a stranger, got %v", err)
	}
	if _, err := s.ResolveOperator(ctx, projectToken); !errors.Is(err, credential.ErrUnknown) {
		t.Fatalf("the operator door must refuse a project credential as a stranger, got %v", err)
	}
	if g, err := s.ResolveOperator(ctx, operatorToken); err != nil || g.CredentialID != operator.CredentialID {
		t.Fatalf("the operator door opens for the operator, got %+v %v", g, err)
	}
	if _, err := s.ResolveOperator(ctx, ""); !errors.Is(err, credential.ErrUnknown) {
		t.Fatal("an empty token is a stranger at the operator door too")
	}
	// The kind is the registry's: a row that claims to be both, or neither, is refused by the
	// database, so no code path can mint one.
	for _, row := range []struct{ kind, project string }{{"operator", project}, {"project", ""}, {"admin", project}} {
		if _, err := pool.Exec(ctx, `INSERT INTO `+string(migrate.ControlSchema)+`.credential (credential_id, token_digest, token_prefix, name, project, access, kind)
			VALUES (gen_random_uuid(), gen_random_bytes(32), 'tsk_xxxxxxxx', 'bad', $1, 'read_write', $2)`, row.project, row.kind); err == nil {
			t.Fatalf("the registry accepted kind %q with project %q", row.kind, row.project)
		}
	}
	listed, err := s.List(ctx, project, 10)
	if err != nil || len(listed) != 1 || listed[0].Kind != credential.KindProject || listed[0].TokenPrefix == "" {
		t.Fatalf("listing a project's credentials describes them without the token, got %+v %v", listed, err)
	}
	if strings.Contains(fmt.Sprint(listed), projectToken) {
		t.Fatal("a listing must never carry the token")
	}
	if err := s.Revoke(ctx, operator.CredentialID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveOperator(ctx, operatorToken); !errors.Is(err, credential.ErrUnknown) {
		t.Fatal("a revoked operator credential is a stranger")
	}
}
