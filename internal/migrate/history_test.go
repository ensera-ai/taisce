// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package migrate

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestHistoryUpgradePreservesBaselineWithoutInventingPastVersions(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	scripts, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	var prior []Script
	for _, s := range scripts {
		if s.Version < 29 {
			prior = append(prior, s)
		}
	}
	schema, _ := NewSchema("history_upgrade_" + strings.ReplaceAll(uuid.NewString(), "-", ""))
	if _, err := pool.Exec(ctx, "CREATE SCHEMA "+schema.String()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE") })
	if _, err := apply(ctx, pool, schema, prior); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	if _, err := pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.fact(fact_id,scope,predicate,statement,cardinality,valid,known,source_role)
 VALUES($1,'p1','works_at','Retained baseline','many','[2020-01-01,2021-01-01)'::tstzrange,'[2020-01-01,)'::tstzrange,'user')`), id); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(ctx, pool, schema); err != nil {
		t.Fatal(err)
	}
	var cutoff time.Time
	var histories, scopes int
	var original bool
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT available_from,(SELECT count(*) FROM {schema}.fact_history),
 (SELECT count(*) FROM {schema}.fact_history_boundary),EXISTS(SELECT 1 FROM {schema}.fact WHERE fact_id=$1 AND valid='[2020-01-01,2021-01-01)'::tstzrange AND known='[2020-01-01,)'::tstzrange) FROM {schema}.fact_history_boundary WHERE scope='p1'`), id).Scan(&cutoff, &histories, &scopes, &original); err != nil {
		t.Fatal(err)
	}
	if histories != 0 || scopes != 1 || !original || cutoff.Before(time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("upgrade fabricated history or changed retained baseline")
	}
	if _, err := Apply(ctx, pool, schema); err != nil {
		t.Fatal(err)
	}
	var again time.Time
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT available_from FROM {schema}.fact_history_boundary WHERE scope='p1'`)).Scan(&again); err != nil || !again.Equal(cutoff) {
		t.Fatal("restart moved historical boundary")
	}
}
