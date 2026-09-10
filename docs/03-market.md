# What is already shipped elsewhere, and what it obliges here

**This is the one document where naming another system is the point.** Everywhere else — the register,
the goals, the issues, the code — argues from first principles, because a decision that needs a
competitor's name to justify it is not finished. Here the subject *is* the other system, so it is named,
and it is kept out of the register deliberately.

**It goes stale.** Every entry carries the date it was read and the version it describes where one is
stated. A capability list with no date is a claim about today made from whenever somebody last looked.

**What crosses is the capability, never the implementation and never the reasoning.** What a thing lets
a developer do is a requirement here until it is deliberately rejected — because somebody evaluating
this will have seen it, and an absence they notice should be one that was decided rather than one that
happened. How it was built there was decided under constraints that are not these constraints.

**Labelling.** *Fact* — a source says so, and the source is linked. *Inference* — it follows from what a
source says. *Unverified* — plausible and nothing confirms it. Nothing here is a benchmark, a number or
a behaviour that was not read in a document or measured in this repository.

---

## Microsoft GraphRAG

Read **2026-09-07**, from the project documentation at `microsoft.github.io/graphrag` — the indexing
architecture, the default dataflow, and prompt tuning. No code was read and none was copied.

### What it does, as a capability list

**Fact.** The indexing pipeline is: compose text units from documents (chunk size configurable,
1200 tokens by default) → extract entities, relationships and optional claims with a model → summarise
entity and relationship descriptions with a model → detect communities with hierarchical Leiden →
generate community reports with a model, then summarise those reports → embed three things: text unit
text, entity descriptions, and community report text.

**Fact.** Claim extraction is optional and off by default.

**Fact.** It caches model responses: identical prompts and parameters return the stored result. The
stated reasons are resilience to network errors and throttling, idempotence, and speed.

**Fact.** It abstracts over storage behind a knowledge model, and exposes factories for language
models, input readers, caches, logging, storage, vector stores and pipelines, so a developer can
register their own.

**Fact.** It ships **auto prompt tuning**: sample text units from the deployment's own corpus (random
by default, or the k nearest to a centroid in a reduced-dimensional space), then run model calls and
template substitutions to write three prompt files — `extract_graph.txt`,
`summarize_descriptions.txt`, `community_report.txt`. A `--domain` can be given or inferred, and
`--discover-entity-types` lets the model decide what entity types exist. Defaults include 15 text units
sampled, a 200-token chunk size for sampling, and a 2000-token ceiling on the generated prompt.

**Fact.** The documentation calls auto tuning optional and "highly encouraged".

### What obliges us, re-derived

**1. A deployment's extractor should fit the deployment's domain.** This is the real capability behind
prompt tuning, and it is a requirement here. A generic extractor applied to a specialist corpus refuses
relations it has no name for, and the person running it has no way to find out.

*What does not cross is the implementation.* Generated prompt files read from a `prompts/` folder is
exactly what rule 14 and the embedded prompt close off: extraction feeds the write path, so a prompt
the filesystem can change is a path by which whoever can write next to the binary changes what ends up
in somebody's memory. And a prompt written by a model from sampled customer content puts that content
into an artefact nobody reviewed, on the path where a planted instruction becomes a stored fact.

*What we already have that answers it better.* The adaptable part here is a **table**: the predicate
vocabulary, with a description per relation, rendered into the prompt from the database that enforces
it. That is domain adaptation as a closed set in the substrate rather than as generated prose — a
relation the operator adds is one the extractor may use and the foreign key admits, and nothing about
the prompt's structure, its fence or its refusals changes.

*What we are missing, and it is the finding.* **The operator cannot reach it.** The vocabulary is seeded
by a migration; there is no operation, no command and no portal path to add a predicate. So the one
adaptation surface the design has is unreachable without writing SQL as an identity the running service
deliberately does not hold — #107.

*And they cannot tell what to add.* Knowing the vocabulary is wrong requires knowing what the extractor
is refusing, which is #43.

**Rejected, so it is not proposed again:** letting the model discover entity types. The closed
vocabulary is the mechanism that makes a relation queryable, a cardinality decidable and supersession
possible, and it is enforced by a foreign key rather than by a prompt. An open type set moves that
decision into a model's judgement per message, which is the failure the closed set exists to prevent —
four names for one relation and a traversal that finds a quarter of what it should.

**2. Response caching, which we do not have and which a scope rebuild needs.** Their reasons are network
resilience and idempotence. Ours would be cost: a scope-wide rebuild re-runs every extraction, and
extraction here was measured at forty to ninety seconds per message on a local model. A bounded
single-source generation exists, but it is still priced at one model call per message until a cache
with an erasure contract is designed (#58, #108).

*What does not cross is where it lives.* A cache of model responses keyed on prompt content is a second
place a customer's words live, and D1 refuses a second datastore precisely because it is a second sweep
an erasure has to prove it covered. If this is built it lives in PostgreSQL and registers as a
projection like everything else, which makes it erasable by construction — #108.

**3. Their entity embedding is over a model-written description.** We measured our re-derivation of that
and it did not separate a paraphrase from a near miss on our fixtures (D59). That is a result about our
surfaces, our pairs and one embedder — **not** a claim about their system, which we have not run. It is
recorded here because it is the one place where a capability crossed, was built, was measured and was
rejected on the evidence rather than on an argument.

**4. Deliberate divergences, with the reason, so they are not read as omissions.**

*Chunks are messages, not fixed token windows.* A message has a speaker, and the speaker is
load-bearing: an assistant's sentence never becomes the user's fact. A 1200-token window spanning four
turns has no single speaker, so the role policy could not be enforced per chunk.

*No storage abstraction and no factories.* They are a library that has to fit many deployments; this is
one service an operator runs. PostgreSQL is the only required dependency, and a pluggable store would
be a second place data lives, reintroduced through an interface.

*Community reports are stored and never shared* (D57), and every part must be countable after a delete.
That constraint is ours and shapes what a report may be.

### What this does not tell us

Nothing here is a comparison of quality, and no number in this section came from running their system.
The documentation says what it does; it does not say how well, and neither do we. A head-to-head is
#15's and #16's business, on hardware that can be held still.

---

## Microsoft LazyGraphRAG

Read **2026-09-10**, from the Microsoft Research announcement of 2024-11-25, the BenchmarkQED
announcement, the `benchmark-qed` documentation and repository, and graphrag discussion #1490. No
code was read, because none is published: the last maintainer statement (2024-12-09) names it the
next release and nothing has shipped in the open-source library since; it is offered inside
Microsoft Discovery and Azure Local. Tracking: [#148](https://github.com/althunibat/taisce/issues/148).

### What it does, as a capability list

**Fact.** Indexing uses no model. Noun phrases are extracted with NLP, a co-occurrence graph is
built over them, and communities are detected hierarchically. The stated index cost is identical to
vector RAG and 0.1% of full GraphRAG.

**Fact.** At query time a model expands the question into three to five subqueries, each subquery
ranks text chunks by embedding similarity and ranks communities by their best chunks, a model rates
the relevance of the top untested chunks sentence by sentence, the search recurses into
sub-communities after a run of communities yields nothing, and it stops when no relevant community
remains or the relevance-test budget divided by the subquery count is spent. The surviving chunks are
grouped by community, a model extracts claims from them, the claims are ranked and filtered, and the
answer is written map-reduce.

**Fact.** One parameter, the relevance-test budget, moves cost against quality. The announcement
measured budgets of 100, 500 and 1,500 on 5,590 AP news articles with 100 synthetic queries, and
claims quality comparable to GraphRAG global at more than 700 times lower query cost, and better
than vector RAG and DRIFT on local queries at comparable cost.

**Fact.** On BenchmarkQED, with GPT-4o writing every system's answers, it won all 96 pairwise
comparisons against vector RAG at three context sizes, GraphRAG local, global and DRIFT, LightRAG,
RAPTOR and TREX, across the four query classes and four criteria. The judge was GPT-4.1 or GPT-4o.
The announcement notes that data-local queries benefit most from vector RAG's direct chunk ranking,
so its relevance margin is narrowest there.

**Fact.** BenchmarkQED is MIT, Python 3.11 or later, and evaluates answer files offline: a system
produces `{question_id, question_text, answer}` per question set and the harness scores them. AutoQ
writes questions and assertions in four classes plus data-linked multi-hop questions typed bridge,
comparison, intersection and temporal; AutoE scores pairwise, against references, or against
assertions with significance tests. Its judge configuration names OpenAI and Azure providers and
admits a custom provider class; whether the OpenAI provider accepts a foreign base URL is
**unverified** here and is settled by running it.

### What obliges us, re-derived

**The bet is opposite, and the reason is the reader.** Their reader is a person with a question and
a budget; ours is an agent turn, and a memory injected into every turn cannot pay hundreds of model
calls to be assembled (rule 9, G1, G8). Their claims exist for one answer and are gone; ours are rows
with a verbatim quote, a validity interval and an erasure registration (G6). So every model call they
make at query time, we make once at write time, and nothing on the read path asks a model to write.
That divergence is the design and is not reopened here.

**What crosses is the spectrum.** Their single mechanism answers local, global and linked questions,
and their benchmark measures all of them. A developer evaluating this will ask the same four kinds of
question, and each kind must have a stored surface behind it. Each does: typed edges with two-hop
chains and bitemporal reads for the linked classes, exact anchoring for a named entity, entity
candidates (D110) for an entity named by meaning, hierarchical reports (D57, D58, D112) for a theme,
and passages (D106) for a statement the vocabulary did not admit.

**What we are missing, and it is the finding.** Recall reads only the first two. The other three are
separate routes a caller has to know about, so G5's promise that an unanchored question is answered
from themes holds only for a caller who composes the answer themselves, which G8 forbids putting in
an adapter. The decision to make recall the one composed read, in a fixed order and under the budget
D82 already defines, is [#149](https://github.com/althunibat/taisce/issues/149). Their budget knob
crosses as the budget we have; their search loop does not.

**The risk their benchmark would expose first is the vocabulary, not retrieval.** Their claims are
whatever a model can phrase about a chunk; ours are the relations a closed ontology admits, and it
was written for what an agent learns about people, organisations and places. Applied to health news,
much of what a judge rewards will have been refused as `unmapped_relation`. The unmapped rate
([#43](https://github.com/althunibat/taisce/issues/43)) is therefore measured on the benchmark corpus
before any quality number is quoted, because it decides whether the gap closes by falling back to
passages or by widening the vocabulary, and because without it a result measures the ontology and is
reported as retrieval.

**Rejected, so it is not proposed again.** A query-time relevance-test loop in recall. Its cost lands
on every agent turn, its claims cannot be counted after a delete, and it trades the flat read cost G1
exists for. A co-occurrence graph as the substrate, which G3 already rejects: an edge with no
direction and no predicate gives a multi-hop question nothing to follow.

### What this does not tell us

No number here came from running their system or ours on the same corpus. Their published wins are
against their comparison conditions, with their writer and their judge, on their datasets. Whether
this design is close in outcome is #15's business, on hardware that can be held still, and only after
#149 has made recall the thing being measured.
