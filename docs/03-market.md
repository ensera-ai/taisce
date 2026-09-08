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
