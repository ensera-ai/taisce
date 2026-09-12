// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package formation_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/ensera-ai/taisce/internal/formation"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/google/uuid"
)

func TestRetentionDriverRemovesUnusedSubjectMappingsWithoutAnyObservations(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "fm_subject_expiry")
	if err := migrate.ProvisionScope(ctx, pool, schema.String(), "p1"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.project SET retention=interval '1 microsecond' WHERE scope='p1'`)); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.NewSubjectStore(pool, schema).Register(ctx, "p1", uuid.NewString(), pg.SubjectRegistration{IdempotencyKey: uuid.NewString()}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.project SET retention=NULL WHERE scope='p1'`)); err != nil {
		t.Fatal(err)
	}
	driver := formation.NewDriver(workerWith(t, pool, schema, scriptedModel{}, impatient()), pg.NewObservationStore(pool), pg.NewRetentionStore(pool, schema), pg.NewAuditStore(pool, schema), schema, impatient(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := driver.SweepRetention(ctx); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT (SELECT count(*) FROM {schema}.data_subject)+(SELECT count(*) FROM {schema}.subject_retry WHERE subject_id IS NOT NULL OR request_digest IS NOT NULL)`)).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatal("quiet project retained inactive mapping or retry metadata")
	}
}
