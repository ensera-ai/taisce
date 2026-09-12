// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ensera-ai/taisce/internal/infra/pg"
)

// ── A rebuild says what it left behind ────────────────────────────────────────────────────────
//
// A rebuild publishes a new interpretation and finishes. From the operator's side that looks
// complete, and it is not: the entities the old interpretation made are still there, the embedding
// generations were built over content that has changed, and the reports were deleted by their
// invalidation trigger and come back only when the subject pass reaches them.
//
// This does not repair any of that — re-deriving those projections is separate work. It says
// so, which is the difference between an operator who knows what is owed and one who does not.
func TestARebuildSaysWhatItLeftBehindAndSaysNothingWhenItLeftNothing(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "stale_report")
	jobs := pg.NewRebuildJobStore(pool)

	// A project with one formed turn and nothing else: no rebuild has happened, no embedding
	// generation has ever been built, and there is nothing to report.
	formed(t, pool, schema, subjectTurn("subject-1", "Marta works at Ensera."))
	quiet, err := jobs.Staleness(ctx, schema, "p1")
	if err != nil {
		t.Fatalf("read staleness: %v", err)
	}
	if quiet.Anything() {
		t.Fatalf("a project nobody rebuilt reported staleness: %+v", quiet)
	}
	// A surface with no generation is not stale. Saying so would send an operator looking for a
	// problem they do not have.
	if quiet.MessageEmbeddings.Active || quiet.MessageEmbeddings.Behind != 0 {
		t.Fatalf("a surface with no generation was called stale: %+v", quiet.MessageEmbeddings)
	}

	// Now the state a rebuild leaves: an entity the current facts no longer refer to, with its alias
	// receipt, and a community whose report is gone.
	var entity string
	if err := pool.QueryRow(ctx, schema.SQL(
		`SELECT entity_id::text FROM {schema}.entity WHERE scope='p1' AND normalized_name='ensera'`)).Scan(&entity); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(
		`DELETE FROM {schema}.fact WHERE scope='p1' AND (subject_entity_id=$1::uuid OR object_entity_id=$1::uuid)`), entity); err != nil {
		t.Fatal(err)
	}

	after, err := jobs.Staleness(ctx, schema, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if after.Entities == 0 {
		t.Fatal("an entity no current fact refers to was not reported")
	}
	if !after.Anything() {
		t.Fatalf("staleness was not reported at all: %+v", after)
	}

	// And the coverage arithmetic: a generation that covers less than the log is behind by the
	// difference, and one that covers all of it is not behind at all.
	var current int64
	if err := pool.QueryRow(ctx, schema.SQL(
		`SELECT coalesce(max(log_offset),0) FROM {schema}.observation WHERE scope='p1'`)).Scan(&current); err != nil {
		t.Fatal(err)
	}
	activateEmbeddingGeneration(t, pool, schema, current-1)
	behind, err := jobs.Staleness(ctx, schema, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if !behind.MessageEmbeddings.Active || behind.MessageEmbeddings.Behind != 1 {
		t.Fatalf("a generation one turn behind the log was not reported as such: %+v", behind.MessageEmbeddings)
	}

	// A second generation covering the whole log, activated in place of the first. A generation's
	// identity is immutable by trigger, so catching up is a new one and a switch — which is the
	// mechanism this is reporting on.
	activateEmbeddingGeneration(t, pool, schema, current)
	caught, err := jobs.Staleness(ctx, schema, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if caught.MessageEmbeddings.Behind != 0 {
		t.Fatalf("a generation covering the whole log was called behind: %+v", caught.MessageEmbeddings)
	}

	// A subject whose report the invalidation trigger removed. This is the window in which a recall
	// that would return a theme returns none, and it is the one an operator most needs told.
	if _, err := pool.Exec(ctx, schema.SQL(
		`INSERT INTO {schema}.community (scope, community_id, level) VALUES ('p1', gen_random_uuid(), 0)`)); err != nil {
		t.Fatal(err)
	}
	waiting, err := jobs.Staleness(ctx, schema, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if waiting.Communities != 1 {
		t.Fatalf("a subject waiting for a report was not reported: %+v", waiting)
	}

	// Entity and report embeddings keep no offset; the database marks their build stale when the set
	// they cover changes, and that verdict is what this reports for them.
	markEmbeddingBuildStale(t, pool, schema, "entity_embedding")
	verdict, err := jobs.Staleness(ctx, schema, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if !verdict.EntityEmbeddings.Active || !verdict.EntityEmbeddings.Stale {
		t.Fatalf("the database's own stale verdict was not reported: %+v", verdict.EntityEmbeddings)
	}
	if verdict.EntityEmbeddings.Behind != 0 {
		t.Fatal("an offset was invented for a surface that keeps none")
	}
	if !verdict.EntityEmbeddings.Behindhand() {
		t.Fatal("a stale generation was not counted as needing a new one")
	}
}

// markEmbeddingBuildStale activates a generation for a surface that keeps no offset and marks its
// build stale, which is the state the database itself writes when what the generation covers changes.
func markEmbeddingBuildStale(t *testing.T, pool *pgxpool.Pool, schema pg.Schema, surface string) {
	t.Helper()
	ctx := context.Background()
	var generation string
	if err := pool.QueryRow(ctx, schema.SQL(`
		INSERT INTO {schema}.`+surface+`_generation
		       (generation_id, scope, operation_key, model_name, model_revision, endpoint_hash,
		        identity_hash, dimensions)
		VALUES (gen_random_uuid(), 'p1', gen_random_uuid(), 'stale-fixture', 'v1', $1, $2, 3)
		RETURNING generation_id::text`),
		strings.Repeat("c", 64), strings.Repeat("d", 64)).Scan(&generation); err != nil {
		t.Fatalf("build a %s generation: %v", surface, err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`
		INSERT INTO {schema}.`+surface+`_build (scope, generation_id, state) VALUES ('p1', $1::uuid, 'stale')`),
		generation); err != nil {
		t.Fatalf("mark the %s build stale: %v", surface, err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`
		INSERT INTO {schema}.`+surface+`_active (scope, generation_id) VALUES ('p1', $1::uuid)
		ON CONFLICT (scope) DO UPDATE SET generation_id = EXCLUDED.generation_id`), generation); err != nil {
		t.Fatalf("activate the %s generation: %v", surface, err)
	}
}

// activateEmbeddingGeneration makes a project's message embeddings active at an offset, which is the
// state an activated generation leaves behind and the state a later rebuild invalidates.
func activateEmbeddingGeneration(t *testing.T, pool *pgxpool.Pool, schema pg.Schema, through int64) {
	t.Helper()
	ctx := context.Background()
	var generation string
	if err := pool.QueryRow(ctx, schema.SQL(`
		INSERT INTO {schema}.embedding_generation
		       (generation_id, scope, operation_key, model_name, model_revision, endpoint_hash,
		        identity_hash, dimensions, through_offset)
		VALUES (gen_random_uuid(), 'p1', gen_random_uuid(), 'stale-fixture', 'v1', $1, $2, 3, $3)
		RETURNING generation_id::text`),
		strings.Repeat("a", 64), strings.Repeat("b", 64), through).Scan(&generation); err != nil {
		t.Fatalf("build a generation: %v", err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`
		INSERT INTO {schema}.embedding_active (scope, generation_id) VALUES ('p1', $1::uuid)
		ON CONFLICT (scope) DO UPDATE SET generation_id = EXCLUDED.generation_id`), generation); err != nil {
		t.Fatalf("activate the generation: %v", err)
	}
}

// When a table it counts is gone, the staleness read fails rather than reporting zero. A count that
// silently becomes zero because a query could not run is the worst possible answer here: it tells an
// operator that a rebuild left nothing behind, which is exactly what they wanted to hear.
func TestAStalenessReadThatCannotCountRefusesRatherThanReportingNone(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "stale_broken")
	jobs := pg.NewRebuildJobStore(pool)

	// Every table the read counts: the entities, their receipts, the communities, the log itself,
	// and each embedding surface's active generation and build state.
	for _, table := range []string{
		"entity_name_receipt", "community", "observation",
		"embedding_active", "entity_embedding_build", "report_embedding_build",
	} {
		if _, err := pool.Exec(ctx, schema.SQL(
			`ALTER TABLE {schema}.`+table+` RENAME TO `+table+`_moved`)); err != nil {
			t.Fatal(err)
		}
		if _, err := jobs.Staleness(ctx, schema, "p1"); err == nil {
			t.Fatalf("staleness reported a number with %s gone", table)
		}
		if _, err := pool.Exec(ctx, schema.SQL(
			`ALTER TABLE {schema}.`+table+`_moved RENAME TO `+table)); err != nil {
			t.Fatal(err)
		}
	}

	// With everything in place it answers, and answers nothing.
	quiet, err := jobs.Staleness(ctx, schema, "p1")
	if err != nil || quiet.Anything() {
		t.Fatalf("an untouched project reported staleness: %+v %v", quiet, err)
	}
}
