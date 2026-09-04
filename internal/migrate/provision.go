package migrate

import (
	"context"
	"fmt"
	"regexp"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// scopePattern is what a scope may contain.
//
// A partition name is built from the scope and CANNOT be parameterised — DDL takes identifiers, not
// bind values — so this is the only thing standing between a scope and an injection. It is a strict
// allowlist rather than an escape routine, because an allowlist fails closed on anything it did not
// anticipate and an escape routine fails open.
var scopePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

// ProvisionScope creates a project's chunk partition and its ANN index.
//
// Called once when a project is created. Until it runs, writes for that scope land in the default
// partition — visible, erasable, and slow to search, which is the right failure mode: losing a
// customer's memory because provisioning missed a step is worse than an unindexed one.
//
// IF NOT EXISTS throughout, so re-provisioning is a no-op rather than an error. Provisioning is the
// kind of step that gets retried by an operator who is not sure whether it ran.
func ProvisionScope(ctx context.Context, pool *pgxpool.Pool, scope string) error {
	if !scopePattern.MatchString(scope) {
		return fmt.Errorf("scope %q is not a valid identifier: %s", scope, scopePattern)
	}
	partition := pgx.Identifier{"memory", "chunk_" + scope}.Sanitize()
	index := pgx.Identifier{"chunk_" + scope + "_ann_idx"}.Sanitize()

	// The partition, then the index INSIDE it.
	//
	// An index on the partitioned parent would be built across every project's vectors, which is
	// the global HNSW that memra's 0021 dropped for being unreachable behind a scope predicate.
	// Per partition, the planner prunes to one project and then walks a graph containing only that
	// project's vectors — so there is no post-filtering and no `ef_search` to tune.
	statements := []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s PARTITION OF memory.chunk FOR VALUES IN ('%s')`,
			partition, scope),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s ON %s USING hnsw (embedding vector_cosine_ops)`,
			index, partition),
	}
	for _, statement := range statements {
		if _, err := pool.Exec(ctx, statement); err != nil {
			return fmt.Errorf("provision scope %q: %w", scope, err)
		}
	}
	return nil
}
