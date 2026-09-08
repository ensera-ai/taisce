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

func TestCredentialAccessUpgradePreservesExistingKeysAndAdmitsRestrictedKeys(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	scripts, err := LoadControl()
	if err != nil {
		t.Fatal(err)
	}
	schema, _ := NewSchema("access_upgrade_" + strings.ReplaceAll(uuid.NewString(), "-", ""))
	if _, err := pool.Exec(ctx, "CREATE SCHEMA "+schema.String()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE") })
	var prior []Script
	for _, s := range scripts {
		if s.Version < 4 {
			prior = append(prior, s)
		}
	}
	if _, err := apply(ctx, pool, schema, prior); err != nil {
		t.Fatal(err)
	}
	token := "tsk_" + strings.Repeat("a", 43)
	id := uuid.NewString()
	if _, err := pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.credential(credential_id,token_digest,token_prefix,name,project) VALUES($1,$2,$3,'legacy','p1')`), id, credential.Digest(token), token[:12]); err != nil {
		t.Fatal(err)
	}
	if _, err := apply(ctx, pool, schema, scripts); err != nil {
		t.Fatal(err)
	}
	store := credential.NewStore(pool, schema.String())
	grant, err := store.Resolve(ctx, token)
	if err != nil || grant.Access != credential.ReadWrite || grant.CredentialID != id || grant.Project != "p1" {
		t.Fatalf("existing authority changed: %+v %v", grant, err)
	}
	_, reader, err := store.IssueWithAccess(ctx, "reader", "p1", credential.ReadOnly)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.credential SET access='admin' WHERE credential_id=$1`), reader.CredentialID); err == nil {
		t.Fatal("database admitted undefined authority")
	}
}
