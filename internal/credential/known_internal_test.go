// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package credential

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// A registry that cannot be read never widens what a stranger may do, and is not asked again by
// the next request.
//
// With no set, every token is unknown, which is the single shared allowance this replaced — the
// conservative side to fail to. And the failed read is not repeated until the interval has passed:
// without that, a registry that was slow or locked would be asked by every request in turn, each
// waiting out the read's deadline on a connection the lookups themselves need.
func TestARegistryThatCannotBeReadKnowsNobodyAndIsNotAskedAgainAtOnce(t *testing.T) {
	dsn := os.Getenv("TAISCE_TEST_DSN")
	if dsn == "" {
		t.Skip("TAISCE_TEST_DSN is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	// A registry schema that does not exist: every read of the set fails.
	s := NewStore(pool, "no_such_registry")
	token := Prefix + strings.Repeat("A", 43)

	before := time.Now()
	if _, known := s.Known(ctx, token, KindProject); known {
		t.Fatal("a token was known to a store that has never read its registry")
	}
	if s.live.Load() != nil {
		t.Fatal("a failed read left a set behind")
	}
	retry := s.retryAt.Load()
	if retry < before.Add(knownFor).UnixNano() {
		t.Fatalf("after a failed read the next attempt is %v away; it must wait out the interval",
			time.Duration(retry-before.UnixNano()))
	}
	// The next request does not try again: the moment it may is unchanged.
	if _, known := s.Known(ctx, token, KindProject); known {
		t.Fatal("a token became known without the registry having been read")
	}
	if s.retryAt.Load() != retry {
		t.Fatal("the registry was asked again inside the interval")
	}
	if s.refreshing.Load() {
		t.Fatal("the reader's place was not given back")
	}
}
