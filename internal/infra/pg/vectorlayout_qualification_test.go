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
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	vectorLayoutDimensions = 2560
	vectorLayoutHashParts  = 32
)

type vectorLayoutProject struct {
	Scope      string
	Generation uuid.UUID
	Rows       int
}

type vectorLayoutOperation struct {
	Seconds  float64 `json:"seconds"`
	WALBytes int64   `json:"wal_bytes"`
}

type vectorLayoutLatency struct {
	P50MS float64 `json:"p50_ms"`
	P95MS float64 `json:"p95_ms"`
	P99MS float64 `json:"p99_ms"`
}

type vectorLayoutANN struct {
	Candidates  int                 `json:"candidates"`
	RecallAt10  float64             `json:"recall_at_10"`
	MeanResults float64             `json:"mean_results"`
	Latency     vectorLayoutLatency `json:"latency"`
}

type vectorLayoutPlan struct {
	PlanningMS       float64  `json:"planning_ms"`
	ExecutionMS      float64  `json:"execution_ms"`
	SharedHitBlocks  int64    `json:"shared_hit_blocks"`
	SharedReadBlocks int64    `json:"shared_read_blocks"`
	WALBytes         int64    `json:"wal_bytes"`
	Indexes          []string `json:"indexes"`
	Relations        []string `json:"relations"`
}

type vectorLayoutMeasurement struct {
	Name          string                `json:"name"`
	Partitions    int                   `json:"partitions"`
	Relations     int                   `json:"relations"`
	Bytes         int64                 `json:"bytes"`
	Provision     vectorLayoutOperation `json:"provision"`
	Load          vectorLayoutOperation `json:"load"`
	ExactLatency  vectorLayoutLatency   `json:"exact_latency"`
	ANN           []vectorLayoutANN     `json:"ann"`
	CandidatePlan vectorLayoutPlan      `json:"candidate_plan"`
	Erasure       vectorLayoutErasure   `json:"erasure"`
}

type vectorLayoutErasure struct {
	Remove           vectorLayoutOperation `json:"remove"`
	Vacuum           vectorLayoutOperation `json:"vacuum"`
	DeadBefore       int64                 `json:"dead_before_vacuum"`
	BytesBefore      int64                 `json:"bytes_before"`
	BytesAfter       int64                 `json:"bytes_after_remove"`
	BytesAfterVacuum int64                 `json:"bytes_after_vacuum"`
}

type vectorLayoutCatalog struct {
	Generations      int                   `json:"generations"`
	Relations        int                   `json:"relations"`
	Bytes            int64                 `json:"bytes"`
	Cumulative       vectorLayoutOperation `json:"cumulative_provision"`
	PlanningLatency  vectorLayoutLatency   `json:"planning_latency"`
	PlannedRelations int                   `json:"planned_relations"`
}

type vectorLayoutResult struct {
	Corpus       string                    `json:"corpus"`
	Dimensions   int                       `json:"dimensions"`
	Projects     int                       `json:"projects"`
	Rows         int                       `json:"rows"`
	Distribution map[string]int            `json:"project_distribution"`
	Queries      int                       `json:"queries"`
	Layouts      []vectorLayoutMeasurement `json:"layouts"`
	Catalog      []vectorLayoutCatalog     `json:"catalog"`
}

func TestVectorLayoutOperatingEnvelope(t *testing.T) {
	if os.Getenv("TAISCE_VECTOR_LAYOUT_QUALIFICATION") != "1" {
		t.Fatal("qualification requires TAISCE_VECTOR_LAYOUT_QUALIFICATION=1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()
	pool := testPool(t)
	schema := "vector_layout_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := pool.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), "DROP SCHEMA "+quotedSchema+" CASCADE") })

	projects := vectorLayoutProjects()
	totalRows := 0
	for _, project := range projects {
		totalRows += project.Rows
	}
	if _, err := pool.Exec(ctx, fmt.Sprintf(`CREATE TABLE %s.corpus(
 scope text NOT NULL,generation_id uuid NOT NULL,item_id bigint NOT NULL,embedding vector NOT NULL,
 PRIMARY KEY(scope,generation_id,item_id))`, quotedSchema)); err != nil {
		t.Fatal(err)
	}
	if err := loadVectorLayoutCorpus(ctx, pool, quotedSchema, projects); err != nil {
		t.Fatal(err)
	}

	layouts := []string{"ordinary", "generation", "hash"}
	measurements := make([]vectorLayoutMeasurement, 0, len(layouts))
	for _, layout := range layouts {
		measurement, err := qualifyVectorLayout(ctx, pool, schema, layout, projects)
		if err != nil {
			t.Fatalf("%s layout: %v", layout, err)
		}
		measurements = append(measurements, measurement)
	}
	catalog, err := qualifyVectorLayoutCatalog(ctx, pool, schema)
	if err != nil {
		t.Fatal(err)
	}
	result := vectorLayoutResult{
		Corpus: "vector-layout-v1", Dimensions: vectorLayoutDimensions, Projects: len(projects),
		Rows: totalRows, Distribution: map[string]int{"small_16": 112, "medium_256": 15, "large_8192": 1},
		Queries: 24, Layouts: measurements, Catalog: catalog,
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("VECTOR_LAYOUT_JSON %s", encoded)
}

func vectorLayoutProjects() []vectorLayoutProject {
	projects := make([]vectorLayoutProject, 128)
	for i := range projects {
		rows := 16
		if i >= 112 {
			rows = 256
		}
		if i == len(projects)-1 {
			rows = 8192
		}
		scope := fmt.Sprintf("p%04d", i)
		projects[i] = vectorLayoutProject{Scope: scope,
			Generation: uuid.NewSHA1(uuid.NameSpaceOID, []byte("vector-layout-v1/"+scope)), Rows: rows}
	}
	return projects
}

func vectorLayoutLiteral(project, row int) string {
	seed := uint64(project+1)*0x9e3779b97f4a7c15 ^ uint64(row+1)*0xbf58476d1ce4e5b9
	buffer := make([]byte, 0, vectorLayoutDimensions*3)
	buffer = append(buffer, '[')
	for dimension := 0; dimension < vectorLayoutDimensions; dimension++ {
		seed ^= seed >> 12
		seed ^= seed << 25
		seed ^= seed >> 27
		value := int64((seed*0x2545f4914f6cdd1d)%7) - 3
		if dimension > 0 {
			buffer = append(buffer, ',')
		}
		buffer = strconv.AppendInt(buffer, value, 10)
	}
	buffer = append(buffer, ']')
	return string(buffer)
}

func loadVectorLayoutCorpus(ctx context.Context, pool *pgxpool.Pool, schema string, projects []vectorLayoutProject) error {
	const batchSize = 64
	scopes := make([]string, 0, batchSize)
	generations := make([]uuid.UUID, 0, batchSize)
	items := make([]int64, 0, batchSize)
	vectors := make([]string, 0, batchSize)
	flush := func() error {
		if len(scopes) == 0 {
			return nil
		}
		_, err := pool.Exec(ctx, fmt.Sprintf(`INSERT INTO %s.corpus(scope,generation_id,item_id,embedding)
 SELECT scope,generation_id,item_id,embedding::vector
 FROM unnest($1::text[],$2::uuid[],$3::bigint[],$4::text[])
 AS rows(scope,generation_id,item_id,embedding)`, schema), scopes, generations, items, vectors)
		scopes = scopes[:0]
		generations = generations[:0]
		items = items[:0]
		vectors = vectors[:0]
		return err
	}
	for projectIndex, project := range projects {
		for row := 0; row < project.Rows; row++ {
			scopes = append(scopes, project.Scope)
			generations = append(generations, project.Generation)
			items = append(items, int64(row))
			vectors = append(vectors, vectorLayoutLiteral(projectIndex, row))
			if len(scopes) == batchSize {
				if err := flush(); err != nil {
					return err
				}
			}
		}
	}
	return flush()
}

func qualifyVectorLayout(ctx context.Context, pool *pgxpool.Pool, schema, layout string,
	projects []vectorLayoutProject) (vectorLayoutMeasurement, error) {
	measurement := vectorLayoutMeasurement{Name: layout}
	operation, err := measureVectorOperation(ctx, pool, func() error {
		return createVectorLayout(ctx, pool, schema, layout, projects)
	})
	if err != nil {
		return measurement, err
	}
	measurement.Provision = operation
	operation, err = measureVectorOperation(ctx, pool, func() error {
		if _, err := pool.Exec(ctx, fmt.Sprintf(`INSERT INTO %s.%s SELECT * FROM %s.corpus`, schema, layout, schema)); err != nil {
			return err
		}
		_, err := pool.Exec(ctx, fmt.Sprintf(`ANALYZE %s.%s`, schema, layout))
		return err
	})
	if err != nil {
		return measurement, err
	}
	measurement.Load = operation
	measurement.Partitions, measurement.Relations, measurement.Bytes, err = vectorLayoutStorage(ctx, pool, schema, layout)
	if err != nil {
		return measurement, err
	}
	if measurement.Relations == 0 || measurement.Bytes == 0 {
		return measurement, fmt.Errorf("storage measurement is empty: relations=%d bytes=%d",
			measurement.Relations, measurement.Bytes)
	}
	measurement.ExactLatency, measurement.ANN, err = measureVectorLayoutQueries(ctx, pool, schema, layout, projects)
	if err != nil {
		return measurement, err
	}
	representative := projects[len(projects)-1]
	query, err := vectorLayoutQuery(ctx, pool, schema, representative, 73)
	if err != nil {
		return measurement, err
	}
	measurement.CandidatePlan, err = explainVectorLayout(ctx, pool, schema, layout, representative, query)
	if err != nil {
		return measurement, err
	}
	if layout == "generation" && !containsVectorLayoutIndex(measurement.CandidatePlan.Indexes, layout+"_") {
		return measurement, fmt.Errorf("candidate plan did not use the %s HNSW index: %v", layout, measurement.CandidatePlan.Indexes)
	}
	measurement.Erasure, err = eraseVectorLayoutProject(ctx, pool, schema, layout, representative)
	return measurement, err
}

func createVectorLayout(ctx context.Context, pool *pgxpool.Pool, schema, layout string,
	projects []vectorLayoutProject) error {
	base := fmt.Sprintf(`(scope text NOT NULL,generation_id uuid NOT NULL,item_id bigint NOT NULL,
 embedding vector NOT NULL,PRIMARY KEY(scope,generation_id,item_id))`)
	index := func(table, name string) error {
		_, err := pool.Exec(ctx, fmt.Sprintf(`CREATE INDEX %s ON %s USING hnsw
 ((l2_normalize(embedding)::halfvec(%d)) halfvec_cosine_ops)`, name, table, vectorLayoutDimensions))
		return err
	}
	switch layout {
	case "ordinary":
		table := schema + ".ordinary"
		if _, err := pool.Exec(ctx, "CREATE TABLE "+table+base); err != nil {
			return err
		}
		return index(table, "ordinary_ann")
	case "generation":
		parent := schema + ".generation"
		if _, err := pool.Exec(ctx, "CREATE TABLE "+parent+base+" PARTITION BY LIST(generation_id)"); err != nil {
			return err
		}
		for i, project := range projects {
			child := fmt.Sprintf("%s.generation_p_%04d", schema, i)
			if _, err := pool.Exec(ctx, fmt.Sprintf(`CREATE TABLE %s PARTITION OF %s FOR VALUES IN ('%s')`,
				child, parent, project.Generation)); err != nil {
				return err
			}
			if err := index(child, fmt.Sprintf("generation_p_%04d_ann", i)); err != nil {
				return err
			}
		}
		return nil
	case "hash":
		parent := schema + ".hash"
		if _, err := pool.Exec(ctx, "CREATE TABLE "+parent+base+" PARTITION BY HASH(scope)"); err != nil {
			return err
		}
		for remainder := 0; remainder < vectorLayoutHashParts; remainder++ {
			child := fmt.Sprintf("%s.hash_p_%02d", schema, remainder)
			if _, err := pool.Exec(ctx, fmt.Sprintf(`CREATE TABLE %s PARTITION OF %s
 FOR VALUES WITH (MODULUS %d,REMAINDER %d)`, child, parent, vectorLayoutHashParts, remainder)); err != nil {
				return err
			}
			if err := index(child, fmt.Sprintf("hash_p_%02d_ann", remainder)); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unknown layout %q", layout)
	}
}

func measureVectorOperation(ctx context.Context, pool *pgxpool.Pool, operation func() error) (vectorLayoutOperation, error) {
	var before string
	if err := pool.QueryRow(ctx, `SELECT pg_current_wal_insert_lsn()::text`).Scan(&before); err != nil {
		return vectorLayoutOperation{}, err
	}
	started := time.Now()
	if err := operation(); err != nil {
		return vectorLayoutOperation{}, err
	}
	result := vectorLayoutOperation{Seconds: time.Since(started).Seconds()}
	err := pool.QueryRow(ctx, `SELECT pg_wal_lsn_diff(pg_current_wal_insert_lsn(),$1::pg_lsn)::bigint`, before).Scan(&result.WALBytes)
	return result, err
}

func vectorLayoutStorage(ctx context.Context, pool *pgxpool.Pool, schema, layout string) (int, int, int64, error) {
	var partitions, relations int
	var bytes int64
	err := pool.QueryRow(ctx, `
WITH tree AS (SELECT relid FROM pg_partition_tree(($1||'.'||$2)::regclass)),
objects AS (SELECT c.oid FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname=$1 AND (c.relname=$2 OR c.relname LIKE $2||'\_%' ESCAPE '\')),
tables AS (SELECT c.oid FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname=$1 AND c.relkind IN ('p','r')
   AND (c.relname=$2 OR c.relname LIKE $2||'\_%' ESCAPE '\'))
SELECT greatest((SELECT count(*)-1 FROM tree),0),(SELECT count(*) FROM objects),
 coalesce((SELECT sum(pg_total_relation_size(oid)) FROM tables),0)`, schema, layout).Scan(&partitions, &relations, &bytes)
	return partitions, relations, bytes, err
}

func vectorLayoutQuery(ctx context.Context, pool *pgxpool.Pool, schema string,
	project vectorLayoutProject, row int) (string, error) {
	if row >= project.Rows {
		row %= project.Rows
	}
	var query string
	err := pool.QueryRow(ctx, fmt.Sprintf(`SELECT embedding::text FROM %s.corpus
 WHERE scope=$1 AND generation_id=$2 AND item_id=$3`, schema), project.Scope, project.Generation, row).Scan(&query)
	return query, err
}

func vectorLayoutSamples(projects []vectorLayoutProject) []struct {
	Project vectorLayoutProject
	Row     int
} {
	samples := make([]struct {
		Project vectorLayoutProject
		Row     int
	}, 0, 24)
	for i := 0; i < 8; i++ {
		samples = append(samples, struct {
			Project vectorLayoutProject
			Row     int
		}{projects[i], i})
	}
	for i := 112; i < 120; i++ {
		samples = append(samples, struct {
			Project vectorLayoutProject
			Row     int
		}{projects[i], (i - 111) * 17})
	}
	for i := 0; i < 8; i++ {
		samples = append(samples, struct {
			Project vectorLayoutProject
			Row     int
		}{projects[len(projects)-1], 73 + i*997})
	}
	return samples
}

func measureVectorLayoutQueries(ctx context.Context, pool *pgxpool.Pool, schema, layout string,
	projects []vectorLayoutProject) (vectorLayoutLatency, []vectorLayoutANN, error) {
	const limit = 10
	bounds := []int{32, 64, 128}
	table := schema + "." + layout
	exactSQL := fmt.Sprintf(`WITH eligible AS MATERIALIZED (
 SELECT item_id,embedding FROM %s WHERE scope=$1 AND generation_id=$2)
 SELECT item_id FROM eligible ORDER BY embedding <=> $3::vector,item_id LIMIT %d`, table, limit)
	annSQL := fmt.Sprintf(`WITH candidates AS MATERIALIZED (
 SELECT item_id,embedding FROM %s WHERE scope=$1 AND generation_id=$2
 ORDER BY (l2_normalize(embedding)::halfvec(%d)) <=>
          (l2_normalize($3::vector)::halfvec(%d)) LIMIT $4)
 SELECT item_id FROM candidates ORDER BY embedding <=> $3::vector,item_id LIMIT %d`,
		table, vectorLayoutDimensions, vectorLayoutDimensions, limit)
	samples := vectorLayoutSamples(projects)
	latencies := make([]time.Duration, 0, len(samples))
	annLatencies := make(map[int][]time.Duration, len(bounds))
	overlaps := make(map[int]int, len(bounds))
	returned := make(map[int]int, len(bounds))
	for sampleIndex, sample := range samples {
		query, err := vectorLayoutQuery(ctx, pool, schema, sample.Project, sample.Row)
		if err != nil {
			return vectorLayoutLatency{}, nil, err
		}
		started := time.Now()
		exact, err := vectorLayoutIDs(ctx, pool, exactSQL, sample.Project, query, 0)
		latencies = append(latencies, time.Since(started))
		if err != nil || len(exact) != limit || exact[0] != int64(sample.Row) {
			return vectorLayoutLatency{}, nil, fmt.Errorf("exact sample %d result=%v err=%v", sampleIndex, exact, err)
		}
		truth := make(map[int64]struct{}, len(exact))
		for _, id := range exact {
			truth[id] = struct{}{}
		}
		for _, bound := range bounds {
			started = time.Now()
			approximate, err := vectorLayoutIDs(ctx, pool, annSQL, sample.Project, query, bound)
			annLatencies[bound] = append(annLatencies[bound], time.Since(started))
			if err != nil {
				return vectorLayoutLatency{}, nil, err
			}
			returned[bound] += len(approximate)
			for _, id := range approximate {
				if _, ok := truth[id]; ok {
					overlaps[bound]++
				}
			}
			if layout == "generation" && (len(approximate) != limit || approximate[0] != int64(sample.Row)) {
				return vectorLayoutLatency{}, nil, fmt.Errorf("generation ANN sample %d bound=%d result=%v", sampleIndex, bound, approximate)
			}
		}
	}
	ann := make([]vectorLayoutANN, 0, len(bounds))
	for _, bound := range bounds {
		ann = append(ann, vectorLayoutANN{Candidates: bound,
			RecallAt10:  float64(overlaps[bound]) / float64(len(samples)*limit),
			MeanResults: float64(returned[bound]) / float64(len(samples)),
			Latency:     summarizeVectorLayoutDurations(annLatencies[bound])})
	}
	return summarizeVectorLayoutDurations(latencies), ann, nil
}

func vectorLayoutIDs(ctx context.Context, pool *pgxpool.Pool, query string,
	project vectorLayoutProject, vector string, candidates int) ([]int64, error) {
	args := []any{project.Scope, project.Generation, vector}
	if candidates > 0 {
		args = append(args, candidates)
	}
	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func summarizeVectorLayoutDurations(values []time.Duration) vectorLayoutLatency {
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	at := func(percent int) float64 {
		index := (len(values)*percent + 99) / 100
		if index < 1 {
			index = 1
		}
		return float64(values[index-1].Microseconds()) / 1000
	}
	return vectorLayoutLatency{P50MS: at(50), P95MS: at(95), P99MS: at(99)}
}

func explainVectorLayout(ctx context.Context, pool *pgxpool.Pool, schema, layout string,
	project vectorLayoutProject, query string) (vectorLayoutPlan, error) {
	table := schema + "." + layout
	statement := fmt.Sprintf(`EXPLAIN (ANALYZE,BUFFERS,WAL,SUMMARY,FORMAT JSON)
 WITH candidates AS MATERIALIZED (
  SELECT item_id,embedding FROM %s WHERE scope=$1 AND generation_id=$2
  ORDER BY (l2_normalize(embedding)::halfvec(%d)) <=>
           (l2_normalize($3::vector)::halfvec(%d)) LIMIT 64)
 SELECT item_id FROM candidates ORDER BY embedding <=> $3::vector,item_id LIMIT 10`,
		table, vectorLayoutDimensions, vectorLayoutDimensions)
	var raw []byte
	if err := pool.QueryRow(ctx, statement, project.Scope, project.Generation, query).Scan(&raw); err != nil {
		return vectorLayoutPlan{}, err
	}
	return parseVectorLayoutPlan(raw)
}

func parseVectorLayoutPlan(raw []byte) (vectorLayoutPlan, error) {
	var document []map[string]any
	if err := json.Unmarshal(raw, &document); err != nil || len(document) != 1 {
		return vectorLayoutPlan{}, fmt.Errorf("decode explain: %w", err)
	}
	result := vectorLayoutPlan{}
	result.PlanningMS, _ = document[0]["Planning Time"].(float64)
	result.ExecutionMS, _ = document[0]["Execution Time"].(float64)
	root, _ := document[0]["Plan"].(map[string]any)
	if n, ok := root["Shared Hit Blocks"].(float64); ok {
		result.SharedHitBlocks = int64(n)
	}
	if n, ok := root["Shared Read Blocks"].(float64); ok {
		result.SharedReadBlocks = int64(n)
	}
	if n, ok := root["WAL Bytes"].(float64); ok {
		result.WALBytes = int64(n)
	}
	var walk func(any)
	walk = func(value any) {
		switch node := value.(type) {
		case map[string]any:
			if name, ok := node["Index Name"].(string); ok {
				result.Indexes = append(result.Indexes, name)
			}
			if name, ok := node["Relation Name"].(string); ok {
				result.Relations = append(result.Relations, name)
			}
			for _, child := range node {
				walk(child)
			}
		case []any:
			for _, child := range node {
				walk(child)
			}
		}
	}
	walk(document[0]["Plan"])
	sort.Strings(result.Indexes)
	result.Indexes = compactVectorLayoutStrings(result.Indexes)
	sort.Strings(result.Relations)
	result.Relations = compactVectorLayoutStrings(result.Relations)
	return result, nil
}

func compactVectorLayoutStrings(values []string) []string {
	if len(values) == 0 {
		return values
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}

func containsVectorLayoutIndex(indexes []string, prefix string) bool {
	for _, index := range indexes {
		if strings.HasPrefix(index, prefix) && strings.HasSuffix(index, "_ann") {
			return true
		}
	}
	return false
}

func eraseVectorLayoutProject(ctx context.Context, pool *pgxpool.Pool, schema, layout string,
	project vectorLayoutProject) (vectorLayoutErasure, error) {
	result := vectorLayoutErasure{}
	_, _, result.BytesBefore, _ = vectorLayoutStorage(ctx, pool, schema, layout)
	var vacuumRelation string
	operation, err := measureVectorOperation(ctx, pool, func() error {
		if layout == "generation" {
			child := fmt.Sprintf("generation_p_%04d", 127)
			_, err := pool.Exec(ctx, fmt.Sprintf(`ALTER TABLE %s.generation DETACH PARTITION %s.%s;
 DROP TABLE %s.%s`, schema, schema, child, schema, child))
			return err
		}
		if layout == "hash" {
			if err := pool.QueryRow(ctx, fmt.Sprintf(`SELECT tableoid::regclass::text FROM %s.hash
 WHERE scope=$1 AND generation_id=$2 LIMIT 1`, schema), project.Scope, project.Generation).Scan(&vacuumRelation); err != nil {
				return err
			}
		} else {
			vacuumRelation = schema + ".ordinary"
		}
		_, err := pool.Exec(ctx, fmt.Sprintf(`DELETE FROM %s.%s WHERE scope=$1 AND generation_id=$2`,
			schema, layout), project.Scope, project.Generation)
		return err
	})
	if err != nil {
		return result, err
	}
	result.Remove = operation
	_, _, result.BytesAfter, _ = vectorLayoutStorage(ctx, pool, schema, layout)
	if layout == "generation" {
		result.BytesAfterVacuum = result.BytesAfter
		return result, nil
	}
	if _, err := pool.Exec(ctx, "ANALYZE "+vacuumRelation); err != nil {
		return result, err
	}
	if err := pool.QueryRow(ctx, `SELECT coalesce(sum(n_dead_tup),0)::bigint FROM pg_stat_all_tables
 WHERE relid=$1::regclass`, vacuumRelation).Scan(&result.DeadBefore); err != nil {
		return result, err
	}
	result.Vacuum, err = measureVectorOperation(ctx, pool, func() error {
		_, err := pool.Exec(ctx, "VACUUM (ANALYZE) "+vacuumRelation)
		return err
	})
	if err != nil {
		return result, err
	}
	_, _, result.BytesAfterVacuum, _ = vectorLayoutStorage(ctx, pool, schema, layout)
	return result, nil
}

func qualifyVectorLayoutCatalog(ctx context.Context, pool *pgxpool.Pool, schema string) ([]vectorLayoutCatalog, error) {
	parent := schema + ".catalog_generation"
	if _, err := pool.Exec(ctx, fmt.Sprintf(`CREATE TABLE %s(
 scope text NOT NULL,generation_id uuid NOT NULL,item_id bigint NOT NULL,embedding vector NOT NULL,
 PRIMARY KEY(scope,generation_id,item_id)) PARTITION BY LIST(generation_id)`, parent)); err != nil {
		return nil, err
	}
	var before string
	if err := pool.QueryRow(ctx, `SELECT pg_current_wal_insert_lsn()::text`).Scan(&before); err != nil {
		return nil, err
	}
	started := time.Now()
	checkpoints := map[int]bool{1: true, 100: true, 500: true, 1000: true}
	var results []vectorLayoutCatalog
	query := vectorLayoutLiteral(0, 0)
	for count := 1; count <= 1000; count++ {
		generation := uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("catalog/%d", count)))
		child := fmt.Sprintf("catalog_generation_p_%04d", count)
		index := child + "_ann"
		if _, err := pool.Exec(ctx, fmt.Sprintf(`CREATE TABLE %s.%s PARTITION OF %s FOR VALUES IN ('%s');
 CREATE INDEX %s ON %s.%s USING hnsw
 ((l2_normalize(embedding)::halfvec(%d)) halfvec_cosine_ops)`, schema, child, parent, generation,
			index, schema, child, vectorLayoutDimensions)); err != nil {
			return nil, err
		}
		if !checkpoints[count] {
			continue
		}
		measurement := vectorLayoutCatalog{Generations: count,
			Cumulative: vectorLayoutOperation{Seconds: time.Since(started).Seconds()}}
		if err := pool.QueryRow(ctx, `SELECT pg_wal_lsn_diff(pg_current_wal_insert_lsn(),$1::pg_lsn)::bigint`, before).Scan(&measurement.Cumulative.WALBytes); err != nil {
			return nil, err
		}
		if err := pool.QueryRow(ctx, `SELECT count(*),coalesce(sum(
	 CASE WHEN c.relkind IN ('p','r') THEN pg_total_relation_size(c.oid) ELSE 0 END),0)
 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname=$1 AND c.relname LIKE 'catalog_generation%'`, schema).Scan(&measurement.Relations, &measurement.Bytes); err != nil {
			return nil, err
		}
		if measurement.Relations == 0 || measurement.Bytes == 0 {
			return nil, fmt.Errorf("catalog measurement is empty at %d generations: relations=%d bytes=%d",
				count, measurement.Relations, measurement.Bytes)
		}
		planning := make([]time.Duration, 0, 9)
		for iteration := 0; iteration < 9; iteration++ {
			began := time.Now()
			var raw []byte
			err := pool.QueryRow(ctx, fmt.Sprintf(`EXPLAIN (ANALYZE,BUFFERS,SUMMARY,FORMAT JSON)
 SELECT item_id FROM %s WHERE scope='p0000' AND generation_id='%s'
 ORDER BY (l2_normalize(embedding)::halfvec(%d)) <=>
          (l2_normalize($1::vector)::halfvec(%d)) LIMIT 10`, parent, generation,
				vectorLayoutDimensions, vectorLayoutDimensions), query).Scan(&raw)
			planning = append(planning, time.Since(began))
			if err != nil {
				return nil, err
			}
			plan, err := parseVectorLayoutPlan(raw)
			if err != nil {
				return nil, err
			}
			measurement.PlannedRelations = len(plan.Relations)
			if measurement.PlannedRelations != 1 || plan.Relations[0] != child {
				return nil, fmt.Errorf("catalog plan did not prune to %s: %v", child, plan.Relations)
			}
		}
		measurement.PlanningLatency = summarizeVectorLayoutDurations(planning)
		results = append(results, measurement)
	}
	return results, nil
}
