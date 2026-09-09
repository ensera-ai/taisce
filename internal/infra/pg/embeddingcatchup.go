// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"context"
	"github.com/jackc/pgx/v5"
	"math"
)

// CatchUpPage advances only the explicitly selected active model. A page sequence captures one
// finite log boundary; later observations wait for the next sequence. Completed ranges never need
// to be re-embedded. Source repair remains explicit and resets its own coverage claim.
func (s *MessageEmbeddingStore) CatchUpPage(ctx context.Context, scope, id, actor string, model EmbeddingModel, limit int, embed EmbeddingBatch) (EmbeddingBuildPage, error) {
	if err := validEmbeddingIDs(scope, id, actor); err != nil {
		return EmbeddingBuildPage{}, err
	}
	if err := model.Validate(); err != nil {
		return EmbeddingBuildPage{}, err
	}
	if limit < 1 || limit > MaxEmbeddingPage || embed == nil {
		return EmbeddingBuildPage{}, ErrInvalidEmbedding
	}
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := embeddingProjectLock(ctx, tx, s.schema, scope); err != nil {
			return err
		}
		g, err := scanEmbeddingGeneration(tx.QueryRow(ctx, s.schema.SQL(embeddingGenerationSQL+` WHERE g.scope=$1 AND g.generation_id=$2::uuid FOR UPDATE OF b`), scope, id))
		if err != nil {
			return err
		}
		if !g.Active || g.Model.Identity() != model.Identity() || (g.State != "ready" && g.State != "building") {
			return ErrEmbeddingConflict
		}
		var available bool
		if err := tx.QueryRow(ctx, s.schema.SQL(`SELECT EXISTS(SELECT 1 FROM {schema}.project WHERE scope=$1 AND suspended_at IS NULL)`), scope).Scan(&available); err != nil {
			return err
		}
		if !available {
			return ErrNoSuchProject
		}
		if g.State == "building" {
			return nil
		}
		if g.CoveredThroughOffset != g.ThroughOffset {
			return ErrEmbeddingIncomplete
		}
		// The append watermark is transactionally serialized per project. It includes erased and
		// non-message observations; using a source count would neither be ordered nor preserve gaps.
		var through int64
		if err := tx.QueryRow(ctx, s.schema.SQL(`SELECT coalesce((SELECT log_offset FROM {schema}.watermark WHERE scope=$1),-1)`), scope).Scan(&through); err != nil {
			return err
		}
		if through <= g.ThroughOffset {
			return nil
		}
		ordinal := math.MaxInt32
		if g.ThroughOffset < 0 {
			ordinal = -1
		}
		if _, err := tx.Exec(ctx, s.schema.SQL(`UPDATE {schema}.embedding_build SET through_offset=$3,after_offset=$4,after_ordinal=$5,state='building',lease_id=NULL,updated_at=clock_timestamp() WHERE scope=$1 AND generation_id=$2::uuid`), scope, id, through, g.ThroughOffset, ordinal); err != nil {
			return err
		}
		// The existing build operation accounts for work preparation without extending audit vocabulary.
		return embeddingAudit(ctx, tx, s.schema, scope, actor, "embedding.build", 0)
	})
	if err != nil {
		return EmbeddingBuildPage{}, err
	}
	return s.buildPage(ctx, scope, id, actor, model, limit, embed, true)
}
