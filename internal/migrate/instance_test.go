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

func TestReconcilingAnExistingLoginRemovesElevatedRoleAttributes(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	name := "taisce_role_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err := pool.Exec(ctx, "CREATE ROLE "+quoted+" LOGIN SUPERUSER CREATEDB CREATEROLE REPLICATION BYPASSRLS"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DROP ROLE "+quoted) })
	if err := ensureRole(ctx, pool, name, "isolated-test-password"); err != nil {
		t.Fatal(err)
	}
	var elevated bool
	if err := pool.QueryRow(ctx, `SELECT rolsuper OR rolcreatedb OR rolcreaterole OR rolreplication OR rolbypassrls FROM pg_roles WHERE rolname=$1`, name).Scan(&elevated); err != nil || elevated {
		t.Fatalf("existing login retained elevated attributes: %v %v", elevated, err)
	}
}
