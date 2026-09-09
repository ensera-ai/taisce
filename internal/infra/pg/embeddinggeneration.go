// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const embeddingGenerationSQL = `SELECT g.generation_id::text,g.scope,g.operation_key::text,
 g.model_name,g.model_revision,g.endpoint_hash,g.dimensions,b.through_offset,g.through_offset,b.covered_through_offset,
 b.after_offset,b.after_ordinal,b.state,coalesce(a.generation_id=g.generation_id,false),b.examined,b.stored,g.created_at
 FROM {schema}.embedding_generation g JOIN {schema}.embedding_build b USING(scope,generation_id)
 LEFT JOIN {schema}.embedding_active a USING(scope)`

func scanEmbeddingGeneration(row pgx.Row) (EmbeddingGeneration, error) {
	var out EmbeddingGeneration
	err := row.Scan(&out.ID, &out.Project, &out.Key, &out.Model.Name, &out.Model.Revision, &out.Model.EndpointHash, &out.Model.Dimensions, &out.ThroughOffset,
		&out.InitialThroughOffset, &out.CoveredThroughOffset, &out.After.Offset, &out.After.Ordinal, &out.State, &out.Active, &out.Examined, &out.Stored, &out.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return EmbeddingGeneration{}, ErrEmbeddingNotFound
	}
	return out, err
}
func (s *MessageEmbeddingStore) Generation(ctx context.Context, scope, id string) (EmbeddingGeneration, error) {
	if err := validEmbeddingIDs(scope, id); err != nil {
		return EmbeddingGeneration{}, err
	}
	return scanEmbeddingGeneration(s.pool.QueryRow(ctx, s.schema.SQL(embeddingGenerationSQL+` WHERE g.scope=$1 AND g.generation_id=$2::uuid`), scope, id))
}
func (s *MessageEmbeddingStore) Active(ctx context.Context, scope string) (EmbeddingGeneration, error) {
	if err := validEmbeddingIDs(scope); err != nil {
		return EmbeddingGeneration{}, err
	}
	return scanEmbeddingGeneration(s.pool.QueryRow(ctx, s.schema.SQL(embeddingGenerationSQL+` WHERE g.scope=$1 AND a.generation_id=g.generation_id`), scope))
}
func embeddingProjectLock(ctx context.Context, tx pgx.Tx, schema Schema, scope string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1)::bigint)`, schema.String()+":embedding-policy:"+scope)
	return err
}
func embeddingAudit(ctx context.Context, tx pgx.Tx, schema Schema, scope, actor, operation string, magnitude int64) error {
	_, err := tx.Exec(ctx, schema.SQL(`INSERT INTO {schema}.audit_entry(operation,principal,principal_kind,project,magnitude,outcome)
 VALUES($1,$2::uuid,'operator',$3,$4,'allowed')`), operation, actor, scope, magnitude)
	return err
}
func embeddingPartitionName(id string) string {
	return "message_vector_" + strings.ReplaceAll(id, "-", "")
}
func embeddingIndexExpression(column string, dimensions int) (string, string) {
	if dimensions > 2000 {
		return fmt.Sprintf("l2_normalize(%s)::halfvec(%d)", column, dimensions), "halfvec_cosine_ops"
	}
	return fmt.Sprintf("%s::vector(%d)", column, dimensions), "vector_cosine_ops"
}

// Start captures a finite log boundary and allocates a dimension-specific index. Its operation key
// survives lost output. Model identity never changes within a generation, including after pruning.
func (s *MessageEmbeddingStore) Start(ctx context.Context, scope, key, actor string, model EmbeddingModel) (EmbeddingGeneration, error) {
	if err := validEmbeddingIDs(scope, key, actor); err != nil {
		return EmbeddingGeneration{}, err
	}
	if err := model.Validate(); err != nil {
		return EmbeddingGeneration{}, err
	}
	var out EmbeddingGeneration
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := embeddingProjectLock(ctx, tx, s.schema, scope); err != nil {
			return err
		}
		existing, err := scanEmbeddingGeneration(tx.QueryRow(ctx, s.schema.SQL(embeddingGenerationSQL+` WHERE g.scope=$1 AND g.operation_key=$2::uuid`), scope, key))
		if err == nil {
			if existing.Model.Identity() != model.Identity() {
				return ErrEmbeddingConflict
			}
			out = existing
			return nil
		}
		if !errors.Is(err, ErrEmbeddingNotFound) {
			return err
		}
		var present int
		if err := tx.QueryRow(ctx, s.schema.SQL(`SELECT 1 FROM {schema}.project WHERE scope=$1 AND suspended_at IS NULL FOR SHARE`), scope).Scan(&present); errors.Is(err, pgx.ErrNoRows) {
			return ErrNoSuchProject
		} else if err != nil {
			return err
		}
		var count int
		if err := tx.QueryRow(ctx, s.schema.SQL(`SELECT count(*) FROM {schema}.embedding_build WHERE scope=$1 AND state<>'discarded'`), scope).Scan(&count); err != nil {
			return err
		}
		if count >= MaxRetainedEmbeddingGenerations {
			return ErrEmbeddingCapacity
		}
		var through int64
		if err := tx.QueryRow(ctx, s.schema.SQL(`SELECT coalesce(max(log_offset),-1) FROM {schema}.observation WHERE scope=$1`), scope).Scan(&through); err != nil {
			return err
		}
		id := uuid.NewString()
		if _, err := tx.Exec(ctx, s.schema.SQL(`INSERT INTO {schema}.embedding_generation
   (generation_id,scope,operation_key,model_name,model_revision,endpoint_hash,identity_hash,dimensions,through_offset)
   VALUES($1::uuid,$2,$3::uuid,$4,$5,$6,$7,$8,$9)`), id, scope, key, model.Name, model.Revision, model.EndpointHash, model.Identity(), model.Dimensions, through); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, s.schema.SQL(`INSERT INTO {schema}.embedding_build(scope,generation_id,through_offset) VALUES($1,$2::uuid,$3)`), scope, id, through); err != nil {
			return err
		}
		table := pgx.Identifier{s.schema.String(), embeddingPartitionName(id)}.Sanitize()
		parent := pgx.Identifier{s.schema.String(), "message_embedding"}.Sanitize()
		if _, err := tx.Exec(ctx, fmt.Sprintf(`CREATE TABLE %s PARTITION OF %s FOR VALUES IN ('%s')`, table, parent, id)); err != nil {
			return err
		}
		expression, operator := embeddingIndexExpression("embedding", model.Dimensions)
		if _, err := tx.Exec(ctx, fmt.Sprintf(`CREATE INDEX %s ON %s USING hnsw ((%s) %s)`, pgx.Identifier{embeddingPartitionName(id) + "_ann"}.Sanitize(), table, expression, operator)); err != nil {
			return err
		}
		if err := embeddingAudit(ctx, tx, s.schema, scope, actor, "embedding.start", 1); err != nil {
			return err
		}
		out, err = scanEmbeddingGeneration(tx.QueryRow(ctx, s.schema.SQL(embeddingGenerationSQL+` WHERE g.scope=$1 AND g.generation_id=$2::uuid`), scope, id))
		return err
	})
	if err != nil {
		return EmbeddingGeneration{}, err
	}
	return out, nil
}

// Activation independently checks surviving authoritative messages, not a worker's progress claim.
// The project policy lock serializes activation, cancellation, allocation and pruning.
func (s *MessageEmbeddingStore) Activate(ctx context.Context, scope, id, actor string) error {
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
		if g.Active {
			return nil
		}
		if g.State != "ready" {
			return ErrEmbeddingIncomplete
		}
		var missing bool
		if err := tx.QueryRow(ctx, s.schema.SQL(`SELECT EXISTS(SELECT 1 FROM {schema}.observation o JOIN {schema}.turn_message m ON m.observation_id=o.observation_id
   LEFT JOIN {schema}.message_embedding v ON v.scope=o.scope AND v.generation_id=$2::uuid AND v.chunk_id=m.chunk_id
   WHERE o.scope=$1 AND o.kind='turn' AND o.log_offset<=$3
   AND (v.chunk_id IS NULL OR v.input_digest<>sha256(convert_to(m.content,'UTF8'))))`), scope, id, g.ThroughOffset).Scan(&missing); err != nil {
			return err
		}
		if missing {
			return ErrEmbeddingIncomplete
		}
		if _, err := tx.Exec(ctx, s.schema.SQL(`INSERT INTO {schema}.embedding_active(scope,generation_id) VALUES($1,$2::uuid)
   ON CONFLICT(scope) DO UPDATE SET generation_id=excluded.generation_id,activated_at=clock_timestamp()`), scope, id); err != nil {
			return err
		}
		return embeddingAudit(ctx, tx, s.schema, scope, actor, "embedding.activate", 1)
	})
}
func (s *MessageEmbeddingStore) Cancel(ctx context.Context, scope, id, actor string) error {
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
		if g.Active {
			return ErrEmbeddingConflict
		}
		if g.State == "cancelled" || g.State == "discarded" {
			return nil
		}
		if _, err := tx.Exec(ctx, s.schema.SQL(`UPDATE {schema}.embedding_build SET state='cancelled',lease_id=NULL,updated_at=clock_timestamp() WHERE scope=$1 AND generation_id=$2::uuid`), scope, id); err != nil {
			return err
		}
		return embeddingAudit(ctx, tx, s.schema, scope, actor, "embedding.cancel", 1)
	})
}

// Prune deletes at most one page from a non-active, non-building generation. Only its empty,
// catalog-validated partition is dropped; the operation key remains as a content-free tombstone.
func (s *MessageEmbeddingStore) Prune(ctx context.Context, scope, id, actor string, limit int) (int, error) {
	if err := validEmbeddingIDs(scope, id, actor); err != nil {
		return 0, err
	}
	if limit < 1 || limit > MaxEmbeddingPage {
		return 0, ErrInvalidEmbedding
	}
	removed := 0
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := embeddingProjectLock(ctx, tx, s.schema, scope); err != nil {
			return err
		}
		g, err := scanEmbeddingGeneration(tx.QueryRow(ctx, s.schema.SQL(embeddingGenerationSQL+` WHERE g.scope=$1 AND g.generation_id=$2::uuid FOR UPDATE OF b`), scope, id))
		if err != nil {
			return err
		}
		if g.Active || g.State == "building" {
			return ErrEmbeddingConflict
		}
		if g.State == "discarded" {
			return nil
		}
		tag, err := tx.Exec(ctx, s.schema.SQL(`DELETE FROM {schema}.message_embedding WHERE scope=$1 AND generation_id=$2::uuid AND chunk_id IN
   (SELECT chunk_id FROM {schema}.message_embedding WHERE scope=$1 AND generation_id=$2::uuid ORDER BY chunk_id LIMIT $3)`), scope, id, limit)
		if err != nil {
			return err
		}
		removed = int(tag.RowsAffected())
		var remaining bool
		if err := tx.QueryRow(ctx, s.schema.SQL(`SELECT EXISTS(SELECT 1 FROM {schema}.message_embedding WHERE scope=$1 AND generation_id=$2::uuid)`), scope, id).Scan(&remaining); err != nil {
			return err
		}
		if !remaining {
			var matches bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_inherits i JOIN pg_class p ON p.oid=i.inhparent JOIN pg_namespace pn ON pn.oid=p.relnamespace
    JOIN pg_class c ON c.oid=i.inhrelid JOIN pg_namespace cn ON cn.oid=c.relnamespace
    WHERE pn.nspname=$1 AND cn.nspname=$1 AND p.relname='message_embedding' AND c.relname=$2 AND pg_get_expr(c.relpartbound,c.oid)=format('FOR VALUES IN (%L)',$3::text))`, s.schema.String(), embeddingPartitionName(id), id).Scan(&matches); err != nil {
				return err
			}
			if !matches {
				return ErrEmbeddingConflict
			}
			partition := pgx.Identifier{s.schema.String(), embeddingPartitionName(id)}.Sanitize()
			if _, err := tx.Exec(ctx, s.schema.SQL(`ALTER TABLE {schema}.message_embedding DETACH PARTITION `)+partition); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `DROP TABLE `+partition); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, s.schema.SQL(`UPDATE {schema}.embedding_build SET state='discarded',lease_id=NULL,updated_at=clock_timestamp() WHERE scope=$1 AND generation_id=$2::uuid`), scope, id); err != nil {
				return err
			}
		}
		return embeddingAudit(ctx, tx, s.schema, scope, actor, "embedding.prune", int64(removed))
	})
	if err != nil {
		return 0, err
	}
	return removed, nil
}
