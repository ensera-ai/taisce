// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package migrate

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// A project that existed before it could say whether its people share a world keeps the rule its
// facts were formed under, and a project created afterwards gets the one whose mistake is visible.
//
// Before the setting existed there was one rule: a later statement closed an earlier one, whoever
// made either. Every fact a project already holds was written under it and is filed under the one
// shared account. Upgrading must leave such a project `shared` — changing the rule underneath facts
// already written would leave rows filed under one account and governed by another — while a project
// nobody has decided about yet is `separate`, because a shared world left on `separate` shows two
// answers where there should be one, and separate worlds left on `shared` lose a true fact silently.
func TestAnExistingProjectKeepsTheRuleItsFactsWereFormedUnder(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema, _ := NewSchema("statements_upgrade_" + strings.ReplaceAll(uuid.NewString(), "-", ""))
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
		if script.Version < 75 {
			prior = append(prior, script)
		}
	}
	if _, err := apply(ctx, pool, schema, prior); err != nil {
		t.Fatal(err)
	}
	// A project, an entity and one single-valued fact, as a release before this one left them.
	if _, err := pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.project(scope,label) VALUES('before','before')`)); err != nil {
		t.Fatal(err)
	}
	var entity string
	if err := pool.QueryRow(ctx, schema.SQL(`INSERT INTO {schema}.entity(entity_id,scope,canonical_name,normalized_name,entity_type)
		VALUES(gen_random_uuid(),'before','Atlas','atlas','project') RETURNING entity_id::text`)).Scan(&entity); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.fact
		(fact_id,scope,subject_entity_id,predicate,statement,valid,confidence,cardinality,source_role,data_subject_id)
		VALUES(gen_random_uuid(),'before',$1::uuid,'located_in','Atlas is in Dublin.',tstzrange(now(),NULL),0.9,'one','user','alice')`), entity); err != nil {
		t.Fatal(err)
	}

	if _, err := Apply(ctx, pool, schema); err != nil {
		t.Fatalf("the migration failed on a project that already held a fact: %v", err)
	}

	var statements, account string
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT p.statements, f.account FROM {schema}.project p
		JOIN {schema}.fact f ON f.scope=p.scope WHERE p.scope='before'`)).Scan(&statements, &account); err != nil {
		t.Fatal(err)
	}
	if statements != "shared" || account != "" {
		t.Fatalf("a project that existed before the setting is %q with its fact filed under %q; it must stay shared, under the shared account", statements, account)
	}
	// The rule it kept still binds: a second current value from somebody else is refused there,
	// exactly as it was before the upgrade.
	_, err = pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.fact
		(fact_id,scope,subject_entity_id,predicate,statement,valid,confidence,cardinality,source_role,data_subject_id)
		VALUES(gen_random_uuid(),'before',$1::uuid,'located_in','Atlas is in Oslo.',tstzrange(now(),NULL),0.9,'one','user','bob')`), entity)
	if err == nil || !strings.Contains(err.Error(), "fact_single_cardinality_excl") {
		t.Fatalf("in a project that stayed shared, a second current value from another person was stored: %v", err)
	}

	if err := ProvisionScope(ctx, pool, schema.String(), "after"); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT statements FROM {schema}.project WHERE scope='after'`)).Scan(&statements); err != nil {
		t.Fatal(err)
	}
	if statements != "separate" {
		t.Fatalf("a project created after the setting existed is %q; nobody chose, so it must be separate", statements)
	}
}
