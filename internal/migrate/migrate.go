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
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ensera-ai/taisce/internal/infra/pg"
)

// Schema and NewSchema live in the pg adapter, which is where every other statement builder needs
// them. Aliased rather than re-declared: two definitions of a validated identifier is two places
// for the validation to drift.
type Schema = pg.Schema

// NewSchema validates a tenant's schema name.
var NewSchema = pg.NewSchema

// maxIdentifierLength is PostgreSQL's limit, restated for the tests that assert the boundary.
const maxIdentifierLength = 63

//go:embed sql/*.sql control/*.sql
var scripts embed.FS

// The two migration sets, and the schema each belongs to.
//
// They advance independently. A tenant is migrated when its schema is brought up to date, which
// happens per tenant at a moment somebody chose; the registry is migrated once. One version number
// over both would make the second wait on the first, or claim a version half its tables had never
// seen.
const (
	tenantScripts  = "sql"
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
func Load() ([]Script, error) { return load(tenantScripts) }

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

// Apply brings ONE TENANT's schema up to the latest embedded version.
//
// Per tenant, not per database. The schema is the hard boundary, so migrating is something that
// happens to a tenant — and a tenant provisioned tomorrow gets the same statements as one
// provisioned today rather than a schema assembled from whatever migrations existed at the time.
//
// Each migration runs in ITS OWN transaction with its version recorded in the same transaction, so
// a failure half way leaves the database at the last version that fully applied rather than in a
// state no version describes.
func Apply(ctx context.Context, pool *pgxpool.Pool, schema Schema) (applied []int, err error) {
	all, err := Load()
	if err != nil {
		return nil, err
	}
	return apply(ctx, pool, schema, all)
}

// ApplyControl brings the registry's schema up to the latest embedded version.
//
// Separate from Apply because the registry is not a tenant. It is migrated once for the deployment
// rather than once per customer, and running it through the tenant path would create a tenant that
// is really the registry — which is exactly the confusion the boundary exists to prevent.
func ApplyControl(ctx context.Context, pool *pgxpool.Pool) ([]int, error) {
	all, err := LoadControl()
	if err != nil {
		return nil, err
	}
	return apply(ctx, pool, ControlSchema, all)
}

func apply(ctx context.Context, pool *pgxpool.Pool, schema Schema, all []Script) (applied []int, err error) {

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
