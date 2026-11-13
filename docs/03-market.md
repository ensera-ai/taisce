<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# What is already shipped elsewhere, and what it obliges here

**This is the one document where naming another system is the point.** The register, the goals, the
issues and the code argue from first principles, because a decision that needs a competitor's name to
justify it is not finished. Here the subject *is* the other system, so it is named — and it is kept out
of the register deliberately.

**It goes stale, and the date is the most important thing on every entry.** A capability list with no
date is a claim about today made from whenever somebody last looked. Nothing below should be cited
without its date, and an entry two releases old is a lead to re-read rather than a fact to quote.

**What crosses is the capability, never the implementation and never the reasoning.** What a thing lets
a developer do is a requirement here until it is deliberately rejected, because somebody evaluating
this will have seen it, and an absence they notice should be one that was decided rather than one that
happened. How it was built there was decided under constraints that are not these constraints.

**Labelling.** **Fact** — a source says so, and the source is named. **Inference** — it follows from
what a source says. **Unverified** — plausible, and nothing confirms it; every "none found" is
Unverified, because absence established by searching is not absence established. Nothing here is a
number measured in this repository, and nothing here was run.

---

## Read on 2026-11-09

Four parallel reads of primary sources — project documentation, repository pages, API references,
pricing pages, specifications and papers. No code was read from any of them and none was copied.
Tracking: [#140](https://github.com/ensera-ai/taisce/issues/140).

Reached: Mem0 (the Apache-2.0 repository and the hosted platform), Graphiti, Zep Cloud, Letta, Cognee,
LangMem with the LangGraph store, Supermemory, Basic Memory, MemU, MemOS, Memary, A-MEM, the Redis
Agent Memory Server, the MCP reference memory server, OpenAI's ChatGPT memory and its Conversations
state, the Anthropic memory tool, Vertex AI Agent Engine Memory Bank, AWS Bedrock AgentCore Memory,
Microsoft Foundry memory, Papr and Honcho; as substrate, pgvector 0.8.7, pgvectorscale 0.9.1,
VectorChord 1.1.1, ParadeDB 0.26.6, pg_textsearch 1.5.1, Apache AGE, LightRAG and RAPTOR; as seams,
the Model Context Protocol and thirteen agent frameworks; and as measurement, LoCoMo, LongMemEval,
BenchmarkQED, MemBench and five benchmarks published during 2026.

**Not reached, and outstanding.** The Neo4j Labs agent-memory project and its conformance kit, which
the previous read ([#30](https://github.com/ensera-ai/taisce/issues/30)) flagged for re-reading because
it covers five languages and not Java. Papr and Honcho could not be read: one host refused the fetch
and both pricing pages returned 404. AG2 was not read. The page fetches were summarised by a small
model, so quoted wording is a summary of a page rather than its text.

## The four stated differences, re-checked

The site states four properties as what is different. Each is re-checked below against what was read on
2026-11-09, two months after the read that settled them.

**Evidence to the byte, or no fact — holds.** No project or product read returns a quote with an offset
into the source that produced it. The nearest remains lineage at the level of the whole source:
Graphiti traces a fact to the episode it came from (**Fact**, github.com/getzep/graphiti), Zep states
provenance connecting facts to the episodes it derived them from (**Fact**, help.getzep.com/concepts),
and Basic Memory returns the matched chunk text of a note with no offset (**Fact**). Everything else:
not found, so **Unverified**.

**A receipt that counts what is left — holds, and the ground moved toward it.** No product read states
a deletion receipt or a proof of what remains. Mem0's delete returns a queued acknowledgement and an
`event_id`, and its own reference does not document a way to check that id (**Fact**,
docs.mem0.ai/api-reference/memory/delete-memories). Zep deletes a user and the whole user graph and
says nothing about a receipt (**Fact**, help.getzep.com/users). AgentCore deletes per record, and its
own documentation says expired records can still be returned by reads for days after their TTL passes
(**Fact**, docs.aws.amazon.com/bedrock-agentcore). Against that, the European Data Protection Board
adopted a coordinated-enforcement report on the right to erasure on 2026-02-10, covering 32
supervisory authorities and 764 controllers, which flags inconsistent use of the Article 17(3)
exceptions (**Unverified** at document level: the PDF would not extract and this rests on a summary).

**Entities and time on one database — holds as a conjunction, weaker on each half.** LightRAG is MIT,
builds a knowledge graph with vector retrieval, and PostgreSQL can serve all four of its storage roles
(**Fact**, github.com/HKUDS/LightRAG) — so entity-anchored retrieval on PostgreSQL alone is no longer
unoccupied. It carries no temporal validity (not found, **Unverified**). Of everything read, temporal
validity was confirmed only for Graphiti, which invalidates a superseded fact rather than deleting it
and needs Neo4j, FalkorDB or Neptune to run (**Fact**); Vertex Memory Bank has memory revisions
(**Fact**); Supermemory claims contradiction and expiry handling with no mechanism described
(**Unverified**). The conjunction of the two halves on one required dependency is still unoccupied.

**One contract for every adapter — holds, and the gap widened.** See the next section; this is the
difference with the most evidence behind it.

## The seams nobody else occupies

Read 2026-11-09 from each framework's own documentation.
Tracking: [#145](https://github.com/ensera-ai/taisce/issues/145).

Microsoft's own provider matrix for the Agent Framework, updated **2026-10-28**, lists Mem0 as
available for Python and **not** for C# or Go; the .NET side of that matrix carries Azure Cosmos DB,
Microsoft Foundry, Neo4j, Redis and Valkey — general stores and Microsoft's own (**Fact**,
learn.microsoft.com/en-us/agent-framework). The same absence was recorded on 2026-09-13, against a page
then dated 2026-08-31: **two months, unchanged.** Semantic Kernel documents a Mem0 provider for .NET
and marks it experimental, with Python and Java "Coming Soon", on a page whose own date is 2025-05-21
(**Fact**). Spring AI 2.0.2 lists six `ChatMemoryRepository` implementations, all general databases, and
names no memory vendor (**Fact**, docs.spring.io/spring-ai). LangChain4j lists eight `ChatMemoryStore`
implementations, all general data stores, and names no memory vendor (**Fact**,
docs.langchain4j.dev). Mem0's own integration list names 22 frameworks and includes none of Spring AI,
LangChain4j, Semantic Kernel or the Microsoft Agent Framework (**Fact**, docs.mem0.ai/integrations).

**What this obliges is not building anything** — the three adapters already sit on exactly these seams,
each with a conformance driver that passes against a live deployment. It changes what may be claimed,
and it answers the question of what would still be better here if the storage design were copied
tomorrow: occupying these seams is integration work in two ecosystems the market's Python-first
projects have not entered in the two months between two reads, and the conformance suite is the only
thing that makes "the same shape on every seam" more than an assertion.

## The market floor

Counted across the sixteen self-hostable projects read (**Inference**, tallied from the entries, and
weak because a project's own page is not an inventory): semantic retrieval in ten, a Python client in
eleven, deleting an item or a dataset in seven, entity or graph extraction in seven, an MCP server or
MCP tools in six, keyword or hybrid retrieval in six, a per-user scoping key in six, a TypeScript
client in five, background extraction in five, framework integrations in six.

Rare enough not to be floor: temporal validity (Graphiti alone), evidence to a source span (none),
a deletion receipt (none), a Java client (one, in a project with no memory feature), a .NET client
(**none found anywhere**).

**One floor capability is absent here: a TypeScript client.** That is the second language of this
market, not a niche, so the absence is written down as a decision rather than left to read as an
oversight — [#144](https://github.com/ensera-ai/taisce/issues/144), the sibling of
[#128](https://github.com/ensera-ai/taisce/issues/128).

## An extensible vocabulary is a capability, and ours is unreachable

An operator-extensible type set is shipped by several products: Zep meters custom entity and edge types
per plan tier, Graphiti takes custom entity types, AgentCore has custom extraction strategies, and
Foundry ships three memory types (**Fact**, each from the vendor's own page, 2026-11-09).

The capability that crosses is that a deployment's extraction fits the deployment's domain. What does
not cross is a model deciding the type set: the closed vocabulary is what makes a relation queryable, a
cardinality decidable and supersession possible, and a foreign key rather than a prompt enforces it.
An open set moves that per-message into a model's judgement, which is the failure the closed set exists
to prevent.

**The finding is that the adaptation surface the design does have is unreachable.** The vocabulary is
seeded by a migration, and no route, no command and no portal path adds a predicate, so fitting a
deployment to its domain requires connecting to the database as an identity the running service
deliberately does not hold — [#141](https://github.com/ensera-ai/taisce/issues/141).

## The field's numbers do not mean what they appear to mean

Read 2026-11-09. Tracking: [#142](https://github.com/ensera-ai/taisce/issues/142).

LoCoMo's answer key is wrong in 99 of its 1,540 questions, putting the ceiling at 93.57%, and its judge
accepted 62.81% of answers written to be vague and wrong (**Fact**, github.com/dial481/locomo-audit;
third-party and not peer-reviewed). One system's LoCoMo score is reported as 65.99%, 75.14% and 58.44%
by three parties who disagree about adversarial questions, timestamps and API version; all three are
recorded here and none is reconciled. Changing only the embedding model, with the rest of a pipeline
identical, moved LongMemEval-S accuracy by 6.2 points (**Fact**, arxiv.org/abs/2606.29914). A study
with planted facts and validity intervals found rankings invert with the time horizon, and full history
tying or beating the best memory system at three weeks (**Fact**, arxiv.org/abs/2607.21962). **No
agent-memory product read publishes a p50 or p95 retrieval latency with its conditions stated.**

**What this obliges.** Not a score. Publishing a bare number would add one more to a pile whose
conditions nobody states. What crosses is the obligation that a number carry its conditions — the
embedding model, the answer model, the judge, the corpus, the hardware, the run count and the write
cost — which is what none of the figures above do, and which this project's own rule on measured claims
already requires.

## Nothing outside the deployment can see how it is running

The operator is the second reader this product has, and an instance that can only be understood by
opening its own web page cannot be alerted on or left alone. `/metrics` returns 404 and the tree
carries no telemetry dependency — [#143](https://github.com/ensera-ai/taisce/issues/143).

The convention this would be emitted under is not settled: the OpenTelemetry GenAI semantic conventions
have moved to their own repository and are at **Development** status with no release, and while they
define a retrieval signal there is no memory-specific span (**Fact**,
github.com/open-telemetry/semantic-conventions-genai). MCP's 2026-07-28 revision deprecates its own
logging feature and names OpenTelemetry as the migration (**Fact**, modelcontextprotocol.io).
**Inference:** the stable part to adopt is the retrieval signal and the numbers an operator needs
first, not a full tracing surface that would be re-emitted when the conventions settle.

## Supply chain: ahead, and currently undermined from inside

Of seven comparable self-hostable projects checked on 2026-11-09, **two** attach signing or provenance
to their latest release — Cognee (sigstore and in-toto files) and LiteLLM (cosign instructions for its
images) — and **none** publishes an SBOM or a SLSA level statement in release assets (**Fact**, GitHub
API; "none in assets" does not rule out registry-side attestations, which were not checked).

This project's releases are signed keylessly with SLSA provenance and an SBOM attached, so the posture
is ahead of the sample. **The claim is undermined by this project's own open backlog**, not by the
market: dependencies are not checked against published advisories, and several shipped dependencies sit
inside the range of published advisories — [#122](https://github.com/ensera-ai/taisce/issues/122)
through [#126](https://github.com/ensera-ai/taisce/issues/126). Being ahead on the publishing path
while behind on the dependency path is worth knowing in that order.

## Read on 2026-11-13: a semantic layer for analytics agents

One project, read as source: NVIDIA's Auto Ontology, `github.com/NVIDIA/auto-ontology` at commit
`fc43a82` (committed 2026-11-09), Apache-2.0, created 2026-04-27 and carrying 12,406 stars on the day
(**Fact**, the repository). The README, the MCP server, the answerability and readiness code, the
compilation pipeline, the certification and publication references and the personal-data rules were
read. Nothing was run and nothing was copied. Tracking:
[#290](https://github.com/ensera-ai/taisce/issues/290).

**What it is, and why most of it has nothing to cross to.** It reads the schemas of connected
databases, drafts an ontology of business terms mapped to columns, has people certify the draft, and
answers questions by writing SQL against it (**Fact**, README). Its subject is somebody else's
tables; the subject here is what was said. Source connectors, query generation and repair, hosted
prediction and charts are a different product, and are recorded below only so the feature list is not
proposed again by the next person who reads it.

**Knowing before answering whether the store covers the question — held, by another route.** Its
`check_answerable` returns the share of a question's entities that have a hit within an
embedding-distance ceiling, and optionally the entities that have none (**Fact**,
`retrieval/entity_coverage/agents/coverage.py`). The entities come from a model call before retrieval
(**Inference**, from the README's table of model roles; that module was not read). A recall here
already reports what the question offered, what of it was found and where the facts came from, with
no model call and no threshold — and a distance ceiling is the mechanism this repository measured as
unable to separate a near miss from a true pair. So nothing crosses as a mechanism.

What the read did find is that the answer was computed and then left unread: a model reaching memory
over the protocol was never told the fields exist, because the explanation lived in one host's skill
files — [#288](https://github.com/ensera-ai/taisce/issues/288). Its readiness tool makes the same
point from the other side: three reads folded into one verdict, with what stands in the way listed
apart from what the caller is merely not permitted to see (**Fact**,
`mcp/auto_ontology_mcp/readiness.py`), on the argument that "not set up" and "not understood" look
identical from outside and are fixed in different places.

**A typed vocabulary that people have the last word on — ahead on relations, and open where nobody
had looked.** Its terms, synonyms and SQL expressions are drafted by a model and carry certification
fields; its own reference says those fields "are not an atomic review/promotion system" (**Fact**,
`skills/nvidia-ontology-management/references/publication.md`). Relations here are closed by a
foreign key and extended only by an operator. But reading what "typed" means on this side turned up
the one typed slot nothing bounded: an entity's kind was written by the model and held to the six
kinds by neither code nor schema — [#287](https://github.com/ensera-ai/taisce/issues/287) for the
write path, [#289](https://github.com/ensera-ai/taisce/issues/289) for the column.

**A small agent surface, chosen rather than generated — held.** Nine tools over an API of 87
operations, as an allow-list that fails at startup when a named operation is gone (**Fact**,
`mcp/auto_ontology_mcp/tools.py`). Six here, written by hand and pinned by a test.

**Usage and corrections improving the store — held; there is nothing there to match.** The README
states it. The usage score that would rank suggested expressions returns 0.0 for every input, and its
own test pins that as a known defect (**Fact**, `semantic/tests/test_usage_score.py`). Feedback here
changes nothing until somebody promotes it into a correction or a retraction, and then it does.

**Its knowledge graph, and ours — two graphs of different things.** Its nodes are databases, schemas,
tables, columns, saved queries, business terms and their attributes; its edges are foreign keys,
shared queries and relations between terms such as `IS_A`, `PART_OF`, `REPRESENTS` and `SEMANTIC_FK`
(**Fact**, `catalog/constants.py`, `semantic/constants.py`). It holds no fact about an individual
thing: rows stay in the source database (**Inference**, from the README). The graph here is the
facts themselves, each edge carrying when it held, when it was known, and the words behind it; no
time or evidence on an edge was found there (**Unverified**). Both are tables in PostgreSQL and
neither uses a graph engine (**Fact**, README). So neither is a superset of the other, and "the same
knowledge graph" is not a claim either side can make.

What its graph can be asked that ours cannot is two things, tracked in
[#294](https://github.com/ensera-ai/taisce/issues/294). It serves a path between two terms and a
paged list of a node's neighbours, and a view of the whole graph (**Fact**,
`server/exploration/router.py`; the path function itself was not read). Here a recall walks one or
two hops from each entity a question names, so a connection can be visible in an answer and nothing
states it as one; and the facts around one entity can be reached only by asking a question, which
returns a bounded, unranked sample, because the record inventory filters by data subject and by
nothing else.

## Rejected, so it is not proposed again

**A model discovering entity types**, for the reason given above: it moves a decision a foreign key
makes into a model's judgement per message.

**LoCoMo as a headline benchmark**, on the audit evidence above: a benchmark whose answer key is 6.4%
wrong and whose judge rewards vagueness cannot carry a claim.

**Role-based access control and single sign-on for the operator console, for now.** Of the sources
read, two name RBAC and two name SSO, against three or more for SOC 2, HIPAA, BYOK, audit logging,
deletion and self-hosting — so by the evidence collected it is a differentiator, not a floor
(**Inference**, from a small sample). One instance with one operator has no second principal whose
rights differ, and building the mechanism before there is one is building before there is a reader.
This is recorded so that it is not read as an absence nobody considered.

**A cache of model responses, for now.** Nothing read ships one, so this is not a market obligation; it
is a cost argument about rebuilds, and it would be a second place a customer's words live.

**Configuration of the model or the extractor from a page.** A live change to the write path made from
a browser session either breaks the chain between a fact and the extractor identity that produced it,
or starts a corpus-wide reinterpretation from a form submission.

**From the read of 2026-11-13.** *A coverage score on a recall*: it needs the question's entities,
which means a model on the read path, and a distance ceiling, which is measured here not to work.
*A certified flag on a fact*: nothing would read it, because facts are not ranked and D66 keeps it
that way until a measurement says otherwise; it becomes a question when
[#282](https://github.com/ensera-ai/taisce/issues/282) has a harness. *Detecting personal data*:
there it is pattern rules over column and table names (**Fact**, `pii_detection/rules.py`), which has
no counterpart in free text; erasure by subject with a counted residual is the mechanism here, and a
per-record class was removed as inert. *A generation model per purpose*: four roles with fallback
there (**Fact**, README); one model serves extraction, reports and compaction here and no measurement
says a second would pay. *Loading the vocabulary from a file*: adding a relation already answers
"already there" distinctly so that a script can loop, and a second format has no reader. *A graph
engine or a graph query language*: it uses neither. *A picture of the whole graph*: its graph is a
few dozen terms over a schema, and a fact graph is as large as its project. *Applying `instance_of`,
`part_of` or `same_as` when answering*: they are relations a fact can carry and resolution does not
apply them; doing so changes what an answer contains, and waits on the same measure as any other
change to the read path ([#282](https://github.com/ensera-ai/taisce/issues/282)).

## What this does not tell us

Nothing here is a comparison of quality, and no number in this document was measured here. Every "none
found" is an absence established by reading a page and searching, which is why they are labelled
Unverified: a vendor may ship something its own documentation does not list. Three absences rest on a
single search each and are the weakest lines in the document — that four vector-database vendors ship
no named memory product, that no PostgreSQL extension other than VectorChord serves late interaction,
and that no memory vendor ships a Java client. A head-to-head with numbers is
[#142](https://github.com/ensera-ai/taisce/issues/142)'s business, on hardware that can be held still.

Of the read of 2026-11-13: one project, and nothing run, so nothing in it compares quality. Its
benchmark harness lives in another repository and was not read, and neither was the exchange format
it imports and exports (both **Unverified**).
