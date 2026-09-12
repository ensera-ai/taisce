// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

// Package migrate applies the schema and provisions a project's storage.
//
// The SQL lives beside the code because go:embed cannot reach outside its package directory, and
// one embedded copy is what makes the binary self-contained: a deployment ships a schema rather
// than depending on a file being present next to it.
package migrate

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ensera-ai/taisce/internal/infra/pg"
)

// Schema and NewSchema live in the pg adapter, which is where every other statement builder needs
// them. Aliased rather than re-declared: two definitions of a validated identifier is two places
// for the validation to drift.
type Schema = pg.Schema

// NewSchema validates a database namespace.
var NewSchema = pg.NewSchema

// maxIdentifierLength is PostgreSQL's limit, restated for the tests that assert the boundary.
const maxIdentifierLength = 63

//go:embed sql/*.sql control/*.sql
var scripts embed.FS

// Memory and control have independent migration sequences within one instance. Schema parameters
// support upgrades of existing namespace names and isolated database fixtures, not tenant routing.
const (
	memoryScripts  = "sql"
	controlScripts = "control"
	// ControlSchema is where the registry lives. Fixed rather than configurable: it is one schema
	// for the whole deployment, and a name that can vary is a name a grant can be written against
	// wrongly.
	ControlSchema Schema = "control"
)

// Script is one migration, in the order it must be applied.
type Script struct {
	Version int
	Name    string
	SQL     string
}

// Load reads the embedded migrations, ordered by version.
//
// A duplicate version is an error rather than a last-one-wins, because two files claiming the same
// version apply in filename order on one machine and in another order on the next.
func Load() ([]Script, error) { return load(memoryScripts) }

// LoadControl reads the embedded registry migrations, ordered by version.
func LoadControl() ([]Script, error) { return load(controlScripts) }

func load(dir string) ([]Script, error) {
	entries, err := fs.ReadDir(scripts, dir)
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}

	var out []Script
	seen := map[int]string{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".sql") {
			continue
		}
		prefix, rest, ok := strings.Cut(strings.TrimSuffix(name, ".sql"), "_")
		if !ok {
			return nil, fmt.Errorf("migration %q is not <version>_<name>.sql", name)
		}
		version, err := strconv.Atoi(prefix)
		if err != nil {
			return nil, fmt.Errorf("migration %q has a non-numeric version: %w", name, err)
		}
		if previous, dup := seen[version]; dup {
			return nil, fmt.Errorf("migrations %q and %q both claim version %d", previous, name, version)
		}
		seen[version] = name

		body, err := scripts.ReadFile(dir + "/" + name)
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", name, err)
		}
		out = append(out, Script{Version: version, Name: rest, SQL: string(body)})
	}
	if len(out) == 0 {
		return nil, errors.New("no migration scripts are embedded")
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

// Apply upgrades an instance memory namespace. Each migration and its version marker commit in
// one transaction, so failure leaves the last fully applied version. Project creation never calls it.
func Apply(ctx context.Context, pool *pgxpool.Pool, schema Schema) (applied []int, err error) {
	all, err := Load()
	if err != nil {
		return nil, err
	}
	err = withNamespaceLock(ctx, pool, schema, func(on statements) error {
		applied, err = apply(ctx, on, schema, all)
		return err
	})
	return applied, err
}

// withNamespaceLock runs fn holding the one lock that serialises changes to a memory namespace's
// structure and grants.
//
// Bootstrap runs on every start and the chart starts replicas together, so two processes can migrate
// one namespace at once. Each migration is its own transaction, which keeps one migration whole but
// lets two runners interleave: both find version N unapplied, and the loser fails on an object the
// winner just created. The grants after the migrations race the same way. So the lock is a
// session-level advisory lock held across all of it, rather than a lock per transaction.
//
// Taken on a connection of its own, and everything under it runs on that connection, for the
// reason EstablishPlanes gives: a caller that borrows a second connection while holding the first
// deadlocks once the pool has as many waiters as connections.
func withNamespaceLock(ctx context.Context, pool *pgxpool.Pool, schema Schema, fn func(on statements) error) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire a connection to migrate %q: %w", schema, err)
	}
	defer conn.Release()
	key := "taisce:migrate:" + schema.String()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(hashtext($1)::bigint)`, key); err != nil {
		return fmt.Errorf("take the migration lock for %q: %w", schema, err)
	}
	defer func() {
		// WithoutCancel so a cancelled caller still releases it: a held session lock would outlive
		// the caller and stall the next start.
		_, _ = conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock(hashtext($1)::bigint)`, key)
	}()
	return fn(conn)
}

// applyControl upgrades the fixed instance credential namespace using its separate migration set.
//
// Unexported, and there is deliberately no exported wrapper. Its one caller is bootstrap, which
// already holds a connection for its session lock and passes that connection in rather than
// borrowing another from the pool — see `statements`. An exported form taking a pool was a second way
// in that nothing used, and a second way in is one somebody eventually calls from a path holding a
// lock, which is the deadlock that comment describes.
func applyControl(ctx context.Context, on statements) ([]int, error) {
	all, err := LoadControl()
	if err != nil {
		return nil, err
	}
	return apply(ctx, on, ControlSchema, all)
}

// statements is what applying needs from a pool, and a single pooled connection offers the same.
// A caller that already holds one connection for a session lock runs its migrations on that
// connection rather than borrowing more from the pool: with N such callers on a pool of N, every
// connection is held by a waiter and the lock holder waits for one that never comes back.
type statements interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Begin(ctx context.Context) (pgx.Tx, error)
}

func apply(ctx context.Context, pool statements, schema Schema, all []Script) (applied []int, err error) {

	// The bootstrap problem: the table recording which migrations ran is itself created by the
	// first migration. Asking for its contents before it exists is expected, not an error.
	current := map[int]bool{}
	rows, err := pool.Query(ctx, schema.SQL(`SELECT version FROM {schema}.schema_migration`))
	if err == nil {
		for rows.Next() {
			var v int
			if scanErr := rows.Scan(&v); scanErr != nil {
				rows.Close()
				return nil, scanErr
			}
			current[v] = true
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	for _, script := range all {
		if current[script.Version] {
			continue
		}
		if err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
			if _, execErr := tx.Exec(ctx, schema.SQL(script.SQL)); execErr != nil {
				return fmt.Errorf("migration %d (%s): %w", script.Version, script.Name, execErr)
			}
			_, recErr := tx.Exec(ctx, schema.SQL(
				`INSERT INTO {schema}.schema_migration (version, name) VALUES ($1, $2)
				 ON CONFLICT (version) DO NOTHING`),
				script.Version, script.Name)
			return recErr
		}); err != nil {
			return applied, err
		}
		applied = append(applied, script.Version)
	}
	return applied, nil
}
