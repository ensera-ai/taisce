# Decision register

Every decision, closed. **Nothing in this document is an open question.** If another document appears
to leave a choice, that is a defect in that document — this register is the authority.

Each entry says what was decided, why, and **what was rejected**. The rejections are the point: a
decision recorded without its alternatives gets reopened by whoever has only heard the winning
argument.

Entries are added as decisions are made. An entry is written when a `decision`-labelled issue closes,
or when building something forces a choice that a later reader would otherwise have to re-derive.

---

## Shape

*These entries were taken on 2026-09-07 and they govern the ones below. Where an earlier entry
assumed a hosted, multi-tenant, part-closed product, it carries a note saying what replaced it.*

### D35 — Taisce is one open-source service, single-tenant, run by its operator

An instance holds one organisation's memory, on infrastructure that organisation controls: `docker
compose up` on a machine, a Helm chart in a cluster. There is no hosted edition and no part of the
system that is withheld.

The argument is adoption. A memory layer competes with a vector store somebody already runs and with
doing nothing, and both alternatives are free to start. Anything that makes evaluation require a
signup, a key, or a conversation loses to them before the design is ever considered. Self-hosting
also turns the governance claims from promises into observations: the data does not leave, and the
residual an erasure counts is computed on the operator's own substrate rather than asserted by a
vendor.

**What it costs.** The multi-tenant machinery already built has no reader in this shape, and the
security argument that a cross-customer read is *unreachable* becomes trivially true rather than
hard-won. Scaling evidence gets harder to gather: nobody is running a corpus of everyone's data, so
the numbers in G1 have to come from a benchmark rather than from production.

**Rejected:** the multi-tenant hosted service this was (it tells the governance story second-hand, and
every evaluation starts with a signup); running both (the out-of-scope section's own argument —
hosted and self-hosted cannot both be told well, and the one that suffers is whichever is not being
demonstrated).

**How hard to undo.** Moderate in code and severe in expectation. The schema-per-scope substrate would
carry a hosted edition with little change, but a licence cannot be narrowed and a community that
adopted an open product does not quietly accept it closing.

### D36 — Apache-2.0, for all of it

Service, portal, adapters, clients. One permissive licence, no split.

Adapters link into other people's applications, and a reciprocal licence there is a thing a legal
review refuses — which would cost exactly the adoption the adapters exist to win. Applying the same
licence to the service keeps the answer to *"what can I do with this"* one sentence long, and a
licence an operator has to reason about is friction at the moment they are deciding whether to try it
at all.

**What it costs, stated rather than discovered.** Anyone may host this as a competing service with no
obligation to contribute back. That is the price of the adoption, and it is paid permanently.

**Rejected:** AGPL for the service with Apache-2.0 adapters (the split most infrastructure projects
land on; it would preserve a hosted business and it was rejected because there is not going to be
one, and because AGPL anywhere in a stack is a question every adopter's legal team asks); BSL or
another source-available licence (protects a commercial position best, and it is not open source, so
it contradicts the thing being built).

**How hard to undo.** Permanent for anything published. Registries refuse to reuse a version number,
and code released under Apache-2.0 stays available under it.

### D37 — LiteLLM ships in the deployment; it is not a dependency

The service speaks the OpenAI-compatible HTTP interface and nothing more specific. The compose file
and the chart include LiteLLM configured as the default hop to a provider, because "bring your own
endpoint" as the only answer means the first five minutes involve choosing and configuring one.

Shipped is not required. An operator who already holds a key points the endpoint at it and runs
nothing extra. D1 is intact: PostgreSQL remains the only thing that has to be there.

**Rejected:** requiring LiteLLM (a second moving part on the write path, mandatory, in exchange for
convenience that an operator with a key does not need — and D1's argument applies to a proxy exactly
as it applies to a cache); shipping nothing and documenting a provider (correct and slower, and the
five minutes it costs are the five minutes that decide adoption); calling providers directly per
vendor (one interface is why inference can move without reaching the extractor).

**How hard to undo.** Easy. It is a service in a compose file and a subchart.

**Amended on 2026-09-07, when the compose file was built.** The reasoning above assumed LiteLLM
removed the configuration from the first five minutes. It does not: LiteLLM still needs a provider
and a key behind it, so shipping it by default adds a container without removing the signup. What
actually removes the signup is a model on the operator's own machine, so the quickstart points at a
local Ollama over the same OpenAI-compatible interface, and LiteLLM is an opt-in profile for the case
it is genuinely for — several providers, one key store, and cost visible in one place.

The conclusion is unchanged and better founded: the endpoint is configuration, nothing about it is
required, and the default is whichever answer costs an evaluator the least.

### D38 — The chart is highly available; the compose file is one machine and says so

The Helm chart defaults to several stateless replicas and a PostgreSQL topology that survives losing
a node. The compose file is a single host, documented as an evaluation and development deployment.

The application needs nothing new for this. Formation takes a per-scope advisory lock and the
watermark row is taken `FOR UPDATE`, so a second replica forming the same scope is refused rather
than racing — which is already tested. Availability is therefore what the chart arranges.

**Rejected:** arranging compose to look highly available (it survives a process dying and not the
machine, so the word would be doing work it cannot support); deferring availability until an RTO and
RPO exist (the topology is the part that is expensive to retrofit, and it can be built while the
number is still unstated — provided the claim is not made).

**What is deliberately not claimed.** No RTO or RPO has been agreed, so the chart is a topology with a
failover path rather than a guarantee. The first measurement of that number is the closing evidence
on the chart's own issue.

**How hard to undo.** Easy for compose, moderate for the chart.

### D39 — There is no commercial line, so none is designed for

No paid tier, no enterprise edition, no feature held back. Bundles, plan catalogues, entitlement
enforcers and subscription lifecycles are removed from scope rather than deferred.

An open-core line drawn later is a refactor through every feature that straddles it, and — worse — it
changes what gets built in the meantime, because anything that might end up on the paid side gets
designed with a seam it does not need. Deciding there is no line now is what stops that.

**Rejected:** open core with the portal or availability behind it (it makes operations the paid part
of a product whose argument is that operations are visible); a hosted edition later (D35); keeping the
bundle work dormant in case (a design nobody is building rots, and a milestone nobody will finish is
a roadmap that lies about what is coming).

**How hard to undo.** Easy in code, since removing unbuilt scope removes nothing that runs. Hard in
expectation, for the reason in D35.

### D40 — The adapters are Python, Java and .NET, in that order

Held against the argument that adoption should send us to TypeScript first, and kept.

**Go where there is a named incumbent or no competitor, not where the noise is loudest.** .NET has a
first-party memory seam already occupied, which makes the claim testable: a head-to-head against a
specific thing a developer can name beats *"we also support this"*. Java has framework seams with no
first-party competitor at all. Python is where agents are built. Each of those is a reason a
developer would switch, rather than a place to be present.

**Rejected: TypeScript first.** It is the largest agent ecosystem and it has a client here with no
adapter, and that argument is real — recorded rather than dismissed, because it is the one that gets
made again. It loses on differentiation: every memory product ships a TypeScript SDK, so arriving
there means competing on presence in the most crowded seam, while the two seams where we would be
either the only option or the challenger to a named one go unserved. The counter-argument is that
crowding is evidence of where the users are, and if the first two adapters do not move adoption, that
is the evidence that reopens this.

**Also rejected:** one adapter taken all the way before a second exists (the second use case is what
reveals the right shape, and deferring it defers the lesson); MCP as the primary surface instead of
adapters (it reaches coding assistants, which are a different user from the agent applications the
adapters target — so it is additional, not a substitute, and it stays on the roadmap as its own
work).

**How hard to undo.** Easy. It is an ordering, not a structure, and the conformance suite is written
before any adapter moves so a fourth language is held to the same contract as the first three.

### D41 — The name is Taisce, the owner is Ensera, and the module path is `github.com/ensera-ai/taisce`

Stated by the owner on 2026-09-07 and recorded because two of these three are permanent.

**One project, one name.** The work that preceded this repository under a different name is the same
project and the same ownership; the name it carries now is Taisce. There is no second system to
migrate from, which is why the earlier caution about importing from it was a discipline about
re-deriving reasoning rather than a boundary between two owners.

**Ensera holds it.** That settles the question that blocked reuse: relicensing under Apache-2.0 is the
copyright holder's to do, and the holder is the party doing it. `LICENSE` and `NOTICE` name Ensera.

**`github.com/ensera-ai/taisce`.** Baked into every import in this repository and into every Go module
that will ever depend on it.

**Why it is taken before publishing rather than after.** A Go module path cannot be changed without
being a different module: consumers do not follow a rename, they break. Registries refuse to reuse a
version number. The cost of choosing now is one search and replace across 21 files; the cost of
choosing after the first tag is a permanent redirect and a second name in every dependency graph.

**Rejected:** a personal namespace (the repository's remote today, and it makes the project look like
one person's side project to exactly the audience being courted, while making a later move to an
organisation the breaking change above); a bare `taisce` vanity path over a redirect (a permanent
dependency on DNS and a redirect that must outlive interest in maintaining it, for a shorter string).

**How hard to undo.** The name and the module path are permanent for anything published. Ownership is
a matter of record rather than of code.

**Where it lives, and why there are two repositories.** `ensera-ai/taisce` is the home and the record.
The personal repository the work started in is kept rather than moved, at the owner's instruction, so
its history stays where anyone who already had the link will find it. GitHub refuses to transfer
issues between different owners, so the issue record was **recreated** in order — all 82, numbers
preserved, because commit messages and issue bodies cross-reference them by number and a renumbering
would break the argument trail this project is mostly made of. What did not survive is the original
timestamp on 46 comments; their text and authorship did.

**The consequence to watch:** two repositories now hold the same issue numbers, and only one of them
is the roadmap. An issue list that exists twice drifts, and the copy nobody is watching is the one
that will be wrong.

### D42 — One image, and its command decides what it does

Serving reads and forming the backlog run in one process by default. A `TAISCE_ROLE` of `api` or
`worker` runs one half, from the same image and the same binary.

**They are genuinely different workloads.** The API is fast and database-bound; formation is slow and
model-bound, and one of them wants pausing during a provider incident while the other keeps
answering. Those are real reasons to RUN them apart.

**They are not reasons to BUILD them apart.** Two binaries is two images, two build pipelines, two
version numbers that can disagree, and a schema migration that has to land in both — paid forever,
for a separation that is a runtime choice. With one image, running them apart is a line in a chart.

**The default runs both, and splitting should follow a number.** Nothing here has been measured
needing two deployments, and the development machine cannot produce that measurement. A split adopted
from a diagram is complexity with no argument behind it.

**What makes the split safe was already there.** The backlog lives in the database, not in a process,
so a paused worker means turns accumulate rather than disappear; and formation takes a per-scope
advisory lock, so several workers is a deployment decision rather than a code change.

**Rejected:** separate `api` and `worker` binaries (the permanent cost above, for a runtime choice);
one process always doing both (correct on a laptop and wrong the first time somebody needs to stop
extraction without stopping reads); a role that defaults when misspelled (a typo would silently give
every pod both halves, which is the state the split exists to avoid).

**A worker binds no port at all.** One that did would be a process an orchestrator could route traffic
to by accident, with a health check passing about reads it does not serve.

**How hard to undo.** Easy. It is an environment variable and a switch.

**Amended the same day, when the deployment shape was stated.** The compose file runs `api` and
`worker` as two services rather than one combined process, and the workers are what scale. The
reasoning above is unchanged — one image, role at runtime — and what changed is the DEFAULT: a
quickstart whose topology is nothing like a deployment is a topology that has to be unlearned, and
the cost of teaching the right one is a second container of an image that is already built.

### D43 — Two boundaries: the deployment is the tenant, the project is the isolation

**The tenant is the instance.** One deployment holds one organisation's memory (D35). There is no
tenant boundary to cross because there is no second tenant to cross into — not mitigated, unreachable.

**The project is the unit of isolation, and it is a real object rather than a string.** A project is a
namespace that data belongs to, that API tokens are attached to, that declares which kinds of memory
it keeps, and that carries its own retention policy. Today it is none of those: it is a table
partition, a watermark row, and a `scope` column on everything else. There is no project table at all,
which means there is nowhere for a setting to live and nothing for a token to be attached to.

**A token belongs to one project.** Not to a set of them. An agent that needs two projects holds two
tokens.

That is a deliberate narrowing of what was built, and it costs something specific: a recall can no
longer return one bundle spanning several projects, which G11 originally designed for. It is worth the
loss. A token that reaches one namespace makes "what can this credential see" a fact rather than a
list, makes every audit row unambiguous about which project an operation touched, and makes a leaked
token a bounded incident. And the direction is the safe one: widening a token to more projects later
breaks nothing, while narrowing one after clients depend on the width breaks all of them.

**Per-project settings, and what they are not.** A project declares which memory surfaces are active —
history, context provider, and whichever others exist — and its retention policy. This is the
operator configuring their own instance, and it must not become the entitlement machinery D39
removed: there is no plan, nothing is withheld, and a setting exists because an operator turns it off,
not because somebody has not paid for it. The distinction is worth guarding, because the two look
identical in a schema and only one of them has a reader.

**Rejected:** a project as a schema of its own (a multi-project query becomes a cross-schema union
that cannot be planned as one statement, and erasure would walk N namespaces instead of one); a token
holding a set of projects (what was built — it enables the cross-project bundle and makes the blast
radius of a leak a list rather than a name); settings on the credential rather than the project (two
tokens into one project could then disagree about what that project keeps, which is a project setting
by definition).

**How hard to undo.** The narrowing is easy to reverse and hard to re-apply, which is why it is taken
now. The project table is a migration; the settings on it are only as reversible as the behaviour they
switch.

### D44 — The service is one repository; each adapter has its own

`ensera-ai/taisce` holds the server, the workers and the migrations. Every framework adapter — Python,
Java, .NET — is a separate repository.

**The service is one deployable and one version.** Server and worker are the same binary in different
roles (D42), and a migration has to ship with the code that needs it: a schema change released ahead
of its reader, or behind it, is a deployment that half works. Splitting them across repositories would
create a version pair somebody has to reason about on every release.

**An adapter belongs to somebody else's ecosystem.** It is released on that ecosystem's cadence, into
that ecosystem's registry, under that ecosystem's versioning conventions — and it is consumed by
people who will never look at this repository. A Python package at `0.3.0` and a Go service at
`0.3.0` mean different things, and forcing them to move together means every adapter waits for the
slowest one.

**What that costs, and how it is paid.** The wire contract is now shared across repositories that
release independently, so it can drift — an adapter written against an operation the service has
changed. That is what the conformance suite (#26) is for, and it is why the suite is written before
any adapter moves rather than after the second one disagrees.

**Rejected:** a monorepo (one tag would mean five different things, and every ecosystem's tooling has
to agree about prefixed tags before any of them can release); a repository per component of the
service (server and worker are one binary, so it would be one codebase in two places).

**How hard to undo.** Hard once packages ship. A published module path is permanent, so moving an
adapter between repositories later means a new name and an abandoned one.

### D45 — Contributors sign off; there is no contributor agreement

A Developer Certificate of Origin: a contributor adds `Signed-off-by:` to each commit, asserting they
wrote it and may submit it under this licence. Checked by CI. No document, no account, no rights
transfer.

**How this relates to the licence, because the two are easily confused.** Apache-2.0 is the OUTBOUND
licence: what this project grants to everyone who receives it. A DCO or a CLA is INBOUND: what a
contributor grants to this project when they hand over a patch.

Apache-2.0 already settles the inbound licence by itself. Section 5: *"any Contribution intentionally
submitted for inclusion in the Work by You to the Licensor shall be under the terms and conditions of
this License, without any additional terms or conditions."* A contributor opening a pull request has
therefore already licensed that code under Apache-2.0, patent grant included. No agreement is needed
to obtain it.

**What section 5 does not say is whether they had the right to submit it.** It licenses the
contribution; it does not establish that the contributor owned what they were licensing. Code pasted
from a copyleft project, or written under an employment contract that assigns it to somebody else, is
licensed by section 5 to the same extent the contributor could license it — which may be not at all,
and the way that is discovered is the owner asking. A sign-off is the record that they said they
could.

**A CLA exists to let the project owner do something the licence otherwise would not permit** —
usually relicensing, including to a proprietary or dual-licensed edition. D36 makes everything
Apache-2.0 and D39 removes the commercial line, so there is nothing here for a CLA to enable. Note
that a hosted edition would not need one either: Apache-2.0 already permits hosting contributed code.
The only thing a CLA buys is the option to move *away* from Apache-2.0, which is the option D36
deliberately gave up.

**So the cost decides it.** A CLA needs a signing service, a legal entity to sign with, and a step
between a contributor's first patch and it being reviewed — the point at which most drive-by
contributors stop. A sign-off is a flag on a commit.

**Rejected:** a CLA (buys a relicensing option that has been given up, at the cost of friction on
every first contribution); nothing at all (no record that a contributor had the right to submit what
they submitted, which is the one thing that matters if it turns out they did not).

**How hard to undo.** Adding either later means asking every existing contributor to sign
retroactively, and the ones who cannot be reached are code that has to be removed or rewritten. Which
is why it is decided before the repository is readable rather than after.

### D46 — Nothing bakes in a public address

No URL of this instance is compiled in, signed into anything, or written into a stored record. Where
a public address is genuinely needed it is configuration, read at runtime.

**Because the address is the operator's, not ours.** This is self-hosted: there is no canonical
hostname, and a value that is right for one deployment is wrong for every other.

**And because an address inside a signed artefact is a migration.** A public site URL baked into
cryptographic checkpoint signatures has already been seen making a domain change into a data
migration rather than a DNS edit. #86's ledger is the place that hazard could recur, so the rule is
recorded before the ledger exists rather than after.

**Rejected:** a canonical domain in the code (wrong for every deployment but one); a compiled-in
default with an override (the default is what ships, so the override is what nobody sets).

**How hard to undo.** Easy while nothing is signed. Hard afterwards, which is the point.

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

**Amended by D35.** There is one tenant, so the hard boundary this describes is now trivially true
rather than load-bearing. The schema stays for the reasons that survive — erasure walks one namespace,
migration applies in one order, a backup has an obvious edge — and the project half of this entry is
unchanged and is the boundary that still does work.

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

## The graph

### D15 — The predicate vocabulary is closed, and a foreign key is what closes it

Thirty-nine relation types, seeded per tenant into `{schema}.predicate`, with `fact.predicate`
referencing it. Each carries a **cardinality** (`one` — at most one currently-valid fact per subject
and predicate; `many` — several coexist), an **object kind**, and a **semantic type**.

An extractor asked for a relation invents one per sentence: `works_at`, `employed_by`,
`is_employed_at`, `has_employer` — four edge types describing one relation. A traversal asking where
somebody works matches one and silently misses three quarters of what was extracted, and every row
involved looks correct. It also makes the stages after it unbuildable: supersession asks "is there a
current fact with this subject and this predicate", which only reconciles when two assertions chose
the same word, and a cardinality rule cannot be written for a vocabulary nobody can enumerate.

Cardinality is declared **when the predicate is defined**, because at supersession time all that is
visible is two rows and the question is no longer answerable. Its enforcer is the exclusion
constraint in #9; declaring it now is what lets that arrive as a constraint rather than as a redesign.

The size is bounded at 30–50 and asserted by a test. Too small and real relations have nowhere to go.
Too large and the near-duplicates are back, spread over a set nobody can hold in their head — which
is harder to see than an open vocabulary, not easier.

**Rejected:** an open vocabulary (above). A list in application code rather than a foreign key — that
is a convention every write path has to remember, and the path that forgets is the one written last;
as a constraint an unknown relation cannot be stored by any path, including a hand-written `INSERT`
during an incident. A generic `has_value` or `has_attribute` — a wildcard makes the closed set
vacuous, since anything can be phrased as a value of a compound object, and every claim the ontology
was meant to refuse arrives through it wearing an accepted name. Inverse predicates such as
`reports_to` alongside `manages` — edges are directed and traversal follows both ends, so an inverse
is a second spelling of one relation and reintroduces exactly the split this decision prevents. A
`mentioned` or `co_occurs_with` relation — an edge between two things that appeared near each other
has no direction and no meaning, so a multi-hop question gets no hops out of it. `ON DELETE CASCADE`
on the reference — retiring a predicate would delete the memory in order to permit a schema edit, so
it is `RESTRICT` and the change fails loudly.

### D16 — A relation outside the vocabulary is recorded as unmapped, never admitted as an edge

Not dropped, and not admitted.

Nothing is lost by refusing it, because the refusal is narrower than it looks: the message is already
stored as a row and retrievable as a passage, so what is refused is **the typed edge**, not the
memory. This is the same shape as the role policy — an assistant's sentence is still stored, it
simply does not become something the person is recorded as having said.

Admitting it is what cannot be allowed. One relation admitted outside the set means the set is
advisory, and an advisory vocabulary is an open one with extra steps.

The unmapped claim is kept with its evidence so the rate is a query rather than an estimate: a high
unmapped rate is evidence the vocabulary is wrong, and that argument needs a number. D18 is where
that record lives, and it holds the other silent refusal alongside this one.

**Rejected:** dropping unmapped claims silently (loses the evidence that the vocabulary is wrong,
and the failure is invisible precisely when it is worst); admitting them as edges with a flag (a flag
is a filter every future query has to remember, so the vocabulary is closed only in the queries
somebody remembered); mapping them onto the nearest admitted relation by similarity (records a
relation nobody asserted, and the wrongness is unrecoverable because the original wording is gone).

## Extraction

### D17 — A quote is a search term, never evidence. Whitespace is tolerated and nothing else is

The extractor takes the model's quote, locates it in the message, and stores **the text it found
there**. Every claim satisfies `content[ByteStart:ByteEnd] == Quote` by construction rather than by
the model's cooperation, and a claim whose quote cannot be located is discarded — not a weaker claim,
an uncitable one, in a product whose argument is citation.

Matching is exact first, then whitespace-tolerant, with the span mapped back to the original bytes. A
model copying text routinely turns a line break inside a sentence into a space; the words are the
same words, so refusing that discards a correct citation over a formatting artefact. A span into the
normalised copy would be the same failure from the other side, so it is never returned.

Case is not tolerated. A model that changed the case retyped rather than copied, and a tolerance wide
enough to match `moved to dublin` against `moved to Dublin` is wide enough to match a sentence the
model composed.

A quote appearing twice takes the first occurrence: the same words in both places resolve to the same
text, only the instance differs, and nothing reads the instance.

**Rejected:** trusting the span the model returns (a span that is off by one character resolves to
the wrong words while looking correct, and there is no way to notice); exact matching only (discards
correct citations over a newline); case-insensitive or fuzzy matching (no line left to draw once
retyping is admitted); embedding similarity between quote and message (a threshold nobody can
justify, applied to the one thing that must be verbatim); storing the model's wording alongside a
located span (a quote its own span does not reproduce — self-inconsistent, and it looks fine).

### D18 — A claim outside the vocabulary or without a locatable quote is written down, not counted

`{schema}.rejected_claim` keeps both refusals with the model's own wording, and its `predicate`
column deliberately has no foreign key.

Both refusals are silent and both are claims about quality nobody can check without the text. A rate
of eleven percent cannot distinguish one missing relation asserted five hundred times — the
vocabulary is wrong and the fix is one row — from five hundred asserted once, which is the tail and
is noise. Those need opposite responses.

It registers for erasure like every other projection: a refused claim still carries somebody's words,
and a table of "things we did not store" that turns out to store them is the worst version of this
feature.

**Rejected:** a counter per reason (cannot distinguish the two cases above, which is the only reason
to measure); an error log (both refusals are ordinary, so they would be noise inside the record of
things that are not); normalising the recorded predicate (the near-duplicates that justify adding a
relation are visible only in the exact words proposed).

## Formation

### D19 — Forming happens behind the append, and freshness is two numbers

A turn is stored immediately and formed afterwards. Forming means a model call per message; held
inside the append, an agent's turn would block on its own memory write and the product would be
slower than having no memory at all.

What that costs is a window where a turn is stored and not yet recallable, so the window has to be
readable. `watermark.log_offset` advances on append and says the turn was stored — which the caller
already knows, because the append returned. `watermark.formed_offset` is the highest offset whose
turn has been through extraction **with nothing unformed below it**, and it is the number a recall
depends on.

It is derived from the backlog, not incremented per completion. An increment is correct only if
completions happen in order, and two turns forming concurrently finish in whichever order their model
calls return — so the number would run past a gap and a caller told "current to 40" would be missing
turn 12. That is the one failure a freshness number must not have, because it is acted on rather than
read.

`formed_offset` is NULL until the scope's first turn forms, not -1. Offsets start at zero, so a
sentinel is a value in the same domain as the data and every comparison carries a clause to exclude
it that one query eventually forgets.

One worker per scope, held by an advisory lock across the whole drain. Two workers taking the same
turn would assert every fact twice, and the duplicates would be indistinguishable afterwards from a
person saying the same thing in two conversations — a lock inside the transaction that picks the turn
is released before the model call it was meant to protect.

**Rejected:** synchronous formation (correct recall immediately, at the price of an append that waits
on a model); redefining the single watermark to mean formed (loses the answer to "did you receive
it", which is what an append is asked to confirm); incrementing the formed offset per completion
(above); a tenant-wide formation lock (one busy project would starve every quiet one beside it).

### D20 — The role applied to a claim is the message's, never the turn's

Formation passes each message's own speaker to the fact table.

A turn opened by the user does not make an assistant message three positions later speak for the
principal — and turns opened by the user are most turns, so passing the turn's role would quietly
promote nearly every model sentence in the system.

Every message is extracted, including ones whose claims will then be refused. Skipping them would
save a model call and produce identical rows, but only because formation would be applying the role
policy itself, in a second place, with reasoning that has to stay in step with the first. The policy
lives on the one road to the fact table so that a path which forgets it does not exist.

Refusals by role are counted and **not** written to `rejected_claim`. Unlike the other two refusals
this is not a quality signal — it is the policy working as designed, and its rate is already knowable
from the roles of the messages in the turn.

**Rejected:** the turn's lead role (above); a role on the observation only, with no per-message column
(the policy would be unenforceable for every message after the first); skipping extraction for
messages that cannot speak for the principal (duplicates the policy to save a call).

### D22 — Message content leaves only to a host somebody wrote down

`TAISCE_INFERENCE_ALLOWLIST` names the permitted hosts, separately from the endpoint, and a
disagreement between them is fatal at startup.

Extraction sends a customer's message text to a model. That is an egress of exactly the data this
product exists to hold, and the destination is a string in an environment variable — one typo, one
copied deployment file, and the content goes somewhere nobody chose. Unlike most misconfigurations it
cannot be undone once noticed.

Matched on host **and port**, never by prefix: a prefix match accepts
`https://inference.example.attacker.test` for a list containing `inference.example`, which is the
failure an allowlist exists for. Plaintext is refused anywhere but the local machine — listing a host
says where content may go, not that it may go there in the clear.

An empty allowlist permits nothing. Listing a host is a deliberate act, and that is the whole
mechanism: it cannot be arrived at by default, only by somebody writing the host down.

**Rejected:** deriving the permitted host from the endpoint (there is then nothing to disagree with,
so a typo configures its own approval); a warning rather than a refusal (read after the egress it was
meant to prevent); open when unset (one forgotten variable away from not being a guard); a single
combined variable (the same string cannot both propose a destination and authorise it).

### D21 — Evidence names the message it indexes, not only the turn

`fact_evidence.source_ordinal`, in the primary key.

A span indexes one message and an observation is a whole turn. Without the ordinal, resolving a
citation means joining to the turn's messages with nothing to choose between them — and a byte offset
taken against the wrong message does not fail. It returns a plausible fragment of a different
sentence and attributes it to the fact as its evidence, and the two disagree only if somebody reads
them side by side.

In the key rather than beside it, because one fact can be evidenced from two messages and two spans
starting at the same offset in different messages are different evidence. Outside the key the second
silently replaces the first.

**Rejected:** the ordinal as a plain column (a duplicate key would drop a citation); one chunk per
turn so the span indexes the turn (a span into a concatenation that exists nowhere); resolving by
searching the turn for the quote (the search is what produced the span in the first place, and it
would find the wrong occurrence exactly when two messages restate each other).

## Recall

### D23 — Anchors are resolved by windowing the question, not by a model call

Every window of up to four words in the question is normalised the way entity names are and looked up
against canonical names and aliases.

A model call per question would put a second inference hop in front of every read, on the path a
caller is waiting on, to answer something a lookup already answers: an entity is in memory or it is
not, and its name is a string we already hold. Windowing is exact, has no vocabulary of its own, and
costs one query.

Multi-word windows are what make an entity reachable by the name it is stored under. Single words
alone leave every two-word entity unreachable. Aliases are matched alongside canonical names because
a person asks about something the way they mentioned it, and the alias list is exactly the record of
how they mentioned it.

**Rejected:** named-entity recognition by a model (a second inference hop on the read path, with its
own vocabulary that can disagree with what is stored); single words only (two-word entities become
unreachable by name); full-text search over entity names (ranks, and a ranking that nothing measured
is a stage doing unknown work); trigram or fuzzy name matching (it is D6's over-merge problem moved
to read time, where it is invisible instead of inspectable).

### D24 — The authorised project set is an argument on every read, and empty returns nothing

A project is a permission inside a tenant, so `scope = ANY($1)` is on every statement and the set
comes from the caller.

Empty returns nothing rather than everything. The failure being prevented is a read that WIDENS when
its permission list is missing — the one mistake a permission list exists to stop, and the one that
is invisible in a test suite where every caller happens to pass a scope.

This is the soft boundary and it is deliberately not the hard one. Cross-project reads are intended:
a principal granted several projects recalls across them in one bundle. The hard boundary is the
schema name in the statement, which no predicate can be wrong about.

**Rejected:** a scope held on the reader and defaulted (a default is a decision taken once for every
future caller); empty meaning all (fails open, and fails open exactly when something upstream
forgot); a project as a schema (would make the intended cross-project read a cross-schema query).

**Amended by D43.** A token belongs to one project, so the authorised set has one member and the
cross-project bundle this entry contemplated is not available to a single credential. Everything else
here is unchanged and matters more: the set is still an argument on every read rather than a default,
and an empty one still returns nothing — that property is what makes a revoked or unattached token
harmless rather than dangerous.

### D25 — The first recall ranks nothing, and a bundle that was cut says so

Facts come back in a deterministic order that carries no judgment — predicate, then id. Not recency,
not confidence, not salience.

Each of those is a stage that can only be justified by a measurement it improves. Introduced before
the plain anchor-and-return path is measured, they make it impossible to say which stage is doing the
work or which is doing harm — and one of them usually is.

The limit is a cap, not a selection, so `Truncated` is on the bundle. Without it a caller reads the
cut as a verdict: "these are the important ones" is the one thing this bundle is not claiming.

A fact with no evidence is not returned. That is a product statement rather than an oversight: what
is sold is a memory that can show its receipt.

**Rejected:** a recency lens, reciprocal rank fusion, a cross-encoder reranker (all deferred until a
measurement justifies them — see G7); ordering by confidence (confidence is what an extractor
asserted, not what a reader needs, and using it as a rank quietly makes it one); silent truncation
(the caller cannot tell an exhaustive answer from a cut one, and will assume the wrong one).

### D26 — A projection kind declares where it lives, and an undeclared one cannot be registered

`{schema}.projection_kind` holds every kind of derived row and the table it lives in.
`projection_dependency.projection_kind` references it.

D9 made erasure walk a registered dependency rather than a list of tables. That leaves one thing
still held somewhere: a kind and an id do not say which table to delete from. As a switch inside the
eraser that mapping is a list somebody maintains, and every milestone after this one adds a
projection — embeddings, community reports, summaries. Producer and eraser drift in one direction
only: the producer ships and the eraser is updated later, or not at all. What survives is a residual
count that says zero because it counted the kinds somebody remembered.

As a foreign key, a projection kind that is not declared **cannot be registered at all**. The failure
moves from "erasure quietly misses it" to "the write fails the first time it runs" — a failure landing
on the person adding the projection, on the day they add it. And because the eraser iterates the
declarations rather than a list of its own, a kind declared later is covered without the eraser
changing.

The table and column names are constrained to `[a-z][a-z0-9_]*` at the source, because they are
interpolated into a `DELETE`: PostgreSQL parameters are values and never identifiers, so the
constraint is what makes the interpolation safe where the name is written rather than where it is
used.

**Rejected:** a switch in the eraser (above); a test asserting the eraser covers every kind seen in
`projection_dependency` (only fails if the test's write path happens to produce the new kind, so the
projection most likely to be missed is the one written by a path nobody added to the test); erasure
by scanning every table for a `data_subject_id` column (an entity has none, and a projection that
does not carry one would be invisible while looking covered).

### D27 — A projection shared with another subject is kept, and keeping it is not a residual

The deletion predicate removes a projection only when every registration of it belongs to the subject
being erased. The residual is counted with that same predicate.

An entity is the case that decides it. `Ensera` is registered by every subject who mentioned it, and
deleting it because one of them asked to be forgotten would take a node out of everybody else's graph
— an erasure that damages the people who did not request one. Counting it as a residual instead would
be worse: every erasure would report a non-zero residual for correct behaviour, and the number would
stop being read.

**Rejected:** deleting every registered projection (damages other subjects' graphs); keeping shared
projections but counting them as residual (makes a clean erasure indistinguishable from a failed one,
so the number is ignored); a reference count on the projection row (a second place the truth lives,
and it drifts the moment a delete happens outside the path that maintains it).

### D28 — The residual is counted before the observations are deleted, in the same transaction

Projections are deleted through their registrations, counted through the **same** registrations, and
only then are the observations removed.

Deleting the observations first cascades the registrations away, and the count is then made against a
predicate that selects nothing — a residual of zero meaning "I could not find anything to check"
rather than "nothing survived". That is the failure mode of a residual count: it is far easier to
produce a zero that is vacuous than one that is earned.

Counting after the commit would be a separate read of a moving target: a concurrent formation writing
a new projection for the same subject appears as a residual the erasure did not leave, and two
concurrent erasures of one subject each report the other's work as their own clean sweep.

**Rejected:** counting after the transaction commits (above); deleting the observation first and
relying on cascade for everything (the cascade is what makes the count vacuous, and an entity has no
foreign key to cascade through anyway); a residual in a log line rather than a column (D10 — it is
the artefact the product is sold on, and a log line is not something a customer can be shown).

## The control plane

### D29 — One database, two schemas, two database identities

The registry — tenants, projects, principals, credentials, plans — lives in its own schema, owned by
its own login role. The memory service connects as a different role with no privilege there at all.

**Why the boundary is a grant and not a rule.** The memory service serves one tenant per statement,
and its entire boundary is that another tenant's name is not in the statement it is running. A
component that can list every customer, read every credential and read every subscription is one bad
predicate away from crossing that. Expressed in code, "the memory service does not read the registry"
is something every future query has to remember, and the query that forgets is the one written by
hand during an incident. Expressed as a grant, no statement can break it.

**Why one database rather than two.** Creating a tenant is a registry row, a `CREATE SCHEMA` and a
full migration run. DDL is transactional here, so in one database that is one transaction and it
either all happened or none of it did. Across two databases it is not atomic and needs a reconciler:
a registry row with no schema is a customer who can authenticate and has nowhere to write, and a
schema with no row is storage nobody bills and nobody reclaims. Two databases buy a stronger
isolation than the grant already provides, and they buy it by making the most safety-critical
operation in the system non-atomic.

**What this boundary does not buy, stated so nobody claims more.** It does not separate tenants from
each other. One process serves every tenant, so it necessarily holds privilege on every tenant
schema, and the separation between them remains D8's — the schema name in the statement. It also
cannot hide the *existence* of other tenants: every role can read the system catalogue, and that is
not revocable without breaking ordinary tooling.

Neither is a shortfall. A component that already holds every customer's memory learns nothing from
the list of their names. What the grant protects is a different category — credentials,
subscriptions, and the people behind the accounts — and none of that is memory.

**Rejected:** one identity with the rule in application code (a convention every query must remember;
the one that forgets is written under pressure); two databases (above); a single schema with the
registry beside the tenants (there is then no boundary to grant across, only a naming convention);
building the registry first and fencing it afterwards (it is readable by the memory service for
however long that takes, and every query written meanwhile assumed that was allowed).

**Deferred rather than rejected:** running the two planes as separate processes. The grant is what
makes the guarantee, and process separation adds isolation on top of it without changing the data
model — so it is a deployment choice that can be made later, and making it now would buy nothing that
is not already bought.

**Amended by D35.** The registry it separates has no reader in a single-tenant instance, so the
control plane as a *commercial* boundary is retired. The least-privilege half is kept and matters more
than before: the portal is a new reader of personal data, and a role that cannot change the schema is
the difference between an operator surface and a database client.

### D30 — A credential is exchanged for a short-lived signed grant, verified without a round trip

The control plane resolves a credential to a tenant schema, an authorised project set and an
entitlement set, and issues them as a short-lived signed grant. The memory service verifies the
signature with a public key and reads nothing.

**Why not ask the control plane per request.** Recall is on the critical path of every agent turn.
A lookup there puts a network hop on every read and makes the whole product's availability equal to
the registry's — so a control plane that is down stops every agent that is already working, not just
the ones trying to sign up. Verifying a signature offline keeps memory serving through an outage,
and what stops is issuing new grants: the right thing to lose.

**Why not let the memory service read the registry.** D29.

**Why entitlements travel with the grant.** A bundle constrains in both directions, and an enforcer
that fetches the promise before enforcing it fails open the moment the fetch fails. Carried in the
grant, enforcement is local and a failed lookup is a failed request rather than an unlimited one.

**The cost, and the thing that bounds it.** Revocation is not instant: a grant remains valid until it
expires, so the lifetime is the revocation window and it is a number somebody has to choose rather
than a property to hope about. Where instant matters — a compromised credential, or a subject whose
erasure must take effect now — the control plane publishes a list of revoked grant identifiers, which
is small, contains nothing about anybody, and can be replicated to the memory service without giving
it a single privilege in the registry.

**Rejected:** a lookup per request (above); a long-lived grant (makes the revocation window a
liability rather than a bound); a shared secret rather than a signature (every party that verifies
can also mint, so the memory service could issue itself any authority it liked); putting the
credential itself in the grant (the memory service would then hold something it could replay against
the control plane, which is the authority it was deliberately not given).

**Superseded by D35.** A credential resolving to a tenant and its entitlements describes a product
that no longer exists. What replaces it is narrower and still required: a credential resolves to an
authorised project set, and an empty set returns nothing. The issue that owns the replacement says so.

### D31 — The read audit is append-only, and it holds nothing that could ever need erasing

Who accessed a project, with which credential, when, and how much came back. Not whose memory it was,
and not what was asked.

**The two properties only fit together because of the second one.** An audit trail an erasure can
delete is not an audit trail — the record of who read a person's memory would vanish on the request of
whoever wanted it gone, and that request is the last thing that should be able to do it. An audit
trail an erasure cannot touch is personal data surviving an erasure, which the product exists to make
impossible. The way out is to record nothing that an erasure could ever be asked about, which makes
append-only unconditional rather than a conflict to manage.

So no data subject identifier, and **no query text** — a question is content, sometimes the most
sensitive content anyone sends, and a hash of a short question is guessable rather than opaque.

**What this can answer:** an unusual volume or pattern against a credential, which is how a
compromised key is found; and which of a customer's own principals reached which project, when.

**What it cannot answer, stated as a loss rather than omitted:** a data subject asking who read *their*
memory specifically. Answering that would mean recording, permanently and un-erasably, that a
particular person's data was read — which is a worse thing to hold than the question is worth
answering.

**Recorded behind the response, not before it.** Recall is on the critical path of every agent turn,
and a synchronous insert makes every read wait on a write that no caller is waiting for. The cost is
that a process dying between the response and the record loses that entry, which is acceptable for a
trail whose purpose is pattern rather than proof of any single read.

**Rejected:** the subject identifier in the record (above); a pseudonymous or hashed subject (still an
identifier, and it would be the one un-erasable fact about a person in the system); deleting audit
records with the subject they name (makes the trail unreliable exactly when it matters); the query
text or an embedding of it (content, in a log that can never be cleaned).

### D32 — Inside a subscription the customer's clock governs retention. Outside it, ours

`retention_until` is set from a policy the customer configures. We enforce what they set and impose
nothing. A customer who configures no policy keeps everything.

Retention is their judgement about their own obligations, and those differ by industry, jurisdiction
and contract in ways no default of ours could be right about. A plan-derived expiry would also change
what happens to data already stored when somebody changes plan, which means a commercial decision
silently rewriting a compliance one.

The one clock that is ours is the one that runs when there is no subscription at all — see D33. That
line is worth stating because it is the whole of it: while they are a customer, nothing of theirs
expires except on their own terms.

**Rejected:** a retention default derived from the plan (a commercial decision rewriting a compliance
one, retroactively, on downgrade); a platform maximum (an opinion about somebody else's obligations);
no retention mechanism at all (three columns carrying an expiry that nothing reads is a cost with no
mechanism behind it).

**Superseded by D39.** There is no subscription, so there is no second clock. Retention runs under
the operator's policy, unconditionally — which is what this entry argued was correct whenever it was
possible.

### D33 — A lapsed subscription gets a grace period, then the tenant's schema is dropped

Not suspended indefinitely, and not deleted on the day a payment fails.

Storing a customer's memory forever after they stop paying makes the product free to anyone willing to
let a card expire, and unbounded storage from the cheapest action anybody can take. Deleting at the
moment of failure destroys a year of somebody's memory because a card expired and a notice went to
spam. A bounded grace period is the only shape that is neither.

**Export works throughout the grace period, and so does erasure.** Those are rights that do not depend
on a commercial relationship, and a product that withholds them to collect a debt is holding data
hostage. The customer is told, more than once, what will be deleted and when — a deletion nobody was
warned about is the same failure as a deletion nobody asked for.

**The mechanism is dropping the schema, which is the strongest erasure available here.** A tenant is a
schema, so there is no sweep, no walk over registered projections, and no residual to count: the
container is gone, so residual is zero by construction rather than by the correctness of a routine.

**Two consequences that follow and should not be discovered later.** Anything held in that schema goes
with it, including the tenant's own verification log — so a receipt issued to a departing customer
becomes uncheckable once the tenant is gone, and the export they were urged to take is the only thing
that survives. And an abandoned account that never paid at all is the same path: signing up is the
cheapest action anyone can take, and a tenant created and forgotten is a schema, a partition and an
index sitting in the substrate forever.

**Rejected:** indefinite suspension (unbounded storage from a free action); deletion on first payment
failure (a card expiring is not a request to be forgotten); withholding export from a non-paying
customer (holding data hostage); marking rows deleted rather than dropping the schema (leaves the
residual question open when the answer could have been structural).

**Superseded by D39.** Nothing lapses. Dropping the schema remains the strongest erasure available
and is now an operator action rather than a billing consequence.

### D34 — One verification log per tenant

The log's origin names the tenant, its checkpoints are the tenant's, and the anchor a client retains
is about their own history and nothing else.

A tenant is a schema and the boundary is that another tenant's name is not in the statement. A single
log across all customers would be a structure whose size discloses our total governance volume, and
which any one customer verifying it would learn the rate at which everybody else is erasing. Per
tenant, a customer's verification tells them everything about themselves and nothing about anyone
else — which is the same shape as every other boundary here.

The cost is more checkpoints to produce and, if it is ever offered, more for an auditor to hold. That
is the cost of the boundary and it is the same cost every other table pays.

**Rejected:** one log for the deployment (discloses volume and rate across customers, to any customer
who checks); a log per project (a project is a permission and not a boundary, so this would multiply
the structure without separating anything that is not already separated by scope).

## Method

**Amended by D35.** One tenant means one log by construction. The reasoning that a shared log
discloses other customers' governance volume no longer applies, and what remains is the simpler
version: the log covers the instance.

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

**The exception was invoked on 2026-09-07**, for the predecessor system this work continues — read on
request, to establish scope and to learn what it got wrong. The boundary held while reading it is the
one rule 3 already sets: **capabilities and findings cross, files do not.** What came back is issues
with their own arguments — #80, #81, and confirming evidence on #62 and #74 — each re-derived against
this codebase rather than justified by where it was seen.

Copying files is blocked separately and for a different reason: that repository carries no licence and
almost all of its history is authored under a company address, so publishing derived code under
Apache-2.0 is a question of who owns it rather than a question of engineering. #79 holds it.
