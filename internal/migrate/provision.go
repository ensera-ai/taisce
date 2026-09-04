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

// ProvisionTenant creates a tenant's schema and brings it to the latest version.
//
// This is where the hard boundary is established. After it, every statement about this tenant names
// the schema and no other tenant's data is addressable from it.
func ProvisionTenant(ctx context.Context, pool *pgxpool.Pool, tenant string) error {
	schema, err := NewSchema(tenant)
	if err != nil {
		return err
	}
	if _, err := pool.Exec(ctx,
		fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS %s`, pgx.Identifier{schema.String()}.Sanitize())); err != nil {
		return fmt.Errorf("create tenant schema %q: %w", tenant, err)
	}
	if _, err := Apply(ctx, pool, schema); err != nil {
		return fmt.Errorf("migrate tenant %q: %w", tenant, err)
	}
	return nil
}

// ProvisionScope creates a project's chunk partition and its ANN index, inside a tenant's schema.
//
// Called once when a project is created. Until it runs, writes for that project land in the default
// partition — visible, erasable, and slow to search, which is the right failure: losing a customer's
// memory because provisioning missed a step is worse than an unindexed project.
//
// IF NOT EXISTS throughout, so re-provisioning is a no-op. This is the kind of step an operator
// retries when unsure whether it ran.
func ProvisionScope(ctx context.Context, pool *pgxpool.Pool, tenant, scope string) error {
	schema, err := NewSchema(tenant)
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
	// An index on the partitioned parent would be built across every project in the tenant, which is
	// the unreachable-behind-a-scope-predicate shape memra's 0021 dropped. Per partition, the planner
	// prunes to one project and then walks a graph holding only that project's vectors — no
	// post-filtering, no `ef_search` to tune.
	for _, statement := range []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s PARTITION OF %s.chunk FOR VALUES IN ('%s')`,
			partition, schema, scope),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s ON %s USING hnsw (embedding vector_cosine_ops)`,
			index, partition),
	} {
		if _, err := pool.Exec(ctx, statement); err != nil {
			return fmt.Errorf("provision project %q in tenant %q: %w", scope, tenant, err)
		}
	}
	return nil
}
