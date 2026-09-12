// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRuntimeWritesUseReservationsWithoutPermissionToForgeThem(t *testing.T) {
	ctx := context.Background()
	owner := testPool(t)
	if err := migrate.EstablishPlanes(ctx, owner, "taisce-test-control", "taisce-test-data"); err != nil {
		t.Fatal(err)
	}
	schema := tenant(t, owner, "ingestion_privileges")
	defer owner.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	config := owner.Config()
	config.ConnConfig.User = migrate.DataRole
	config.ConnConfig.Password = "taisce-test-data"
	runtime, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if err := migrate.ValidateRolePrivileges(ctx, owner, schema, migrate.DataRole, migrate.MemoryPlane); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		`UPDATE {schema}.ingestion_budget SET max_pending=1000000`,
		`DELETE FROM {schema}.ingestion_reservation`,
		`INSERT INTO {schema}.ingestion_project_usage(scope,pending) VALUES('forged',1)`,
		`TRUNCATE {schema}.ingestion_reservation`,
		`SELECT {schema}.sync_ingestion_reservation()`,
		`SELECT {schema}.release_ingestion_reservation()`,
	} {
		if _, err := runtime.Exec(ctx, schema.SQL(sql)); err == nil {
			t.Fatalf("runtime could forge capacity through %s", sql)
		}
	}
	store := pg.NewObservationStore(runtime)
	turn := turnOf(domain.Message{Role: domain.RoleUser, Content: "runtime reservation"})
	first, err := store.Append(ctx, schema, turn)
	if err != nil {
		t.Fatalf("installed trigger needed an unsafe grant: %v", err)
	}
	assertIngestionAccounting(t, owner, schema, 1)
	if err := store.MarkFormed(ctx, schema, first.ID); err != nil {
		t.Fatal(err)
	}
	assertIngestionAccounting(t, owner, schema, 0)
	if _, err := store.Append(ctx, schema, turn); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.NewEraser(runtime).Erase(ctx, schema, "p1", turn.DataSubjectID, "test"); err != nil {
		t.Fatal(err)
	}
	assertIngestionAccounting(t, owner, schema, 0)
	// Even an accidental EXECUTE grant cannot attach the privileged release function to a
	// caller-controlled temporary table and decrement somebody else's counters.
	if _, err := owner.Exec(ctx, schema.SQL(`GRANT EXECUTE ON FUNCTION {schema}.release_ingestion_reservation(),{schema}.sync_ingestion_reservation() TO taisce_data`)); err != nil {
		t.Fatal(err)
	}
	defer owner.Exec(ctx, schema.SQL(`REVOKE EXECUTE ON FUNCTION {schema}.release_ingestion_reservation(),{schema}.sync_ingestion_reservation() FROM taisce_data`))
	conn, err := runtime.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, schema.SQL(`CREATE TEMP TABLE ingestion_reservation(scope text); INSERT INTO ingestion_reservation VALUES('p1');
CREATE TRIGGER forged_release AFTER DELETE ON ingestion_reservation FOR EACH ROW EXECUTE FUNCTION {schema}.release_ingestion_reservation()`)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `DELETE FROM pg_temp.ingestion_reservation`); err == nil || !strings.Contains(err.Error(), "invalid ingestion release trigger target") {
		t.Fatalf("spoofed trigger target accepted: %v", err)
	}
	if _, err := conn.Exec(ctx, schema.SQL(`CREATE TEMP TABLE observation(observation_id uuid);
CREATE TRIGGER forged_reservation AFTER INSERT ON observation FOR EACH ROW EXECUTE FUNCTION {schema}.sync_ingestion_reservation()`)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO pg_temp.observation VALUES(gen_random_uuid())`); err == nil || !strings.Contains(err.Error(), "invalid ingestion reservation trigger target") {
		t.Fatalf("spoofed reservation trigger accepted: %v", err)
	}
	assertIngestionAccounting(t, owner, schema, 0)
}
