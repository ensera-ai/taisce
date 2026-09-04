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

---

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
