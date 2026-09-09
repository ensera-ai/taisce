// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestMessageEmbeddingSearchUsesCurrentSourcesAndExplicitModelIdentity(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "embedding_search_sources")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	if _, err := pg.NewObservationStore(pool).Append(ctx, schema, turnOf(domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "user evidence"}, domain.Message{Ordinal: 1, Role: domain.RoleAssistant, Content: "assistant evidence"})); err != nil {
		t.Fatal(err)
	}
	store := pg.NewMessageEmbeddingStore(pool, schema)
	actor := uuid.NewString()
	model := pg.EmbeddingModel{Name: "test", Revision: "v1", EndpointHash: strings.Repeat("a", 64), Dimensions: 3}
	gen, err := store.Start(ctx, "p1", uuid.NewString(), actor, model)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.BuildPage(ctx, "p1", gen.ID, actor, model, 64, func(context.Context, []string) ([][]float32, error) { return [][]float32{{1, 0, 0}, {0, 1, 0}}, nil }); err != nil {
		t.Fatal(err)
	}
	if err := store.Activate(ctx, "p1", gen.ID, actor); err != nil {
		t.Fatal(err)
	}
	for _, candidates := range []int{0, 32} {
		result, err := store.Search(ctx, "p1", actor, model, []float32{1, 0, 0}, pg.MessageSearchOptions{Limit: 2, Candidates: candidates})
		if err != nil || len(result.Matches) != 2 || result.Matches[0].Preview != "user evidence" || result.Matches[0].Similarity != 1 || result.Approximate != (candidates > 0) {
			t.Fatalf("search %+v %v", result, err)
		}
	}
	result, err := store.Search(ctx, "p1", actor, model, []float32{1, 0, 0}, pg.MessageSearchOptions{Limit: 2, Role: "assistant", Subject: "subject-1"})
	if err != nil || len(result.Matches) != 1 || result.Matches[0].Role != "assistant" {
		t.Fatalf("filters %+v %v", result, err)
	}
	changed := model
	changed.Revision = "different"
	if _, err := store.Search(ctx, "p1", actor, changed, []float32{1, 0, 0}, pg.MessageSearchOptions{Limit: 2}); !errors.Is(err, pg.ErrEmbeddingConflict) {
		t.Fatalf("mixed spaces: %v", err)
	}
	if _, err := store.Search(ctx, "p2", actor, model, []float32{1, 0, 0}, pg.MessageSearchOptions{Limit: 2}); !errors.Is(err, pg.ErrEmbeddingNotFound) {
		t.Fatalf("cross project: %v", err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.turn_message SET content='changed authoritative text' WHERE ordinal=0`)); err != nil {
		t.Fatal(err)
	}
	result, err = store.Search(ctx, "p1", actor, model, []float32{1, 0, 0}, pg.MessageSearchOptions{Limit: 2})
	if err != nil || len(result.Matches) != 1 || result.Matches[0].Role != "assistant" {
		t.Fatalf("stale input returned %+v %v", result, err)
	}
}

// This is an index eligibility and partition-pruning proof, not a latency or recall claim. A
// controlled cost setting makes the index choice observable on a deliberately tiny fixture.
func TestTheVectorSearchPrunesToOnePartitionAndUsesItsIndex(t *testing.T) {
	for _, dimensions := range []int{1024, 2560} {
		t.Run(fmt.Sprint(dimensions), func(t *testing.T) {
			ctx := context.Background()
			pool := testPool(t)
			schema := tenant(t, pool, fmt.Sprintf("embedding_plan_%d", dimensions))
			defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
			store := pg.NewMessageEmbeddingStore(pool, schema)
			if _, err := pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.project(scope,label) VALUES('p2','p2')`)); err != nil {
				t.Fatal(err)
			}
			actor := uuid.NewString()
			model := pg.EmbeddingModel{Name: "test", Revision: "v1", EndpointHash: strings.Repeat("a", 64), Dimensions: dimensions}
			for page := 0; page < 16; page++ {
				messages := make([]domain.Message, 32)
				for i := range messages {
					messages[i] = domain.Message{Ordinal: i, Role: domain.RoleUser, Content: fmt.Sprintf("message %d/%d", page, i)}
				}
				if _, err := pg.NewObservationStore(pool).Append(ctx, schema, turnOf(messages...)); err != nil {
					t.Fatal(err)
				}
			}
			first, err := store.Start(ctx, "p1", uuid.NewString(), actor, model)
			if err != nil {
				t.Fatal(err)
			}
			second, err := store.Start(ctx, "p2", uuid.NewString(), actor, model)
			if err != nil {
				t.Fatal(err)
			}
			for {
				page, err := store.BuildPage(ctx, "p1", first.ID, actor, model, 64, func(_ context.Context, texts []string) ([][]float32, error) {
					vectors := make([][]float32, len(texts))
					for i := range vectors {
						vectors[i] = make([]float32, dimensions)
						vectors[i][i%dimensions] = 1
					}
					return vectors, nil
				})
				if err != nil {
					t.Fatal(err)
				}
				if page.Complete {
					break
				}
			}
			if _, err := pool.Exec(ctx, schema.SQL(`ANALYZE {schema}.message_embedding`)); err != nil {
				t.Fatal(err)
			}
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			if _, err := tx.Exec(ctx, `SET LOCAL enable_seqscan=off`); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(ctx, `SET LOCAL enable_sort=off`); err != nil {
				t.Fatal(err)
			}
			expression := fmt.Sprintf("embedding::vector(%d)", dimensions)
			query := fmt.Sprintf("array_fill(0.5::real,ARRAY[%d])::vector(%d)", dimensions, dimensions)
			if dimensions > 2000 {
				expression = fmt.Sprintf("l2_normalize(embedding)::halfvec(%d)", dimensions)
				query = fmt.Sprintf("l2_normalize(array_fill(0.5::real,ARRAY[%d])::vector)::halfvec(%d)", dimensions, dimensions)
			}
			rows, err := tx.Query(ctx, schema.SQL(fmt.Sprintf(`EXPLAIN SELECT chunk_id FROM {schema}.message_embedding WHERE scope='p1' AND generation_id='%s' ORDER BY (%s) <=> (%s) LIMIT 10`, first.ID, expression, query)))
			if err != nil {
				t.Fatal(err)
			}
			var lines []string
			for rows.Next() {
				var line string
				if err := rows.Scan(&line); err != nil {
					t.Fatal(err)
				}
				lines = append(lines, line)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			plan := strings.Join(lines, "\n")
			t.Log(plan)
			if !strings.Contains(plan, "message_vector_"+strings.ReplaceAll(first.ID, "-", "")+"_ann") || strings.Contains(plan, strings.ReplaceAll(second.ID, "-", "")) {
				t.Fatalf("wrong plan: %s", plan)
			}
		})
	}
}

func TestEmbeddingGenerationAuditFailureRollsBackItsPartitionIndexAndMetadata(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "embedding_generation_rollback")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	if _, err := pool.Exec(ctx, schema.SQL(`CREATE FUNCTION {schema}.refuse_embedding_start() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.operation='embedding.start' THEN RAISE EXCEPTION 'injected embedding audit failure'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER refuse_embedding_start BEFORE INSERT ON {schema}.audit_entry FOR EACH ROW EXECUTE FUNCTION {schema}.refuse_embedding_start()`)); err != nil {
		t.Fatal(err)
	}
	store := pg.NewMessageEmbeddingStore(pool, schema)
	actor, key := uuid.NewString(), uuid.NewString()
	model := pg.EmbeddingModel{Name: "test", Revision: "v1", EndpointHash: strings.Repeat("a", 64), Dimensions: 1024}
	if _, err := store.Start(ctx, "p1", key, actor, model); err == nil || !strings.Contains(err.Error(), "injected embedding audit failure") {
		t.Fatalf("audit failure %v", err)
	}
	var relations, metadata int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname LIKE 'message_vector_%'`, schema.String()).Scan(&relations); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.embedding_generation`)).Scan(&metadata); err != nil || metadata != 0 || relations != 0 {
		t.Fatalf("partial generation: relations=%d metadata=%d %v", relations, metadata, err)
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER refuse_embedding_start ON `+pgx.Identifier{schema.String(), "audit_entry"}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Start(ctx, "p1", key, actor, model); err != nil {
		t.Fatalf("retry after remediation: %v", err)
	}
}
