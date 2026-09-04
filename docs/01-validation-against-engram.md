# Validated against Engram

**2026-09-04.** [`docs/00-goals.md`](00-goals.md) was written from measurement of memra and from
reading Microsoft GraphRAG. Before any of it was built, it was checked against
`zyndax-platform/kernel/src/platform/engram` — a working .NET memory core by the same author, on
PostgreSQL + Apache AGE + pgvector, with NATS JetStream as its bus.

Engram matters here because it is the **AGE arm of the experiment, already run in production**. The
substrate question in [`memra/docs/24`](https://github.com/althunibat/memra/blob/main/docs/24-graph-substrate-evaluation.md)
was answered there by building and benchmarking AGE in a scratch container. Engram answers it with
operational history, which is better evidence, and it changes two conclusions.

---

## 1. Engram reached this design from the other direction

Its migration `M202607260001_MemorySchemaAndFactProjection.sql` — the fix, in its own words:

> **THE PROBLEM.** The semantic lens ran this against the AGE graph on every recall:
> `MATCH (s:Entity)-[f:Fact]->(o:Entity) WHERE f.scope IN [...] AND f.valid_at <= t AND (f.invalid_at = 0 OR f.invalid_at > t)`
>
> Every predicate is on an EDGE PROPERTY. **AGE stores edge properties in a single `agtype` column
> and cannot index inside it**, so this is a full scan of every Fact edge in the database — all
> scopes, all history, valid and invalidated alike — **on the hottest read path there is.**
>
> **THE FIX.** Project the bi-temporal fact rows into **a real table with real indexes**, and keep
> the graph for what a graph is actually for — multi-hop traversal.

That is `memory.fact_current`: a plain PostgreSQL table with `scope`, `subject`, `predicate`,
`object`, the bitemporal columns, and four indexes — including a partial one on the live rows and
one on `(scope, subject, predicate)` for reconciliation.

**Taisce's `memory.fact` is that table.** The same conclusion, reached by measurement rather than by
incident, and reached before the graph engine was adopted rather than after.

The corollary is the substrate decision. Engram now maintains **both** — the AGE graph and the
relational projection, written in one transaction so they cannot drift. That is the second copy
[`memra/docs/24`](https://github.com/althunibat/memra/blob/main/docs/24-graph-substrate-evaluation.md)
raised as a cost and then withdrew for lacking evidence. The evidence exists; it is a maintained
duplicate on the write path, and it is the price of keeping AGE once the read path has moved off it.

## 2. AGE with a closed label set works, and that confirms rather than refutes the objection

`M202606200002_ApacheAgeGraph.sql` creates exactly **one vertex label and one edge label**:

```sql
SELECT create_vlabel('engram', 'Entity');   -- subjects/objects
SELECT create_elabel('engram', 'Fact');     -- bi-temporal fact edges
```

The predicate is a property on the `Fact` edge, not a label. So Engram never met the table-per-label
explosion — it took the workaround docs/24 named:

> "collapsing every predicate to one label with the name in `properties` — at which point the graph
> is one edge table with a text discriminator, which is `fact_current`."

Which is precisely what Engram then had to build, for precisely the reason predicted: once the
predicate is a property, it cannot be indexed, and the read path has to move to a real table.

**The objection is upheld and sharpened.** AGE is usable with a closed label set. What it is not is
*load-bearing* — with one vertex label and one edge label it contributes a query language over data
that has to be duplicated relationally to be queryable at all.

## 3. What AGE cost Engram operationally, from its own migrations

Recorded because none of it appears in a benchmark, and all of it is real:

- **`shared_preload_libraries`, not `LOAD`.** "A non-superuser tenant role cannot `LOAD 'age'`
  (bare-name LOAD is superuser-only)." The CNPG image's `$libdir/plugins` symlink was superseded by
  preloading — an extension in every backend process, cluster-wide.
- **A `search_path` bug that lost a table.** `create_graph('engram')` creates a *schema* named
  `engram`; the migrator connects as role `engram`; so `"$user"` resolved to the brand-new graph
  schema and captured every unqualified `CREATE TABLE` after it. `workspace` landed inside the
  graph's schema, the repair sweep did not cover it, and Observe failed with `42P01` at runtime.
  The migration's own note: *"Invisible on any database migrated before the graph existed; it only
  bites on a from-empty run, which is exactly what a demo rebuild does."*
- **Table ownership repair.** A table created outside the migrator's connection is owned by
  `postgres`, and the runtime role then gets `42501` on its first insert — originally patched by a
  post-deploy superuser Job, later replaced by a deploy-time assertion.
- **DbUp variable interpolation** had to be disabled globally so `$user` in the AGE migration was
  not mangled.

## 4. Two gaps this validation found in Taisce, both now fixed

Engram had them and migration `0001_the_spine.sql` did not.

**The supersession lookup had no index.** Engram's `fact_current_reconcile_idx ON (scope, subject,
predicate) WHERE invalid_at = 0` serves the question every append asks first: *is there a current
fact this one contradicts?* Taisce's traversal indexes lead with the entity and end with the
validity range, so finding one predicate among a subject's facts meant reading all of them — on the
hottest write path, once per extracted triple. Added as `fact_reconcile_idx`, partial on
`upper_inf(valid)`, and it is also what M3's exclusion constraint will rest on.

**Freshness and erasure had no tables.** Engram has `watermark` (per-scope contract) and `tombstone`
(erasure registry with `requested_at` / `completed_at`). Added as `memory.watermark` and
`memory.erasure_request` — the latter with a `residual` jsonb, because the residual count per
projection kind is the artefact the product is sold on and it should be a column, not a log line.

`confidence` was added to `memory.fact` for the same reason Engram separates it: salience is
*computed* and confidence is *reported*, and collapsing them makes a weakly-extracted fact that is
mentioned often indistinguishable from a certain one.

## 5. Where Taisce deliberately diverges, with Engram's experience as the reason

| | Engram | Taisce | Why |
|---|---|---|---|
| Bitemporal encoding | `bigint`, `0` = open | `tstzrange` + GiST | Engram's own comment says the sentinel exists so the column is *"character-for-character the graph's predicate"* — a constraint imposed **by AGE**. Without AGE it buys nothing, and ranges give containment operators and one index |
| Vector index | one global `ix_embedding_ann` HNSW | partitioned by scope, HNSW per partition | This is the shape memra's migration 0021 dropped after measuring it unreachable behind a scope predicate. **Prediction: Engram will find the same** — the planner takes `ix_embedding_scope_kind` and sorts exactly, and the ANN index is maintained on every write and read never |
| Entity identity | AGE vertex `key` property | `(scope, normalized_name)`, type as attribute | memra keyed on `(scope, entity_type, normalized_name)` and silently split one entity across two type readings |
| Bus | NATS JetStream | none | Engram's own topology comments record the cost — `AckWait` shorter than an LLM extraction produced duplicate episodes and facts *"with no failure involved"*. One substrate, no broker |
| Erasure index | `observation_ids` jsonb + GIN | `projection_dependency` rows | memra's migration 0045 dropped the equivalent GIN as maintained-and-never-read; a dependency row is countable per projection kind, which is what G6 needs |

---

## 6. The net effect on the goals

Nothing in [`00-goals.md`](00-goals.md) changed. Three things hardened:

- **G3/G4** — the fact table as the primary edge store is no longer a design argument; it is where a
  production system on AGE ended up.
- **Out of scope: a graph database** — was decided on a benchmark, and now has operational history:
  the read path had to leave AGE, and what remains is a duplicate maintained on every write.
- **G6** — erasure gets a request registry with the residual as a column, taken from Engram's
  `tombstone` and extended by what memra's residual count proved worth recording.
