// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package migrate

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Reproduce a real version-21 database, rather than bypassing the new constraints after upgrading.
func TestSpeakerMigrationPreservesNamedIdentityAndRefusesAmbiguousHistory(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	all, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	var prior []Script
	for _, s := range all {
		if s.Version < 22 {
			prior = append(prior, s)
		}
	}
	for _, tc := range []struct {
		name   string
		refuse bool
	}{{"Ensera", false}, {"I", true}, {"أنا", true}} {
		t.Run(tc.name, func(t *testing.T) {
			schema, _ := NewSchema("speaker_upgrade_" + strings.ReplaceAll(uuid.NewString(), "-", ""))
			if _, err := pool.Exec(ctx, "CREATE SCHEMA "+schema.String()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE") })
			if _, err := apply(ctx, pool, schema, prior); err != nil {
				t.Fatal(err)
			}
			id := uuid.NewString()
			if _, err := pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.entity(entity_id,scope,canonical_name,normalized_name)
       VALUES($1,'p1',$2,lower($2))`), id, tc.name); err != nil {
				t.Fatal(err)
			}
			_, err := Apply(ctx, pool, schema)
			if tc.refuse {
				if err == nil || !strings.Contains(err.Error(), "projection rebuild") {
					t.Fatalf("ambiguous history accepted: %v", err)
				}
				var exists bool
				if err := pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, schema.String()+".speaker_term").Scan(&exists); err != nil {
					t.Fatal(err)
				}
				if exists {
					t.Fatal("failed migration committed partial DDL")
				}
				var latest int
				if err := pool.QueryRow(ctx, schema.SQL(`SELECT max(version) FROM {schema}.schema_migration`)).Scan(&latest); err != nil || latest != 21 {
					t.Fatalf("version changed on refusal: %d %v", latest, err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				var kind string
				if err := pool.QueryRow(ctx, schema.SQL(`SELECT identity_kind FROM {schema}.entity WHERE entity_id=$1`), id).Scan(&kind); err != nil || kind != "named" {
					t.Fatal(fmt.Sprint(kind, err))
				}
			}
		})
	}
}
