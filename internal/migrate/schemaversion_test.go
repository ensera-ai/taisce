// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package migrate

import (
	"context"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
)

// A binary does not report ready on a schema older than the one it was built for.
//
// An upgrade starts new pods and a migration at the same moment, and nothing orders the two. A new
// pod that reached the database before the migration committed reported ready — its checks ask
// whether tables exist, and they did — and then served requests against a schema missing whatever
// its own release had added, failing each one in its own way for as long as the migration took.
//
// The check is one-sided on purpose. A schema older than the binary is unambiguous: the binary
// cannot work without what it was built for, so it waits. A schema *newer* than the binary is a pod
// from the previous release still serving after the migration, and whether that is safe depends on
// what a migration is allowed to change between releases — a promise that has not been made. So
// this refuses the first and says nothing about the second.
func TestABinaryIsNotReadyOnASchemaOlderThanItWasBuiltFor(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema, _ := NewSchema("schema_gate_" + strings.ReplaceAll(uuid.NewString(), "-", ""))
	if _, err := pool.Exec(ctx, "CREATE SCHEMA "+schema.String()); err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	memory, err := pg.NewSchema(schema.String())
	if err != nil {
		t.Fatal(err)
	}
	all, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	built, err := BuiltFor()
	if err != nil {
		t.Fatal(err)
	}
	if built != all[len(all)-1].Version {
		t.Fatalf("this binary says it was built for version %d and its newest migration is %d", built, all[len(all)-1].Version)
	}

	// Nothing has been migrated at all: the record of what ran does not exist yet.
	if err := pg.CheckSchemaBuiltFor(ctx, pool, memory, built); err == nil {
		t.Fatal("a binary is ready on a namespace nothing has been migrated into")
	}

	// The release before this one: everything but the newest migration.
	if _, err := apply(ctx, pool, schema, all[:len(all)-1]); err != nil {
		t.Fatal(err)
	}
	err = pg.CheckSchemaBuiltFor(ctx, pool, memory, built)
	if err == nil {
		t.Fatalf("a binary built for version %d is ready on a schema at version %d", built, all[len(all)-2].Version)
	}
	// It says both numbers and what it is waiting for, because the pod that prints this is one an
	// operator is watching restart and deciding whether to worry about.
	for _, want := range []string{
		" " + itoa(all[len(all)-2].Version) + " ", " " + itoa(built) + ";", "waiting for the migration",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", strings.TrimSpace(want), err)
		}
	}

	// The migration lands, and the same binary is ready without restarting.
	if _, err := Apply(ctx, pool, schema); err != nil {
		t.Fatal(err)
	}
	if err := pg.CheckSchemaBuiltFor(ctx, pool, memory, built); err != nil {
		t.Fatalf("after the migration the binary it was built for is still not ready: %v", err)
	}
	// A schema newer than the binary is not this check's to refuse.
	if err := pg.CheckSchemaBuiltFor(ctx, pool, memory, built-1); err != nil {
		t.Fatalf("a binary from the release before is refused on a newer schema, which no decision has asked for: %v", err)
	}
}

func itoa(n int) string {
	const digits = "0123456789"
	if n == 0 {
		return "0"
	}
	var out []byte
	for ; n > 0; n /= 10 {
		out = append([]byte{digits[n%10]}, out...)
	}
	return string(out)
}
