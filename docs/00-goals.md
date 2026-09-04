# Goals

**Adopted 2026-09-04.** This is the first document in the repository and the one every other
document answers to. It says what Taisce is for, what must be true of it, and — for each goal —
what would prove it. A goal with no way to be proved wrong is a slogan, and none of those belong
here.

The predecessor is [`althunibat/memra`](https://github.com/althunibat/memra). It works, it is
measured, and its numbers are the baseline this has to beat. Nothing here is a criticism of it;
this exists because of what measuring it revealed.

---

## 1. What Taisce is

Memory for agents. An agent writes what it observes; later it asks a question, and gets back a
bundle of what is relevant, current, and attributable — with the evidence for each claim, the
supersession history behind it, and a guarantee that anything erased is gone and can be shown to be
gone.

Sold as SaaS, hosted in the EU, on one PostgreSQL substrate.

---

## 2. Why a new codebase and not a refactor

One reason, and it is structural rather than aesthetic.

**memra retrieves passages and treats entities as a side table. Taisce retrieves entities and treats
passages as evidence.** That inverts the read path, the write path, the schema and the budget
policy. Every one of memra's five lenses, its fusion, its quotas and its packing exist to rank
passages; none of them survives the inversion intact. A refactor that replaces all of those, one at
a time, on a live read path, is slower and riskier than starting from the model that is already
known to be right — and the migration is not lost work, because the parts that were right port
across directly.

The evidence that forced it, from memra's full 500-question LongMemEval run
([13-benchmark.md §5](https://github.com/althunibat/memra/blob/main/docs/13-benchmark.md)):

| | value | reading |
|---|---|---|
| recall hit rate | 0.985 | retrieval finds the right session almost always |
| accuracy | 0.770 | and the answer is still wrong a quarter of the time |
| recall p50 | 473 ms | over 59,196 facts — **3.75× the 126 ms at a third the size** |
| bundle | 3,978 / 4,000 tokens | every bundle is full; nothing is competing to get in, everything is competing to stay |

Two conclusions. The gap is not retrieval, so more retrieval strategies cannot close it. And the
cost is **linear in corpus size** — which is why `MEMRA_MAX_PROJECT_MEMORIES` exists at 50,000 and
why the full run had to disable it to complete. That ceiling is not a safety limit that happens to
be there; it is the architecture's scaling law showing through.

Entity-anchored retrieval is O(neighbourhood) rather than O(corpus). That is the whole reason for
this repository.

---

## 3. The goals

Each states what must be true, and what would prove it.

### G1 — Retrieval cost does not grow with the corpus

A recall anchors on the entities a question names, expands their neighbourhood, and reads passages
only as evidence for what the traversal found.

**Proved by:** recall p50 measured at 50k, 250k and 1M facts in one project, with the ratio between
them flat rather than linear. The 50,000-memory ceiling is deleted, not raised.

### G2 — An entity is reachable by meaning, not only by its name

"Our Dublin site" must reach the Dublin Office node. Today it does not: resolution is lowercase,
trim, collapse whitespace, and memra's own code comment concedes the limit.

The mechanism is GraphRAG's — a semantic surface per entity, embedded and searched. The surface is
built from **aliases and the verbatim evidence quotes already stored**, never from model-written
prose about a person: an embedding over source spans is countable and erasable by construction,
re-derives on rebuild, and cannot drift from what was actually said.

**Proved by:** a resolution set of paraphrase pairs — "the Dublin office" / "our site in Dublin" /
"Dublin HQ" — with a measured merge rate, and a companion set of near-misses that must *not* merge.
Both reported, because over-merging two people onto one node is a governance failure and
under-merging is only a recall cost.

### G3 — The graph has typed, directed, temporally-scoped edges, and nothing else

One edge list: the fact. Subject entity, predicate from a **closed ontology**, object entity, valid
interval, transaction interval. No co-occurrence layer — memra's `concept_edge` connects noun
phrases that appeared near each other, which is a similarity signal with no direction and no
meaning, and the benchmark showed the lens it fed losing to one ordered vector query.

**Proved by:** benchmark-qed's data-linked classes (bridge, comparison, intersection, temporal),
which measure multi-hop directly. memra's relation hop has never had a number because LongMemEval
contains no entity-bridging question.

### G4 — Time is a first-class type, and supersession is a constraint

Valid time and transaction time as `tstzrange`, GiST-indexed. Cardinality invariants enforced by
`EXCLUDE USING gist`, not by an application-held lock.

This is the goal that most decides the substrate. Graph engines are built for topology and are
uniformly weak on intervals; PostgreSQL has range types, GiST, and exclusion constraints. The
hardest thing memra does — supersession with counterevidence — is a temporal problem, and it should
be the database's invariant rather than the worker's discipline.

**Proved by:** a concurrency test that asserts conflicting facts from N workers simultaneously and
shows the constraint rejecting the overlap, with no application lock in the path.

### G5 — Communities and their summaries follow Microsoft GraphRAG

Hierarchical Leiden, GraphRAG's parameters, GraphRAG's report structure and roll-up. Leiden rather
than Louvain because its refinement phase guarantees communities are internally connected — a
bridge node moved out of a Louvain community can leave two pieces no edge joins, and that gets
summarised as though it were one subject.

Already ported and tested in the predecessor
([`internal/app/graph/leiden.go`](https://github.com/althunibat/memra/blob/main/internal/app/graph/leiden.go)),
including determinism pinned to fixed random vectors so a Go upgrade cannot silently reorganise a
customer's graph.

**Proved by:** the ported test suite, plus benchmark-qed's global classes, which are the only thing
that measures whether community summaries earn their cost.

### G5b — Three things are embedded, because three surfaces ask different questions

GraphRAG embeds **three artefact classes**, not one, and each answers a different shape of question.
The same three are needed here, and they line up with the three provider seams an agent framework
actually exposes (G8):

| GraphRAG embeds | Taisce | Answers | Serves |
|---|---|---|---|
| text unit text | `chunk.embedding` | "what passage resembles this" | `TextSearchProvider` |
| entity + relationship **description** | `entity_embedding`, built from aliases and verbatim evidence (G2) | "what is this thing, whatever it was called" | `ContextProvider` |
| community report **full content** | report embedding | "what theme bears on this" | `ContextProvider`, global |

**One vector index over passages cannot serve all three.** A thematic question resembles no single
passage; an entity reference resembles the sentences that mention it rather than the sentences about
it. memra had only the first and its global lens had nothing of its own to search, which is a large
part of why that lens never paid.

**Proved by.** Each embedding kind has a retrieval test that fails if the other two are used
instead — otherwise "three indexes" is three copies of one.

### G6 — Everything stored about a person is countable, and erasure proves itself

`DELETE` through a registered dependency, then `COUNT(*)` against the same predicate in the same
transaction. Every projection kind registers, including any that arrive later.

This is the product's differentiator and it is the one goal that constrains the others rather than
being traded against them. Where G5 and G6 meet — a community report is model-written prose over
many subjects, and prose has no residual count — the resolution is a **decision recorded in the
decision register, not an accident of implementation**: the report generator is a pure function, and
whether its output is persisted or produced per query is one flag, decided explicitly and with the
erasure consequence written down.

**Proved by:** an erasure test per projection kind asserting a residual of zero, and a test that
fails when a new projection kind is added without registering one.

### G7 — Quality is measured on questions the product actually claims to answer

memra is measured on LongMemEval, which is a needle in one user's fifty sessions. It contains no
corpus-level question and no entity-bridging question, so two of the three things this architecture
is for are unmeasured.

**Proved by:** benchmark-qed (MIT) as the primary harness — AutoQ's four classes plus data-linked
multi-hop, AutoE's assertion-based scoring as the headline number. AutoE's pairwise metrics
(comprehensiveness, empowerment) are reported as secondary and never optimised toward: they reward
longer, more synthesised answers, which is a bias against a tight grounded bundle. LongMemEval is
retained as the regression baseline against memra.

### G8 — The integration surfaces are the product, not a wrapper over it

An agent developer never calls `recall`. They install a context provider, and everything this
repository does reaches them through three seams that Microsoft Agent Framework defines and every
comparable framework mirrors:

- **`ContextProvider`** (`AIContextProvider`: `InvokingCoreAsync` / `InvokedCoreAsync`) — memory
  injected per turn, and the store hook on the way back. This is the main surface.
- **`TextSearchProvider`** — MAF's own drop-in RAG, the same mechanism wired to a search function.
  Taisce is the search function, and this is how a passage-RAG user arrives without adopting the
  memory model first.
- **`HistoryProvider`** — the conversation turn by turn, which MAF keeps in memory and therefore
  loses.

Two rules that are not negotiable at this seam, both learned in memra: recall failures are
**non-fatal** (an agent runs without memory rather than not at all) while write failures are
**never silent** (a lost turn is invisible until a DSAR); and injected memory arrives as exactly one
`user` message marked untrusted, never `system`, never `assistant`.

**Proved by.** A conformance suite each SDK runs against a live deployment — not mocks, which prove
only that the mock matches the assertion.

### G9 — Compaction is a durable strategy inside MAF's framework, not a second framework

**Agent Framework already has compaction**, and it is well specified: a `MessageIndex` of atomic
`MessageGroup`s (`System`, `User`, `AssistantText`, `ToolCall`, `Summary`), a `CompactionTrigger`
delegate with a `CompactionTriggers` factory (`TokensExceed`, `TurnsExceed`, `GroupsExceed`,
`HasToolCalls`, composed with `All`/`Any`), a separate **target** predicate controlling when to stop,
a `MinimumPreserved` floor, and four built-in strategies composed by a pipeline. Go ships the same
shape in `agent/compaction`.

**So Taisce contributes a strategy, not a framework.** Anything else is a parallel abstraction the
customer has to choose between, and they will choose the one their framework ships.

Three constraints inherited from that framework, each of which breaks something if ignored:

- **Groups are atomic.** An assistant message with tool calls and its tool results is one unit;
  removing half of it is an API error from the model, not a degraded summary. A hierarchical roll-up
  must align its runs to group boundaries and may never split a `ToolCall` group.
- **Trigger and target are separate.** When to begin compacting and when to stop are different
  questions, and collapsing them into one threshold is how a strategy either never fires or compacts
  everything.
- **System messages are always preserved.**

**What Taisce's strategy has that the four built-ins do not: it is durable and reversible.**
`SummarizationCompactionStrategy` replaces messages with a synthetic summary in the in-flight
context; the original is whatever the history provider happened to keep. Taisce's summary is an
**observation carrying the observation ids it covers**, so the originals stay in the log, the summary
is a projection over a contiguous recorded range, and the whole thing re-derives.

That is what makes the roll-up bounded — the same roll-up G5 needs, on the time axis instead of the
graph axis:

| | truncation | sliding window | summarise-everything | Taisce roll-up |
|---|---|---|---|---|
| old context | lost | lost | retained, lossily | retained, compressed, **re-derivable** |
| cost per turn | O(1) | O(1) | **O(n)** | **amortised O(1)** |
| erasure blast radius | n/a | n/a | **whole history** | **O(log n) segments** |

The last column is why this may store prose where a community report is contested (#14): a segment
records the contiguous observation range it covers, so erasing one observation invalidates exactly
the segments containing it and their ancestors. The residual count is over segments that either
cover the erased observation or do not. A community report aggregates across subjects and has no
such bound.

**One registration rule, and it is a correctness rule rather than a preference.** Agent Framework's
own documentation warns that a `CompactionProvider` registered through `ChatClientAgentOptions` runs
*before chat history is stored*, so **synthetic summary messages can become part of the persisted
history**. For Taisce that is model-written prose entering the observation log as though it were
something a person said — the exact artefact class G6 exists to keep out. Registration is on the
`ChatClientBuilder` via `UseAIContextProviders`, and a conformance test asserts no synthetic message
reaches `observe`.

**Proved by.** Compact 10,000 turns and assert model calls are linear in turns rather than
quadratic. Erase one observation from the middle of a compacted history and assert exactly the
covering segments regenerated, residual zero, the rest untouched. And a test that a `ToolCall` group
is never split.

### G10 — The competitor at this seam is `ChatHistoryMemoryProvider`, and its gaps are the pitch

MAF ships `ChatHistoryMemoryProvider`: chat history in a vector store, embedded on write, searched by
similarity before each invocation, with `storageScope` and `searchScope` (store per session, search
across a user's sessions). That is the incumbent an agent developer already has, and Taisce has to be
better on something they can name.

Its own documentation names the gaps:

| `ChatHistoryMemoryProvider` | Taisce |
|---|---|
| *"Data from the store is accepted as-is without validation"* — indirect prompt injection | retrieved content returns as data, marked untrusted; asserted by test |
| *"messages are stored as vectors… may contain PII"*, with no erasure path | erasure by data subject with a residual count per projection kind (G6) |
| Scope is **metadata on a row**, filtered at query time | scope is the partition, and authorisation is on every read |
| Similarity over raw messages | entities, typed edges, supersession, communities — the message is evidence, not the unit |
| No supersession: an old preference and its replacement both match | validity intervals, with counterevidence reported |
| Requires `Microsoft.Extensions.VectorData` | never implemented — being a vector-store connector discards the governance that is the product |

**Proved by.** A head-to-head on a session set where the answer changed: the incumbent retrieves both
the stale and the current claim, Taisce returns the current one with the stale one marked superseded.
That is one demo and it is the whole argument.

---

### G11 — Two boundaries, and they are not the same kind of thing

**A tenant is a schema. Hard.** A cross-tenant read is impossible because the other tenant's name is
not in the statement — not because a predicate excluded it. There is no `tenant_id` column to
forget, and a bug in a `WHERE` clause cannot leak across it. The schema name cannot be a bind
parameter (PostgreSQL parameters are values, never identifiers), so it is interpolated from a
validated type that exists only after `NewSchema`: forgetting to validate is a compile error rather
than an injection.

**A project is a scope. Soft.** Inside one tenant, and cross-project reads are *intended* — a
principal granted several projects gets one bundle across them. So scope is a column, every read
carries the authorised set, and the boundary is a permission that is checked rather than a wall that
cannot be crossed.

Getting this backwards in either direction is a serious defect. Tenancy as a column is one missing
predicate away from a cross-customer leak; projects as schemas makes a multi-project recall a
cross-schema union, which cannot be planned as one query.

**Proved by.** Two tenants holding the same project name and the same data-subject reference, where
a tenant-scoped read returns exactly its own row **with no tenant predicate in the query at all** —
and a multi-project read inside one tenant returning two of three projects by authorisation.

## 4. What carries over unchanged

These were right and are not being redesigned. Porting them is work, not thinking.

- **The append-only observation log** as the erasure root, and projections derived from it.
- **Rebuild, never repair.** Generations activated atomically; after an erasure the next rebuild is
  derived only from surviving content, so residual is zero by construction rather than by the
  correctness of a repair routine.
- **Facts as claims with evidence** — verbatim quote, byte span, extractor version, supersession
  chain. `resolve_citation` is the feature nothing else in the market has.
- **The role policy.** An assistant's claim never becomes a user's fact.
- **Injection containment.** Retrieved content returns as data, flagged untrusted.
- **One binary, one substrate.** PostgreSQL is the only required dependency.
- **Determinism**, everywhere it is cheap, because it is what makes a rebuild a rebuild.

## 5. What is deliberately dropped

- **The concept graph** — `concept`, `concept_edge`, `concept_mention`. Co-occurrence between noun
  phrases, written by every rebuild, feeding a lens that loses to flat cosine.
- **Passage-first fusion.** Five lenses ranked by RRF over passages is the architecture G1 replaces.
- **Louvain.** Superseded by G5.
- **Sentinel-encoded time.** Four bigints with zero meaning "open", superseded by G4.

## 6. Explicitly out of scope

- **A graph database.** Evaluated properly, including building Apache AGE against the substrate and
  benchmarking it: it compiles on Alpine/musl, its label tables are ordinary heap tables so erasure
  would work, and it is *equivalent* on traversal speed at corpus scale. It fails on a model
  mismatch — one table per edge label against an open predicate vocabulary — and it helps least at
  G4, which is the hardest goal. Full evaluation:
  [`memra/docs/24-graph-substrate-evaluation.md`](https://github.com/althunibat/memra/blob/main/docs/24-graph-substrate-evaluation.md).
- **Full GraphRAG's indexing pipeline.** Its entity/relationship *descriptions* and community
  reports are the parts G5 adopts and G6 constrains; its six-phase batch DAG is incompatible with a
  streaming watermark and is not adopted.
- **A second datastore of any kind** — vector DB, broker, cache, object store.
- **An inference gateway.** The adapter seam already exists.

## 7. How progress is judged

Work is ordered as **one end-to-end journey**, never breadth-first: a thin path that an agent can
actually drive, deepened in passes. A milestone is done when the journey runs against a real
deployment and its claim has a test that runs — not when the code exists.

The roadmap is the issue list on this repository. Issues are the source of truth for scope; this
document is the source of truth for why.
