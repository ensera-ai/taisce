// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package migrate

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Starters that provision one fresh namespace together all succeed, and each migration is recorded
// once: the lock makes them take turns rather than fail on what another just created.
func TestStartersProvisioningOneNamespaceTogetherAllSucceed(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	name := "lock_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	t.Cleanup(func() { pool.Exec(ctx, "DROP SCHEMA IF EXISTS "+name+" CASCADE") })
	const starters = 4
	errs := make([]error, starters)
	var wg sync.WaitGroup
	for i := range starters {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = ProvisionMemorySchema(ctx, pool, name)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("starter %d: %v", i, err)
		}
	}
	all, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	schema, _ := NewSchema(name)
	var recorded int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.schema_migration`)).Scan(&recorded); err != nil {
		t.Fatal(err)
	}
	if recorded != len(all) {
		t.Fatalf("recorded %d migrations, want %d", recorded, len(all))
	}
}

// A migration waits while another session holds the namespace's lock, and proceeds when it is
// released: the lock is the one Apply takes, not a lock that merely exists.
func TestAMigrationWaitsForTheNamespaceLock(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	name := "lock_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	t.Cleanup(func() { pool.Exec(ctx, "DROP SCHEMA IF EXISTS "+name+" CASCADE") })
	holder, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Release()
	key := "taisce:migrate:" + name
	if _, err := holder.Exec(ctx, `SELECT pg_advisory_lock(hashtext($1)::bigint)`, key); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- ProvisionMemorySchema(ctx, pool, name) }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity
			WHERE wait_event_type='Lock' AND wait_event='advisory' AND query LIKE '%pg_advisory_lock%'
			  AND pid <> pg_backend_pid())`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("provisioning finished while the namespace lock was held: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("provisioning never waited on the namespace lock")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := holder.Exec(ctx, `SELECT pg_advisory_unlock(hashtext($1)::bigint)`, key); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("provisioning did not proceed after the lock was released")
	}
}
