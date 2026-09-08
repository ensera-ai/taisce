// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package migrate

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestBacklogMigrationPreservesPreviouslyAcceptedWorkAboveItsNewLimit(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema, _ := NewSchema("ingestion_upgrade_" + strings.ReplaceAll(uuid.NewString(), "-", ""))
	if _, err := pool.Exec(ctx, "CREATE SCHEMA "+schema.String()); err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	all, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	var prior []Script
	for _, script := range all {
		if script.Version < 25 {
			prior = append(prior, script)
		}
	}
	if _, err := apply(ctx, pool, schema, prior); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.observation(observation_id,scope,log_offset,kind,occurred_at,ingested_at,source_role)
SELECT gen_random_uuid(),'p1',n,'turn',now(),now(),'user' FROM generate_series(0,512) n`)); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(ctx, pool, schema); err != nil {
		t.Fatal(err)
	}
	var observations, pending, reservations, limit int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT (SELECT count(*) FROM {schema}.observation),pending,(SELECT count(*) FROM {schema}.ingestion_reservation),max_pending_per_project FROM {schema}.ingestion_budget`)).Scan(&observations, &pending, &reservations, &limit); err != nil {
		t.Fatal(err)
	}
	if observations != 513 || pending != 513 || reservations != 513 || limit != 512 {
		t.Fatalf("accepted history changed: %d %d %d %d", observations, pending, reservations, limit)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.observation(observation_id,scope,log_offset,kind,occurred_at,ingested_at,source_role) VALUES(gen_random_uuid(),'p1',513,'turn',now(),now(),'user')`)); err == nil {
		t.Fatal("over-capacity backlog accepted another turn")
	}
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.observation SET formed_at=now() WHERE log_offset<2`)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.observation(observation_id,scope,log_offset,kind,occurred_at,ingested_at,source_role) VALUES(gen_random_uuid(),'p1',513,'turn',now(),now(),'user')`)); err != nil {
		t.Fatalf("capacity did not recover after draining: %v", err)
	}
}
