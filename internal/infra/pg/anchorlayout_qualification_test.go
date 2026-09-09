//go:build qualification

// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type anchorLayoutProject struct {
	Scope string `json:"scope"`
	Rows  int    `json:"rows"`
}

type anchorLayoutOperation struct {
	Seconds  float64 `json:"seconds"`
	WALBytes int64   `json:"wal_bytes"`
	Bytes    int64   `json:"bytes"`
}

type anchorLayoutLatency struct {
	P50MS float64 `json:"p50_ms"`
	P95MS float64 `json:"p95_ms"`
	P99MS float64 `json:"p99_ms"`
}

type anchorLayoutPlan struct {
	PlanningMS       float64  `json:"planning_ms"`
	ExecutionMS      float64  `json:"execution_ms"`
	ReturnedRows     int64    `json:"returned_rows"`
	ScanRows         int64    `json:"scan_rows"`
	SharedHitBlocks  int64    `json:"shared_hit_blocks"`
	SharedReadBlocks int64    `json:"shared_read_blocks"`
	Indexes          []string `json:"indexes"`
	SequentialScans  []string `json:"sequential_scans"`
}

type anchorLayoutQuery struct {
	Layout  string              `json:"layout"`
	Project string              `json:"project"`
	Rows    int                 `json:"project_rows"`
	Case    string              `json:"case"`
	Terms   int                 `json:"terms"`
	Latency anchorLayoutLatency `json:"latency"`
	Plan    anchorLayoutPlan    `json:"plan"`
}

type anchorLayoutResult struct {
	Corpus            string                           `json:"corpus"`
	Projects          []anchorLayoutProject            `json:"measured_projects"`
	NoiseProjects     int                              `json:"noise_projects"`
	NoiseRowsEach     int                              `json:"noise_rows_each"`
	Entities          int                              `json:"entities"`
	VariantsPerEntity int                              `json:"variants_per_entity"`
	MaxVariants       int                              `json:"max_variants"`
	Layouts           map[string]anchorLayoutOperation `json:"layouts"`
	Queries           []anchorLayoutQuery              `json:"queries"`
}

const legacyAnchorSQL = `
WITH terms AS (SELECT DISTINCT unnest($2::text[]) AS term)
SELECT * FROM (
 SELECT * FROM (
    SELECT e.entity_id::text,e.scope,e.canonical_name,e.normalized_name,e.entity_type,t.term
      FROM terms t JOIN %s.legacy_entity e
        ON e.scope=ANY($1) AND e.normalized_name=t.term
    UNION ALL
    SELECT e.entity_id::text,e.scope,e.canonical_name,e.normalized_name,e.entity_type,t.term
      FROM terms t JOIN %s.legacy_entity e
        ON e.scope=ANY($1) AND e.normalized_aliases @> ARRAY[t.term]
       AND e.normalized_name<>t.term
 ) candidates WHERE $3::text IS NULL LIMIT $4
) anchors ORDER BY length(term) DESC,canonical_name,entity_id`

const relationAnchorSQL = `
WITH terms AS (SELECT DISTINCT unnest($2::text[]) AS term)
SELECT * FROM (
 SELECT * FROM (
    SELECT e.entity_id::text,e.scope,e.canonical_name,e.normalized_name,e.entity_type,t.term
      FROM terms t JOIN %s.entity e
        ON e.scope=ANY($1) AND e.identity_kind='named' AND e.normalized_name=t.term
    UNION ALL
    SELECT e.entity_id::text,e.scope,e.canonical_name,e.normalized_name,e.entity_type,t.term
      FROM terms t JOIN %s.alias_relation a
        ON a.scope=ANY($1) AND a.normalized_name=t.term
      JOIN %s.entity e ON e.scope=a.scope AND e.entity_id=a.entity_id
       AND e.normalized_name<>t.term
 ) candidates WHERE $3::text IS NULL LIMIT $4
) anchors ORDER BY length(term) DESC,canonical_name,entity_id`

func TestAnchorLayoutOperatingEnvelope(t *testing.T) {
	if os.Getenv("TAISCE_ANCHOR_LAYOUT_QUALIFICATION") != "1" {
		t.Fatal("qualification requires TAISCE_ANCHOR_LAYOUT_QUALIFICATION=1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	pool := testPool(t)
	schema := tenant(t, pool, "anchor_layout")
	t.Cleanup(func() { pool.Exec(context.Background(), "DROP SCHEMA "+schema.String()+" CASCADE") })

	measured := []anchorLayoutProject{{Scope: "p1", Rows: 1000}, {Scope: "p_medium", Rows: 20000}, {Scope: "p_large", Rows: 200000}}
	const noiseProjects, noiseRows = 64, 2000
	projects := append([]anchorLayoutProject(nil), measured...)
	for i := 0; i < noiseProjects; i++ {
		projects = append(projects, anchorLayoutProject{Scope: fmt.Sprintf("p_noise_%02d", i), Rows: noiseRows})
	}
	if err := loadAnchorCorpus(ctx, pool, schema, projects); err != nil {
		t.Fatal(err)
	}

	layouts := map[string]anchorLayoutOperation{}
	selectedBytes, err := anchorRelationBytes(ctx, pool, schema, "entity", "entity_named_identity_uniq")
	if err != nil {
		t.Fatal(err)
	}
	layouts["canonical"] = anchorLayoutOperation{Bytes: selectedBytes}
	for _, layout := range []string{"legacy_array_gin", "normalized_relation"} {
		operation, err := createAnchorAlternative(ctx, pool, schema, layout)
		if err != nil {
			t.Fatalf("%s: %v", layout, err)
		}
		layouts[layout] = operation
	}

	queries := []anchorLayoutQuery{}
	for _, project := range measured {
		cases := []struct {
			name  string
			terms []string
		}{
			{name: "canonical_hit", terms: []string{"entity 900"}},
			{name: "sixty_four_spelling_variants", terms: []string{"entity 900"}},
			{name: "miss", terms: []string{"absent identity"}},
			{name: "large_question", terms: missingAnchorTerms(128)},
		}
		for _, layout := range []string{"canonical", "legacy_array_gin", "normalized_relation"} {
			query := anchorQuery(schema, layout)
			for _, queryCase := range cases {
				measurement, err := measureAnchorQuery(ctx, pool, schema, layout, query, project, queryCase.name, queryCase.terms)
				if err != nil {
					t.Fatalf("%s/%s/%s: %v", layout, project.Scope, queryCase.name, err)
				}
				queries = append(queries, measurement)
			}
		}
	}

	entities := 0
	for _, project := range projects {
		entities += project.Rows
	}
	result := anchorLayoutResult{
		Corpus: "anchor-layout-v1", Projects: measured, NoiseProjects: noiseProjects,
		NoiseRowsEach: noiseRows, Entities: entities, VariantsPerEntity: 4,
		MaxVariants: 64, Layouts: layouts, Queries: queries,
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("ANCHOR_LAYOUT_JSON %s", encoded)
}

func loadAnchorCorpus(ctx context.Context, pool *pgxpool.Pool, schema pg.Schema, projects []anchorLayoutProject) error {
	for _, project := range projects {
		if project.Scope != "p1" {
			if _, err := pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.project(scope,label) VALUES($1,$1)`), project.Scope); err != nil {
				return err
			}
		}
		if _, err := pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.entity(entity_id,scope,canonical_name,normalized_name,aliases)
SELECT gen_random_uuid(),$1,'Entity '||n,'entity '||n,
 ARRAY(SELECT repeat(' ',variant)||CASE WHEN variant%2=0 THEN upper('entity '||n) ELSE 'Entity '||n END
       FROM generate_series(1,CASE WHEN n=900 THEN 64 ELSE 4 END) variant)
FROM generate_series(1,$2::int) n`), project.Scope, project.Rows); err != nil {
			return err
		}
	}
	_, err := pool.Exec(ctx, schema.SQL(`ANALYZE {schema}.entity`))
	return err
}

func createAnchorAlternative(ctx context.Context, pool *pgxpool.Pool, schema pg.Schema, layout string) (anchorLayoutOperation, error) {
	var startLSN string
	if err := pool.QueryRow(ctx, `SELECT pg_current_wal_lsn()::text`).Scan(&startLSN); err != nil {
		return anchorLayoutOperation{}, err
	}
	started := time.Now()
	var statement string
	switch layout {
	case "legacy_array_gin":
		statement = schema.SQL(`CREATE TABLE {schema}.legacy_entity AS
SELECT entity_id,scope,canonical_name,normalized_name,entity_type,ARRAY[normalized_name]::text[] AS normalized_aliases
FROM {schema}.entity WHERE identity_kind='named';
CREATE UNIQUE INDEX legacy_entity_identity_idx ON {schema}.legacy_entity(scope,normalized_name);
CREATE INDEX legacy_entity_aliases_idx ON {schema}.legacy_entity USING gin(normalized_aliases);
ANALYZE {schema}.legacy_entity`)
	case "normalized_relation":
		statement = schema.SQL(`CREATE TABLE {schema}.alias_relation AS
SELECT scope,normalized_name,entity_id FROM {schema}.entity WHERE identity_kind='named';
CREATE UNIQUE INDEX alias_relation_identity_idx ON {schema}.alias_relation(scope,normalized_name,entity_id);
ANALYZE {schema}.alias_relation`)
	default:
		return anchorLayoutOperation{}, fmt.Errorf("unknown layout %q", layout)
	}
	if _, err := pool.Exec(ctx, statement); err != nil {
		return anchorLayoutOperation{}, err
	}
	operation := anchorLayoutOperation{Seconds: time.Since(started).Seconds()}
	if err := pool.QueryRow(ctx, `SELECT pg_wal_lsn_diff(pg_current_wal_lsn(),$1::pg_lsn)::bigint`, startLSN).Scan(&operation.WALBytes); err != nil {
		return operation, err
	}
	var err error
	if layout == "legacy_array_gin" {
		operation.Bytes, err = anchorRelationBytes(ctx, pool, schema, "legacy_entity", "legacy_entity_identity_idx", "legacy_entity_aliases_idx")
	} else {
		operation.Bytes, err = anchorRelationBytes(ctx, pool, schema, "alias_relation", "alias_relation_identity_idx")
	}
	return operation, err
}

func anchorRelationBytes(ctx context.Context, pool *pgxpool.Pool, schema pg.Schema, names ...string) (int64, error) {
	var total int64
	for _, name := range names {
		var bytes int64
		if err := pool.QueryRow(ctx, `SELECT pg_relation_size($1::regclass)`, schema.String()+"."+name).Scan(&bytes); err != nil {
			return 0, err
		}
		total += bytes
	}
	return total, nil
}

func anchorQuery(schema pg.Schema, layout string) string {
	quoted := pgx.Identifier{schema.String()}.Sanitize()
	switch layout {
	case "canonical":
		return schema.SQL(pg.AnchorQueryForTesting)
	case "legacy_array_gin":
		return fmt.Sprintf(legacyAnchorSQL, quoted, quoted)
	case "normalized_relation":
		return fmt.Sprintf(relationAnchorSQL, quoted, quoted, quoted)
	default:
		panic("unknown anchor layout " + layout)
	}
}

func missingAnchorTerms(count int) []string {
	terms := make([]string, count)
	for i := range terms {
		terms[i] = fmt.Sprintf("missing %03d", i)
	}
	return terms
}

func measureAnchorQuery(ctx context.Context, pool *pgxpool.Pool, schema pg.Schema, layout, query string,
	project anchorLayoutProject, name string, terms []string) (anchorLayoutQuery, error) {
	measurement := anchorLayoutQuery{Layout: layout, Project: project.Scope, Rows: project.Rows, Case: name, Terms: len(terms)}
	var raw json.RawMessage
	if err := pool.QueryRow(ctx, "EXPLAIN (ANALYZE, BUFFERS, WAL, FORMAT JSON) "+query,
		[]string{project.Scope}, terms, nil, domain.MaxRecallMatches+1).Scan(&raw); err != nil {
		return measurement, err
	}
	plan, err := parseAnchorPlan(raw)
	if err != nil {
		return measurement, err
	}
	measurement.Plan = plan
	want := int64(0)
	if name == "canonical_hit" || name == "sixty_four_spelling_variants" {
		want = 1
	}
	if plan.ReturnedRows != want {
		return measurement, fmt.Errorf("returned %d rows, want %d", plan.ReturnedRows, want)
	}
	if layout == "canonical" {
		for _, relation := range plan.SequentialScans {
			if relation == "entity" {
				return measurement, fmt.Errorf("production query scanned entity")
			}
		}
		found := false
		for _, index := range plan.Indexes {
			if index == "entity_named_identity_uniq" {
				found = true
			}
		}
		if !found {
			return measurement, fmt.Errorf("production identity index not selected: %v", plan.Indexes)
		}
	}

	for i := 0; i < 3; i++ {
		if _, err := executeAnchorQuery(ctx, pool, query, project.Scope, terms); err != nil {
			return measurement, err
		}
	}
	durations := make([]float64, 31)
	for i := range durations {
		started := time.Now()
		count, err := executeAnchorQuery(ctx, pool, query, project.Scope, terms)
		if err != nil {
			return measurement, err
		}
		if int64(count) != want {
			return measurement, fmt.Errorf("query returned %d rows, want %d", count, want)
		}
		durations[i] = float64(time.Since(started).Microseconds()) / 1000
	}
	sort.Float64s(durations)
	measurement.Latency = anchorLayoutLatency{P50MS: anchorPercentile(durations, 0.50), P95MS: anchorPercentile(durations, 0.95), P99MS: anchorPercentile(durations, 0.99)}
	return measurement, nil
}

func executeAnchorQuery(ctx context.Context, pool *pgxpool.Pool, query, scope string, terms []string) (int, error) {
	rows, err := pool.Query(ctx, query, []string{scope}, terms, nil, domain.MaxRecallMatches+1)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var values [6]string
		if err := rows.Scan(&values[0], &values[1], &values[2], &values[3], &values[4], &values[5]); err != nil {
			return 0, err
		}
		count++
	}
	return count, rows.Err()
}

type anchorExplainEnvelope struct {
	Plan          anchorExplainNode `json:"Plan"`
	PlanningTime  float64           `json:"Planning Time"`
	ExecutionTime float64           `json:"Execution Time"`
}

type anchorExplainNode struct {
	NodeType          string              `json:"Node Type"`
	Relation          string              `json:"Relation Name"`
	Index             string              `json:"Index Name"`
	ActualRows        float64             `json:"Actual Rows"`
	ActualLoops       float64             `json:"Actual Loops"`
	RowsRemovedFilter float64             `json:"Rows Removed by Filter"`
	SharedHitBlocks   int64               `json:"Shared Hit Blocks"`
	SharedReadBlocks  int64               `json:"Shared Read Blocks"`
	Plans             []anchorExplainNode `json:"Plans"`
}

func parseAnchorPlan(raw json.RawMessage) (anchorLayoutPlan, error) {
	var envelope []anchorExplainEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil || len(envelope) != 1 {
		return anchorLayoutPlan{}, fmt.Errorf("decode plan: %w", err)
	}
	result := anchorLayoutPlan{
		PlanningMS: envelope[0].PlanningTime, ExecutionMS: envelope[0].ExecutionTime,
		ReturnedRows:    int64(envelope[0].Plan.ActualRows * envelope[0].Plan.ActualLoops),
		SharedHitBlocks: envelope[0].Plan.SharedHitBlocks, SharedReadBlocks: envelope[0].Plan.SharedReadBlocks,
	}
	indexes, scans := map[string]bool{}, map[string]bool{}
	var inspect func(anchorExplainNode)
	inspect = func(node anchorExplainNode) {
		if strings.Contains(node.NodeType, "Scan") {
			result.ScanRows += int64((node.ActualRows + node.RowsRemovedFilter) * node.ActualLoops)
		}
		if node.Index != "" {
			indexes[node.Index] = true
		}
		if node.NodeType == "Seq Scan" && node.Relation != "" {
			scans[node.Relation] = true
		}
		for _, child := range node.Plans {
			inspect(child)
		}
	}
	inspect(envelope[0].Plan)
	for index := range indexes {
		result.Indexes = append(result.Indexes, index)
	}
	for scan := range scans {
		result.SequentialScans = append(result.SequentialScans, scan)
	}
	sort.Strings(result.Indexes)
	sort.Strings(result.SequentialScans)
	return result, nil
}

func anchorPercentile(sorted []float64, quantile float64) float64 {
	index := int(float64(len(sorted)-1) * quantile)
	return sorted[index]
}
