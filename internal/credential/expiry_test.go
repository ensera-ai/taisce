// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package credential_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/migrate"
)

// brief is a lifetime short enough to outlive inside a test and long enough that the credential is
// certainly alive when it is first resolved. The credential really expires, by the database's
// clock; no row is edited to pretend it did.
const brief = 600 * time.Millisecond

// TestACredentialPastItsExpiryIsRefusedExactlyAsARevokedOneIs holds the expiry to the one answer
// every refused token gets, at each door.
func TestACredentialPastItsExpiryIsRefusedExactlyAsARevokedOneIs(t *testing.T) {
	ctx := context.Background()
	s := store(t)

	expiring, grant, err := s.IssueExpiring(ctx, "expires-soon", "alpha", credential.Agent, brief)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if grant.ExpiresAt == nil {
		t.Fatal("a credential issued with a lifetime was not told when it ends")
	}
	operator, _, err := s.IssueOperatorFor(ctx, "expires-soon-operator", brief)
	if err != nil {
		t.Fatal(err)
	}
	revoked, revokedGrant, err := s.Issue(ctx, "revoked-for-comparison", "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Revoke(ctx, revokedGrant.CredentialID); err != nil {
		t.Fatal(err)
	}
	dateless, _, err := s.Issue(ctx, "has-no-date", "alpha")
	if err != nil {
		t.Fatal(err)
	}

	// While it lives it resolves, and the grant says when it will stop.
	if got, err := s.Resolve(ctx, expiring); err != nil || got.ExpiresAt == nil || !got.ExpiresAt.Equal(*grant.ExpiresAt) {
		t.Fatalf("a credential inside its lifetime must resolve and carry its expiry: %+v %v", got, err)
	}
	if _, err := s.ResolveOperator(ctx, operator); err != nil {
		t.Fatalf("an operator credential inside its lifetime must resolve: %v", err)
	}
	if _, known := credential.NewStore(registry(t), string(migrate.ControlSchema)).Known(ctx, expiring, credential.KindProject); !known {
		t.Fatal("a live credential is not in the set of credentials the instance issued")
	}

	time.Sleep(brief + 300*time.Millisecond)

	_, revokedErr := s.Resolve(ctx, revoked)
	if !errors.Is(revokedErr, credential.ErrUnknown) {
		t.Fatalf("the revoked credential this compares against still resolves: %v", revokedErr)
	}
	if _, err := s.Resolve(ctx, expiring); !errors.Is(err, credential.ErrUnknown) || err.Error() != revokedErr.Error() {
		t.Errorf("an expired credential was answered %v at the memory door; a revoked one is answered %v", err, revokedErr)
	}
	if _, err := s.ResolveOperator(ctx, operator); !errors.Is(err, credential.ErrUnknown) {
		t.Errorf("an expired operator credential still opens the management door: %v", err)
	}
	if _, err := s.ResolveScraper(ctx, operator); !errors.Is(err, credential.ErrUnknown) {
		t.Errorf("an expired operator credential still opens the metrics door: %v", err)
	}
	// It stops being one of the instance's own, too: its lookups are paid for from the allowance
	// strangers share, as a revoked credential's are. A new store, so the set is read now.
	if _, known := credential.NewStore(registry(t), string(migrate.ControlSchema)).Known(ctx, expiring, credential.KindProject); known {
		t.Error("an expired credential is still counted among the credentials the instance issued")
	}
	// A credential with no date is untouched by any of this.
	if _, err := s.Resolve(ctx, dateless); err != nil {
		t.Errorf("a credential with no date stopped resolving: %v", err)
	}

	// The list says so, by the database's reading of the clock rather than the reader's.
	listed, err := s.List(ctx, "alpha", 500)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]credential.Listed{}
	for _, l := range listed {
		seen[l.CredentialID] = l
	}
	if l := seen[grant.CredentialID]; !l.Expired || l.ExpiresAt == nil || l.RevokedAt != nil {
		t.Errorf("the expired credential is not listed as expired: %+v", l)
	}
	if l := seen[revokedGrant.CredentialID]; l.Expired || l.ExpiresAt != nil || l.RevokedAt == nil {
		t.Errorf("the revoked credential is listed as something other than revoked: %+v", l)
	}
}

// TestAnOperatorCredentialEndsUnlessSomebodySaysOtherwise holds which credentials get a date nobody
// asked for, and which do not.
func TestAnOperatorCredentialEndsUnlessSomebodySaysOtherwise(t *testing.T) {
	ctx := context.Background()
	s := store(t)

	before := time.Now()
	_, operator, err := s.IssueOperator(ctx, "default-lifetime")
	if err != nil {
		t.Fatal(err)
	}
	if operator.ExpiresAt == nil {
		t.Fatal("an operator credential minted without a word about its lifetime has none")
	}
	// A minute either side: the expiry is fixed by the database's clock, and this machine's is
	// another one.
	want := before.Add(credential.OperatorLifetime)
	if d := operator.ExpiresAt.Sub(want); d < -time.Minute || d > time.Minute {
		t.Errorf("the default operator lifetime is %s; the credential ends %s from when that says", credential.OperatorLifetime, d)
	}

	if _, permanent, err := s.IssueOperatorFor(ctx, "said-permanent", credential.NoExpiry); err != nil || permanent.ExpiresAt != nil {
		t.Errorf("an operator credential somebody said should not end was given a date: %+v %v", permanent, err)
	}
	// An application holds a project credential and a scraper holds an observer. A date nobody
	// chose is the day one stops authenticating and the other stops reporting.
	if _, project, err := s.Issue(ctx, "application", "alpha"); err != nil || project.ExpiresAt != nil {
		t.Errorf("a project credential was given a date nobody asked for: %+v %v", project, err)
	}
	if _, observer, err := s.IssueObserver(ctx, "scraper"); err != nil || observer.ExpiresAt != nil {
		t.Errorf("an observer credential was given a date nobody asked for: %+v %v", observer, err)
	}

	if _, _, err := s.IssueExpiring(ctx, "negative", "alpha", credential.Agent, -time.Hour); err == nil {
		t.Error("a negative lifetime minted a credential")
	}
	// The registry refuses what the code above it should never send: a credential that ended
	// before it began.
	if _, err := registry(t).Exec(ctx, `UPDATE `+string(migrate.ControlSchema)+`.credential
		SET expires_at = created_at - interval '1 second' WHERE credential_id = $1`, operator.CredentialID); err == nil {
		t.Error("the registry accepted an expiry before the credential was issued")
	}
}

func TestALifetimeInDaysIsADateOrNone(t *testing.T) {
	for days, want := range map[int]time.Duration{0: credential.NoExpiry, 1: 24 * time.Hour, 90: credential.OperatorLifetime, credential.MaxLifetimeDays: credential.MaxLifetimeDays * 24 * time.Hour} {
		if got, err := credential.LifetimeOfDays(days); err != nil || got != want {
			t.Errorf("%d days: got %s, %v", days, got, err)
		}
	}
	for _, days := range []int{-1, credential.MaxLifetimeDays + 1} {
		if _, err := credential.LifetimeOfDays(days); err == nil {
			t.Errorf("%d days was accepted as a lifetime", days)
		}
	}
}
