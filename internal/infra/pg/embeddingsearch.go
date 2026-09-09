// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

type MessageSearchOptions struct {
	Limit      int
	Candidates int // Zero is the exact baseline; a positive bounded count opts into ANN candidates.
	Subject    string
	Role       string
}

type MessageMatch struct {
	ChunkID    string  `json:"chunk_id"`
	SourceID   string  `json:"source_id"`
	Ordinal    int     `json:"ordinal"`
	Role       string  `json:"role"`
	Preview    string  `json:"preview"`
	Similarity float64 `json:"similarity"`
}

type MessageSearchResult struct {
	Generation  EmbeddingGeneration `json:"generation"`
	Approximate bool                `json:"approximate"`
	Matches     []MessageMatch      `json:"matches"`
}

// Search returns message evidence, not asserted facts. The exact baseline materializes eligible
// rows before ordering, preventing PostgreSQL from substituting an approximate index traversal.
// ANN candidates are opt-in and reranked against full-precision values and current source hashes.
func (s *MessageEmbeddingStore) Search(ctx context.Context, scope, actor string, model EmbeddingModel, vector []float32, options MessageSearchOptions) (MessageSearchResult, error) {
	out := MessageSearchResult{Matches: []MessageMatch{}, Approximate: options.Candidates > 0}
	if err := validEmbeddingIDs(scope, actor); err != nil {
		return out, err
	}
	if err := model.Validate(); err != nil {
		return out, err
	}
	if err := validateEmbeddingBatch([][]float32{vector}, 1, model.Dimensions); err != nil {
		return out, err
	}
	if options.Limit < 1 || options.Limit > 100 || options.Candidates < 0 || options.Candidates > 1000 || (options.Candidates > 0 && options.Candidates < options.Limit) || len(options.Subject) > 1024 || !utf8.ValidString(options.Subject) || strings.ContainsRune(options.Subject, 0) {
		return out, ErrInvalidEmbedding
	}
	if options.Role != "" && options.Role != "user" && options.Role != "assistant" && options.Role != "system" && options.Role != "tool" {
		return out, ErrInvalidEmbedding
	}
	encoded, _ := json.Marshal(vector)
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		generation, err := scanEmbeddingGeneration(tx.QueryRow(ctx, s.schema.SQL(embeddingGenerationSQL+` WHERE g.scope=$1 AND a.generation_id=g.generation_id FOR SHARE OF b`), scope))
		if err != nil {
			return err
		}
		if generation.Model.Identity() != model.Identity() {
			return ErrEmbeddingConflict
		}
		out.Generation = generation
		candidates := `SELECT v.* FROM {schema}.message_embedding v WHERE v.scope=$1 AND v.generation_id=$2::uuid`
		if options.Candidates > 0 {
			expression, _ := embeddingIndexExpression("v.embedding", model.Dimensions)
			queryExpression, _ := embeddingIndexExpression("$3::vector", model.Dimensions)
			candidates += fmt.Sprintf(` ORDER BY (%s) <=> (%s) LIMIT $7`, expression, queryExpression)
		}
		query := `WITH candidates AS MATERIALIZED (` + candidates + `), eligible AS MATERIALIZED (
   SELECT v.chunk_id,o.observation_id,m.ordinal,m.role,substring(m.content FOR 512) AS preview,v.embedding
   FROM candidates v JOIN {schema}.observation o ON o.scope=v.scope AND o.observation_id=v.source_observation_id
   JOIN {schema}.turn_message m ON m.observation_id=o.observation_id AND m.chunk_id=v.chunk_id
   WHERE ($4::text='' OR o.data_subject_id=$4) AND ($5::text='' OR m.role=$5)
   AND v.input_digest=sha256(convert_to(m.content,'UTF8')))
   SELECT chunk_id::text,observation_id::text,ordinal,role,preview,1-(embedding <=> $3::vector)
   FROM eligible ORDER BY embedding <=> $3::vector,chunk_id LIMIT $6`
		args := []any{scope, generation.ID, string(encoded), options.Subject, options.Role, options.Limit}
		if options.Candidates > 0 {
			args = append(args, options.Candidates)
		}
		rows, err := tx.Query(ctx, s.schema.SQL(query), args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var match MessageMatch
			if err := rows.Scan(&match.ChunkID, &match.SourceID, &match.Ordinal, &match.Role, &match.Preview, &match.Similarity); err != nil {
				rows.Close()
				return err
			}
			out.Matches = append(out.Matches, match)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		return embeddingAudit(ctx, tx, s.schema, scope, actor, "embedding.search", int64(len(out.Matches)))
	})
	if err != nil {
		return MessageSearchResult{}, err
	}
	return out, nil
}
