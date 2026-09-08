// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package migrate

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestRuntimeRelationshipsCannotCrossProjectsOrLeaveDanglingTargets(t *testing.T) {
	ctx := context.Background()
	owner := testPool(t)
	password := establish(t, owner, "pl_integrity")
	data := asDataPlane(t, password)
	schema, _ := NewSchema("pl_integrity")
	for _, scope := range []string{"p1", "p2"} {
		if err := ProvisionScope(ctx, owner, schema.String(), scope); err != nil {
			t.Fatal(err)
		}
		content := "Marta works at Ensera."
		o, err := pg.NewObservationStore(data).Append(ctx, schema, domain.Turn{Scope: scope, DataSubjectID: scope, OccurredAt: time.Now(), Messages: []domain.Message{{Role: domain.RoleUser, Content: content}}})
		if err != nil {
			t.Fatal(err)
		}
		_, err = pg.NewFactStore(data).Assert(ctx, schema, scope, o.ID, domain.RoleUser, scope, domain.Claim{Subject: "Marta", Object: "Ensera", Predicate: "works_at", Cardinality: domain.CardinalityMany, Statement: content, Quote: content, ByteEnd: len(content)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := data.Exec(ctx, schema.SQL(`INSERT INTO {schema}.community(scope,community_id,level) VALUES($1,gen_random_uuid(),0)`), scope); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct{ name, sql string }{
		{"fact subject", `UPDATE {schema}.fact SET subject_entity_id=(SELECT entity_id FROM {schema}.entity WHERE scope='p2' LIMIT 1) WHERE scope='p1'`},
		{"fact object", `UPDATE {schema}.fact SET object_entity_id=(SELECT entity_id FROM {schema}.entity WHERE scope='p2' LIMIT 1) WHERE scope='p1'`},
		{"evidence source", `UPDATE {schema}.fact_evidence SET source_observation_id=(SELECT observation_id FROM {schema}.observation WHERE scope='p2') WHERE scope='p1'`},
		{"evidence fact", `UPDATE {schema}.fact_evidence SET fact_id=(SELECT fact_id FROM {schema}.fact WHERE scope='p2') WHERE scope='p1'`},
		{"evidence missing message", `UPDATE {schema}.fact_evidence SET source_ordinal=99 WHERE scope='p1'`},
		{"dependency source", `UPDATE {schema}.projection_dependency SET source_observation_id=(SELECT observation_id FROM {schema}.observation WHERE scope='p2') WHERE scope='p1' AND projection_kind='fact'`},
		{"dependency target", `UPDATE {schema}.projection_dependency SET projection_id=(SELECT fact_id::text FROM {schema}.fact WHERE scope='p2') WHERE scope='p1' AND projection_kind='fact'`},
		{"dependency absent target", `UPDATE {schema}.projection_dependency SET projection_id=gen_random_uuid()::text WHERE scope='p1' AND projection_kind='fact'`},
		{"chunk source", `UPDATE {schema}.chunk SET source_observation_id=(SELECT observation_id FROM {schema}.observation WHERE scope='p2') WHERE scope='p1'`},
		{"rejected source", `INSERT INTO {schema}.rejected_claim(rejected_claim_id,scope,source_observation_id,source_ordinal,predicate,statement,quote,reason,extractor_version) SELECT gen_random_uuid(),'p1',observation_id,0,'unknown','fixture','fixture','unmapped_relation','fixture' FROM {schema}.observation WHERE scope='p2'`},
		{"rejected message", `INSERT INTO {schema}.rejected_claim(rejected_claim_id,scope,source_observation_id,source_ordinal,predicate,statement,quote,reason,extractor_version) SELECT gen_random_uuid(),'p1',observation_id,99,'unknown','fixture','fixture','unmapped_relation','fixture' FROM {schema}.observation WHERE scope='p1'`},
		{"community member", `INSERT INTO {schema}.community_member(scope,community_id,entity_id) SELECT 'p1',c.community_id,e.entity_id FROM {schema}.community c CROSS JOIN {schema}.entity e WHERE c.scope='p1' AND e.scope='p2' LIMIT 1`},
		{"community parent", `UPDATE {schema}.community SET level=1,parent_id=(SELECT community_id FROM {schema}.community WHERE scope='p2') WHERE scope='p1'`},
		{"delete referenced entity", `DELETE FROM {schema}.entity WHERE scope='p1'`},
		{"delete referenced source", `DELETE FROM {schema}.observation WHERE scope='p1'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := pgx.BeginFunc(ctx, data, func(tx pgx.Tx) error { _, err := tx.Exec(ctx, schema.SQL(tc.sql)); return err })
			var pgerr *pgconn.PgError
			if !errors.As(err, &pgerr) || pgerr.Code != "23503" {
				t.Fatalf("wanted FK refusal at statement or commit, got %v", err)
			}
		})
	}
	// This role is deliberately deployment-wide. Referential integrity does not replace the
	// authenticated scope set in SELECT queries, and no RLS confidentiality claim is made.
	var scopes int
	if err := data.QueryRow(ctx, schema.SQL(`SELECT count(DISTINCT scope) FROM {schema}.observation`)).Scan(&scopes); err != nil || scopes != 2 {
		t.Fatalf("rollback or deployment role changed: %d %v", scopes, err)
	}
	var super, bypass bool
	if err := data.QueryRow(ctx, `SELECT rolsuper,rolbypassrls FROM pg_roles WHERE rolname=current_user`).Scan(&super, &bypass); err != nil || super || bypass {
		t.Fatalf("privileged runtime role: %v %v %v", super, bypass, err)
	}
	for _, sql := range []string{
		`ALTER TABLE {schema}.fact DROP CONSTRAINT fact_subject_project_fk`,
		`ALTER TABLE {schema}.fact DISABLE TRIGGER ALL`,
		`UPDATE {schema}.projection_kind SET survives_sharing=true`,
		`DELETE FROM {schema}.speaker_term`,
		`DELETE FROM {schema}.unresolvable_term`,
		`DELETE FROM {schema}.predicate`,
		`DELETE FROM {schema}.schema_migration`,
	} {
		_, err := data.Exec(ctx, schema.SQL(sql))
		var pgerr *pgconn.PgError
		if !errors.As(err, &pgerr) || pgerr.Code != "42501" {
			t.Fatalf("runtime changed schema/policy: %s: %v", sql, err)
		}
	}
	// A complete transaction may delete in either order; a partial erasure cannot commit.
	err := pgx.BeginFunc(ctx, data, func(tx pgx.Tx) error {
		for _, table := range []string{"entity", "fact", "chunk", "observation"} {
			if _, err := tx.Exec(ctx, schema.SQL(`DELETE FROM {schema}.`+table+` WHERE scope='p1'`)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("complete deferred transaction refused: %v", err)
	}
}

func TestRelationshipMigrationValidatesHistoryBeforeInvalidatingReports(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	scripts, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	var prior []Script
	for _, s := range scripts {
		if s.Version < 23 {
			prior = append(prior, s)
		}
	}
	for _, bad := range []bool{false, true} {
		t.Run(map[bool]string{false: "valid", true: "cross-project"}[bad], func(t *testing.T) {
			schema, _ := NewSchema("integrity_upgrade_" + strings.ReplaceAll(uuid.NewString(), "-", ""))
			if _, err := pool.Exec(ctx, "CREATE SCHEMA "+schema.String()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE") })
			if _, err := apply(ctx, pool, schema, prior); err != nil {
				t.Fatal(err)
			}
			scope := "p1"
			if bad {
				scope = "p2"
			}
			entity := uuid.NewString()
			fact := uuid.NewString()
			if _, err := pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.entity(entity_id,scope,canonical_name,normalized_name) VALUES($1,$2,'Marta','marta')`), entity, scope); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.fact(fact_id,scope,subject_entity_id,predicate,statement,valid,cardinality,source_role) VALUES($1,'p1',$2,'works_at','fixture',tstzrange(now(),NULL),'many','user')`), fact, entity); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.community(scope,community_id,level) VALUES('p1','11111111-1111-1111-1111-111111111111',0);
                INSERT INTO {schema}.community_report(scope,report_id,community_id,title,summary) VALUES('p1',gen_random_uuid(),'11111111-1111-1111-1111-111111111111','old','unverified provenance')`)); err != nil {
				t.Fatal(err)
			}
			_, err = Apply(ctx, pool, schema)
			if bad {
				if err == nil {
					t.Fatal("invalid relationship upgraded")
				}
				var version int
				var column bool
				if err := pool.QueryRow(ctx, schema.SQL(`SELECT max(version) FROM {schema}.schema_migration`)).Scan(&version); err != nil || version != 22 {
					t.Fatalf("partial version: %d %v", version, err)
				}
				if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=$1 AND table_name='fact_evidence' AND column_name='scope')`, schema.String()).Scan(&column); err != nil || column {
					t.Fatalf("partial DDL: %v %v", column, err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			var reports int
			if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.community_report`)).Scan(&reports); err != nil {
				t.Fatal(err)
			}
			expected := 0
			if bad {
				expected = 1
			}
			if reports != expected {
				t.Fatalf("report invalidation was not atomic: %d", reports)
			}
			var retained int
			if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.fact WHERE fact_id=$1`), fact).Scan(&retained); err != nil || retained != 1 {
				t.Fatalf("lost authoritative projection: %d %v", retained, err)
			}
		})
	}
}
