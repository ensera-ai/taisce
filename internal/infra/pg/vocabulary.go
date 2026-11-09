// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ensera-ai/taisce/internal/domain"
)

// The write side of the relation vocabulary: adding a relation the seeded set did not name.
//
// # What this decides, and what it must not
//
// It decides one thing — that the statement is an INSERT of five declarations and nothing else, so
// no caller reaches an UPDATE or a DELETE through it. It decides nothing about whether a
// declaration is acceptable. The name's shape, the three closed axes and the uniqueness of the name
// are CHECK and key constraints on the table, and they are left to refuse, because a rule enforced
// here as well would be the same rule in two places and the copy nothing enforces is the one that
// drifts. What this does instead is report *which* constraint refused, so an operator is told what
// to fix rather than that something was wrong.

// NewPredicate is a relation being declared: the five statements about it that have to be made when
// it is defined, because each one is unanswerable afterwards.
//
// `cardinality` is the reason none of them has a default. It is what supersession rests on — whether
// a new fact closes the previous one or coexists with it — and at supersession time all that is
// visible is two rows, so a guess made here is a wrong answer months later that nothing reports.
type NewPredicate struct {
	Predicate    string
	SemanticType string
	Cardinality  string
	ObjectKind   string
	Description  string
}

// VocabularyRefusal is the database declining a relation, carrying what declined it.
//
// A distinguished error rather than a message, because the caller has to answer a duplicate
// differently from a malformed declaration: one means the vocabulary already admits the relation and
// there is nothing to do, the other means the declaration has to be corrected.
type VocabularyRefusal struct {
	// Constraint is the constraint that refused — or, for a declaration that was left out, the name
	// of the column whose NOT NULL refused it. Either way it is the substrate's own vocabulary and
	// never anything the caller supplied, so it is safe to report.
	Constraint string
	// Duplicate marks a relation the vocabulary already holds.
	Duplicate bool
}

func (r *VocabularyRefusal) Error() string {
	if r.Duplicate {
		return "the vocabulary already admits that relation: " + r.Constraint
	}
	return "the vocabulary refused that relation: " + r.Constraint
}

// VocabularyStore extends and reads one tenant's relation vocabulary.
//
// It takes the connection it is given, which for this operation is the administrative one the
// management surface holds. The memory identity has SELECT on this table and no write at all, and
// that stays true: this is reached from the operator surface and from nowhere on the memory path.
type VocabularyStore struct {
	pool   *pgxpool.Pool
	schema Schema
}

func NewVocabularyStore(pool *pgxpool.Pool, schema Schema) *VocabularyStore {
	return &VocabularyStore{pool: pool, schema: schema}
}

// Add declares one relation and returns the row the database stored.
//
// Returned rather than echoed back, so what an operator is shown is what the table holds — including
// `event`, which is not one of the five and takes the column's default of false. False is the
// conservative value: a past-tense assertion of the relation is read as one that has ended and
// refused as not current, rather than being written as a current fact. A relation that reports a
// completed occurrence is a later judgement about the relation, made once somebody has seen how it
// is asserted, and it is not a decision to make blind at the moment the name is chosen.
func (s *VocabularyStore) Add(ctx context.Context, p NewPredicate) (domain.Predicate, error) {
	var out domain.Predicate
	var cardinality string
	err := s.pool.QueryRow(ctx, s.schema.SQL(insertPredicateSQL),
		blankIsNull(p.Predicate), blankIsNull(p.SemanticType), blankIsNull(p.Cardinality),
		blankIsNull(p.ObjectKind), blankIsNull(p.Description)).
		Scan(&out.Name, &out.SemanticType, &cardinality, &out.ObjectKind, &out.Description, &out.Event)
	if err != nil {
		var db *pgconn.PgError
		if errors.As(err, &db) {
			switch db.Code {
			case uniqueViolation:
				return domain.Predicate{}, &VocabularyRefusal{Constraint: db.ConstraintName, Duplicate: true}
			case checkViolation:
				return domain.Predicate{}, &VocabularyRefusal{Constraint: db.ConstraintName}
			case notNullViolation:
				// A declaration left out arrives as NULL (see blankIsNull), so the column that
				// refused it is the name of the declaration that is missing.
				return domain.Predicate{}, &VocabularyRefusal{Constraint: db.ColumnName}
			}
		}
		// Deliberately without the declaration that failed: an operator's words go to the operator
		// in the refusal, not into this deployment's logs.
		return domain.Predicate{}, fmt.Errorf("extend the relation vocabulary: %w", err)
	}
	out.Cardinality = domain.Cardinality(cardinality)
	return out, nil
}

// List is the vocabulary as the extractor sees it.
//
// Read through the same loader the extractor uses, rather than with a query of its own, so an
// operator inspecting the vocabulary and the model being told it cannot be shown different sets —
// which is the disagreement that would make a refused relation impossible to explain.
func (s *VocabularyStore) List(ctx context.Context) ([]domain.Predicate, error) {
	ontology, err := LoadOntology(ctx, s.pool, s.schema)
	if err != nil {
		return nil, err
	}
	return ontology.All(), nil
}

// The five declarations and nothing else. RETURNING, because the row the table holds is the answer:
// `event` is set by the column's default and is part of what the relation now is.
const insertPredicateSQL = `
INSERT INTO {schema}.predicate (predicate, semantic_type, cardinality, object_kind, description)
VALUES ($1, $2, $3, $4, $5)
RETURNING predicate, semantic_type, cardinality, object_kind, description, event`

// The refusals this operation can meet, by the code the substrate gives them.
const (
	uniqueViolation  = "23505"
	checkViolation   = "23514"
	notNullViolation = "23502"
)

// blankIsNull sends a declaration that was left out as NULL.
//
// That is what makes "all five are required" a property of the table rather than of this function:
// the column's NOT NULL refuses it, and the refusal names the column. Checking it here as well
// would put the same rule in two places, and an empty string is not a declaration — a relation whose
// description is blank is one nobody can tell apart from its nearest neighbour later.
func blankIsNull(s string) *string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return &s
}
