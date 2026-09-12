<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Indexing and query plans

A recall's cost is meant to grow with the neighbourhood of what a question names, not with how much a
project has stored. This page shows which indexes serve which questions, how the SQL is written so the
planner can use them, what the plan tests assert, and what has not been measured yet.

**You'll learn:**

- the fixed bounds every recall runs under;
- how `chunk` and the vector tables are partitioned, and why;
- the indexes behind graph traversal and exact anchoring;
- how vector indexes and exact versus approximate search work;
- the rules the SQL templates follow to keep plans bounded;
- which plan tests exist, what they prove, and what is still open.

It names indexes rather than listing them all; the generated schema reference in the site's Reference
section lists every one. Read [the data model](data-model.md) first for the tables, and
[the read path](../architecture/read-path.md) for how a recall is composed.

## The shape of a bounded read

A recall does four things:

1. Resolves the terms in the question to entities.
2. Expands those entities' neighbourhood one or two hops through `fact`.
3. Reads each fact's evidence, cutting the surrounding text out of the message inside the database.
4. Optionally consults the semantic surfaces.

Each step has a bound that holds whatever the corpus size:

| Bound | Value | Where |
|---|---|---|
| Question size | 8,192 bytes | `MaxRecallQuestionBytes`, [recalllimits.go](../../internal/domain/recalllimits.go) |
| Candidate terms | 512 | `MaxRecallTerms` |
| Anchor matches | 256; a 257th refuses the whole recall | `MaxRecallMatches` |
| Hops | 2 | `MaxHops`, [recall.go](../../internal/recall/recall.go) |
| Relations followed per entity, per direction, per hop | 64 | `Fanout`, [domain.go](../../internal/domain/domain.go) |
| Context either side of a quote | 160 bytes | `ContextWindow` |
| Passage search results | 100, from at most 1,000 approximate candidates | [embeddingsearch.go](../../internal/infra/pg/embeddingsearch.go) |

These are admission ceilings. They are not measured optima or relevance thresholds, and each was chosen
before a workload existed to tune it.

Every read also carries the authorized project set as a parameter, `scope = ANY($1)`, and an empty set
returns nothing. Project isolation is a predicate on every statement, not row-level security, because
one database login serves every project. That is also why `scope` leads almost every index in the
schema.

## Partitioning

Two partitioning schemes exist, for different reasons.

### `chunk` is list-partitioned by project

`ProvisionScope` in [provision.go](../../internal/migrate/provision.go) creates one partition per
project, in the same transaction as the project row and under a per-project advisory lock.

- The partition is named `chunk_<project>`. For a project name longer than 49 bytes it is `chunk_` plus
  the base32 SHA-256 of the name, so PostgreSQL's truncation of long identifiers cannot merge two
  projects.
- The default partition `chunk_unpartitioned` catches writes for an unprovisioned project.
- `TestLongProjectNamesWithTheSamePrefixHaveDistinctStorageAndPrune` in
  [provision_test.go](../../internal/migrate/provision_test.go) asserts that a project-filtered read
  plans only that project's partition.

`chunk` is the only table partitioned by project. Every other table, including `observation`, `fact`
and `entity`, is a single table with `scope` as the leading index column.

`chunk` has no vector column ([0046](../../internal/migrate/sql/0046_message_embeddings_belong_to_model_generations.sql)).
Its partitioning is kept for locality of source lookup, erasure and maintenance. That locality benefit
has not been measured.

### The vector tables are list-partitioned by embedding generation

`message_embedding`, `entity_embedding` and `report_embedding` each get one child table per generation.
The generation's `Start` transaction creates the child and one HNSW index on it
([embeddinggeneration.go](../../internal/infra/pg/embeddinggeneration.go),
[entityembedding.go](../../internal/infra/pg/entityembedding.go),
[reportembedding.go](../../internal/infra/pg/reportembedding.go)).

- The parent owns no HNSW index, because a partitioned parent's index is only a template for its
  children.
- The project is the logical boundary of every vector query; the generation is its physical boundary.
- Retiring a model means detaching and dropping one child, not a filtered delete followed by a vacuum.
- `TestGenerationIndexIsDirectlyOwnedByItsChildPartition` in
  [embeddingpartition_test.go](../../internal/infra/pg/embeddingpartition_test.go) asserts the catalog
  ownership.

## The traversal

The fact indexes that serve traversal:

| Index | Definition | Serves |
|---|---|---|
| `fact_subject_idx` | GiST `(scope, subject_entity_id, valid)` where the subject is not null | the forward arm, and valid-time reads |
| `fact_object_idx` | GiST `(scope, object_entity_id, valid)` where the object is not null | the backward arm |
| `fact_reconcile_idx` | B-tree `(scope, subject_entity_id, predicate)` where `upper_inf(valid)` | the supersession lookup on every assertion, and current forward reads |
| `fact_principal_idx` | B-tree `(scope, subject_entity_id)` where `source_role = 'user'` | the default source filter |
| `fact_bitemporal_idx` | GiST `(scope, valid, known)` | as-of questions over both time axes |
| `fact_history_fact_id_known_excl` | GiST `(fact_id, known)`, the exclusion constraint's index | per-fact lookup of archived intervals |

The GiST indexes that combine `scope`, an entity id and a `tstzrange` depend on `btree_gist`. That
extension supplies GiST operator classes for `text` and `uuid` equality, so one index can combine an
equality on the entity with range operators on time. The same extension lets
`fact_single_cardinality_excl` mix `=` and `&&`.

The partial predicates keep the hot indexes small. `fact_reconcile_idx` holds only currently valid
facts, so it does not grow with every superseded version
([0001](../../internal/migrate/sql/0001_the_spine.sql)).

The traversal is one recursive statement, `factsAboutTemplate` in
[recallstore.go](../../internal/infra/pg/recallstore.go). Each hop is a lateral join of two arms, one
per direction:

```sql
CROSS JOIN LATERAL (
    SELECT * FROM (
        SELECT ... FROM {schema}.fact f
         WHERE f.scope = ANY($1) AND f.subject_entity_id = r.entity_id
           AND f.source_role = ANY($4) AND upper_inf(f.valid) AND upper_inf(f.known)
         ORDER BY f.fact_id LIMIT $7          -- the fanout cap, per direction
    ) forward
    UNION ALL
    SELECT * FROM (
        SELECT ... FROM {schema}.fact f
         WHERE f.scope = ANY($1) AND f.object_entity_id = r.entity_id
           AND f.source_role = ANY($4) AND upper_inf(f.valid) AND upper_inf(f.known)
         ORDER BY f.fact_id LIMIT $7
    ) backward
) step
```

Three choices in this statement exist for the planner's sake:

- **Two arms instead of an `OR`.** `subject = x OR object = x` across two columns cannot use either
  index, and the planner falls back to scanning every fact in the project once per level.
- **One statement per temporal shape.** The current, valid-time, knowledge-time and bitemporal reads
  are four statements that differ in one predicate. A single statement with
  `($t IS NULL AND upper_inf(valid)) OR valid @> $t` would be unindexable in both branches, because the
  planner cannot know which branch it is planning for. Only the knowledge-time reads join
  `fact_history`, so the common current read never pays for history.
- **The current read asks `upper_inf`, not `@> now()`.** That is the question actually being asked
  ("is this still the answer"). It does not hide a fact whose start is slightly ahead of the server's
  clock. And it is exactly the predicate `fact_reconcile_idx` is partial on.

Every bound is written once and applies at every level: the project set, the source-role filter, the
subject attribution filter and the temporal predicate. A fact is returned once, at the shortest route
that reached it.

The context window is cut with byte arithmetic on `convert_to(m.content, 'UTF8')` in the database, so a
hundred-kilobyte message does not cross the wire for three hundred bytes of it.

### What is not yet known about the traversal

The traversal is the one hot-path read with no plan test (see [The plan tests](#the-plan-tests)). The
following is inferred from the SQL and the index definitions, and has not been measured:

- **The fanout cap bounds rows returned, not rows read.** No index orders one entity's facts by
  `fact_id`, so at a hub entity each arm must read every current fact of that entity and sort them to
  keep 64.
- **The project filter may not be an index condition.** GiST does not accept `= ANY(array)` as an index
  condition the way a B-tree does. Without a captured plan, it is uncertain whether the planner uses
  the project as an index condition or only as a filter.

## Exact anchors

`selectAnchorsSQL` in [recallstore.go](../../internal/infra/pg/recallstore.go) joins the question's
distinct terms to `entity` on `(scope = ANY($1), identity_kind = 'named', normalized_name = term)`.

- That join is served by the partial unique index `entity_named_identity_uniq`.
- A second arm adds the selected speaker through `entity_speaker_identity_uniq`.
- When a data subject is named, the subject filter is an `EXISTS` against `projection_dependency`,
  served by `projection_dependency_kind_idx` `(scope, projection_kind, projection_id)`.
- The statement stops at 257 rows (`LIMIT $4`) and sorts only those. The Go caller refuses the recall
  if a 257th row arrives, so the traversal never starts from an unbounded seed set.

There is no separate alias lookup. Every spelling a source used is kept in `entity_name_receipt`, and a
trigger requires it to normalize to its entity's `normalized_name`
([0043](../../internal/migrate/sql/0043_entity_name_variants_have_source_owners.sql),
[0051](../../internal/migrate/sql/0051_canonical_names_are_the_exact_anchor_key.sql)). So the canonical
key already matches every recorded spelling, and one B-tree probe per term is the whole lookup.

[28-anchor-query-plans.md](../28-anchor-query-plans.md) records the plan comparison that led to separate
indexable lookups rather than one combined query. Its alias-lookup rows describe the schema before
0051, when aliases still had their own GIN index.

## Vector indexes

A generation's dimension comes from the operator's model, anywhere from 1 to 4,000, and the index
expression depends on it. `embeddingIndexExpression` in
[embeddinggeneration.go](../../internal/infra/pg/embeddinggeneration.go) produces:

```sql
-- up to 2,000 dimensions
CREATE INDEX ... USING hnsw ((embedding::vector(1536)) vector_cosine_ops);
-- above 2,000 dimensions
CREATE INDEX ... USING hnsw ((l2_normalize(embedding)::halfvec(2560)) halfvec_cosine_ops);
```

1536 and 2560 are example dimensions. The switch at 2,000 follows pgvector's documented HNSW limits:
2,000 dimensions for `vector` and 4,000 for `halfvec`. That is also why a generation's dimension is
capped at 4,000.

The column itself is untyped `vector`, and the dimension cast lives in the index expression. A query
must use the identical expression for the index to apply, so the search code builds both from the same
function.

Search has two shapes, both in [embeddingsearch.go](../../internal/infra/pg/embeddingsearch.go):

- **Exact, the default.** The generation's rows go into a `MATERIALIZED` common table expression first,
  and are then ordered by `<=>`. Materializing stops the planner from substituting the approximate
  index, so a result labelled exact really is exact.
- **Approximate, opt-in.** The candidate CTE is ordered by the index expression with
  `LIMIT candidates`, which lets the planner use the pruned child's HNSW index. The candidates are then
  re-ranked at full precision, and any vector whose `input_digest` no longer matches the message's
  current text is dropped. `TestTheVectorSearchPrunesToOnePartitionAndUsesItsIndex` in
  [embeddingsearch_test.go](../../internal/infra/pg/embeddingsearch_test.go) asserts that the plan names
  the active generation's `_ann` index and no other generation's table.

Cosine similarity is only meaningful for finite, non-zero vectors. The table refuses a zero norm, and
the adapter refuses non-finite components before a vector can be stored.

### Layout qualification

The layout was qualified in [27-vector-layout-qualification.md](../27-vector-layout-qualification.md).
That run compared generation partitions against a shared table and a project-hash layout, on a
disposable eight-A100 GPU virtual machine running PostgreSQL under Compose on the same machine. Read
its numbers there, with its stated limits.

It is one controlled distribution on one host. It sets no production ceiling and no universal candidate
bound. Exact search remains the default because that run did not justify making approximate search the
default.

## How the SQL templates are shaped

The SQL lives as Go string constants in [internal/infra/pg](../../internal/infra/pg/). A handful of
rules recur, each so that a plan stays bounded:

| Rule | Why | Example |
|---|---|---|
| The namespace is interpolated from a validated `Schema`; everything else is a bind parameter | An identifier cannot be a parameter, and validating at construction makes forgetting a compile error | `schema.SQL(...)` in every store |
| The authorized project set is an array parameter on every read | Isolation is a predicate the statement cannot omit, and an empty set returns nothing | `scope = ANY($1)` |
| No `OR` across two indexed columns | An `OR` defeats both indexes | the traversal's two arms |
| One statement per temporal shape | An `OR` on the temporal predicate is unindexable in both branches | `selectFactsAboutSQL` and its three siblings |
| Partial-index predicates are written verbatim | The planner uses a partial index only when the query implies its predicate | `upper_inf(valid)`; `formed_at IS NULL AND parked_at IS NULL` in `selectNextUnformedSQL` |
| Keyset pagination with a tuple cursor in index order, never `OFFSET` | An offset reads and discards everything before the page | record, history, feedback and parked-turn pages |
| `LIMIT n+1` to detect overflow | Refusing needs to know there was more, without reading all of it | anchors (257), generation claims (101), name variants (65) |
| `MATERIALIZED` where the plan shape is part of the meaning | Stops the planner inlining or reordering | the exact vector search; the supersession lookup |
| Byte windows are cut in SQL | Returns the window, not the message | `substring(convert_to(m.content,'UTF8') ...)` |
| Background work claims bounded pages with `FOR UPDATE SKIP LOCKED` | A second worker takes the next page instead of waiting | retention sweeps, notification deliveries |

## The plan tests

The plan tests run `EXPLAIN` against a real PostgreSQL, over fixtures large enough that the planner's
choice is real. Each asserts the shape of the plan: which index is used, and that no whole relation is
scanned. None asserts a timing.

| Test | File | Fixture | Asserts |
|---|---|---|---|
| `TestAnchorQueryPlans` | [anchorplans_test.go](../../internal/infra/pg/anchorplans_test.go) | two projects, 10,000 entities each, analyzed | the production anchor query uses `entity_named_identity_uniq` and never scans `entity`, for a hit, a miss and a 128-term question; results come only from the authorized project |
| `TestRecordPagesSeekIndexedPositionsAndHistoryPagesDoNotRepeat` | [recordplans_test.go](../../internal/infra/pg/recordplans_test.go) | 10,000 facts, 2,000 archived versions of one fact | project pages seek `fact_scope_id_uniq`; subject pages seek `dependency_subject_fact_page_idx`; history pages seek `fact_history_page_idx`; no scan of `fact`, `fact_history` or `projection_dependency`; paging returns every version once, in order |
| `TestFeedbackPagesSeekIndexesRatherThanScanningTheQueue` | [feedbackplans_test.go](../../internal/infra/pg/feedbackplans_test.go) | 10,000 feedback rows, one in twenty open, spread over 500 records | the open queue seeks `memory_feedback_open_idx`; one record's feedback seeks `memory_feedback_target_idx`; no scan of `memory_feedback`; paging terminates |

Other tests make the same kind of assertion for a single read:

| Test | File | Asserts |
|---|---|---|
| `TestHistoricalLookupUsesFactRangeIndex` | [facthistory_test.go](../../internal/infra/pg/facthistory_test.go) | a per-fact knowledge-time lookup uses an index and no sequential scan |
| `TestEntityInventoryUsesProjectIdentityIndex` | [entityinspection_test.go](../../internal/infra/pg/entityinspection_test.go) | entity inventory uses `entity_scope_id_uniq` with no sort |
| `TestMessageReferenceSurvivesProjectionLossAndHasAnUnambiguousIndex` | [messagewindow_test.go](../../internal/infra/pg/messagewindow_test.go) | a message reference is eligible for `message_chunk_lookup_unique` |
| `TestParkedPagesAreBoundedProjectScopedAndNeverContainProviderText` | [formationrecovery_test.go](../../internal/infra/pg/formationrecovery_test.go) | parked-turn pages use `observation_parked_idx` with no sort |
| `TestTheVectorSearchPrunesToOnePartitionAndUsesItsIndex` | [embeddingsearch_test.go](../../internal/infra/pg/embeddingsearch_test.go) | approximate search prunes to one generation and uses its HNSW index |
| `TestLongProjectNamesWithTheSamePrefixHaveDistinctStorageAndPrune` | [provision_test.go](../../internal/migrate/provision_test.go) | a project-filtered chunk read plans one partition |

These run on whatever machine runs the suite, usually a development laptop. They prove that the index
is eligible and chosen at the fixture's size. They do not prove latency, and
[28-anchor-query-plans.md](../28-anchor-query-plans.md) says the same of its own results.

No plan test covers:

- the traversal (`selectFactsAboutSQL` and its temporal siblings);
- the entity-candidate and report-candidate vector searches;
- erasure's per-kind deletes, which are built at runtime from `projection_kind` and reach their targets
  through the typed `*_ref` indexes on `projection_dependency`.

Two qualification tests, `TestAnchorLayoutOperatingEnvelope` and `TestVectorLayoutOperatingEnvelope`,
sit behind the `qualification` build tag. They run on qualification hardware, not in the ordinary
suite.

## pg_stat_statements and the profile snapshot

`pg_stat_statements` is preloaded in the PostgreSQL image, and the Helm chart's cluster declares it too
([deploy/postgres/Dockerfile](../../deploy/postgres/Dockerfile),
[00-extensions.sql](../../deploy/postgres/initdb/00-extensions.sql)). It is one of three extensions the
image carries. It is there because it answers "which statement got slower" after a deploy without
adding instrumentation to the server.

[scripts/postgres-profile.sql](../../scripts/postgres-profile.sql) is a bounded snapshot that contains
no row data. It reports:

- the performance-relevant settings and database-level counters;
- connections by state, checkpointer activity and `pg_stat_io`;
- the twenty largest relations, with their dead-tuple counts;
- the twenty statements with the most total execution time;
- any index build in progress.

Statements are identified by `queryid` only. The query text is left out, because utility statements can
keep literal values, credentials among them.

`make perf-reset-stats`, `make perf-workload` and `make perf-stats` wrap the snapshot for the isolated
qualification stack. [21-production-postgresql.md](../21-production-postgresql.md) describes the
workflow and the gates a settings change must pass. `perf-workload` is the database test suite, a
repeatable regression workload. It does not simulate production traffic.

## The first bottleneck

**On the read path**, the expected first bottleneck is a hub entity, not corpus size. An anchor with
ten thousand facts reaches ten thousand entities, and expanding each of them is what would take a read
path down. The fanout cap exists to bound that.

On the SQL as written, though, the cap bounds rows returned and not rows read (see
[What is not yet known about the traversal](#what-is-not-yet-known-about-the-traversal)). A hub's degree
therefore still reaches the database, one sort per arm per visited entity. That is an inference, not a
measurement.

**On the write path**, there is one intentional serialization point: the single-row `ingestion_budget`
counter, updated at every append's commit. Appends to one project also serialize on that project's
watermark row. [Concurrency](concurrency.md) covers both.

**None of this has been measured at size.** A development machine running the model, the database and
the service together cannot produce performance evidence.

- The news corpus used so far reached a maximum entity degree of 58, under the cap of 64, so the case
  the cap exists for never occurred in it.
- [deploy/gpu/readpath-scale.py](../../deploy/gpu/readpath-scale.py) builds that case deliberately. It
  writes a synthetic graph with nodes under, at and past the cap, at 50,000, 250,000 and 1,000,000
  facts, and drives recall through the public route at several concurrencies. It runs as the last phase
  of the GPU qualification controller.
- No published page reports its results yet. Recall percentiles at size, and deployment-wide
  mixed-workload capacity, are therefore still open.

No latency target has been agreed either. When the measurement lands, it will be a measurement, not a
verdict.

## Where to go next

- [The data model](data-model.md): the tables these indexes belong to.
- [Concurrency](concurrency.md): the locks and constraints on the write side.
- [The read path](../architecture/read-path.md): how a recall composes these reads.
- [27-vector-layout-qualification.md](../27-vector-layout-qualification.md) and
  [21-production-postgresql.md](../21-production-postgresql.md): measured layout results and the
  settings workflow.
