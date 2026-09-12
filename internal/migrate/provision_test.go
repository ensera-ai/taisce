// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package migrate

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func storageObjects(t *testing.T, pool *pgxpool.Pool, schema Schema) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1`, schema.String()).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestProjectNameRefusalNeverCreatesStorage(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	name := freshTenant(t, pool)
	schema, _ := NewSchema(name)
	before := storageObjects(t, pool, schema)
	for _, bad := range []string{"has-hyphen", "1leading", "_leading", "Uppercase", "", strings.Repeat("a", 64)} {
		if err := ProvisionScope(ctx, pool, name, bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
		if _, err := pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.project(scope,label) VALUES($1,'fixture')`), bad); err == nil {
			t.Fatalf("database accepted %q", bad)
		}
	}
	if after := storageObjects(t, pool, schema); after != before {
		t.Fatalf("invalid names left storage: %d -> %d", before, after)
	}
}

func TestLongProjectNamesWithTheSamePrefixHaveDistinctStorageAndPrune(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	name := freshTenant(t, pool)
	schema, _ := NewSchema(name)
	projects := []string{strings.Repeat("a", 62) + "x", strings.Repeat("a", 62) + "y"}
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); errs <- ProvisionScope(ctx, pool, name, projects[i%2]) }(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	partitions := make([]string, 2)
	for i, scope := range projects {
		if err := pool.QueryRow(ctx, findProjectPartitionSQL, name, scope).Scan(&partitions[i]); err != nil {
			t.Fatal(err)
		}
		table := projectStorageNames(scope)
		if len(table) > 63 || partitions[i] != table {
			t.Fatalf("unsafe physical name: %q %q", table, partitions[i])
		}
		source := seedObservation(t, pool, schema, scope)
		if _, err := pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.chunk(chunk_id,scope,source_observation_id,text,occurred_at,source_role) VALUES(gen_random_uuid(),$1,$2,'fixture',now(),'user')`), scope, source); err != nil {
			t.Fatal(err)
		}
	}
	if partitions[0] == partitions[1] {
		t.Fatal("long project names collided")
	}
	plan := explain(t, pool, schema.SQL(fmt.Sprintf(`SELECT chunk_id FROM {schema}.chunk WHERE scope='%s'`, projects[0])))
	if !strings.Contains(plan, partitions[0]) || strings.Contains(plan, partitions[1]) {
		t.Fatalf("wrong project pruning: %s", plan)
	}
	var count int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.project`)).Scan(&count); err != nil || count != 2 {
		t.Fatalf("concurrent provisioning: %d projects %v", count, err)
	}
}

func TestProjectRowFailureRollsBackItsPartitionAndIndex(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	name := freshTenant(t, pool)
	schema, _ := NewSchema(name)
	if _, err := pool.Exec(ctx, schema.SQL(`CREATE FUNCTION {schema}.refuse_project() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected project failure'; END $$;
 CREATE TRIGGER refuse_project BEFORE INSERT ON {schema}.project FOR EACH ROW EXECUTE FUNCTION {schema}.refuse_project()`)); err != nil {
		t.Fatal(err)
	}
	before := storageObjects(t, pool, schema)
	if err := ProvisionScope(ctx, pool, name, "failed"); err == nil || !strings.Contains(err.Error(), "injected project failure") {
		t.Fatalf("unexpected result: %v", err)
	}
	if after := storageObjects(t, pool, schema); after != before {
		t.Fatalf("failed transaction left storage: %d -> %d", before, after)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`DROP TRIGGER refuse_project ON {schema}.project`)); err != nil {
		t.Fatal(err)
	}
	if err := ProvisionScope(ctx, pool, name, "failed"); err != nil {
		t.Fatalf("retry after remediation: %v", err)
	}
}

func TestConflictingProjectRelationsAreNotTreatedAsSuccessfulProvisioning(t *testing.T) {
	for _, kind := range []string{"ordinary table", "wrong bound", "wrong parent"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			pool := testPool(t)
			name := freshTenant(t, pool)
			schema, _ := NewSchema(name)
			var sql string
			switch kind {
			case "ordinary table":
				sql = `CREATE TABLE {schema}.chunk_collision(marker int)`
			case "wrong bound":
				sql = `CREATE TABLE {schema}.chunk_collision PARTITION OF {schema}.chunk FOR VALUES IN ('other')`
			case "wrong parent":
				sql = `CREATE TABLE {schema}.other_chunk (LIKE {schema}.chunk INCLUDING DEFAULTS INCLUDING CONSTRAINTS) PARTITION BY LIST(scope);
      CREATE TABLE {schema}.chunk_collision PARTITION OF {schema}.other_chunk FOR VALUES IN ('collision')`
			}
			if _, err := pool.Exec(ctx, schema.SQL(sql)); err != nil {
				t.Fatal(err)
			}
			before := storageObjects(t, pool, schema)
			if err := ProvisionScope(ctx, pool, name, "collision"); !errors.Is(err, ErrProjectStorageConflict) {
				t.Fatalf("conflicting relation accepted: %v", err)
			}
			if after := storageObjects(t, pool, schema); after != before {
				t.Fatalf("conflict changed existing storage: %d -> %d", before, after)
			}
			var projects int
			if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.project`)).Scan(&projects); err != nil || projects != 0 {
				t.Fatalf("conflict created a project: %d %v", projects, err)
			}
		})
	}
}

func TestValidLegacyPartitionAndIndexNamesAreReused(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	name := freshTenant(t, pool)
	schema, _ := NewSchema(name)
	scope := strings.Repeat("p", 63)
	if _, err := pool.Exec(ctx, schema.SQL(fmt.Sprintf(`CREATE TABLE {schema}.legacy_partition PARTITION OF {schema}.chunk FOR VALUES IN ('%s');
 CREATE INDEX legacy_lookup ON {schema}.legacy_partition(occurred_at)`, scope))); err != nil {
		t.Fatal(err)
	}
	before := storageObjects(t, pool, schema)
	for i := 0; i < 2; i++ {
		if err := ProvisionScope(ctx, pool, name, scope); err != nil {
			t.Fatal(err)
		}
	}
	if after := storageObjects(t, pool, schema); after != before {
		t.Fatalf("legacy storage was duplicated: %d -> %d", before, after)
	}
	var partition string
	if err := pool.QueryRow(ctx, findProjectPartitionSQL, name, scope).Scan(&partition); err != nil || partition != "legacy_partition" {
		t.Fatalf("legacy attachment changed: %s %v", partition, err)
	}
}

func TestProjectNameMigrationValidatesExistingRowsAtomically(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	all, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	var prior []Script
	for _, script := range all {
		if script.Version < 24 {
			prior = append(prior, script)
		}
	}
	for _, length := range []int{63, 64} {
		t.Run(fmt.Sprint(length), func(t *testing.T) {
			schema, _ := NewSchema("project_upgrade_" + strings.ReplaceAll(uuid.NewString(), "-", ""))
			if _, err := pool.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema.String()}.Sanitize()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE") })
			if _, err := apply(ctx, pool, schema, prior); err != nil {
				t.Fatal(err)
			}
			scope := strings.Repeat("a", length)
			if _, err := pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.project(scope,label) VALUES($1,'existing')`), scope); err != nil {
				t.Fatal(err)
			}
			_, err := Apply(ctx, pool, schema)
			if length == 64 && err == nil {
				t.Fatal("oversized existing project was silently accepted")
			}
			if length == 63 && err != nil {
				t.Fatal(err)
			}
			var retained, version int
			if err := pool.QueryRow(ctx, schema.SQL(`SELECT (SELECT count(*) FROM {schema}.project WHERE scope=$1),(SELECT max(version) FROM {schema}.schema_migration)`), scope).Scan(&retained, &version); err != nil {
				t.Fatal(err)
			}
			expected := all[len(all)-1].Version
			if length == 64 {
				expected = 23
			}
			if retained != 1 || version != expected {
				t.Fatalf("partial migration or renamed identity: %d %d", retained, version)
			}
		})
	}
}

func TestProjectTableNamesCannotCollideWithAnotherProjectsIndex(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	name := freshTenant(t, pool)
	for _, scope := range []string{"alpha", "alpha_ann_idx"} {
		if err := ProvisionScope(ctx, pool, name, scope); err != nil {
			t.Fatalf("valid project %s collided with another project's index: %v", scope, err)
		}
	}
}

func TestExistingDefaultPartitionDataIsPreservedWhenAttachmentIsRefused(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	name := freshTenant(t, pool)
	schema, _ := NewSchema(name)
	source := seedObservation(t, pool, schema, "existing")
	if _, err := pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.chunk(chunk_id,scope,source_observation_id,text,occurred_at,source_role) VALUES(gen_random_uuid(),'existing',$1,'retained',now(),'user')`), source); err != nil {
		t.Fatal(err)
	}
	before := storageObjects(t, pool, schema)
	if err := ProvisionScope(ctx, pool, name, "existing"); err == nil || !strings.Contains(err.Error(), "create project partition") {
		t.Fatalf("overlapping attachment accepted: %v", err)
	}
	if after := storageObjects(t, pool, schema); after != before {
		t.Fatalf("attachment failure left storage: %d -> %d", before, after)
	}
	var retained int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.chunk_unpartitioned WHERE source_observation_id=$1 AND text='retained'`), source).Scan(&retained); err != nil || retained != 1 {
		t.Fatalf("default-partition data lost: %d %v", retained, err)
	}
}

func TestACancelledProvisionerLeavesNoProjectOrStorage(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	name := freshTenant(t, pool)
	schema, _ := NewSchema(name)
	held, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Rollback(ctx)
	if _, err := held.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1)::bigint)`, name+":project:waiting"); err != nil {
		t.Fatal(err)
	}
	before := storageObjects(t, pool, schema)
	bounded, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if err := ProvisionScope(bounded, pool, name, "waiting"); err == nil {
		t.Fatal("cancelled provisioner reported success")
	}
	if after := storageObjects(t, pool, schema); after != before {
		t.Fatalf("cancelled transaction left storage: %d -> %d", before, after)
	}
}

func TestALegacyPartitionWithNoANNIndexIsCompletedWithoutRenamingIt(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	name := freshTenant(t, pool)
	schema, _ := NewSchema(name)
	if _, err := pool.Exec(ctx, schema.SQL(`CREATE TABLE {schema}.legacy_unindexed PARTITION OF {schema}.chunk FOR VALUES IN ('legacy')`)); err != nil {
		t.Fatal(err)
	}
	if err := ProvisionScope(ctx, pool, name, "legacy"); err != nil {
		t.Fatal(err)
	}
	var indexed bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid JOIN pg_am a ON a.oid=c.relam WHERE i.indrelid=$1::regclass AND a.amname='hnsw')`, pgx.Identifier{name, "legacy_unindexed"}.Sanitize()).Scan(&indexed); err != nil || indexed {
		t.Fatalf("source partition unexpectedly requires an unstamped vector index: %v %v", indexed, err)
	}
	var partition string
	if err := pool.QueryRow(ctx, findProjectPartitionSQL, name, "legacy").Scan(&partition); err != nil || partition != "legacy_unindexed" {
		t.Fatalf("attachment changed: %s %v", partition, err)
	}
}
