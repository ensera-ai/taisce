// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package migrate

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestAnExistingMemoryNamespaceUpgradesWithoutMovingItsObservations(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema, _ := NewSchema("instance_upgrade_" + strings.ReplaceAll(uuid.NewString(), "-", ""))
	if _, err := pool.Exec(ctx, "CREATE SCHEMA "+schema.String()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE") })
	all, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	var prior []Script
	for _, s := range all {
		if s.Version < 23 {
			prior = append(prior, s)
		}
	}
	if _, err := apply(ctx, pool, schema, prior); err != nil {
		t.Fatal(err)
	}
	source := seedObservation(t, pool, schema, "existing")
	for i := 0; i < 2; i++ {
		if err := ProvisionMemorySchema(ctx, pool, schema.String()); err != nil {
			t.Fatal(err)
		}
	}
	if err := ProvisionScope(ctx, pool, schema.String(), "next"); err != nil {
		t.Fatal(err)
	}
	var id string
	var version int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT observation_id::text FROM {schema}.observation WHERE scope='existing'`)).Scan(&id); err != nil || id != source {
		t.Fatalf("upgrade moved or replaced source: %s %v", id, err)
	}
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT max(version) FROM {schema}.schema_migration`)).Scan(&version); err != nil || version != all[len(all)-1].Version {
		t.Fatalf("upgrade version: %d %v", version, err)
	}
}

// A plane login that gained a privilege this system never grants is refused, and left as it was.
//
// Repairing it silently would close a door somebody opened without anyone learning it had been
// opened; and the repair is a superuser act that a cluster deployment's administrative identity
// cannot perform, so a bootstrap that repaired under one identity and failed under another would
// be two behaviours under one name. Refusing, naming every attribute, is the one answer that does
// not depend on who runs it.
func TestAnExistingLoginThatGainedElevatedAttributesIsRefusedNotRepaired(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	name := "taisce_role_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err := pool.Exec(ctx, "CREATE ROLE "+quoted+" LOGIN SUPERUSER CREATEDB CREATEROLE REPLICATION BYPASSRLS"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DROP ROLE "+quoted) })
	err := ensureRole(ctx, pool, name, "isolated-test-password")
	if err == nil {
		t.Fatal("an elevated login was adopted")
	}
	for _, attribute := range []string{"superuser=true", "createdb=true", "createrole=true", "replication=true", "bypassrls=true"} {
		if !strings.Contains(err.Error(), attribute) {
			t.Fatalf("the refusal must name %s: %v", attribute, err)
		}
	}
	var elevated bool
	if err := pool.QueryRow(ctx, `SELECT rolsuper AND rolcreatedb AND rolcreaterole AND rolreplication AND rolbypassrls FROM pg_roles WHERE rolname=$1`, name).Scan(&elevated); err != nil || !elevated {
		t.Fatalf("a refused login must be left as it was for an operator to see: %v %v", elevated, err)
	}
}
