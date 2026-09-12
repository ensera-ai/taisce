// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// EmbeddingBatch is supplied by the worker after checking the configured model identity. Source
// text leaves PostgreSQL only for this bounded call; model output cannot choose ownership or IDs.
type EmbeddingBatch func(context.Context, []string) ([][]float32, error)

type embeddingSource struct {
	source, chunk, content string
	cursor                 ChunkRecoveryCursor
	digest                 []byte
}

type EmbeddingBuildPage struct {
	Examined int                 `json:"examined"`
	Stored   int                 `json:"stored"`
	Complete bool                `json:"complete"`
	After    ChunkRecoveryCursor `json:"after"`
}

// BuildPage keeps a session lock across the provider call but no database transaction. A persisted
// lease fences a disconnected worker: holding an old result never grants permission to publish it.
func (s *MessageEmbeddingStore) BuildPage(ctx context.Context, scope, id, actor string, model EmbeddingModel, limit int, embed EmbeddingBatch) (EmbeddingBuildPage, error) {
	return s.buildPage(ctx, scope, id, actor, model, limit, embed, false)
}

func (s *MessageEmbeddingStore) buildPage(ctx context.Context, scope, id, actor string, model EmbeddingModel, limit int, embed EmbeddingBatch, activeOnly bool) (EmbeddingBuildPage, error) {
	var out EmbeddingBuildPage
	if err := validEmbeddingIDs(scope, id, actor); err != nil {
		return out, err
	}
	id = uuid.MustParse(id).String()
	if limit < 1 || limit > MaxEmbeddingPage || embed == nil {
		return out, ErrInvalidEmbedding
	}
	if err := model.Validate(); err != nil {
		return out, err
	}
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return out, err
	}
	lock := s.schema.String() + ":embedding-build:" + scope + ":" + id
	var locked bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1,0))`, lock).Scan(&locked); err != nil {
		cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = conn.Hijack().Close(cleanup)
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
	var generation EmbeddingGeneration
	err = pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		var e error
		generation, e = scanEmbeddingGeneration(tx.QueryRow(ctx, s.schema.SQL(embeddingGenerationSQL+` WHERE g.scope=$1 AND g.generation_id=$2::uuid FOR UPDATE OF b`), scope, id))
		if e != nil {
			return e
		}
		if generation.Model.Identity() != model.Identity() || (activeOnly && !generation.Active) {
			return ErrEmbeddingConflict
		}
		if generation.State == "ready" {
			return nil
		}
		if generation.State != "building" {
			return ErrEmbeddingConflict
		}
		_, e = tx.Exec(ctx, s.schema.SQL(`UPDATE {schema}.embedding_build SET lease_id=$3::uuid WHERE scope=$1 AND generation_id=$2::uuid`), scope, id, lease)
		return e
	})
	if err != nil {
		return out, err
	}
	out.After = generation.After
	if generation.State == "ready" {
		out.Complete = true
		return out, nil
	}
	entries, complete, err := s.embeddingInputs(ctx, conn, generation, limit)
	if err != nil {
		return EmbeddingBuildPage{}, err
	}
	texts := make([]string, len(entries))
	for i := range entries {
		texts[i] = entries[i].content
	}
	vectors := [][]float32{}
	if len(texts) > 0 {
		vectors, err = embed(ctx, texts)
		if err != nil {
			return EmbeddingBuildPage{}, err
		}
	}
	if err := validateEmbeddingBatch(vectors, len(entries), model.Dimensions); err != nil {
		return EmbeddingBuildPage{}, err
	}
	err = pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		// Serialize publication against operator activation. Runtime workers cannot lock the
		// read-only activation row FOR SHARE, so use the same policy lock as the operator paths.
		if activeOnly {
			if err := embeddingProjectLock(ctx, tx, s.schema, scope); err != nil {
				return err
			}
			var active bool
			if err := tx.QueryRow(ctx, s.schema.SQL(`SELECT EXISTS(SELECT 1 FROM {schema}.embedding_active WHERE scope=$1 AND generation_id=$2::uuid)`), scope, id).Scan(&active); err != nil {
				return err
			}
			if !active {
				return ErrEmbeddingConflict
			}
		}
		var state, currentLease string
		var after ChunkRecoveryCursor
		if err := tx.QueryRow(ctx, s.schema.SQL(`SELECT state,coalesce(lease_id::text,''),after_offset,after_ordinal FROM {schema}.embedding_build WHERE scope=$1 AND generation_id=$2::uuid FOR UPDATE`), scope, id).Scan(&state, &currentLease, &after.Offset, &after.Ordinal); err != nil {
			return err
		}
		if state != "building" || currentLease != lease || after != generation.After {
			return ErrEmbeddingConflict
		}
		stored, err := s.commitEmbeddingInputs(ctx, tx, generation, entries, vectors)
		if err != nil {
			return err
		}
		out.Stored = stored
		out.Examined = len(entries)
		out.Complete = complete
		if len(entries) > 0 {
			out.After = entries[len(entries)-1].cursor
		}
		nextState := "building"
		if complete {
			nextState = "ready"
		}
		if _, err := tx.Exec(ctx, s.schema.SQL(`UPDATE {schema}.embedding_build SET after_offset=$3,after_ordinal=$4,state=$5,examined=examined+$6,stored=stored+$7,lease_id=NULL,covered_through_offset=CASE WHEN $5='ready' THEN through_offset ELSE covered_through_offset END,updated_at=clock_timestamp() WHERE scope=$1 AND generation_id=$2::uuid`), scope, id, out.After.Offset, out.After.Ordinal, nextState, out.Examined, out.Stored); err != nil {
			return err
		}
		return embeddingAudit(ctx, tx, s.schema, scope, actor, "embedding.build", int64(out.Stored))
	})
	if err != nil {
		return EmbeddingBuildPage{}, err
	}
	return out, nil
}

// Metadata is read first so even a malformed oversized source cannot allocate an unbounded payload.
// The second query clips to the checked length; a concurrent edit causes a refusal, not truncation.
func (s *MessageEmbeddingStore) embeddingInputs(ctx context.Context, conn interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, g EmbeddingGeneration, limit int) ([]embeddingSource, bool, error) {
	rows, err := conn.Query(ctx, s.schema.SQL(`SELECT o.observation_id::text,m.chunk_id::text,o.log_offset,m.ordinal,octet_length(m.content)
 FROM {schema}.observation o JOIN {schema}.turn_message m USING(observation_id)
 WHERE o.scope=$1 AND o.kind='turn' AND o.log_offset<=$2 AND (o.log_offset,m.ordinal)>($3,$4)
 ORDER BY o.log_offset,m.ordinal LIMIT $5`), g.Project, g.ThroughOffset, g.After.Offset, g.After.Ordinal, limit+1)
	if err != nil {
		return nil, false, err
	}
	entries := []embeddingSource{}
	sizes := []int{}
	total := 0
	complete := true
	for rows.Next() {
		var e embeddingSource
		var size int
		if err := rows.Scan(&e.source, &e.chunk, &e.cursor.Offset, &e.cursor.Ordinal, &size); err != nil {
			rows.Close()
			return nil, false, err
		}
		if len(entries) == limit {
			complete = false
			break
		}
		if size > 64<<10 || total+size > MaxEmbeddingPageBytes {
			if len(entries) == 0 {
				rows.Close()
				return nil, false, ErrInvalidEmbedding
			}
			complete = false
			break
		}
		total += size
		entries = append(entries, e)
		sizes = append(sizes, size)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if len(entries) == 0 {
		return entries, complete, nil
	}
	sources := make([]string, len(entries))
	ordinals := make([]int, len(entries))
	for i, e := range entries {
		sources[i] = e.source
		ordinals[i] = e.cursor.Ordinal
	}
	rows, err = conn.Query(ctx, s.schema.SQL(`SELECT q.n,CASE WHEN octet_length(m.content)=q.bytes THEN m.content ELSE '' END,octet_length(m.content),m.chunk_id::text
 FROM unnest($2::uuid[],$3::int[],$4::int[]) WITH ORDINALITY q(source_id,ordinal,bytes,n)
 JOIN {schema}.observation o ON o.observation_id=q.source_id AND o.scope=$1
 JOIN {schema}.turn_message m ON m.observation_id=o.observation_id AND m.ordinal=q.ordinal ORDER BY q.n`), g.Project, sources, ordinals, sizes)
	if err != nil {
		return nil, false, err
	}
	count := 0
	for rows.Next() {
		var n, size int
		var content, chunk string
		if err := rows.Scan(&n, &content, &size, &chunk); err != nil {
			rows.Close()
			return nil, false, err
		}
		if n < 1 || n > len(entries) || size != sizes[n-1] || size > 64<<10 || chunk != entries[n-1].chunk || strings.TrimSpace(content) == "" || !utf8.ValidString(content) {
			rows.Close()
			return nil, false, ErrEmbeddingConflict
		}
		entries[n-1].content = content
		digest := sha256.Sum256([]byte(content))
		entries[n-1].digest = digest[:]
		count++
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	// Retry from the unchanged cursor if erasure raced with preparation.
	if count != len(entries) {
		return nil, false, ErrEmbeddingConflict
	}
	return entries, complete, nil
}

func validateEmbeddingBatch(vectors [][]float32, count, dimensions int) error {
	if len(vectors) != count {
		return ErrInvalidEmbedding
	}
	for _, vector := range vectors {
		if len(vector) != dimensions {
			return ErrInvalidEmbedding
		}
		nonzero := false
		for _, v := range vector {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				return ErrInvalidEmbedding
			}
			nonzero = nonzero || v != 0
		}
		if !nonzero {
			return ErrInvalidEmbedding
		}
	}
	return nil
}

// All surviving inputs are locked and checked in one query. The provider's result supplies only
// vector coordinates; every relationship is taken from the retained observation and message.
func (s *MessageEmbeddingStore) commitEmbeddingInputs(ctx context.Context, tx pgx.Tx, g EmbeddingGeneration, entries []embeddingSource, vectors [][]float32) (int, error) {
	if len(entries) == 0 {
		return 0, nil
	}
	sources := make([]string, len(entries))
	ordinals := make([]int, len(entries))
	for i, e := range entries {
		sources[i] = e.source
		ordinals[i] = e.cursor.Ordinal
	}
	rows, err := tx.Query(ctx, s.schema.SQL(`SELECT q.n,m.chunk_id::text,sha256(convert_to(m.content,'UTF8'))
 FROM unnest($2::uuid[],$3::int[]) WITH ORDINALITY q(source_id,ordinal,n)
 JOIN {schema}.observation o ON o.observation_id=q.source_id AND o.scope=$1
 JOIN {schema}.turn_message m ON m.observation_id=o.observation_id AND m.ordinal=q.ordinal
 ORDER BY o.log_offset,m.ordinal FOR SHARE OF o,m`), g.Project, sources, ordinals)
	if err != nil {
		return 0, err
	}
	surviving := []int{}
	for rows.Next() {
		var n int
		var chunk string
		var digest []byte
		if err := rows.Scan(&n, &chunk, &digest); err != nil {
			rows.Close()
			return 0, err
		}
		if n < 1 || n > len(entries) || chunk != entries[n-1].chunk || string(digest) != string(entries[n-1].digest) {
			rows.Close()
			return 0, ErrEmbeddingConflict
		}
		surviving = append(surviving, n-1)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(surviving) == 0 {
		return 0, nil
	}
	// Chunk recovery is exceptional; normal ingestion already owns a compatible registered chunk.
	var chunks []string
	var keptSources []string
	var digests [][]byte
	var encoded []string
	for _, i := range surviving {
		chunks = append(chunks, entries[i].chunk)
		keptSources = append(keptSources, entries[i].source)
		digests = append(digests, entries[i].digest)
		value, err := json.Marshal(vectors[i])
		if err != nil {
			return 0, err
		}
		encoded = append(encoded, string(value))
	}
	rows, err = tx.Query(ctx, s.schema.SQL(`SELECT q.chunk_id::text FROM unnest($2::uuid[]) q(chunk_id)
 LEFT JOIN {schema}.chunk c ON c.scope=$1 AND c.chunk_id=q.chunk_id WHERE c.chunk_id IS NULL`), g.Project, chunks)
	if err != nil {
		return 0, err
	}
	missing := map[string]bool{}
	for rows.Next() {
		var chunk string
		if err := rows.Scan(&chunk); err != nil {
			rows.Close()
			return 0, err
		}
		missing[chunk] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, i := range surviving {
		if missing[entries[i].chunk] {
			if _, _, err := recoverMessageChunkTx(ctx, tx, s.schema, g.Project, entries[i].source, entries[i].cursor.Ordinal); err != nil {
				return 0, err
			}
		}
	}
	var incompatible bool
	var lockedChunks int
	if err := tx.QueryRow(ctx, s.schema.SQL(`SELECT count(*) FROM (SELECT chunk_id FROM {schema}.chunk WHERE scope=$1 AND chunk_id=ANY($2::uuid[]) ORDER BY chunk_id FOR UPDATE) locked`), g.Project, chunks).Scan(&lockedChunks); err != nil {
		return 0, err
	}
	if lockedChunks != len(chunks) {
		return 0, ErrEmbeddingConflict
	}
	if err := tx.QueryRow(ctx, s.schema.SQL(`SELECT EXISTS(SELECT 1 FROM {schema}.chunk c JOIN {schema}.turn_message m ON m.chunk_id=c.chunk_id
 JOIN {schema}.observation o ON o.observation_id=m.observation_id
 WHERE c.scope=$1 AND c.chunk_id=ANY($2::uuid[]) AND
 (c.source_observation_id,c.source_message_ordinal,c.text,c.source_role,c.occurred_at,c.data_subject_id)
 IS DISTINCT FROM (o.observation_id,m.ordinal,m.content,m.role,m.occurred_at,o.data_subject_id))`), g.Project, chunks).Scan(&incompatible); err != nil {
		return 0, err
	}
	if incompatible {
		return 0, ErrEmbeddingConflict
	}
	if _, err := tx.Exec(ctx, s.schema.SQL(`INSERT INTO {schema}.projection_dependency(source_observation_id,scope,projection_kind,projection_id,data_subject_id,pipeline_version)
 SELECT o.observation_id,o.scope,'chunk',m.chunk_id::text,o.data_subject_id,'v1' FROM {schema}.turn_message m JOIN {schema}.observation o USING(observation_id)
 WHERE o.scope=$1 AND m.chunk_id=ANY($2::uuid[]) ON CONFLICT DO NOTHING`), g.Project, chunks); err != nil {
		return 0, err
	}
	tag, err := tx.Exec(ctx, s.schema.SQL(`INSERT INTO {schema}.message_embedding(scope,generation_id,chunk_id,source_observation_id,embedding_key,input_digest,embedding)
 SELECT $1,$2::uuid,q.chunk_id,q.source_id,$2::uuid::text||'/'||q.chunk_id::text,q.digest,q.value::vector
 FROM unnest($3::uuid[],$4::uuid[],$5::bytea[],$6::text[]) q(chunk_id,source_id,digest,value) ON CONFLICT DO NOTHING`), g.Project, g.ID, chunks, keptSources, digests, encoded)
	if err != nil {
		return 0, err
	}
	if err := tx.QueryRow(ctx, s.schema.SQL(`SELECT EXISTS(SELECT 1 FROM unnest($3::uuid[],$4::uuid[],$5::bytea[]) q(chunk_id,source_id,digest)
 JOIN {schema}.message_embedding v ON v.scope=$1 AND v.generation_id=$2::uuid AND v.chunk_id=q.chunk_id
 WHERE (v.source_observation_id,v.input_digest) IS DISTINCT FROM (q.source_id,q.digest))`), g.Project, g.ID, chunks, keptSources, digests).Scan(&incompatible); err != nil {
		return 0, err
	}
	if incompatible {
		return 0, ErrEmbeddingConflict
	}
	if _, err := tx.Exec(ctx, s.schema.SQL(`INSERT INTO {schema}.projection_dependency(source_observation_id,scope,projection_kind,projection_id,data_subject_id,pipeline_version)
 SELECT o.observation_id,o.scope,'message_embedding',$2::uuid::text||'/'||m.chunk_id::text,o.data_subject_id,'v1'
 FROM {schema}.turn_message m JOIN {schema}.observation o USING(observation_id) WHERE o.scope=$1 AND m.chunk_id=ANY($3::uuid[])
 ON CONFLICT DO NOTHING`), g.Project, g.ID, chunks); err != nil {
		return 0, err
	}
	if err := tx.QueryRow(ctx, s.schema.SQL(`SELECT EXISTS(SELECT 1 FROM {schema}.projection_dependency d JOIN {schema}.observation o ON o.observation_id=d.source_observation_id
 WHERE o.scope=$1 AND d.source_observation_id=ANY($2::uuid[]) AND ((d.projection_kind='chunk' AND d.projection_id=ANY($3::text[])) OR (d.projection_kind='message_embedding' AND d.embedding_generation_ref=$4::uuid AND d.embedding_chunk_ref=ANY($3::uuid[])))
 AND (d.scope,d.data_subject_id,d.pipeline_version) IS DISTINCT FROM (o.scope,o.data_subject_id,'v1'::text))`), g.Project, keptSources, chunks, g.ID).Scan(&incompatible); err != nil {
		return 0, err
	}
	if incompatible {
		return 0, ErrEmbeddingConflict
	}
	return int(tag.RowsAffected()), nil
}

// Repair replays the same captured source range and model. It cannot silently extend coverage or
// replace a compatible value. A concurrent in-flight page loses its lease before any write commits.
func (s *MessageEmbeddingStore) Repair(ctx context.Context, scope, id, actor string) error {
	if err := validEmbeddingIDs(scope, id, actor); err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := embeddingProjectLock(ctx, tx, s.schema, scope); err != nil {
			return err
		}
		g, err := scanEmbeddingGeneration(tx.QueryRow(ctx, s.schema.SQL(embeddingGenerationSQL+` WHERE g.scope=$1 AND g.generation_id=$2::uuid FOR UPDATE OF b`), scope, id))
		if err != nil {
			return err
		}
		if g.State == "discarded" {
			return ErrEmbeddingConflict
		}
		if _, err := tx.Exec(ctx, s.schema.SQL(`UPDATE {schema}.embedding_build SET state='building',after_offset=-1,after_ordinal=-1,examined=0,stored=0,lease_id=NULL,covered_through_offset=-1,updated_at=clock_timestamp() WHERE scope=$1 AND generation_id=$2::uuid`), scope, id); err != nil {
			return err
		}
		return embeddingAudit(ctx, tx, s.schema, scope, actor, "embedding.repair", 1)
	})
}
