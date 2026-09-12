// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package pg

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RefusalStore reads what extraction refused, as counts. A count by reason and predicate is
// what tells an operator whether the vocabulary is wrong (unmapped relations cluster on a
// predicate), the model is paraphrasing (unlocatable quotes) or repeating itself (duplicates), and
// it carries none of the words: the words are somebody's, and they are read only under a grant on
// the project they came from, through the memory routes.
type RefusalStore struct {
	pool   *pgxpool.Pool
	schema Schema
}

func NewRefusalStore(pool *pgxpool.Pool, schema Schema) *RefusalStore {
	return &RefusalStore{pool: pool, schema: schema}
}

// RefusalCount is how many proposals one predicate was refused for one reason.
type RefusalCount struct {
	Reason    string `json:"reason"`
	Predicate string `json:"predicate"`
	Count     int64  `json:"count"`
}

// RefusalSummary is one project's refusals: the total and the counts, largest first, at most the
// limit; a predicate past the limit is in the total and not in the list.
type RefusalSummary struct {
	Scope  string         `json:"project"`
	Total  int64          `json:"total"`
	Counts []RefusalCount `json:"counts"`
}

// Summary counts one project's refusals by reason and predicate.
func (s *RefusalStore) Summary(ctx context.Context, scope string, limit int) (RefusalSummary, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	out := RefusalSummary{Scope: scope, Counts: []RefusalCount{}}
	if err := s.pool.QueryRow(ctx, s.schema.SQL(`SELECT count(*) FROM {schema}.rejected_claim WHERE scope=$1`), scope).Scan(&out.Total); err != nil {
		return out, fmt.Errorf("count refusals: %w", err)
	}
	rows, err := s.pool.Query(ctx, s.schema.SQL(`
		SELECT reason, predicate, count(*) FROM {schema}.rejected_claim
		 WHERE scope=$1 GROUP BY reason, predicate ORDER BY count(*) DESC, reason, predicate LIMIT $2`), scope, limit)
	if err != nil {
		return out, fmt.Errorf("summarise refusals: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var c RefusalCount
		if err := rows.Scan(&c.Reason, &c.Predicate, &c.Count); err != nil {
			return out, err
		}
		out.Counts = append(out.Counts, c)
	}
	return out, rows.Err()
}
