# Goals

**Adopted 2026-09-04. Revised 2026-09-07**, when the product changed shape: from a multi-tenant
hosted service with open adapters to an open-source single-tenant service the operator runs. G11,
G12 and G13 were rewritten and G14 added; the register records what that decision rejected. The first
document in the repository and the one every other document answers to. It says what Taisce is for, what must be true of it, and — for each goal — what would prove it.
A goal with no way to be proved wrong is a slogan, and none of those belong here.

---

## 1. What Taisce is

Memory for agents. An agent writes what it observes; later it asks a question and gets back what is
relevant, current and attributable — with the evidence for each claim, the history behind it, and a
guarantee that anything erased is gone and can be shown to be gone.

An open-source service on one PostgreSQL substrate, run by the people who use it: one instance, one
tenant, on infrastructure its operator controls. All of it is Apache-2.0 — service, portal, adapters,
clients.

**Adoption is the measure.** A memory layer nobody installs is a design document with tests. That
puts weight on things a closed product could treat as packaging: how long it takes to get a working
agent from a cold machine, whether it appears at the seam a developer is already standing at, and
whether the claims it makes can be re-run by the person deciding whether to trust them.

---

## 2. The shape, in one paragraph

**Entities are primary and passages are evidence.** A question resolves to the entities it names,
the answer is assembled by expanding their neighbourhood, and passages are read to support what the
traversal found. The alternative — searching passages by similarity and hoping an entity falls out —
makes retrieval cost proportional to the corpus rather than to the question, and makes a fact about a
thing unreachable unless the question happens to share vocabulary with the sentence that recorded it.

Everything below follows from that sentence or constrains it.

---

## 3. The goals

### G1 — Retrieval cost does not grow with the corpus

A recall anchors on the entities a question names, expands their neighbourhood, and reads passages
only as evidence. Cost is proportional to a neighbourhood, not to how much the customer has stored.

This is the goal the architecture exists for. A memory that gets slower as it remembers more has a
ceiling, and a ceiling is a product limit dressed as a configuration value.

**Proved by.** Recall p50 and p95 measured at 50k, 250k and 1M facts in one project, with the ratio
between them flat rather than linear. No configured cap on memories per project, because there is
nothing for one to protect.

### G2 — An entity is reachable by meaning, not only by its name

"Our Dublin site" must reach the same node as "the Dublin office". Matching on a normalised string
merges casing and whitespace and nothing else, which leaves one thing as several nodes each holding a
fraction of what is known about it.

The entity carries a semantic surface — an embedding — built from **its aliases and the verbatim
quotes already stored as evidence**, never from generated prose about it. Source spans are countable,
erasable, and cannot drift from what was actually said.

**Proved by.** A resolution set of paraphrase pairs with a measured merge rate, and a near-miss set
that must **not** merge. Both reported: over-merging two people onto one node is a governance
failure, under-merging is a recall cost, and that asymmetry sets the threshold.

### G3 — The graph has typed, directed, temporally-scoped edges, and nothing else

One edge list: the fact. Subject entity, predicate from a closed ontology, object entity, and the
interval it was true for. No co-occurrence layer — edges between terms that appeared near each other
have no direction and no meaning, so there is nothing to follow and a multi-hop question has no hops.

**Proved by.** Multi-hop question classes — bridge, comparison, intersection, temporal — measured
directly, with the traversal returning the chain rather than only the endpoint.

### G4 — Time is a first-class type, and supersession is a constraint

Valid time (when the world was that way) and transaction time (when we learned it), as ranges with
GiST indexes. Cardinality invariants enforced by an exclusion constraint, not by a lock the write
path has to remember to take.

The hardest question in agent memory is temporal — what is true now, what was true then, what did we
believe then — and a lock is correct only while every write path remembers it, whereas a constraint
is correct because the database will not store the violation.

**Proved by.** A bitemporal test answering all three questions differently and correctly. A
concurrency test asserting conflicting facts from N workers simultaneously, with no application lock
in the path, and the constraint rejecting the overlap.

### G5 — Communities and their summaries follow Microsoft GraphRAG

Hierarchical Leiden with GraphRAG's parameters, GraphRAG's report structure, and its bottom-up
roll-up where sub-community reports substitute for raw context when a parent overflows.

Leiden rather than Louvain because its refinement phase guarantees a community is internally
connected — a bridge node moved out of a Louvain community can leave two pieces no edge joins, and
that gets summarised as though it were one subject.

**Proved by.** Determinism across runs, input orderings and machines, pinned to fixed random vectors
so a toolchain upgrade cannot silently reorganise a customer's graph. Connectedness asserted on
cliques, rings and barbells. And question classes that no single passage can answer.

### G5b — Three things are embedded, because three surfaces ask different questions

| Embedded | Answers | Serves |
|---|---|---|
| passage text | "what passage resembles this" | text search |
| entity surface (G2) | "what is this thing, whatever it was called" | entity-anchored recall |
| community report content | "what theme bears on this" | thematic recall |

One index over passages cannot serve all three. A thematic question resembles no single passage —
that is what makes it thematic. An entity reference resembles the sentences that mention it rather
than the sentences about it.

**Proved by.** A retrieval test per embedding kind that fails when the other two are substituted.
Otherwise "three indexes" is three copies of one.

### G6 — Everything stored about a person is countable, and erasure proves itself

Delete through a registered dependency, then count against the same predicate in the same
transaction, per projection kind. Every projection registers, including ones added later.

This is the differentiator, and it constrains the other goals rather than being traded against them.
An artefact that cannot be counted after a delete cannot be part of an erasure claim, and that rules
some designs out regardless of how well they retrieve.

**Proved by.** A residual-zero test per projection kind, and a test that fails when a new projection
kind is added without registering one.

### G7 — Quality is measured on the questions the product claims to answer

Corpus-level questions and entity-bridging questions are two of the three things this architecture is
for, and a needle-in-a-haystack benchmark measures neither.

**Proved by.** `microsoft/benchmark-qed` as the primary harness — its four query classes plus
data-linked multi-hop (bridge, comparison, intersection, temporal), with assertion-based scoring as
the headline number. Its pairwise metrics are secondary and never optimised toward: they reward
longer, more synthesised answers, which is a bias against a tight grounded bundle.

### G8 — The adapter is the product. The client is plumbing under it

An agent developer never calls recall, and mostly never calls the client. They install a context
provider into the framework they already use. A plain SDK nobody adapts is a REST wrapper — the thing
a competent team writes in an afternoon and does not pay for.

Focus: **Python, Java and .NET**. The seams, whatever a framework calls them — context provider
(memory injected per turn, with a store hook on the way back), text search (drop-in RAG, the on-ramp
that requires believing nothing), history and compaction, session and provider state.

Two rules at every seam: recall failures are **non-fatal** (an agent runs without memory rather than
not at all) while write failures are **never silent** (a lost turn is invisible until a subject
access request). And injected memory arrives as exactly one `user` message marked untrusted — never
`system`, never `assistant`.

**No logic in an adapter.** An adapter that decides things locally is how behaviour forks per
language, and it forks quietly because each one has its own tests.

**Proved by.** One conformance suite, run by every adapter against a live deployment, asserting the
same behaviour from each. Not mocks — a mock proves the mock matches the assertion.

### G9 — Compaction is a Taisce operation, and every SDK is a thin client of it

An agent's history grows without limit and the context window does not. Compaction is the answer
everywhere; the usual implementations truncate, slide a window, or re-summarise everything, and all
three either lose data silently or cost time proportional to the history on every turn.

**It lives in the server**, not in any adapter. The property that distinguishes it is that a summary
is **an observation carrying the observation ids it covers** — which needs the log, the dependency
registration, and one transaction around both. A client-side strategy can only hand the model a
shorter list and forget.

The algorithm is hierarchical and it is G5's roll-up on the time axis: recent groups verbatim, a full
run becomes one level-1 segment, a run of those becomes level-2. Amortised O(1) summarisation per
turn, O(log n) segments to assemble a context, newest-verbatim and oldest-most-compressed.

Two modes: **stateful**, where Taisce holds the history and returns a context under a token budget;
**stateless**, where the application sends its list — lossy by necessity, but it works before a
customer has adopted anything else.

**Groups are atomic.** An assistant message with tool calls and its tool results is one unit;
splitting it is an API error from the model. Because compaction is server-side, the log must carry
enough structure to reconstruct those groups.

**Erasure stays bounded**, which is what lets this store prose: a segment records the contiguous
observation range it covers, so erasing one observation invalidates exactly the segments containing
it and their ancestors.

**Proved by.** 10,000 turns compacted with model calls linear in turns rather than quadratic. One
observation erased mid-history, with exactly the covering segments regenerated and the rest
untouched. A tool-call group never split. No synthetic summary message ever reaching the write path.

### G10 — Beat the incumbent on something a developer can name

The frameworks ship their own chat-history memory: messages in a vector store, embedded on write,
similarity-searched before each invocation. That is what a developer already has, and the argument
against it has to be concrete.

| Incumbent | Taisce |
|---|---|
| retrieved content injected without validation | returned as data, marked untrusted, asserted by test |
| messages stored as vectors, no erasure path | erasure by data subject with a residual count per projection kind |
| scope is metadata on a row, filtered at query time | scope is the partition; authorisation on every read |
| similarity over raw messages | entities, typed edges, supersession — the message is evidence |
| no supersession: a stale claim and its replacement both match | validity intervals, with counterevidence reported |

**Proved by.** A head-to-head on a session set where the answer changed: the incumbent returns both
the stale and the current claim, Taisce returns the current one with the stale one marked superseded.
That is one demo and it is the whole argument.

### G11 — One instance is one tenant, and the boundary that remains is the project

**There is no tenant boundary to cross, because there is one tenant.** An instance holds one
organisation's memory, on their own infrastructure. That removes the hardest security surface a
memory product has — a cross-customer read is not mitigated, it is unreachable, because no other
customer's data is in the database.

**The schema stays.** Not for tenancy: because it is what makes erasure a walk over one namespace,
migration a single ordered application, and a backup a thing with an obvious boundary. A schema name
cannot be a bind parameter, so it is interpolated from a validated type that exists only after
construction, and forgetting to validate is a compile error rather than an injection.

**A project is the isolation, and a token belongs to one of them.** One tenant is not one user or one
application: several agents, several teams and several products share an instance, and a project is
what keeps them apart. A project is a namespace data belongs to, that tokens attach to, that declares
which kinds of memory it keeps, and that carries its own retention policy.

One project per token, not a set. An agent needing two holds two. That costs the cross-project bundle
this goal once described, and buys a credential whose reach is a fact rather than a list — which makes
every audit row unambiguous and a leaked token a bounded incident. Scope is still an argument on every
read rather than a default, and an empty set still returns nothing: that is what makes a revoked token
harmless.

**An operator's portal session is not entitled to project data by virtue of being an operator.** The
instance is what an operator administers; the memory inside a project is what a token reaches.

**Proved by.** A multi-project read returning two of three projects by authorisation, and an empty
authorised set returning an empty bundle rather than everything.

### G12 — All of it is open, and publishing is irreversible

Everything is Apache-2.0: the service, the portal, the adapters and the clients. Permissive
throughout, chosen for adoption — an adapter that links into somebody's application cannot carry a
reciprocal licence without becoming a thing their legal review refuses, and a service under a licence
that scares operators is a service nobody runs.

**What that costs, stated rather than discovered.** Anyone may host this, including as a competing
service, with no obligation to contribute anything back. That is the price of the adoption the
licence buys, and it is paid permanently: a licence cannot be narrowed once packages ship.

**So the line between adapter and service is no longer commercial, and it still holds.** Retrieval,
ranking, supersession, communities, compaction and erasure are the service because that is where a
correctness argument can be tested against a real substrate. Message filtering, envelope construction
and one framework's seam are the adapter. "No logic in an adapter" (G8) is now a consistency rule
rather than a boundary of what is sold — and consistency is why it survives: five adapters with five
implementations of ranking is five behaviours to explain.

**Publishing is irreversible in ways that are easy to notice too late.** Registries refuse to reuse a
version number, a Go module path is permanent, and the REST contract is public the day the first
package ships. So the organisation, the module path and the contract are settled before the first
publish, not after the first user.

**Proved by.** A test that fails when an operation reaches the dispatch table without appearing in
the public contract; signed, provenanced releases on every registry; and a licence header check that
fails the build rather than a note in a README.

### G13 — It runs on one machine in minutes, and in a cluster without a single point of failure

**The first five minutes decide whether there is a sixth.** `docker compose up` brings up the
substrate, the service, the default model hop and the portal, and an agent stores and recalls a
memory against it without editing a configuration file first. Every step between a cold machine and
that moment is a place the evaluation ends, so the number of them is a design constraint rather than
documentation's problem.

**The compose file is one machine and says so.** One host is one failure domain. A compose file
arranged to look highly available would survive a process dying and not the machine it is on, and a
product whose argument is that it proves what it holds cannot make a claim it has not measured.

**The chart is highly available by default.** Several stateless replicas of the service and a
PostgreSQL topology that survives losing a node. The application is already safe to run this way —
formation takes a per-scope advisory lock and the watermark is taken `FOR UPDATE`, so two replicas
forming the same scope is a case that is refused rather than a race — which makes availability the
chart's arrangement rather than something the code has to learn.

**"Highly available" is a topology until it has a number.** No RTO or RPO has been agreed. Until one
is, the chart is a topology with a failover path, not a guarantee, and saying so is part of rule 11.

**Proved by.** A timed run from a cold checkout to a recalled memory, measured rather than estimated;
and a chart test that kills the primary and asserts the service still answers, with the recovery time
recorded as the first measurement of the number nobody has yet agreed.

### G14 — The operator can see what the instance is doing, without reading the database

Somebody now runs this, and it is usually the same person who built the agent. What they need to
answer is narrow and mostly already recorded: is formation keeping up, what is extraction refusing
and why, what did the last erasure prove, what is the model costing, and is the deployment healthy.

**Most of the ops story is already in the schema.** Refusals are rows carrying the model's own words
and a reason — `unmapped_relation` tells you the vocabulary is wrong, `unlocatable_quote` tells you
the model is paraphrasing, `duplicate_claim` tells you it is repeating itself, and those need
different responses. The watermark says how far behind forming is. An erasure receipt carries its
counted residual. The portal reads what is there rather than inventing a telemetry layer beside it.

**The portal is a trust boundary, and single-tenant does not make it one user.** It displays verbatim
quotes from people's messages. It authenticates by default, it is not exposed by default, and what it
may show is a decision rather than a consequence of what is convenient to query.

**Proved by.** A test that the portal serves nothing to an unauthenticated request, including in the
default compose deployment; and a test that every panel is backed by a query against a table that
exists rather than a metric emitted for the panel's benefit.

## 4. Out of scope

- **A second datastore of any kind** — vector database, graph engine, broker, cache, object store.
  PostgreSQL is the only required dependency; each addition is a second place data lives, a second
  thing to operate, and a second sweep an erasure has to prove it covered.
- **A hosted edition, and a paid tier.** Hosted and self-hosted cannot both be told well, and this is
  the one being told. An open-core line drawn later is a refactor through every feature that
  straddles it, so there is no line.
- **Being a vector-store connector.** That makes Taisce a place to put vectors and discards the
  governance that is the product.
- **Requiring a model proxy.** LiteLLM ships in the deployment as the default hop to an
  OpenAI-compatible endpoint. Requiring it would make a second moving part mandatory on the write
  path in exchange for convenience an operator with a key does not need.

---

## 5. How progress is judged

Work is ordered as **one end-to-end journey**, deepened in passes, never breadth-first. A milestone
is done when the journey runs against a real deployment and its claim has a test that runs — not when
the code exists.

The roadmap is the issue list on this repository. Issues are the source of truth for scope; this
document is the source of truth for why.
