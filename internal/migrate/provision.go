// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package migrate

import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// scopePattern is what a project identifier may contain.
//
// A partition's name is built from it and cannot be parameterised, so this is the only thing between
// a project name and an injection. A strict allowlist rather than an escape routine: an allowlist
// fails closed on what it did not anticipate, an escape routine fails open.
var scopePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// ProvisionMemorySchema creates and upgrades an instance's memory namespace. Production binds one
// namespace for its lifetime; parameterization supports existing names and isolated test fixtures.
// The schema name prevents identifier injection. Database privileges, not naming, protect secrets.
func ProvisionMemorySchema(ctx context.Context, pool *pgxpool.Pool, name string) error {
	schema, err := NewSchema(name)
	if err != nil {
		return err
	}
	all, err := Load()
	if err != nil {
		return err
	}
	// Creating, migrating and granting under one lock: two starters racing any of the three can
	// each fail on what the other just did.
	return withNamespaceLock(ctx, pool, schema, func(on statements) error {
		if _, err := on.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS %s`, pgx.Identifier{schema.String()}.Sanitize())); err != nil {
			return fmt.Errorf("create memory namespace %q: %w", name, err)
		}
		if _, err := apply(ctx, on, schema, all); err != nil {
			return fmt.Errorf("migrate memory namespace %q: %w", name, err)
		}
		return grantMemoryToDataPlane(ctx, on, schema)
	})
}

// ErrProjectStorageConflict refuses an existing relation that cannot serve this project's storage.
var ErrProjectStorageConflict = errors.New("project storage conflicts with an existing relation")

// ProvisionScope atomically creates a project's row and source-chunk partition. Vector indexes
// belong to explicit model generations, where their dimension is known.
// Repeated/concurrent calls validate existing storage instead of trusting its name. A failed
// statement rolls back all newly created storage. Unprovisioned writes remain in the default
// partition; moving existing default-partition data is a separate maintenance operation.
func ProvisionScope(ctx context.Context, pool *pgxpool.Pool, namespace, scope string) error {
	schema, err := NewSchema(namespace)
	if err != nil {
		return err
	}
	if !scopePattern.MatchString(scope) {
		return fmt.Errorf("project %q is not a valid identifier: %s", scope, scopePattern)
	}
	tableName := projectStorageNames(scope)
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1)::bigint)`, namespace+":project:"+scope); err != nil {
			return err
		}
		// Find an actual matching attachment first. Legacy names, including previously truncated
		// names, are retained when their bound is correct. No data is renamed or moved here.
		var attachedName string
		err := tx.QueryRow(ctx, findProjectPartitionSQL, namespace, scope).Scan(&attachedName)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("inspect project partition: %w", err)
		}
		if errors.Is(err, pgx.ErrNoRows) {
			partition := pgx.Identifier{namespace, tableName}.Sanitize()
			if _, err := tx.Exec(ctx, fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s PARTITION OF %s.chunk FOR VALUES IN ('%s')`, partition, schema, scope)); err != nil {
				return fmt.Errorf("create project partition: %w", err)
			}
			if err := tx.QueryRow(ctx, findProjectPartitionSQL, namespace, scope).Scan(&attachedName); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return ErrProjectStorageConflict
				}
				return fmt.Errorf("validate project partition: %w", err)
			}
		}
		if _, err := tx.Exec(ctx, schema.SQL(`INSERT INTO {schema}.project(scope,label) VALUES($1,$1) ON CONFLICT(scope) DO NOTHING`), scope); err != nil {
			return fmt.Errorf("record project %q: %w", scope, err)
		}
		return nil
	})
}

// Short project names remain legible; long names use the full SHA-256 digest so a PostgreSQL
// identifier truncation cannot merge two projects. Actual partition attachment is still validated.
func projectStorageNames(scope string) string {
	if len(scope) <= 49 {
		return "chunk_" + scope
	}
	digest := sha256.Sum256([]byte(scope))
	encoded := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(digest[:]))
	return "chunk_" + encoded
}

const findProjectPartitionSQL = `
SELECT child.relname FROM pg_inherits inheritance
JOIN pg_class parent ON parent.oid=inheritance.inhparent
JOIN pg_namespace pn ON pn.oid=parent.relnamespace
JOIN pg_class child ON child.oid=inheritance.inhrelid
JOIN pg_namespace cn ON cn.oid=child.relnamespace
WHERE pn.nspname=$1 AND parent.relname='chunk' AND cn.nspname=$1
 AND child.relispartition AND child.relkind='r'
 AND pg_get_expr(child.relpartbound,child.oid)=format('FOR VALUES IN (%L)',$2::text)`
