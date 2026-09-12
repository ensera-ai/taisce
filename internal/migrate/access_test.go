// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package migrate

import (
	"context"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/google/uuid"
)

func TestFreshRegistryEnforcesCredentialAccessModes(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	scripts, err := LoadControl()
	if err != nil {
		t.Fatal(err)
	}
	schema, _ := NewSchema("access_fresh_" + strings.ReplaceAll(uuid.NewString(), "-", ""))
	if _, err := pool.Exec(ctx, "CREATE SCHEMA "+schema.String()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE") })
	if _, err := apply(ctx, pool, schema, scripts); err != nil {
		t.Fatal(err)
	}
	store := credential.NewStore(pool, schema.String())
	for _, access := range []credential.Access{credential.ReadOnly, credential.ReadWrite} {
		token, issued, err := store.IssueWithAccess(ctx, string(access), "p1", access)
		if err != nil {
			t.Fatal(err)
		}
		grant, err := store.Resolve(ctx, token)
		if err != nil || grant != issued || grant.Access != access || !strings.HasPrefix(token, credential.Prefix) {
			t.Fatalf("fresh registry resolved wrong authority: %+v %v", grant, err)
		}
		for _, invalid := range []any{"admin", nil} {
			if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.credential SET access=$1 WHERE credential_id=$2`), invalid, issued.CredentialID); err == nil {
				t.Fatal("database admitted undefined authority")
			}
		}
	}
}
