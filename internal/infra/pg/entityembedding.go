// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

// Entity embeddings are candidate indexes over retained identities and their verbatim evidence.
// They never merge entities: a candidate still resolves through the exact entity ID chosen by the
// caller. Aggregate provenance is registered to every contributing observation so any source loss
// removes the vector and forces a rebuild from what survived.
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

	"github.com/ensera-ai/taisce/internal/semantic"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const EntityEmbeddingInputVersion = "entity-evidence/v1"
const MaxEntityEmbeddingSources = 4096
const MaxEntityEmbeddingPageSources = 4096
const MaxEntityEmbeddingTargets = 1_000_000

type EntityEmbeddingGeneration struct {
	ID          string         `json:"id"`
	Project     string         `json:"project"`
	Key         string         `json:"operation_key"`
	Model       EmbeddingModel `json:"model"`
	TargetCount int64          `json:"target_count"`
	After       string         `json:"after_entity_id,omitempty"`
	State       string         `json:"state"`
	Active      bool           `json:"active"`
	Examined    int64          `json:"examined"`
	Stored      int64          `json:"stored"`
	CreatedAt   time.Time      `json:"created_at"`
}

type EntityEmbeddingBuildPage struct {
	Examined int    `json:"examined"`
	Stored   int    `json:"stored"`
	Complete bool   `json:"complete"`
	After    string `json:"after_entity_id,omitempty"`
}

type EntityCandidateOptions struct {
	Limit           int
	Candidates      int
	applicationRead bool
}

type EntityCandidate struct {
	EntityID      string  `json:"entity_id"`
	IdentityKind  string  `json:"identity_kind"`
	NamePreview   string  `json:"name_preview"`
	NameBytes     int     `json:"name_bytes"`
	NameTruncated bool    `json:"name_truncated"`
	Similarity    float64 `json:"similarity"`
}

type EntityCandidateResult struct {
	Generation  EntityEmbeddingGeneration `json:"generation"`
	Approximate bool                      `json:"approximate"`
	Candidates  []EntityCandidate         `json:"candidates"`
}

type EntityEmbeddingStore struct {
	pool   *pgxpool.Pool
	schema Schema
}

func NewEntityEmbeddingStore(pool *pgxpool.Pool, schema Schema) *EntityEmbeddingStore {
	return &EntityEmbeddingStore{pool: pool, schema: schema}
}

func entityEmbeddingModelIdentity(model EmbeddingModel) string {
	data, _ := json.Marshal(struct {
		Model        EmbeddingModel
		Metric       string
		InputVersion string
	}{model, "cosine", EntityEmbeddingInputVersion})
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

const entityEmbeddingGenerationSQL = `SELECT g.generation_id::text,g.scope,g.operation_key::text,
 g.model_name,g.model_revision,g.endpoint_hash,g.dimensions,g.target_count,coalesce(b.after_entity_id::text,''),
 b.state,coalesce(a.generation_id=g.generation_id,false),b.examined,b.stored,g.created_at
 FROM {schema}.entity_embedding_generation g JOIN {schema}.entity_embedding_build b USING(scope,generation_id)
 LEFT JOIN {schema}.entity_embedding_active a USING(scope)`

func scanEntityEmbeddingGeneration(row pgx.Row) (EntityEmbeddingGeneration, error) {
	var out EntityEmbeddingGeneration
	err := row.Scan(&out.ID, &out.Project, &out.Key, &out.Model.Name, &out.Model.Revision,
		&out.Model.EndpointHash, &out.Model.Dimensions, &out.TargetCount, &out.After,
		&out.State, &out.Active, &out.Examined, &out.Stored, &out.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return EntityEmbeddingGeneration{}, ErrEmbeddingNotFound
	}
	return out, err
}

func entityEmbeddingPartitionName(id string) string {
	return "entity_vector_" + strings.ReplaceAll(id, "-", "")
}

func (s *EntityEmbeddingStore) Generation(ctx context.Context, scope, id string) (EntityEmbeddingGeneration, error) {
	if err := validEmbeddingIDs(scope, id); err != nil {
		return EntityEmbeddingGeneration{}, err
	}
	return scanEntityEmbeddingGeneration(s.pool.QueryRow(ctx, s.schema.SQL(entityEmbeddingGenerationSQL+
		` WHERE g.scope=$1 AND g.generation_id=$2::uuid`), scope, id))
}

func (s *EntityEmbeddingStore) Active(ctx context.Context, scope string) (EntityEmbeddingGeneration, error) {
	if err := validEmbeddingIDs(scope); err != nil {
		return EntityEmbeddingGeneration{}, err
	}
	return scanEntityEmbeddingGeneration(s.pool.QueryRow(ctx, s.schema.SQL(entityEmbeddingGenerationSQL+
		` WHERE g.scope=$1 AND a.generation_id=g.generation_id`), scope))
}

// Start materializes the current named-entity IDs behind the project row lock. Named-entity inserts
// take that lock in their migration trigger, so an insert is either included or makes this finite
// snapshot stale after Start commits.
func (s *EntityEmbeddingStore) Start(ctx context.Context, scope, key, actor string, model EmbeddingModel) (EntityEmbeddingGeneration, error) {
	if err := validEmbeddingIDs(scope, key, actor); err != nil {
		return EntityEmbeddingGeneration{}, err
	}
	if err := model.Validate(); err != nil {
		return EntityEmbeddingGeneration{}, err
	}
	var out EntityEmbeddingGeneration
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := embeddingProjectLock(ctx, tx, s.schema, scope); err != nil {
			return err
		}
		existing, err := scanEntityEmbeddingGeneration(tx.QueryRow(ctx, s.schema.SQL(entityEmbeddingGenerationSQL+
			` WHERE g.scope=$1 AND g.operation_key=$2::uuid`), scope, key))
		if err == nil {
			if entityEmbeddingModelIdentity(existing.Model) != entityEmbeddingModelIdentity(model) {
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
		if err := tx.QueryRow(ctx, s.schema.SQL(`SELECT count(*) FROM {schema}.entity_embedding_build WHERE scope=$1 AND state<>'discarded'`), scope).Scan(&retained); err != nil {
			return err
		}
		if retained >= MaxRetainedEmbeddingGenerations {
			return ErrEmbeddingCapacity
		}
		var targets int64
		if err := tx.QueryRow(ctx, s.schema.SQL(`SELECT count(*) FROM {schema}.entity WHERE scope=$1 AND identity_kind='named'`), scope).Scan(&targets); err != nil {
			return err
		}
		if targets > MaxEntityEmbeddingTargets {
			return ErrEmbeddingCapacity
		}
		id := uuid.NewString()
		if _, err := tx.Exec(ctx, s.schema.SQL(`INSERT INTO {schema}.entity_embedding_generation
   (generation_id,scope,operation_key,model_name,model_revision,endpoint_hash,identity_hash,dimensions,target_count)
   VALUES($1::uuid,$2,$3::uuid,$4,$5,$6,$7,$8,$9)`), id, scope, key, model.Name,
			model.Revision, model.EndpointHash, entityEmbeddingModelIdentity(model), model.Dimensions, targets); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, s.schema.SQL(`INSERT INTO {schema}.entity_embedding_target(scope,generation_id,entity_id)
 SELECT scope,$2::uuid,entity_id FROM {schema}.entity WHERE scope=$1 AND identity_kind='named'`), scope, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != targets {
			return ErrEmbeddingConflict
		}
		if _, err := tx.Exec(ctx, s.schema.SQL(`INSERT INTO {schema}.entity_embedding_build(scope,generation_id) VALUES($1,$2::uuid)`), scope, id); err != nil {
			return err
		}
		table := pgx.Identifier{s.schema.String(), entityEmbeddingPartitionName(id)}.Sanitize()
		parent := pgx.Identifier{s.schema.String(), "entity_embedding"}.Sanitize()
		if _, err := tx.Exec(ctx, fmt.Sprintf(`CREATE TABLE %s PARTITION OF %s FOR VALUES IN ('%s')`, table, parent, id)); err != nil {
			return err
		}
		expression, operator := embeddingIndexExpression("embedding", model.Dimensions)
		if _, err := tx.Exec(ctx, fmt.Sprintf(`CREATE INDEX %s ON %s USING hnsw ((%s) %s)`,
			pgx.Identifier{entityEmbeddingPartitionName(id) + "_ann"}.Sanitize(), table, expression, operator)); err != nil {
			return err
		}
		if err := embeddingAudit(ctx, tx, s.schema, scope, actor, "entity_embedding.start", targets); err != nil {
			return err
		}
		out, err = scanEntityEmbeddingGeneration(tx.QueryRow(ctx, s.schema.SQL(entityEmbeddingGenerationSQL+
			` WHERE g.scope=$1 AND g.generation_id=$2::uuid`), scope, id))
		return err
	})
	if err != nil {
		return EntityEmbeddingGeneration{}, err
	}
	return out, nil
}

type entityEmbeddingInput struct {
	entity       string
	text         string
	digest       []byte
	sources      []string
	sourceDigest []byte
}

func digestEntitySources(sources []string) []byte {
	digest := sha256.New()
	for _, source := range sources {
		_, _ = digest.Write([]byte(source))
		_, _ = digest.Write([]byte{0})
	}
	return digest.Sum(nil)
}

// loadEntityEmbeddingInput bounds transferred aliases, quotes and source IDs before the caller
// allocates them. Quotes beyond the validation budget cannot affect the 2KB surface.
func (s *EntityEmbeddingStore) loadEntityEmbeddingInput(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, scope, generation, entity string) (entityEmbeddingInput, bool, error) {
	var canonical string
	var aliases, quotes, sources []string
	var compatible bool
	err := q.QueryRow(ctx, s.schema.SQL(`WITH selected AS (
 SELECT e.entity_id,e.canonical_name,e.aliases,
   octet_length(e.canonical_name)+coalesce((SELECT sum(octet_length(a)) FROM unnest(e.aliases[1:64]) a),0) AS base_bytes
 FROM {schema}.entity_embedding_target t JOIN {schema}.entity e USING(scope,entity_id)
 WHERE t.scope=$1 AND t.generation_id=$2::uuid AND t.entity_id=$3::uuid AND e.identity_kind='named'
), distinct_quotes AS (
 SELECT DISTINCT fe.quote FROM selected e JOIN {schema}.fact f
   ON f.scope=$1 AND (f.subject_entity_id=e.entity_id OR f.object_entity_id=e.entity_id)
 JOIN {schema}.fact_evidence fe USING(fact_id) WHERE octet_length(fe.quote)<=$5
), bounded_quotes AS (
 SELECT quote,sum(octet_length(quote)) OVER(ORDER BY quote ROWS UNBOUNDED PRECEDING) AS used
 FROM distinct_quotes
), source_ids AS (
 SELECT r.source_observation_id FROM selected e JOIN {schema}.entity_name_receipt r ON r.scope=$1 AND r.entity_id=e.entity_id
 UNION SELECT d.source_observation_id FROM selected e JOIN {schema}.projection_dependency d
   ON d.scope=$1 AND d.projection_kind='entity' AND d.entity_ref=e.entity_id
 UNION SELECT fe.source_observation_id FROM selected e JOIN {schema}.fact f
   ON f.scope=$1 AND (f.subject_entity_id=e.entity_id OR f.object_entity_id=e.entity_id)
 JOIN {schema}.fact_evidence fe USING(fact_id)
)
SELECT left(e.canonical_name,4096),e.aliases[1:65],
 octet_length(e.canonical_name)<=4096 AND cardinality(e.aliases)<=64
 AND NOT EXISTS(SELECT 1 FROM unnest(e.aliases[1:64]) a WHERE octet_length(a)>4096),
 ARRAY(SELECT quote FROM bounded_quotes WHERE used<=greatest(0,$5-e.base_bytes) ORDER BY quote LIMIT $4),
 ARRAY(SELECT s.source_observation_id::text FROM source_ids s JOIN {schema}.observation o
   ON o.scope=$1 AND o.observation_id=s.source_observation_id ORDER BY s.source_observation_id LIMIT $6)
FROM selected e`), scope, generation, entity, semantic.MaxSurfaceQuotes,
		semantic.MaxSurfaceInputBytes, MaxEntityEmbeddingSources+1).Scan(&canonical, &aliases, &compatible, &quotes, &sources)
	if errors.Is(err, pgx.ErrNoRows) {
		return entityEmbeddingInput{}, false, nil
	}
	if err != nil {
		return entityEmbeddingInput{}, false, err
	}
	if !compatible || len(sources) == 0 || len(sources) > MaxEntityEmbeddingSources {
		return entityEmbeddingInput{}, false, ErrInvalidEmbedding
	}
	text, err := semantic.Surface(canonical, aliases, quotes)
	if err != nil {
		return entityEmbeddingInput{}, false, ErrInvalidEmbedding
	}
	digest := sha256.Sum256([]byte(text))
	return entityEmbeddingInput{entity: entity, text: text, digest: digest[:], sources: sources,
		sourceDigest: digestEntitySources(sources)}, true, nil
}

func (s *EntityEmbeddingStore) BuildPage(ctx context.Context, scope, id, actor string, model EmbeddingModel, limit int, embed EmbeddingBatch) (EntityEmbeddingBuildPage, error) {
	var out EntityEmbeddingBuildPage
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
	lock := s.schema.String() + ":entity-embedding-build:" + scope + ":" + id
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
	var generation EntityEmbeddingGeneration
	err = pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		var scanErr error
		generation, scanErr = scanEntityEmbeddingGeneration(tx.QueryRow(ctx, s.schema.SQL(entityEmbeddingGenerationSQL+
			` WHERE g.scope=$1 AND g.generation_id=$2::uuid FOR UPDATE OF b`), scope, id))
		if scanErr != nil {
			return scanErr
		}
		if entityEmbeddingModelIdentity(generation.Model) != entityEmbeddingModelIdentity(model) {
			return ErrEmbeddingConflict
		}
		if generation.State == "ready" {
			return nil
		}
		if generation.State != "building" {
			return ErrEmbeddingConflict
		}
		_, scanErr = tx.Exec(ctx, s.schema.SQL(`UPDATE {schema}.entity_embedding_build SET lease_id=$3::uuid
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
	rows, err := conn.Query(ctx, s.schema.SQL(`SELECT entity_id::text FROM {schema}.entity_embedding_target
 WHERE scope=$1 AND generation_id=$2::uuid AND entity_id>$3::uuid ORDER BY entity_id LIMIT $4`),
		scope, id, after, limit+1)
	if err != nil {
		return EntityEmbeddingBuildPage{}, err
	}
	var targets []string
	for rows.Next() {
		var target string
		if err := rows.Scan(&target); err != nil {
			rows.Close()
			return EntityEmbeddingBuildPage{}, err
		}
		targets = append(targets, target)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return EntityEmbeddingBuildPage{}, err
	}
	complete := len(targets) <= limit
	if len(targets) > limit {
		targets = targets[:limit]
	}
	entries := make([]entityEmbeddingInput, 0, len(targets))
	processed := make([]string, 0, len(targets))
	sourceTotal := 0
	for _, target := range targets {
		entry, exists, err := s.loadEntityEmbeddingInput(ctx, conn, scope, id, target)
		if err != nil {
			return EntityEmbeddingBuildPage{}, err
		}
		if exists && sourceTotal+len(entry.sources) > MaxEntityEmbeddingPageSources && len(processed) > 0 {
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
			return EntityEmbeddingBuildPage{}, err
		}
	}
	if err := validateEmbeddingBatch(vectors, len(entries), model.Dimensions); err != nil {
		return EntityEmbeddingBuildPage{}, err
	}

	err = pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		var state, currentLease, currentAfter string
		if err := tx.QueryRow(ctx, s.schema.SQL(`SELECT state,coalesce(lease_id::text,''),coalesce(after_entity_id::text,'')
 FROM {schema}.entity_embedding_build WHERE scope=$1 AND generation_id=$2::uuid FOR UPDATE`), scope, id).
			Scan(&state, &currentLease, &currentAfter); err != nil {
			return err
		}
		if state != "building" || currentLease != lease || currentAfter != generation.After {
			return ErrEmbeddingConflict
		}
		stored, err := s.commitEntityEmbeddingInputs(ctx, tx, generation, entries, vectors)
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
		if _, err := tx.Exec(ctx, s.schema.SQL(`UPDATE {schema}.entity_embedding_build
 SET after_entity_id=$3::uuid,state=$4,examined=examined+$5,stored=stored+$6,lease_id=NULL,updated_at=clock_timestamp()
 WHERE scope=$1 AND generation_id=$2::uuid`), scope, id, afterValue, nextState, out.Examined, out.Stored); err != nil {
			return err
		}
		return embeddingAudit(ctx, tx, s.schema, scope, actor, "entity_embedding.build", int64(out.Stored))
	})
	if err != nil {
		return EntityEmbeddingBuildPage{}, err
	}
	return out, nil
}

func (s *EntityEmbeddingStore) commitEntityEmbeddingInputs(ctx context.Context, tx pgx.Tx,
	generation EntityEmbeddingGeneration, entries []entityEmbeddingInput, vectors [][]float32) (int, error) {
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
			s.schema.String()+":entity-embedding:"+entry.entity); err != nil {
			return 0, err
		}
		current, exists, err := s.loadEntityEmbeddingInput(ctx, tx, generation.Project, generation.ID, entry.entity)
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
		tag, err := tx.Exec(ctx, s.schema.SQL(`INSERT INTO {schema}.entity_embedding
 (scope,generation_id,entity_id,input_digest,source_digest,source_count,embedding)
 VALUES($1,$2::uuid,$3::uuid,$4,$5,$6,$7::vector) ON CONFLICT DO NOTHING`), generation.Project,
			generation.ID, entry.entity, entry.digest, entry.sourceDigest, len(entry.sources), string(encoded))
		if err != nil {
			return 0, err
		}
		stored += int(tag.RowsAffected())
		var incompatible bool
		if err := tx.QueryRow(ctx, s.schema.SQL(`SELECT EXISTS(SELECT 1 FROM {schema}.entity_embedding
 WHERE scope=$1 AND generation_id=$2::uuid AND entity_id=$3::uuid
 AND (input_digest,source_digest,source_count) IS DISTINCT FROM ($4::bytea,$5::bytea,$6::integer))`),
			generation.Project, generation.ID, entry.entity, entry.digest, entry.sourceDigest, len(entry.sources)).Scan(&incompatible); err != nil {
			return 0, err
		}
		if incompatible {
			return 0, ErrEmbeddingConflict
		}
		if _, err := tx.Exec(ctx, s.schema.SQL(`INSERT INTO {schema}.entity_embedding_source
 (scope,generation_id,entity_id,source_observation_id)
 SELECT $1,$2::uuid,$3::uuid,source_id FROM unnest($4::uuid[]) source_id ON CONFLICT DO NOTHING`),
			generation.Project, generation.ID, entry.entity, entry.sources); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(ctx, s.schema.SQL(`INSERT INTO {schema}.projection_dependency
 (source_observation_id,scope,projection_kind,projection_id,data_subject_id,pipeline_version)
 SELECT o.observation_id,o.scope,'entity_embedding',$2::uuid::text||'/'||$3::uuid::text,o.data_subject_id,$4
 FROM {schema}.observation o WHERE o.scope=$1 AND o.observation_id=ANY($5::uuid[]) ON CONFLICT DO NOTHING`),
			generation.Project, generation.ID, entry.entity, EntityEmbeddingInputVersion, entry.sources); err != nil {
			return 0, err
		}
	}
	return stored, nil
}

func (s *EntityEmbeddingStore) Activate(ctx context.Context, scope, id, actor string) error {
	if err := validEmbeddingIDs(scope, id, actor); err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := embeddingProjectLock(ctx, tx, s.schema, scope); err != nil {
			return err
		}
		generation, err := scanEntityEmbeddingGeneration(tx.QueryRow(ctx, s.schema.SQL(entityEmbeddingGenerationSQL+
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
		if err := tx.QueryRow(ctx, s.schema.SQL(`SELECT EXISTS(SELECT 1 FROM {schema}.entity_embedding_target t
 LEFT JOIN {schema}.entity_embedding v USING(scope,generation_id,entity_id)
 WHERE t.scope=$1 AND t.generation_id=$2::uuid AND v.entity_id IS NULL)`), scope, id).Scan(&missing); err != nil {
			return err
		}
		if missing {
			return ErrEmbeddingIncomplete
		}
		if _, err := tx.Exec(ctx, s.schema.SQL(`INSERT INTO {schema}.entity_embedding_active(scope,generation_id)
 VALUES($1,$2::uuid) ON CONFLICT(scope) DO UPDATE SET generation_id=excluded.generation_id,activated_at=clock_timestamp()`),
			scope, id); err != nil {
			return err
		}
		return embeddingAudit(ctx, tx, s.schema, scope, actor, "entity_embedding.activate", 1)
	})
}

func (s *EntityEmbeddingStore) Repair(ctx context.Context, scope, id, actor string) error {
	if err := validEmbeddingIDs(scope, id, actor); err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := embeddingProjectLock(ctx, tx, s.schema, scope); err != nil {
			return err
		}
		generation, err := scanEntityEmbeddingGeneration(tx.QueryRow(ctx, s.schema.SQL(entityEmbeddingGenerationSQL+
			` WHERE g.scope=$1 AND g.generation_id=$2::uuid FOR UPDATE OF b`), scope, id))
		if err != nil {
			return err
		}
		if generation.State == "discarded" || generation.State == "stale" {
			return ErrEmbeddingConflict
		}
		if _, err := tx.Exec(ctx, s.schema.SQL(`UPDATE {schema}.entity_embedding_build
 SET state='building',after_entity_id=NULL,examined=0,stored=0,lease_id=NULL,updated_at=clock_timestamp()
 WHERE scope=$1 AND generation_id=$2::uuid`), scope, id); err != nil {
			return err
		}
		return embeddingAudit(ctx, tx, s.schema, scope, actor, "entity_embedding.repair", 1)
	})
}

func (s *EntityEmbeddingStore) Cancel(ctx context.Context, scope, id, actor string) error {
	if err := validEmbeddingIDs(scope, id, actor); err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := embeddingProjectLock(ctx, tx, s.schema, scope); err != nil {
			return err
		}
		generation, err := scanEntityEmbeddingGeneration(tx.QueryRow(ctx, s.schema.SQL(entityEmbeddingGenerationSQL+
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
		if _, err := tx.Exec(ctx, s.schema.SQL(`UPDATE {schema}.entity_embedding_build SET state='cancelled',lease_id=NULL,updated_at=clock_timestamp()
 WHERE scope=$1 AND generation_id=$2::uuid`), scope, id); err != nil {
			return err
		}
		return embeddingAudit(ctx, tx, s.schema, scope, actor, "entity_embedding.cancel", 1)
	})
}

func (s *EntityEmbeddingStore) Prune(ctx context.Context, scope, id, actor string, limit int) (int, error) {
	if err := validEmbeddingIDs(scope, id, actor); err != nil || limit < 1 || limit > MaxEmbeddingPage {
		return 0, ErrInvalidEmbedding
	}
	removed := 0
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := embeddingProjectLock(ctx, tx, s.schema, scope); err != nil {
			return err
		}
		generation, err := scanEntityEmbeddingGeneration(tx.QueryRow(ctx, s.schema.SQL(entityEmbeddingGenerationSQL+
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
		tag, err := tx.Exec(ctx, s.schema.SQL(`DELETE FROM {schema}.entity_embedding WHERE scope=$1 AND generation_id=$2::uuid
 AND entity_id IN (SELECT entity_id FROM {schema}.entity_embedding WHERE scope=$1 AND generation_id=$2::uuid ORDER BY entity_id LIMIT $3)`),
			scope, id, limit)
		if err != nil {
			return err
		}
		removed = int(tag.RowsAffected())
		var remaining bool
		if err := tx.QueryRow(ctx, s.schema.SQL(`SELECT EXISTS(SELECT 1 FROM {schema}.entity_embedding
 WHERE scope=$1 AND generation_id=$2::uuid)`), scope, id).Scan(&remaining); err != nil {
			return err
		}
		if !remaining {
			partition := pgx.Identifier{s.schema.String(), entityEmbeddingPartitionName(id)}.Sanitize()
			if _, err := tx.Exec(ctx, s.schema.SQL(`ALTER TABLE {schema}.entity_embedding DETACH PARTITION `)+partition); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `DROP TABLE `+partition); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, s.schema.SQL(`UPDATE {schema}.entity_embedding_build SET state='discarded',lease_id=NULL,updated_at=clock_timestamp()
 WHERE scope=$1 AND generation_id=$2::uuid`), scope, id); err != nil {
				return err
			}
		}
		return embeddingAudit(ctx, tx, s.schema, scope, actor, "entity_embedding.prune", int64(removed))
	})
	return removed, err
}

func (s *EntityEmbeddingStore) Search(ctx context.Context, scope, actor string, model EmbeddingModel,
	vector []float32, options EntityCandidateOptions) (EntityCandidateResult, error) {
	out := EntityCandidateResult{Candidates: []EntityCandidate{}, Approximate: options.Candidates > 0}
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
		generation, err := scanEntityEmbeddingGeneration(tx.QueryRow(ctx, s.schema.SQL(entityEmbeddingGenerationSQL+
			` WHERE g.scope=$1 AND a.generation_id=g.generation_id FOR SHARE OF b`), scope))
		if err != nil {
			return err
		}
		if generation.State != "ready" || entityEmbeddingModelIdentity(generation.Model) != entityEmbeddingModelIdentity(model) {
			return ErrEmbeddingConflict
		}
		out.Generation = generation
		candidates := `SELECT v.* FROM {schema}.entity_embedding v WHERE v.scope=$1 AND v.generation_id=$2::uuid`
		if options.Candidates > 0 {
			expression, _ := embeddingIndexExpression("v.embedding", model.Dimensions)
			queryExpression, _ := embeddingIndexExpression("$3::vector", model.Dimensions)
			candidates += fmt.Sprintf(` ORDER BY (%s) <=> (%s) LIMIT $5`, expression, queryExpression)
		}
		query := `WITH candidates AS MATERIALIZED (` + candidates + `)
 SELECT e.entity_id::text,e.identity_kind,left(e.canonical_name,512),octet_length(e.canonical_name),char_length(e.canonical_name)>512,
   1-(v.embedding <=> $3::vector) FROM candidates v JOIN {schema}.entity e USING(scope,entity_id)
 ORDER BY v.embedding <=> $3::vector,e.entity_id LIMIT $4`
		args := []any{scope, generation.ID, string(encoded), options.Limit}
		if options.Candidates > 0 {
			args = append(args, options.Candidates)
		}
		rows, err := tx.Query(ctx, s.schema.SQL(query), args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var candidate EntityCandidate
			if err := rows.Scan(&candidate.EntityID, &candidate.IdentityKind, &candidate.NamePreview,
				&candidate.NameBytes, &candidate.NameTruncated, &candidate.Similarity); err != nil {
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
		return embeddingAudit(ctx, tx, s.schema, scope, actor, "entity_embedding.search", int64(len(out.Candidates)))
	})
	if err != nil {
		return EntityCandidateResult{}, err
	}
	return out, nil
}

// FindCandidates is for a caller that has already enforced project authorization and credential
// audit. It returns candidate identities only and cannot merge or mutate them.
func (s *EntityEmbeddingStore) FindCandidates(ctx context.Context, scope string, model EmbeddingModel,
	vector []float32, options EntityCandidateOptions) (EntityCandidateResult, error) {
	options.applicationRead = true
	return s.Search(ctx, scope, "", model, vector, options)
}
