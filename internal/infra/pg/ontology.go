// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ensera-ai/taisce/internal/domain"
)

// LoadOntology reads a tenant's relation vocabulary.
//
// # Why it is read rather than declared in Go
//
// `fact.predicate` references the `predicate` table, so the database is what actually closes the
// set. A Go list beside it would be a second definition of a closed set, and the one that drifts is
// always the one no constraint enforces — at which point the extractor is told a vocabulary the
// database will refuse, and the failure surfaces as a foreign key violation on a claim that looked
// fine.
//
// # Why per tenant
//
// A tenant is a schema, so the vocabulary is a copy per tenant. That is the cost of the hard
// boundary, and it is also what makes a tenant-specific vocabulary possible later without changing
// how anything reads it.
//
// Read once and held, not per extraction: it is forty rows that change when a migration runs.
func LoadOntology(ctx context.Context, pool *pgxpool.Pool, schema Schema) (domain.Ontology, error) {
	rows, err := pool.Query(ctx, schema.SQL(selectOntologySQL))
	if err != nil {
		return domain.Ontology{}, fmt.Errorf("read relation vocabulary: %w", err)
	}
	defer rows.Close()

	var predicates []domain.Predicate
	for rows.Next() {
		var p domain.Predicate
		var cardinality string
		if err := rows.Scan(&p.Name, &p.SemanticType, &cardinality, &p.ObjectKind, &p.Description, &p.Event); err != nil {
			return domain.Ontology{}, err
		}
		switch domain.Cardinality(cardinality) {
		case domain.CardinalityOne, domain.CardinalityMany:
			p.Cardinality = domain.Cardinality(cardinality)
		default:
			// Unreachable while the CHECK holds. Kept because the alternative is a zero-valued
			// cardinality reaching supersession, where it would silently mean "many" and stop a
			// contradiction from ever being closed.
			return domain.Ontology{}, fmt.Errorf("relation %q has cardinality %q", p.Name, cardinality)
		}
		predicates = append(predicates, p)
	}
	if err := rows.Err(); err != nil {
		return domain.Ontology{}, err
	}
	return domain.NewOntology(predicates)
}

// Ordered by name rather than by insertion, so the list a prompt is built from is the same list on
// every deployment. A prompt whose vocabulary arrives in a different order is a different prompt,
// and extraction quality cannot be compared across runs that were not asked the same question.
const selectOntologySQL = `
SELECT predicate, semantic_type, cardinality, object_kind, description, event
  FROM {schema}.predicate
 ORDER BY predicate`

// LoadVocabulary reads both closed sets an extractor needs.
//
// One call rather than two, because they are read together on the same path and a caller that loaded
// one and forgot the other would get an extractor whose refusals silently stop happening — which
// looks like the model improving.
func LoadVocabulary(ctx context.Context, pool *pgxpool.Pool, schema Schema) (domain.Vocabulary, error) {
	ontology, err := LoadOntology(ctx, pool, schema)
	if err != nil {
		return domain.Vocabulary{}, err
	}

	rows, err := pool.Query(ctx, schema.SQL(
		`SELECT DISTINCT term FROM {schema}.unresolvable_term u
          WHERE NOT EXISTS (SELECT 1 FROM {schema}.speaker_term s WHERE s.term=u.term)`))
	if err != nil {
		return domain.Vocabulary{}, fmt.Errorf("read unresolvable terms: %w", err)
	}
	defer rows.Close()

	terms := map[string]struct{}{}
	for rows.Next() {
		var term string
		if err := rows.Scan(&term); err != nil {
			return domain.Vocabulary{}, err
		}
		terms[term] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return domain.Vocabulary{}, err
	}
	return domain.Vocabulary{Ontology: ontology, Unresolvable: terms}, nil
}
