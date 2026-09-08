//go:build inference

// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package inference_test

import (
	"context"
	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/extract"
	"github.com/ensera-ai/taisce/internal/formation"
	"github.com/ensera-ai/taisce/internal/infra/inference"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"strings"
	"testing"
	"time"
)

// This runs the production vocabulary gate and formation path, not only the model's proposal parser.
func TestLiveSpeakersAreBoundAcrossLanguagesAndCorrections(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	cfg, err := inference.ConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	dsn := os.Getenv("TAISCE_TEST_DSN")
	if dsn == "" {
		t.Fatal("TAISCE_TEST_DSN is required")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	const name = "inf_speaker_binding"
	if _, err := pool.Exec(ctx, "DROP SCHEMA IF EXISTS "+name+" CASCADE"); err != nil {
		t.Fatal(err)
	}
	if err := migrate.ProvisionMemorySchema(ctx, pool, name); err != nil {
		t.Fatal(err)
	}
	if err := migrate.ProvisionScope(ctx, pool, name, "p1"); err != nil {
		t.Fatal(err)
	}
	schema, _ := pg.NewSchema(name)
	v, err := pg.LoadVocabulary(ctx, pool, schema)
	if err != nil {
		t.Fatal(err)
	}
	obs := pg.NewObservationStore(pool)
	former := formation.NewFormer(obs, pg.NewFactStore(pool), extract.NewWith(inference.NewModel(cfg), v))
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for i, tc := range []struct {
		subject, content string
		accept           bool
	}{
		{"english-speaker", "I live in Dublin.", true},
		{"arabic-speaker", "أنا أسكن في عمان.", true},
		{"english-speaker", "I live in Paris.", true},
		{"reporter", `John said: "I live in London."`, false},
		{"", "I live in Cork.", false},
	} {
		o, err := obs.Append(ctx, schema, domain.Turn{Scope: "p1", DataSubjectID: tc.subject, OccurredAt: start.Add(time.Duration(i) * time.Hour), Messages: []domain.Message{{Role: domain.RoleUser, Content: tc.content}}})
		if err != nil {
			t.Fatal(err)
		}
		report, err := former.Form(ctx, schema, o)
		if err != nil {
			t.Fatalf("fixture %d: %v", i, err)
		}
		t.Logf("fixture %d: %d asserted, %d rejected", i, report.FactsAsserted, report.ClaimsRejected)
		if tc.accept && report.FactsAsserted != 1 {
			t.Errorf("fixture %d did not retain exactly one fact: %+v", i, report)
		}
		if !tc.accept && report.FactsAsserted != 0 {
			t.Errorf("fixture %d became an attributed fact: %+v", i, report)
		}
	}
	for _, subject := range []string{"english-speaker", "arabic-speaker"} {
		var count int
		err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.fact f JOIN {schema}.entity e ON e.entity_id=f.subject_entity_id
   WHERE f.scope='p1' AND f.data_subject_id=$1 AND e.identity_kind='speaker'
    AND e.speaker_subject_id=$1 AND f.predicate='lives_in' AND upper_inf(f.valid)`), subject).Scan(&count)
		if err != nil || count != 1 {
			t.Fatalf("%s has %d current correctly bound residences: %v", subject, count, err)
		}
		bundle, err := pg.NewRecallStore(pool, schema).FactsAboutForSubject(ctx, []string{"p1"}, []string{}, 20, []string{"user"}, domain.AsOf{}, 2, subject)
		if err != nil || len(bundle) != 0 {
			t.Fatal("empty anchor read widened")
		}
	}
	var obsolete int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.fact WHERE data_subject_id='english-speaker' AND NOT upper_inf(valid)`)).Scan(&obsolete); err != nil || obsolete != 1 {
		t.Fatalf("correction history: %d %v", obsolete, err)
	}
	var quotes []string
	rows, err := pool.Query(ctx, schema.SQL(`SELECT e.quote,m.content,e.byte_start,e.byte_end FROM {schema}.fact_evidence e JOIN {schema}.turn_message m ON m.observation_id=e.source_observation_id AND m.ordinal=e.source_ordinal`))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var q, c string
		var a, b int
		if err := rows.Scan(&q, &c, &a, &b); err != nil {
			t.Fatal(err)
		}
		if a < 0 || b > len(c) || c[a:b] != q {
			t.Fatal("evidence bytes changed")
		}
		quotes = append(quotes, q)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(quotes, " "), "عمان") {
		t.Fatal("Arabic source was lost")
	}
}
