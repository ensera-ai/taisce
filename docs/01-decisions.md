# Decision register

Every decision, closed. **Nothing in this document is an open question.** If another document appears
to leave a choice, that is a defect in that document — this register is the authority.

Each entry says what was decided, why, and **what was rejected**. The rejections are the point: a
decision recorded without its alternatives gets reopened by whoever has only heard the winning
argument.

Entries are added as decisions are made. An entry is written when a `decision`-labelled issue closes,
or when building something forces a choice that a later reader would otherwise have to re-derive.

---

## Substrate

### D1 — PostgreSQL is the only required dependency

Not a vector database, graph engine, message broker, cache or object store.

Each addition is a second place data lives, a second thing to operate, and — decisively — **a second
sweep an erasure has to prove it covered**. G6 rests on deleting through a registered dependency and
counting against the same predicate in the same transaction. That count means "nothing survives"
only while there is one place for something to survive in.

**Rejected:** a dedicated vector store (the vector is a column on the row it describes, and
separating them means two erasure paths and a consistency problem between them); a graph engine (the
fact table with resolved entity ends *is* the edge list, and a graph store is weakest at exactly the
temporal work D3 makes central); a broker (the log is already an ordered, durable, replayable
sequence).

### D2 — An extension ships only with a reader in this codebase

An extension runs inside the backend process, so a fault in one takes the cluster, and the cluster is
the product. The test is not *"might we want this"* but *"what reads it today"*.

Currently: `vector` (`chunk.embedding` and the per-partition HNSW), `btree_gist` (the traversal
indexes and D4's constraint), `pg_stat_statements` (the only way to answer "which query got slower"
after a deploy without adding an instrumentation path to the server).

**Rejected:** a lexical/BM25 extension — no lexical index exists in this schema and none is designed,
since retrieval anchors on entities and reads passages as evidence (G1), and all three embedded
surfaces (G5b) are vector. It is also the most expensive thing the image could carry: a from-source
compile plus a `shared_preload_libraries` entry. If hybrid retrieval is wanted it returns **with the
query that reads it**. Also rejected: `pgcrypto`, because `gen_random_uuid()` has been core since
PostgreSQL 13 and that was its only use here.

### D3 — Time is a range, not a pair of columns with a sentinel

`valid` and `known` as `tstzrange`, GiST-indexed.

The alternative is scalar start/end columns where a sentinel means "open", which puts
`(end = 0 OR end > $1)` in every query and makes the end column a poor leading key in every index —
a representation problem surfacing as an indexing problem. A range is one column, one containment
operator, one index.

**Rejected:** sentinel-encoded bigints (above); `NULL` as open (correct, but a three-valued
comparison in every predicate is the same defect wearing a nicer hat).

### D4 — Supersession is a database constraint, not an application lock

A single-cardinality predicate cannot hold two overlapping valid intervals for one subject, enforced
by `EXCLUDE USING gist`.

A lock is correct only while every write path remembers to take it — including one written next year
by someone who has not read this. A constraint is correct because the database will not store the
violation.

**Rejected:** a fact-slot advisory lock (correct today, and one new write path from being wrong;
also the hottest contention point on the write path); optimistic retry on a unique index (a unique
index cannot express interval overlap, which is the actual invariant).

### D5 — Entity identity is `(scope, normalized_name)`. Type is an attribute

Putting the type in the identity key makes one thing extracted once as `place` and once as `thing`
into **two nodes holding two halves of its facts** — a silent under-merge on every hop, invisible
because both rows look correct on their own.

**Rejected:** `(scope, entity_type, normalized_name)`. A disagreement about type should be a
disagreement about one node, not the creation of a second.

### D6 — Resolution merges by normalised name only; meaning is a separate surface

Lowercase, trim, collapse whitespace. Nothing cleverer at this layer.

Merging "the Dublin office" with "our Dublin site" needs either an embedding comparison with a
threshold or a model call per entity at write time. **Over-merging puts two people's facts on one
node, which is a governance failure; under-merging costs recall on a hop.** That asymmetry decides
it: the conservative rule is the default, and meaning is added as the entity's embedded surface (G2)
where it can be measured and tuned against a held-out set rather than guessed.

**Rejected:** similarity-threshold merging at write time (a threshold nobody can justify, applied
irreversibly); a model call per entity (a write-time artefact with a confidence score attached).

### D7 — The vector index is per project partition, never one index over all of them

`chunk` is LIST-partitioned on scope, with an HNSW built inside each partition.

A single graph behind a scope predicate is unreachable in both directions. The planner prefers an
exact sort and never enters it; and post-filtering an approximate result loses recall in proportion
to how little of the corpus belongs to the caller. Pruning first and scanning inside one project's
graph gives an exact-correct result with no `ef_search` to tune.

A partition is DDL per project at a controlled moment — project creation — not something issued from
a request or a worker. Because a tenant is a schema (D8), the partition count per table is bounded by
projects-per-tenant.

**Rejected:** one HNSW on the partitioned parent (unreachable, and maintained on every insert for
nothing); no ANN index at all with an exact scan per scope (correct, and linear in project size,
which is the thing G1 exists to remove); iterative/relaxed approximate scanning (tuning a recall
knob to compensate for an index built over the wrong set).

### D8 — A tenant is a schema; a project is a column

**Hard and soft, and they are not the same kind of boundary.** A cross-tenant read is impossible
because the other tenant's name is not in the statement — not because a predicate excluded it. There
is no tenant column to forget. Cross-project reads are *intended*, since a principal granted several
projects gets one bundle across them, so scope is a column and the boundary is a permission checked
on every read.

A schema name cannot be a bind parameter, so it is interpolated — which is why it comes from a
validated type that exists only after construction. Forgetting to validate is a compile error rather
than an injection.

**Rejected:** a `tenant_id` column with row-level security as the primary control (one missing
predicate from a cross-customer leak, and the guarantee lives in policy rather than in the absence of
a name); a schema per project (a multi-project recall becomes a cross-schema union that cannot be
planned as one query); a database per tenant (connection pooling and migration both scale badly).

## Erasure

### D9 — Erasure walks a registered dependency, not a list of tables

Every projection registers what it derived and from which observation. Erasure walks that, so
`COUNT(*)` afterwards means *"nothing derived from that observation survives"* rather than *"nothing
survives in the tables somebody remembered to check"*.

A projection kind that does not register is invisible to erasure, so the registration is asserted by
a test that fails when a kind is written without one.

**Rejected:** a hand-maintained delete list (correct until the next projection is added); a
containment index over source ids on each projection (works, and puts the burden on every future
table to remember the same pattern).

### D10 — The residual count is a column, not a log line

`erasure_request.residual` holds the per-projection-kind count after the delete, in the same
transaction, with a constraint that it is present exactly when the request is complete.

This is the artefact the product is sold on. Something a customer may be asked to rely on should be
queryable and constrained, not recoverable by grepping.

**Rejected:** emitting it to the audit log only (unqueryable, and a log is not covered by a
constraint that says an incomplete erasure cannot claim a residual).

### D11 — A confidence is reported; a salience is computed. They are separate columns

Salience is derived — recency, frequency, centrality. Confidence is what the producer of a claim
asserted about it. Collapsed into one number, a weakly-extracted fact that is mentioned often becomes
indistinguishable from a certain one, and extraction quality becomes unreviewable after the fact.

**Rejected:** one score (above); confidence as metadata on the evidence row rather than the fact
(the fact is what gets ranked, so the fact is what needs the number).

## Write path

### D12 — Every observation carries its speaker, from the first migration

`source_role` is `NOT NULL`.

The role policy — an assistant's claim never becomes a user's fact — can only be enforced if every
write surface carries the speaker. A role that is optional at the bottom becomes optional everywhere
above it, and the surface that skips it is usually the one written last and used most.

**Rejected:** a nullable role with a default (the default is a lie about who spoke); role inferred
from the payload shape (guessing at the one thing the policy depends on).

### D13 — The log offset is assigned per scope, not by a global sequence

The freshness watermark is the highest offset with **no gap below it**. A globally-issued sequence
has holes in every scope by construction, so "contiguous" would be unanswerable and the watermark
would have to mean something weaker than it says.

**Rejected:** a global identity column (above); a timestamp as the watermark (clock skew and equal
timestamps make "everything up to here" unprovable).

## Method

### D14 — Nothing is brought in from another codebase unless it is asked for

Not code, not decisions, not measurements. Somewhere something was tried is not a source of answers
here: those choices were made under constraints that are not these constraints, and a decision
carried across without being re-derived is one nobody can defend when it is challenged.

Where something **is** asked for, what transfers is the **idea**: the implementation is rewritten,
reviewed and validated here. The check that catches a copy is D2's — every part must have a reader in
this codebase. Something present because it was in the original, rather than because something here
uses it, is a cost with no mechanism behind it.

**Rejected:** importing a decision register wholesale (imports the frame that produced a different
architecture); "carry the decisions that still hold" (presumes they hold, and the presumption is the
error).
