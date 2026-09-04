# Goals

**Adopted 2026-09-04.** The first document in the repository and the one every other document answers
to. It says what Taisce is for, what must be true of it, and — for each goal — what would prove it.
A goal with no way to be proved wrong is a slogan, and none of those belong here.

---

## 1. What Taisce is

Memory for agents. An agent writes what it observes; later it asks a question and gets back what is
relevant, current and attributable — with the evidence for each claim, the history behind it, and a
guarantee that anything erased is gone and can be shown to be gone.

A multi-tenant SaaS, hosted in the EU, on one PostgreSQL substrate. The adapters and clients that
reach it are open source; the service is not.

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

### G11 — Two boundaries, and they are not the same kind of thing

**A tenant is a schema. Hard.** A cross-tenant read is impossible because the other tenant's name is
not in the statement — not because a predicate excluded it. There is no tenant column to forget, and
a bug in a `WHERE` clause cannot cross it. A schema name cannot be a bind parameter, so it is
interpolated from a validated type that exists only after construction: forgetting to validate is a
compile error rather than an injection.

**A project is a scope. Soft.** Inside one tenant, where cross-project reads are *intended* — a
principal granted several projects gets one bundle across them. So scope is a column, every read
carries the authorised set, and the boundary is a permission that is checked.

Getting this backwards in either direction is a serious defect. Tenancy as a column is one missing
predicate from a cross-customer leak; projects as schemas makes a multi-project recall a cross-schema
union that cannot be planned as one query.

**Proved by.** Two tenants holding the same project name and the same data-subject reference, where a
tenant-scoped read returns exactly its own row **with no tenant predicate in the query at all** — and
a multi-project read inside one tenant returning two of three projects by authorisation.

### G12 — Open client, closed service, and the line between them is the architecture

The adapters and clients are open source. They run inside the customer's process, hold their key, and
see every observation and every recall on the way past; nobody should take that on trust from a
binary, and an adapter that cannot be read will not be adopted by anyone whose security team reads
code.

**The service is not open.** So the line is: **the adapter translates, the service decides.**
Retrieval, ranking, supersession, communities, compaction and erasure are the service. Message
filtering, envelope construction and one framework's seam are the adapter. That makes "no logic in an
adapter" (G8) the boundary between what is given away and what is sold, not merely a consistency
rule.

Three consequences that are easy to notice too late. The REST contract becomes public the day the
first package ships. Publishing is irreversible — registries refuse to reuse a version number, and a
Go module path is permanent — so the organisation and module path are settled before the first
publish. And every README is public copy making claims about the service.

**Proved by.** A test that fails when an operation reaches the dispatch table without appearing in
the public contract; a guard over the SDK READMEs; and signed, provenanced releases on every
registry.

### G13 — A bundle constrains to exactly what it promised, in both directions

Subscription bundles from a free tier upward. Both halves are enforceable claims: **every promise has
an enforcer**, or it is an unbounded cost and a number on a public page with nothing behind it; and
**every enforcer has a promise**, or it is a surprise outage the customer could not have planned for.

Enforcement is **one path** — a single view the quota gate already reads, cached per tenant,
refreshing faster as a tenant nears a ceiling, failing open on a database blip. An entitlement that
needs new machinery is an entitlement designed wrong. Usage is measured **physically**, over the
tenant's own tables including indexes, because the cost being bounded is disk rather than the length
of the strings somebody sent.

**The catalogue is copied onto the tenant at subscribe time**, so a published change cannot silently
re-limit somebody already on a plan.

**The metering that is easy to miss.** Ingest and recall happen on request paths a counter can see.
Compaction (G9) and community reports (G5) call a model **on a schedule the customer sets** —
compaction whenever a run of turns completes, reports per community per rebuild. Neither is visible
to a request counter, and on a free tier that is unbounded spend from an abandoned tenant. When that
budget is exhausted the degradation is decided rather than defaulted: compaction falls back to a
non-LLM strategy, because an agent that stops working when a quota runs out is an outage while one
that compacts more bluntly is a smaller product.

**Reclamation is a line item, not an apology.** An empty tenant schema costs storage before anything
is stored, and every deployment's migration walks all of them. A product whose argument is that it
can prove what it deleted should be the one vendor that deletes what nobody is using, on a published
schedule, and shows the residual afterwards. Dormancy is a **clock, not a state** — an account that
has gone quiet must still be able to sign in, or the notice telling it to is an instruction the
system refuses.

**Proved by.** A test that fails when a plan column exists with no reader. A test that every model
call site lands against a meter. A guard over the feature strings, because a pricing page is public
copy and an unenforced feature is the same class of defect as an unearned claim.

---

## 4. Out of scope

- **A second datastore of any kind** — vector database, graph engine, broker, cache, object store.
  PostgreSQL is the only required dependency; each addition is a second place data lives, a second
  thing to operate, and a second sweep an erasure has to prove it covered.
- **A self-hosted edition.** Hosted and self-hosted cannot both be told well.
- **Being a vector-store connector.** That makes Taisce a place to put vectors and discards the
  governance that is the product.

---

## 5. How progress is judged

Work is ordered as **one end-to-end journey**, deepened in passes, never breadth-first. A milestone
is done when the journey runs against a real deployment and its claim has a test that runs — not when
the code exists.

The roadmap is the issue list on this repository. Issues are the source of truth for scope; this
document is the source of truth for why.
