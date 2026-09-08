// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package migrate

import (
	"context"
	"fmt"
	"regexp"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// scopePattern is what a project identifier may contain.
//
// A partition's name is built from it and cannot be parameterised, so this is the only thing between
// a project name and an injection. A strict allowlist rather than an escape routine: an allowlist
// fails closed on what it did not anticipate, an escape routine fails open.
var scopePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

// ProvisionMemorySchema creates and upgrades an instance's memory namespace. Production binds one
// namespace for its lifetime; parameterization supports existing names and isolated test fixtures.
// The schema name prevents identifier injection. Database privileges, not naming, protect secrets.
func ProvisionMemorySchema(ctx context.Context, pool *pgxpool.Pool, name string) error {
	schema, err := NewSchema(name)
	if err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS %s`, pgx.Identifier{schema.String()}.Sanitize())); err != nil {
		return fmt.Errorf("create memory namespace %q: %w", name, err)
	}
	if _, err := Apply(ctx, pool, schema); err != nil {
		return fmt.Errorf("migrate memory namespace %q: %w", name, err)
	}
	return grantMemoryToDataPlane(ctx, pool, schema)
}

// ProvisionScope creates a project's chunk partition and its ANN index, inside the instance memory namespace.
//
// Called once when a project is created. Until it runs, writes for that project land in the default
// partition — visible, erasable, and slow to search, which is the right failure: losing a customer's
// memory because provisioning missed a step is worse than an unindexed project.
//
// IF NOT EXISTS throughout, so re-provisioning is a no-op. This is the kind of step an operator
// retries when unsure whether it ran.
func ProvisionScope(ctx context.Context, pool *pgxpool.Pool, namespace, scope string) error {
	schema, err := NewSchema(namespace)
	if err != nil {
		return err
	}
	if !scopePattern.MatchString(scope) {
		return fmt.Errorf("project %q is not a valid identifier: %s", scope, scopePattern)
	}
	partition := pgx.Identifier{schema.String(), "chunk_" + scope}.Sanitize()
	index := pgx.Identifier{"chunk_" + scope + "_ann_idx"}.Sanitize()

	// The partition, then the index INSIDE it.
	//
	// An index on the partitioned parent would be built across every project in the instance, and a
	// single graph behind a scope predicate is unreachable: the planner prefers an exact sort, and
	// post-filtering an approximate result loses recall in proportion to how little of the corpus
	// belongs to the caller. Per partition, pruning happens first and the walk covers only that
	// project's vectors.
	for _, statement := range []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s PARTITION OF %s.chunk FOR VALUES IN ('%s')`,
			partition, schema, scope),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s ON %s USING hnsw (embedding vector_cosine_ops)`,
			index, partition),
	} {
		if _, err := pool.Exec(ctx, statement); err != nil {
			return fmt.Errorf("provision project %q in memory namespace %q: %w", scope, namespace, err)
		}
	}

	// The row last, after the storage it describes exists. A project that is listed and has no
	// partition is one a write would land in the default partition of — visible, erasable and slow,
	// which is the safety net rather than the intent.
	//
	// Surfaces and retention are not set here. Provisioning creates the place; deciding what a
	// project keeps is a separate act with its own arguments, and defaulting it would mean every
	// project silently acquiring whatever this function happened to choose.
	if _, err := pool.Exec(ctx, fmt.Sprintf(
		`INSERT INTO %s.project (scope, label) VALUES ($1, $1) ON CONFLICT (scope) DO NOTHING`,
		schema), scope); err != nil {
		return fmt.Errorf("record project %q in memory namespace %q: %w", scope, namespace, err)
	}
	return nil
}
