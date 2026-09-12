// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ProjectStore reads what a project is and what it keeps.
//
// # Why this exists rather than a foreign key
//
// A credential names its project, and the two live on opposite sides of the plane boundary: the
// credential is in the registry schema, which the memory service cannot read, and the project is in
// the tenant schema, which the registry role has no business in. No constraint crosses that, because
// crossing it is what the boundary prevents.
//
// The command that mints a credential checks the project exists, which catches a typo. It cannot
// catch what happens afterwards — a project suspended, or removed, while credentials for it are still
// in circulation. This is that check, made where it can be made: on the memory connection, at the
// moment the credential is used.
//
// That is a better guarantee than the foreign key would have been. A foreign key would have refused a
// bad reference at write time and said nothing about a project whose state changed later.
type ProjectStore struct {
	pool   *pgxpool.Pool
	schema Schema
}

func NewProjectStore(pool *pgxpool.Pool, schema Schema) *ProjectStore {
	return &ProjectStore{pool: pool, schema: schema}
}

// ErrNoSuchProject is returned for a project that does not exist or has been suspended.
//
// One error for both, deliberately. A caller holding a credential for a suspended project learns that
// their credential does not currently work, which is all they can act on; distinguishing "gone" from
// "paused" tells them about an operator's decisions they have no part in.
var ErrNoSuchProject = errors.New("project does not exist or is suspended")

// ErrInvalidRetention refuses a retention nobody could have meant, before it reaches the database.
var ErrInvalidRetention = errors.New("retention must be a number of days, or indefinite")

// Project is what a project keeps.
type Project struct {
	Scope string
	Label string
	// Surfaces are the kinds of memory this project keeps. Chosen at creation, added to afterwards,
	// never removed.
	Surfaces []string
}

// Active resolves a project a credential names, and refuses one that is gone or suspended.
//
// Called on every authenticated request. That is a lookup on the read path, which rule 9 says has to
// be justified against what it costs every caller rather than against what it prevents in the worst
// case: it is one primary-key read of a table with as many rows as the instance has projects, and
// what it buys is that a credential for a project that no longer exists stops working immediately
// rather than writing into a namespace nobody is watching.
func (s *ProjectStore) Active(ctx context.Context, scope string) (Project, error) {
	var p Project
	err := s.pool.QueryRow(ctx, s.schema.SQL(
		`SELECT scope, label, surfaces FROM {schema}.project
		  WHERE scope = $1 AND suspended_at IS NULL`), scope).
		Scan(&p.Scope, &p.Label, &p.Surfaces)
	if errors.Is(err, pgx.ErrNoRows) {
		return Project{}, ErrNoSuchProject
	}
	if err != nil {
		return Project{}, fmt.Errorf("read project %q: %w", scope, err)
	}
	return p, nil
}

// Suspend makes a project's memory unreachable without deleting it.
//
// Not deletion, and the difference is the whole point: a suspended project's memory is intact and can
// be resumed, while deleting it would remove memory without a counted residual — which is the one
// thing this product promises never to do quietly.
// Listed is a project as an operator sees it: its settings and whether it is suspended.
type Listed struct {
	Scope     string   `json:"project"`
	Label     string   `json:"label,omitempty"`
	Surfaces  []string `json:"surfaces"`
	Retention string   `json:"retention,omitempty"`
	Suspended bool     `json:"suspended"`
}

// List reads every project from the project table, suspended ones included. A project is a row,
// so one created and never used is listed: existence does not depend on anybody having used it.
func (s *ProjectStore) List(ctx context.Context) ([]Listed, error) {
	rows, err := s.pool.Query(ctx, s.schema.SQL(
		`SELECT scope, label, surfaces, coalesce(retention::text, ''), suspended_at IS NOT NULL
		   FROM {schema}.project ORDER BY scope`))
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()
	out := []Listed{}
	for rows.Next() {
		var p Listed
		if err := rows.Scan(&p.Scope, &p.Label, &p.Surfaces, &p.Retention, &p.Suspended); err != nil {
			return nil, err
		}
		if p.Surfaces == nil {
			p.Surfaces = []string{}
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SetRetention says how long a project keeps what it is told, in whole days, or indefinitely when
// days is nil.
//
// Whole days rather than an interval the operator types: retention is read as a deadline stamped on
// every turn at append time, and an interval accepted as text is a parser between an operator and a
// deletion schedule. Days are unambiguous, and `make_interval` builds the value rather than a
// string reaching SQL. The policy governs turns that arrive after it; deadlines already stamped are
// not rewritten, which is what the governance page says and what an audit of a deletion relies on.
func (s *ProjectStore) SetRetention(ctx context.Context, scope string, days *int) error {
	if days != nil && (*days < 1 || *days > 36500) {
		return fmt.Errorf("retention is between 1 and 36500 days, or indefinite: %w", ErrInvalidRetention)
	}
	var interval any
	if days != nil {
		interval = *days
	}
	tag, err := s.pool.Exec(ctx, s.schema.SQL(
		`UPDATE {schema}.project
		    SET retention = CASE WHEN $2::int IS NULL THEN NULL ELSE make_interval(days => $2::int) END
		  WHERE scope = $1`), scope, interval)
	if err != nil {
		return fmt.Errorf("set retention for project %q: %w", scope, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNoSuchProject
	}
	return nil
}

func (s *ProjectStore) Suspend(ctx context.Context, scope string) error {
	tag, err := s.pool.Exec(ctx, s.schema.SQL(
		`UPDATE {schema}.project SET suspended_at = now()
		  WHERE scope = $1 AND suspended_at IS NULL`), scope)
	if err != nil {
		return fmt.Errorf("suspend project %q: %w", scope, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNoSuchProject
	}
	return nil
}

// Resume returns a suspended project to service.
func (s *ProjectStore) Resume(ctx context.Context, scope string) error {
	tag, err := s.pool.Exec(ctx, s.schema.SQL(
		`UPDATE {schema}.project SET suspended_at = NULL
		  WHERE scope = $1 AND suspended_at IS NOT NULL`), scope)
	if err != nil {
		return fmt.Errorf("resume project %q: %w", scope, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNoSuchProject
	}
	return nil
}
