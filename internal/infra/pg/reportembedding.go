// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

// Report embeddings are a thematic index over source-owned community reports. They are kept in a
// model generation distinct from message and entity vectors because each space answers a different
// question. Every contributing observation owns the aggregate vector.
package pg

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ensera-ai/taisce/internal/report"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const ReportEmbeddingInputVersion = "community-report/v1"
const MaxReportEmbeddingSources = 4096
const MaxReportEmbeddingPageSources = 4096
const MaxReportEmbeddingTargets = 1_000_000

type ReportEmbeddingGeneration struct {
	ID          string         `json:"id"`
	Project     string         `json:"project"`
	Key         string         `json:"operation_key"`
	Model       EmbeddingModel `json:"model"`
	TargetCount int64          `json:"target_count"`
	After       string         `json:"after_report_id,omitempty"`
	State       string         `json:"state"`
	Active      bool           `json:"active"`
	Examined    int64          `json:"examined"`
	Stored      int64          `json:"stored"`
	CreatedAt   time.Time      `json:"created_at"`
}

type ReportEmbeddingBuildPage struct {
	Examined int    `json:"examined"`
	Stored   int    `json:"stored"`
	Complete bool   `json:"complete"`
	After    string `json:"after_report_id,omitempty"`
}

type ReportCandidateOptions struct {
	Limit           int
	Candidates      int
	applicationRead bool
}

type ReportCandidate struct {
	ReportID         string  `json:"report_id"`
	CommunityID      string  `json:"community_id"`
	TitlePreview     string  `json:"title_preview"`
	TitleBytes       int     `json:"title_bytes"`
	TitleTruncated   bool    `json:"title_truncated"`
	SummaryPreview   string  `json:"summary_preview"`
	SummaryBytes     int     `json:"summary_bytes"`
	SummaryTruncated bool    `json:"summary_truncated"`
	Importance       float32 `json:"importance"`
	Similarity       float64 `json:"similarity"`
}

type ReportCandidateResult struct {
	Generation  ReportEmbeddingGeneration `json:"generation"`
	Approximate bool                      `json:"approximate"`
	Candidates  []ReportCandidate         `json:"candidates"`
}

type ReportEmbeddingStore struct {
	pool   *pgxpool.Pool
	schema Schema
}

func NewReportEmbeddingStore(pool *pgxpool.Pool, schema Schema) *ReportEmbeddingStore {
	return &ReportEmbeddingStore{pool: pool, schema: schema}
}

func reportEmbeddingModelIdentity(model EmbeddingModel) string {
	data, _ := json.Marshal(struct {
		Model        EmbeddingModel
		Metric       string
		InputVersion string
	}{model, "cosine", ReportEmbeddingInputVersion})
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

const reportEmbeddingGenerationSQL = `SELECT g.generation_id::text,g.scope,g.operation_key::text,
 g.model_name,g.model_revision,g.endpoint_hash,g.dimensions,g.target_count,coalesce(b.after_report_id::text,''),
 b.state,coalesce(a.generation_id=g.generation_id,false),b.examined,b.stored,g.created_at
 FROM {schema}.report_embedding_generation g JOIN {schema}.report_embedding_build b USING(scope,generation_id)
 LEFT JOIN {schema}.report_embedding_active a USING(scope)`

func scanReportEmbeddingGeneration(row pgx.Row) (ReportEmbeddingGeneration, error) {
	var out ReportEmbeddingGeneration
	err := row.Scan(&out.ID, &out.Project, &out.Key, &out.Model.Name, &out.Model.Revision,
		&out.Model.EndpointHash, &out.Model.Dimensions, &out.TargetCount, &out.After,
		&out.State, &out.Active, &out.Examined, &out.Stored, &out.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ReportEmbeddingGeneration{}, ErrEmbeddingNotFound
	}
	return out, err
}

func reportEmbeddingPartitionName(id string) string {
	return "report_vector_" + strings.ReplaceAll(id, "-", "")
}

func (s *ReportEmbeddingStore) Generation(ctx context.Context, scope, id string) (ReportEmbeddingGeneration, error) {
	if err := validEmbeddingIDs(scope, id); err != nil {
		return ReportEmbeddingGeneration{}, err
	}
	return scanReportEmbeddingGeneration(s.pool.QueryRow(ctx, s.schema.SQL(reportEmbeddingGenerationSQL+
		` WHERE g.scope=$1 AND g.generation_id=$2::uuid`), scope, id))
}

func (s *ReportEmbeddingStore) Active(ctx context.Context, scope string) (ReportEmbeddingGeneration, error) {
	if err := validEmbeddingIDs(scope); err != nil {
		return ReportEmbeddingGeneration{}, err
	}
	return scanReportEmbeddingGeneration(s.pool.QueryRow(ctx, s.schema.SQL(reportEmbeddingGenerationSQL+
		` WHERE g.scope=$1 AND a.generation_id=g.generation_id`), scope))
}

// Start materializes current report IDs behind the project row lock. Report writes take that lock in
// their migration trigger, so a concurrent report is included or makes the finite snapshot stale.
func (s *ReportEmbeddingStore) Start(ctx context.Context, scope, key, actor string, model EmbeddingModel) (ReportEmbeddingGeneration, error) {
	if err := validEmbeddingIDs(scope, key, actor); err != nil {
		return ReportEmbeddingGeneration{}, err
	}
	if err := model.Validate(); err != nil {
		return ReportEmbeddingGeneration{}, err
	}
	var out ReportEmbeddingGeneration
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := embeddingProjectLock(ctx, tx, s.schema, scope); err != nil {
			return err
		}
		existing, err := scanReportEmbeddingGeneration(tx.QueryRow(ctx, s.schema.SQL(reportEmbeddingGenerationSQL+
			` WHERE g.scope=$1 AND g.operation_key=$2::uuid`), scope, key))
		if err == nil {
			if reportEmbeddingModelIdentity(existing.Model) != reportEmbeddingModelIdentity(model) {
				return ErrEmbeddingConflict
			}
			out = existing
			return nil
		}
		if !errors.Is(err, ErrEmbeddingNotFound) {
			return err
		}
		var present int
		if err := tx.QueryRow(ctx, s.schema.SQL(`SELECT 1 FROM {schema}.project WHERE scope=$1 AND suspended_at IS NULL FOR UPDATE`), scope).Scan(&present); errors.Is(err, pgx.ErrNoRows) {
			return ErrNoSuchProject
		} else if err != nil {
			return err
		}
		var retained int
		if err := tx.QueryRow(ctx, s.schema.SQL(`SELECT count(*) FROM {schema}.report_embedding_build WHERE scope=$1 AND state<>'discarded'`), scope).Scan(&retained); err != nil {
			return err
		}
		if retained >= MaxRetainedEmbeddingGenerations {
			return ErrEmbeddingCapacity
		}
		var targets int64
		if err := tx.QueryRow(ctx, s.schema.SQL(`SELECT count(*) FROM {schema}.community_report WHERE scope=$1`), scope).Scan(&targets); err != nil {
			return err
		}
		if targets > MaxReportEmbeddingTargets {
			return ErrEmbeddingCapacity
		}
		id := uuid.NewString()
		if _, err := tx.Exec(ctx, s.schema.SQL(`INSERT INTO {schema}.report_embedding_generation
   (generation_id,scope,operation_key,model_name,model_revision,endpoint_hash,identity_hash,dimensions,target_count)
   VALUES($1::uuid,$2,$3::uuid,$4,$5,$6,$7,$8,$9)`), id, scope, key, model.Name,
			model.Revision, model.EndpointHash, reportEmbeddingModelIdentity(model), model.Dimensions, targets); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, s.schema.SQL(`INSERT INTO {schema}.report_embedding_target(scope,generation_id,report_id)
 SELECT scope,$2::uuid,report_id FROM {schema}.community_report WHERE scope=$1`), scope, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != targets {
			return ErrEmbeddingConflict
		}
		if _, err := tx.Exec(ctx, s.schema.SQL(`INSERT INTO {schema}.report_embedding_build(scope,generation_id) VALUES($1,$2::uuid)`), scope, id); err != nil {
			return err
		}
		table := pgx.Identifier{s.schema.String(), reportEmbeddingPartitionName(id)}.Sanitize()
		parent := pgx.Identifier{s.schema.String(), "report_embedding"}.Sanitize()
		if _, err := tx.Exec(ctx, fmt.Sprintf(`CREATE TABLE %s PARTITION OF %s FOR VALUES IN ('%s')`, table, parent, id)); err != nil {
			return err
		}
		expression, operator := embeddingIndexExpression("embedding", model.Dimensions)
		if _, err := tx.Exec(ctx, fmt.Sprintf(`CREATE INDEX %s ON %s USING hnsw ((%s) %s)`,
			pgx.Identifier{reportEmbeddingPartitionName(id) + "_ann"}.Sanitize(), table, expression, operator)); err != nil {
			return err
		}
		if err := embeddingAudit(ctx, tx, s.schema, scope, actor, "report_embedding.start", targets); err != nil {
			return err
		}
		out, err = scanReportEmbeddingGeneration(tx.QueryRow(ctx, s.schema.SQL(reportEmbeddingGenerationSQL+
			` WHERE g.scope=$1 AND g.generation_id=$2::uuid`), scope, id))
		return err
	})
	if err != nil {
		return ReportEmbeddingGeneration{}, err
	}
	return out, nil
}

type reportEmbeddingInput struct {
	report       string
	text         string
	digest       []byte
	sources      []string
	sourceDigest []byte
}

func digestReportSources(sources []string) []byte {
	digest := sha256.New()
	for _, source := range sources {
		_, _ = digest.Write([]byte(source))
		_, _ = digest.Write([]byte{0})
	}
	return digest.Sum(nil)
}

// loadReportEmbeddingInput reads the report and its exact source registrations. The pure report
// package bounds and validates the semantic input before provider egress.
func (s *ReportEmbeddingStore) loadReportEmbeddingInput(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, scope, generation, reportID string) (reportEmbeddingInput, bool, error) {
	var title, summary, importanceReason string
	var findings []byte
	var sources, sourceVersions []string
	err := q.QueryRow(ctx, s.schema.SQL(`SELECT r.title,r.summary,r.importance_reason,r.findings,
 ARRAY(SELECT d.source_observation_id::text FROM {schema}.projection_dependency d
   JOIN {schema}.observation o ON o.scope=d.scope AND o.observation_id=d.source_observation_id
  WHERE d.scope=r.scope AND d.projection_kind='community_report' AND d.report_ref=r.report_id
    AND d.report_source_revision=o.fact_revision
  ORDER BY d.source_observation_id LIMIT $4),
 ARRAY(SELECT d.source_observation_id::text||':'||d.report_source_revision::text
   FROM {schema}.projection_dependency d
   JOIN {schema}.observation o ON o.scope=d.scope AND o.observation_id=d.source_observation_id
  WHERE d.scope=r.scope AND d.projection_kind='community_report' AND d.report_ref=r.report_id
    AND d.report_source_revision=o.fact_revision
  ORDER BY d.source_observation_id LIMIT $4)
 FROM {schema}.report_embedding_target t JOIN {schema}.community_report r USING(scope,report_id)
 WHERE t.scope=$1 AND t.generation_id=$2::uuid AND t.report_id=$3::uuid`),
		scope, generation, reportID, MaxReportEmbeddingSources+1).
		Scan(&title, &summary, &importanceReason, &findings, &sources, &sourceVersions)
	if errors.Is(err, pgx.ErrNoRows) {
		return reportEmbeddingInput{}, false, nil
	}
	if err != nil {
		return reportEmbeddingInput{}, false, err
	}
	if len(sources) == 0 || len(sources) > MaxReportEmbeddingSources || len(sources) != len(sourceVersions) {
		return reportEmbeddingInput{}, false, ErrInvalidEmbedding
	}
	text, err := report.EmbeddingSurface(title, summary, importanceReason, findings)
	if err != nil {
		return reportEmbeddingInput{}, false, ErrInvalidEmbedding
	}
	digest := sha256.Sum256([]byte(text))
	return reportEmbeddingInput{report: reportID, text: text, digest: digest[:], sources: sources,
		sourceDigest: digestReportSources(sourceVersions)}, true, nil
}

func (s *ReportEmbeddingStore) BuildPage(ctx context.Context, scope, id, actor string, model EmbeddingModel, limit int, embed EmbeddingBatch) (ReportEmbeddingBuildPage, error) {
	var out ReportEmbeddingBuildPage
	if err := validEmbeddingIDs(scope, id, actor); err != nil || limit < 1 || limit > MaxEmbeddingPage || embed == nil {
		return out, ErrInvalidEmbedding
	}
	if err := model.Validate(); err != nil {
		return out, err
	}
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return out, err
	}
	lock := s.schema.String() + ":report-embedding-build:" + scope + ":" + id
	var locked bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1,0))`, lock).Scan(&locked); err != nil {
		conn.Release()
		return out, err
	}
	if !locked {
		conn.Release()
		return out, ErrEmbeddingBusy
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		var unlocked bool
		if err := conn.QueryRow(cleanup, `SELECT pg_advisory_unlock(hashtextextended($1,0))`, lock).Scan(&unlocked); err != nil || !unlocked {
			_ = conn.Hijack().Close(cleanup)
			return
		}
		conn.Release()
	}()

	lease := uuid.NewString()
	var generation ReportEmbeddingGeneration
	err = pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		var scanErr error
		generation, scanErr = scanReportEmbeddingGeneration(tx.QueryRow(ctx, s.schema.SQL(reportEmbeddingGenerationSQL+
			` WHERE g.scope=$1 AND g.generation_id=$2::uuid FOR UPDATE OF b`), scope, id))
		if scanErr != nil {
			return scanErr
		}
		if reportEmbeddingModelIdentity(generation.Model) != reportEmbeddingModelIdentity(model) {
			return ErrEmbeddingConflict
		}
		if generation.State == "ready" {
			return nil
		}
		if generation.State != "building" {
			return ErrEmbeddingConflict
		}
		_, scanErr = tx.Exec(ctx, s.schema.SQL(`UPDATE {schema}.report_embedding_build SET lease_id=$3::uuid
 WHERE scope=$1 AND generation_id=$2::uuid`), scope, id, lease)
		return scanErr
	})
	if err != nil {
		return out, err
	}
	if generation.State == "ready" {
		out.Complete = true
		out.After = generation.After
		return out, nil
	}
	after := uuid.Nil.String()
	if generation.After != "" {
		after = generation.After
	}
	rows, err := conn.Query(ctx, s.schema.SQL(`SELECT report_id::text FROM {schema}.report_embedding_target
 WHERE scope=$1 AND generation_id=$2::uuid AND report_id>$3::uuid ORDER BY report_id LIMIT $4`),
		scope, id, after, limit+1)
	if err != nil {
		return ReportEmbeddingBuildPage{}, err
	}
	var targets []string
	for rows.Next() {
		var target string
		if err := rows.Scan(&target); err != nil {
			rows.Close()
			return ReportEmbeddingBuildPage{}, err
		}
		targets = append(targets, target)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return ReportEmbeddingBuildPage{}, err
	}
	complete := len(targets) <= limit
	if len(targets) > limit {
		targets = targets[:limit]
	}
	entries := make([]reportEmbeddingInput, 0, len(targets))
	processed := make([]string, 0, len(targets))
	sourceTotal := 0
	for _, target := range targets {
		entry, exists, err := s.loadReportEmbeddingInput(ctx, conn, scope, id, target)
		if err != nil {
			return ReportEmbeddingBuildPage{}, err
		}
		if exists && sourceTotal+len(entry.sources) > MaxReportEmbeddingPageSources && len(processed) > 0 {
			complete = false
			break
		}
		processed = append(processed, target)
		if exists {
			sourceTotal += len(entry.sources)
			entries = append(entries, entry)
		}
	}
	texts := make([]string, len(entries))
	for i := range entries {
		texts[i] = entries[i].text
	}
	vectors := [][]float32{}
	if len(texts) > 0 {
		vectors, err = embed(ctx, texts)
		if err != nil {
			return ReportEmbeddingBuildPage{}, err
		}
	}
	if err := validateEmbeddingBatch(vectors, len(entries), model.Dimensions); err != nil {
		return ReportEmbeddingBuildPage{}, err
	}

	err = pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		var state, currentLease, currentAfter string
		if err := tx.QueryRow(ctx, s.schema.SQL(`SELECT state,coalesce(lease_id::text,''),coalesce(after_report_id::text,'')
 FROM {schema}.report_embedding_build WHERE scope=$1 AND generation_id=$2::uuid FOR UPDATE`), scope, id).
			Scan(&state, &currentLease, &currentAfter); err != nil {
			return err
		}
		if state != "building" || currentLease != lease || currentAfter != generation.After {
			return ErrEmbeddingConflict
		}
		stored, err := s.commitReportEmbeddingInputs(ctx, tx, generation, entries, vectors)
		if err != nil {
			return err
		}
		out.Stored = stored
		out.Examined = len(processed)
		out.Complete = complete
		out.After = generation.After
		if len(processed) > 0 {
			out.After = processed[len(processed)-1]
		}
		nextState := "building"
		if complete {
			nextState = "ready"
		}
		var afterValue any
		if out.After != "" {
			afterValue = out.After
		}
		if _, err := tx.Exec(ctx, s.schema.SQL(`UPDATE {schema}.report_embedding_build
 SET after_report_id=$3::uuid,state=$4,examined=examined+$5,stored=stored+$6,lease_id=NULL,updated_at=clock_timestamp()
 WHERE scope=$1 AND generation_id=$2::uuid`), scope, id, afterValue, nextState, out.Examined, out.Stored); err != nil {
			return err
		}
		return embeddingAudit(ctx, tx, s.schema, scope, actor, "report_embedding.build", int64(out.Stored))
	})
	if err != nil {
		return ReportEmbeddingBuildPage{}, err
	}
	return out, nil
}

func (s *ReportEmbeddingStore) commitReportEmbeddingInputs(ctx context.Context, tx pgx.Tx,
	generation ReportEmbeddingGeneration, entries []reportEmbeddingInput, vectors [][]float32) (int, error) {
	stored := 0
	for i, entry := range entries {
		var lockedSources int
		if err := tx.QueryRow(ctx, s.schema.SQL(`SELECT count(*) FROM (SELECT observation_id FROM {schema}.observation
 WHERE scope=$1 AND observation_id=ANY($2::uuid[]) ORDER BY observation_id FOR SHARE) locked`),
			generation.Project, entry.sources).Scan(&lockedSources); err != nil {
			return 0, err
		}
		if lockedSources != len(entry.sources) {
			continue
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`,
			s.schema.String()+":report-embedding:"+entry.report); err != nil {
			return 0, err
		}
		current, exists, err := s.loadReportEmbeddingInput(ctx, tx, generation.Project, generation.ID, entry.report)
		if err != nil {
			return 0, err
		}
		if !exists {
			continue
		}
		if string(current.digest) != string(entry.digest) || string(current.sourceDigest) != string(entry.sourceDigest) {
			return 0, ErrEmbeddingConflict
		}
		encoded, err := json.Marshal(vectors[i])
		if err != nil {
			return 0, err
		}
		tag, err := tx.Exec(ctx, s.schema.SQL(`INSERT INTO {schema}.report_embedding
 (scope,generation_id,report_id,input_digest,source_digest,source_count,embedding)
 VALUES($1,$2::uuid,$3::uuid,$4,$5,$6,$7::vector) ON CONFLICT DO NOTHING`), generation.Project,
			generation.ID, entry.report, entry.digest, entry.sourceDigest, len(entry.sources), string(encoded))
		if err != nil {
			return 0, err
		}
		stored += int(tag.RowsAffected())
		var incompatible bool
		if err := tx.QueryRow(ctx, s.schema.SQL(`SELECT EXISTS(SELECT 1 FROM {schema}.report_embedding
 WHERE scope=$1 AND generation_id=$2::uuid AND report_id=$3::uuid
 AND (input_digest,source_digest,source_count) IS DISTINCT FROM ($4::bytea,$5::bytea,$6::integer))`),
			generation.Project, generation.ID, entry.report, entry.digest, entry.sourceDigest, len(entry.sources)).Scan(&incompatible); err != nil {
			return 0, err
		}
		if incompatible {
			return 0, ErrEmbeddingConflict
		}
		if _, err := tx.Exec(ctx, s.schema.SQL(`INSERT INTO {schema}.report_embedding_source
 (scope,generation_id,report_id,source_observation_id)
 SELECT $1,$2::uuid,$3::uuid,source_id FROM unnest($4::uuid[]) source_id ON CONFLICT DO NOTHING`),
			generation.Project, generation.ID, entry.report, entry.sources); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(ctx, s.schema.SQL(`INSERT INTO {schema}.projection_dependency
 (source_observation_id,scope,projection_kind,projection_id,data_subject_id,pipeline_version)
 SELECT o.observation_id,o.scope,'report_embedding',$2::uuid::text||'/'||$3::uuid::text,o.data_subject_id,$4
 FROM {schema}.observation o WHERE o.scope=$1 AND o.observation_id=ANY($5::uuid[]) ON CONFLICT DO NOTHING`),
			generation.Project, generation.ID, entry.report, ReportEmbeddingInputVersion, entry.sources); err != nil {
			return 0, err
		}
	}
	return stored, nil
}

func (s *ReportEmbeddingStore) Activate(ctx context.Context, scope, id, actor string) error {
	if err := validEmbeddingIDs(scope, id, actor); err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := embeddingProjectLock(ctx, tx, s.schema, scope); err != nil {
			return err
		}
		generation, err := scanReportEmbeddingGeneration(tx.QueryRow(ctx, s.schema.SQL(reportEmbeddingGenerationSQL+
			` WHERE g.scope=$1 AND g.generation_id=$2::uuid FOR UPDATE OF b`), scope, id))
		if err != nil {
			return err
		}
		if generation.State != "ready" {
			return ErrEmbeddingIncomplete
		}
		if generation.Active {
			return nil
		}
		var missing bool
		if err := tx.QueryRow(ctx, s.schema.SQL(`SELECT EXISTS(SELECT 1 FROM {schema}.report_embedding_target t
 LEFT JOIN {schema}.report_embedding v USING(scope,generation_id,report_id)
 WHERE t.scope=$1 AND t.generation_id=$2::uuid AND v.report_id IS NULL)`), scope, id).Scan(&missing); err != nil {
			return err
		}
		if missing {
			return ErrEmbeddingIncomplete
		}
		if _, err := tx.Exec(ctx, s.schema.SQL(`INSERT INTO {schema}.report_embedding_active(scope,generation_id)
 VALUES($1,$2::uuid) ON CONFLICT(scope) DO UPDATE SET generation_id=excluded.generation_id,activated_at=clock_timestamp()`),
			scope, id); err != nil {
			return err
		}
		return embeddingAudit(ctx, tx, s.schema, scope, actor, "report_embedding.activate", 1)
	})
}

func (s *ReportEmbeddingStore) Repair(ctx context.Context, scope, id, actor string) error {
	if err := validEmbeddingIDs(scope, id, actor); err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := embeddingProjectLock(ctx, tx, s.schema, scope); err != nil {
			return err
		}
		generation, err := scanReportEmbeddingGeneration(tx.QueryRow(ctx, s.schema.SQL(reportEmbeddingGenerationSQL+
			` WHERE g.scope=$1 AND g.generation_id=$2::uuid FOR UPDATE OF b`), scope, id))
		if err != nil {
			return err
		}
		if generation.State == "discarded" || generation.State == "stale" {
			return ErrEmbeddingConflict
		}
		if _, err := tx.Exec(ctx, s.schema.SQL(`UPDATE {schema}.report_embedding_build
 SET state='building',after_report_id=NULL,examined=0,stored=0,lease_id=NULL,updated_at=clock_timestamp()
 WHERE scope=$1 AND generation_id=$2::uuid`), scope, id); err != nil {
			return err
		}
		return embeddingAudit(ctx, tx, s.schema, scope, actor, "report_embedding.repair", 1)
	})
}

func (s *ReportEmbeddingStore) Cancel(ctx context.Context, scope, id, actor string) error {
	if err := validEmbeddingIDs(scope, id, actor); err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := embeddingProjectLock(ctx, tx, s.schema, scope); err != nil {
			return err
		}
		generation, err := scanReportEmbeddingGeneration(tx.QueryRow(ctx, s.schema.SQL(reportEmbeddingGenerationSQL+
			` WHERE g.scope=$1 AND g.generation_id=$2::uuid FOR UPDATE OF b`), scope, id))
		if err != nil {
			return err
		}
		if generation.Active {
			return ErrEmbeddingConflict
		}
		if generation.State == "cancelled" || generation.State == "discarded" {
			return nil
		}
		if _, err := tx.Exec(ctx, s.schema.SQL(`UPDATE {schema}.report_embedding_build SET state='cancelled',lease_id=NULL,updated_at=clock_timestamp()
 WHERE scope=$1 AND generation_id=$2::uuid`), scope, id); err != nil {
			return err
		}
		return embeddingAudit(ctx, tx, s.schema, scope, actor, "report_embedding.cancel", 1)
	})
}

func (s *ReportEmbeddingStore) Prune(ctx context.Context, scope, id, actor string, limit int) (int, error) {
	if err := validEmbeddingIDs(scope, id, actor); err != nil || limit < 1 || limit > MaxEmbeddingPage {
		return 0, ErrInvalidEmbedding
	}
	removed := 0
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := embeddingProjectLock(ctx, tx, s.schema, scope); err != nil {
			return err
		}
		generation, err := scanReportEmbeddingGeneration(tx.QueryRow(ctx, s.schema.SQL(reportEmbeddingGenerationSQL+
			` WHERE g.scope=$1 AND g.generation_id=$2::uuid FOR UPDATE OF b`), scope, id))
		if err != nil {
			return err
		}
		if generation.Active || generation.State == "building" {
			return ErrEmbeddingConflict
		}
		if generation.State == "discarded" {
			return nil
		}
		tag, err := tx.Exec(ctx, s.schema.SQL(`DELETE FROM {schema}.report_embedding WHERE scope=$1 AND generation_id=$2::uuid
 AND report_id IN (SELECT report_id FROM {schema}.report_embedding WHERE scope=$1 AND generation_id=$2::uuid ORDER BY report_id LIMIT $3)`),
			scope, id, limit)
		if err != nil {
			return err
		}
		removed = int(tag.RowsAffected())
		var remaining bool
		if err := tx.QueryRow(ctx, s.schema.SQL(`SELECT EXISTS(SELECT 1 FROM {schema}.report_embedding
 WHERE scope=$1 AND generation_id=$2::uuid)`), scope, id).Scan(&remaining); err != nil {
			return err
		}
		if !remaining {
			partition := pgx.Identifier{s.schema.String(), reportEmbeddingPartitionName(id)}.Sanitize()
			if _, err := tx.Exec(ctx, s.schema.SQL(`ALTER TABLE {schema}.report_embedding DETACH PARTITION `)+partition); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `DROP TABLE `+partition); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, s.schema.SQL(`UPDATE {schema}.report_embedding_build SET state='discarded',lease_id=NULL,updated_at=clock_timestamp()
 WHERE scope=$1 AND generation_id=$2::uuid`), scope, id); err != nil {
				return err
			}
		}
		return embeddingAudit(ctx, tx, s.schema, scope, actor, "report_embedding.prune", int64(removed))
	})
	return removed, err
}

func (s *ReportEmbeddingStore) Search(ctx context.Context, scope, actor string, model EmbeddingModel,
	vector []float32, options ReportCandidateOptions) (ReportCandidateResult, error) {
	out := ReportCandidateResult{Candidates: []ReportCandidate{}, Approximate: options.Candidates > 0}
	var ids []string
	if !options.applicationRead {
		ids = append(ids, actor)
	}
	if err := validEmbeddingIDs(scope, ids...); err != nil || options.Limit < 1 || options.Limit > 100 ||
		options.Candidates < 0 || options.Candidates > 1000 || (options.Candidates > 0 && options.Candidates < options.Limit) {
		return out, ErrInvalidEmbedding
	}
	if err := model.Validate(); err != nil {
		return out, err
	}
	if err := validateEmbeddingBatch([][]float32{vector}, 1, model.Dimensions); err != nil {
		return out, err
	}
	encoded, _ := json.Marshal(vector)
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		generation, err := scanReportEmbeddingGeneration(tx.QueryRow(ctx, s.schema.SQL(reportEmbeddingGenerationSQL+
			` WHERE g.scope=$1 AND a.generation_id=g.generation_id FOR SHARE OF b`), scope))
		if err != nil {
			return err
		}
		if generation.State != "ready" || reportEmbeddingModelIdentity(generation.Model) != reportEmbeddingModelIdentity(model) {
			return ErrEmbeddingConflict
		}
		out.Generation = generation
		candidates := `SELECT v.* FROM {schema}.report_embedding v WHERE v.scope=$1 AND v.generation_id=$2::uuid`
		if options.Candidates > 0 {
			expression, _ := embeddingIndexExpression("v.embedding", model.Dimensions)
			queryExpression, _ := embeddingIndexExpression("$3::vector", model.Dimensions)
			candidates += fmt.Sprintf(` ORDER BY (%s) <=> (%s) LIMIT $5`, expression, queryExpression)
		}
		query := `WITH candidates AS MATERIALIZED (` + candidates + `)
 SELECT r.report_id::text,r.community_id::text,left(r.title,512),octet_length(r.title),char_length(r.title)>512,
   left(r.summary,1024),octet_length(r.summary),char_length(r.summary)>1024,r.importance,
   1-(v.embedding <=> $3::vector)
 FROM candidates v JOIN {schema}.community_report r USING(scope,report_id)
 ORDER BY v.embedding <=> $3::vector,r.report_id LIMIT $4`
		args := []any{scope, generation.ID, string(encoded), options.Limit}
		if options.Candidates > 0 {
			args = append(args, options.Candidates)
		}
		rows, err := tx.Query(ctx, s.schema.SQL(query), args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var candidate ReportCandidate
			if err := rows.Scan(&candidate.ReportID, &candidate.CommunityID, &candidate.TitlePreview,
				&candidate.TitleBytes, &candidate.TitleTruncated, &candidate.SummaryPreview,
				&candidate.SummaryBytes, &candidate.SummaryTruncated, &candidate.Importance,
				&candidate.Similarity); err != nil {
				rows.Close()
				return err
			}
			out.Candidates = append(out.Candidates, candidate)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if options.applicationRead {
			return nil
		}
		return embeddingAudit(ctx, tx, s.schema, scope, actor, "report_embedding.search", int64(len(out.Candidates)))
	})
	if err != nil {
		return ReportCandidateResult{}, err
	}
	return out, nil
}

// FindCandidates is for a caller that has already enforced project authorization and credential
// audit. It returns bounded report previews and cannot mutate memory.
func (s *ReportEmbeddingStore) FindCandidates(ctx context.Context, scope string, model EmbeddingModel,
	vector []float32, options ReportCandidateOptions) (ReportCandidateResult, error) {
	options.applicationRead = true
	return s.Search(ctx, scope, "", model, vector, options)
}
