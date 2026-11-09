<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# What it would take to be state of the art — a design programme, 2026-11-09

Written from the read in [`2026-11-09-memory-provider-assessment.md`](2026-11-09-memory-provider-assessment.md)
and from the code at `c7de5bf`. Every claim about the current system below was checked in the code or
the migrations on 2026-11-09 and is marked where it was read. Nothing here is a measured number: no
latency, throughput or corpus figure exists for this system, and none is invented.

## The uncomfortable part, first

**"State of the art" is not a design target, and treating it as one is how a product acquires
machinery nobody can defend.** Fast, accurate and operable are three targets, and exactly one of them
can be pursued today, because the other two have no number attached. `docs/architecture/read-path.md`
and `internal/infra/pg/recallstore.go` already say why, and migration `0015` says it most plainly:

> Salience is a ranking signal, and recall deliberately ranks nothing yet — a ranking stage
> introduced before the plain path is measured makes it impossible to say afterwards which stage is
> doing the work and which is doing harm.

That reasoning is correct and it is binding. It also means the ordering of this programme is not a
preference: **the measurement is the first change, not the first chapter of documentation.** Every
accuracy change below is a ranking stage or a retrieval stage, and each is unfalsifiable until there
is something it can be shown to improve. Build them first and the result is a system that is
different, with nobody able to say whether it is better.

So the programme has a pass 0, and the honest statement about passes 1 and 2 is that they are
**designed and gated**, not scheduled.

## Pass 0 — make retrieval and latency falsifiable

Nothing else can be justified until these exist. Neither is a large build.

### 0.1 A retrieval measure, with its conditions — #142

`#142` is the open decision and D26 is the rule: a retrieval result is published with its conditions
or no number is published at all. What is needed is narrower than a benchmark programme:

- a corpus of conversations with questions whose correct answers are known, held outside the
  repository the way the AP corpus is (`docs/31-vocabulary-coverage-ap-news.md` is the precedent for
  how to report one);
- a harness that runs it against a real deployment, as `make test` does — a mocked retrieval measures
  the mock;
- one number per configuration, published with the extractor identity, the embedding identity, the
  corpus, the date and the hardware, exactly as the vocabulary-coverage run was.

The measure has to be answer-level, not retrieval-level: whether the bundle contained the fact that
answers the question, within the caller's budget. `recall@k` over fact ids is the cheap proxy and it
is the wrong unit, because this product's output is a budget-cut bundle, not a ranked list.

**Why this and not a published benchmark alone.** LoCoMo, LongMemEval and MemBench were read on
2026-11-09 (`docs/03-market.md`). Running one of them is worth doing and is not sufficient: none of
them measures a byte-anchored bundle under a character budget, which is the thing being sold. A
public benchmark makes the number comparable; a corpus of this product's own shape makes it true.
Both, labelled separately.

### 0.2 Numbers for "fast" — #155

No latency budget, throughput target or corpus size has been agreed, and `AGENTS.md` says so. Until
one is, every performance change in pass 2 is an opinion. What is needed is three numbers and the
load they are measured under:

- recall p50 and p95, at a stated corpus size, with surfaces off and on;
- observations per second sustained on the write path, with formation keeping up;
- the corpus size the instance is expected to hold.

They should be chosen as commitments the deployment can be held to, then measured on hardware that
can be held still. The GPU qualification runs show that hardware is obtainable.

**This also names the first bottleneck, which is currently unnamed.** From reading the path, the
candidates are: the traversal's lateral branches (up to 256 anchors × 2 directions × 64 rows × hops),
the per-fact evidence lateral with its `substring(convert_to(m.content,'UTF8'), …)`, and — when
surfaces are on — three sequential provider round-trips. 2.1 below says why the third is almost
certainly the largest, and it is the one that can be fixed without any measurement at all.

## Pass 1 — the read path answers with judgement instead of a hash

Gated on 0.1. Each item says what it would have to improve to be kept.

### 1.1 Ranking returns, as a stored, indexed, explainable number — #156

**Today.** `ORDER BY f.fact_id LIMIT 64` per direction, per entity, per hop, and `ORDER BY r.hops,
r.predicate, r.fact_id LIMIT 201` at the end. `fact_id` is a content hash (`sourceClaimID`), so both
cuts select by a SHA. At a hub entity, recall answers with whatever hashed low.

**Change.** Bring back `fact.salience` — the column migration `0015` removed for having no reader —
computed at formation and on compaction from signals this system already holds: recency of `valid`
and `recorded_at`, how many evidence rows support the fact, the extractor's `confidence`, the degree
of its entities, and explicit feedback (`/v1/feedback/record` already exists). Index
`(scope, subject_entity_id, salience DESC, fact_id)` and the mirror for `object_entity_id`, so the
branch stays **one bounded index scan** — the property `domain.Fanout`'s comment exists to protect —
while selecting the 64 that matter rather than the 64 that hashed low.

**Why not the obvious alternatives.**
- *Order by recency alone.* One signal, no index cost, and genuinely better than a hash. It is the
  fallback if 1.1 cannot be justified, and it should be measured as the baseline that salience has to
  beat — because if recency alone wins, salience is machinery with no reader again.
- *Rank with a model.* A model call on the read path is paid by every caller (rule 9) and produces an
  order nobody can explain, against a product whose claim is inspectability. See 1.5.
- *Rank at read time in SQL.* Any expression over the whole candidate set defeats the index and makes
  the read cost a property of the busiest entity, which is the cost curve this design exists to avoid.

**Reversibility.** One migration to add the column, one to drop it. The ordering is one clause. This
is as cheap to undo as a schema change gets, which is the argument for trying it rather than debating
it — once 0.1 exists.

**Kept only if** it raises the 0.1 measure against both the current hash order and the recency-only
baseline, and leaves the branch plan an index scan bounded by the cap (the existing
`TestTheFanoutCapBoundsTheRowsAHubEntityIsReadFor` is the shape of that check).

### 1.2 Anchoring becomes a fused hybrid, and stops being a cascade — #157

**Today.** The named anchor is an exact probe on `(scope, normalized_name)`. Semantic anchors run
**only when exact anchoring found nothing** (`composeRest(…, exact == 0)`). There is no lexical arm
anywhere: `tsvector`, `to_tsquery` and `pg_trgm` appear nowhere in the schema or the code.

Three consequences, all reachable by an ordinary question:

- A question that anchors weakly on one name never gets the better semantic anchor, because exact
  anchoring "succeeded". `"where does the Dublin office manager live"` anchors on `dublin` and never
  looks for the manager.
- A misspelling, an inflection or a different word order misses entirely. Normalisation is
  case and whitespace only, which is deliberate and leaves everything else unreachable.
- Aliases are spellings of one normalised key (migration `0051`), so the alias mechanism does not
  cover this either, by design.

**Change.** Run three arms for every question and fuse them: the exact key probe, a lexical arm over
entity names (trigram or `tsvector` — the choice is a measurement, not a preference), and the vector
arm. Combine by reciprocal rank fusion, which has no weights to tune and no training, and keep the
per-anchor `matched` marker saying which arm reached it — `semantic:0.83` already sets that precedent.

**The objection this has to answer**, because the read-path document raises it: a fuzzy match at read
time would be "an invisible merge of two entities instead of an inspectable one". The answer is that
an anchor is not a merge — it is a place to start a walk, it is labelled with how it was reached, and
nothing renames or merges anything. That is already the accepted argument for semantic anchors; the
lexical arm is the same argument with a cheaper retriever. What must not change is that no arm writes.

**Cost.** One GIN index per project schema on the normalised name. Needs `pg_trgm` in the substrate
image, which today ships `pg_stat_statements` and the vector extension — one line, and a dependency
with a reader.

**Kept only if** it raises the 0.1 measure on questions that name something stored imprecisely, and
does not lower it on questions that name something exactly — the second half matters more, because
exact-name precision is the current path's whole strength.

### 1.3 The surfaces compete under one budget instead of queueing

**Today.** Facts fill the budget, then reports, then passages, sequentially, each stopping when the
next item will not fit (`composeRest`). A highly relevant passage never competes with a weakly
relevant fact; it gets whatever the facts left.

**Change.** One fused candidate pool across the surfaces under the single character budget, with a
guaranteed floor of facts so the product's identity — answer from entities, with receipts — survives
a question where passages happen to score well. The fused order is reported, as the cut already is.

**Kept only if** it raises the 0.1 measure. If it does not, the fixed cascade is simpler, is already
built, and is already honestly documented as arbitrary.

### 1.4 A cut always says it was a cut — #151

`#151`: the fanout cap drops facts inside the SQL and `truncated` is not set, so a hub entity's
bundle looks complete. This is the one pass-1 item that needs no measurement to justify, because it
is not a quality change — it is the bundle telling the truth about itself. It should land first and
independently.

It also becomes the signal that 1.1 is working: once ranking exists, a capped bundle is a bundle
whose 64 best were returned rather than whose 64 arbitrary were.

### 1.5 A reranker is a qualification, not a feature

A cross-encoder over the ≤200 surviving candidates is what the retrieval literature would reach for
next, and it is the right thing to **measure** and the wrong thing to default to: it is a model call
on the critical path, paid by every caller, in a product whose current answer to "which reads call a
model" is "none do by default". If 0.1 shows it wins materially, it ships behind the same kind of
operator switch the semantic surfaces already have, reported in `controls` and degradable like every
other surface. If it wins marginally, it does not ship, and the reason is written down.

## Pass 2 — stop paying three times for the same work

2.1 needs no measurement to justify. The rest are gated on 0.2 naming them.

### 2.1 One question embedding per recall, and the surfaces run together — #154

**Today, checked.** `internal/entitycandidate`, `internal/reportcandidate` and `internal/passage`
each call `Embed(ctx, []string{query.Question})` — three identical calls. The read-path document
explains this as "each searches its own model space", and that is true of the *vectors*; it is not
true of the *model*. There is one configured embedding model
(`TAISCE_INFERENCE_EMBEDDING_MODEL`/`_REVISION`/`_ENDPOINT`): the surfaces hold separate generations
and check identity separately, but they send the same text to the same endpoint. And `composeRest`
runs them sequentially, so a recall with all three surfaces on makes **three sequential provider
round-trips for one question**, plus three sequential vector queries.

**Change.** Embed the question once per recall, per distinct model identity, and fan the vector out
to every surface whose active generation matches that identity. A surface whose generation is on a
different model keeps its own call and its own refusal — the existing per-surface identity check is
what makes that safe, and it stays exactly where it is. Then run the surface queries concurrently
rather than in sequence.

**Why this is first among the speed items.** It removes two of three network round-trips and
serialises nothing, on the one part of the read path that leaves the machine. No corpus, no hardware
and no benchmark is needed to know that two fewer calls to a remote endpoint is faster; what a
measurement would add is how much, which is 0.2's job, not a precondition.

**Cost.** None in dependencies. The risk is a correctness one: fanning one vector to a surface whose
generation was built with a different model. The guard already exists per surface and must be read
*before* reuse, not after — that is the test to name.

### 2.2 The citation context is computed once, at formation, not per row per read — #158

**Today.** The evidence join does
`substring(convert_to(m.content,'UTF8') from … for …)` per returned row, to produce 320 bytes of
context. That converts the whole message to bytes on every read of every fact it supports.

**Change.** Store the ±160-byte window on `fact_evidence` when the evidence is written. Quote, span
and message are immutable once stored, so the value cannot drift.

**Why it is nearly free in governance terms**, which is the part that would normally sink it: a new
place text lives is a new sweep erasure must prove. A column on `fact_evidence` is not a new place —
those rows are already deleted and counted through `projection_dependency`, and
`docs/architecture/governance.md` already names evidence quotes as source-owned rows that go when
their observation goes. A separate table would have been a new kind and a new residual key; a column
is not.

**What it does not cover.** The degradation story changes shape: today an erased message yields a
fact with an empty context through the left join. With the window stored, the context survives the
message unless it is deleted with the evidence row. That is a governance regression and must be
closed in the same change — the stored window is erased with the evidence, and a test is named for
it. If that cannot be made exact, this item does not ship; a faster read is not worth a quote
outliving its message.

### 2.3 The anchor cliff becomes a ranked cut

257 matching entities refuses the whole recall with `400 invalid_question`. The reasoning is sound
while nothing ranks: an arbitrary 256 could silently drop the speaker or one side of a real
ambiguity. Once 1.1 exists, the better behaviour is to rank the anchors, keep the top *n*, and report
the cut — a question about a common word gets an answer instead of an error, and the traversal cost
stops being linear in however many entities a word happens to match.

**Strictly gated on 1.1.** Without ranking this change replaces an honest refusal with a silent
arbitrary truncation, which is worse than the cliff.

### 2.4 Formation stops paying a model call for every message

The write path's cost is one extraction call per message, and most messages in a conversation carry
no durable claim — the AP run is the evidence that refusals dominate on unsuitable text (0 facts from
535 articles, 2,651 `not_current`). A cheap gate in front of the extraction call — not a model —
would cut provider cost materially.

This is the item with the worst failure mode in the programme: a false negative is a fact that is
never learned, silently, forever. So it ships only with a measured false-negative rate on the
extraction corpus, it is off by default, and the gate's decision is recorded per message so a
operator can audit what was skipped. If the false-negative rate cannot be driven near zero, the
answer is that extraction is the cost of the product.

## Pass 3 — the instance can be watched, and a recall can be explained

### 3.1 Metrics exist — #143

`#143`. There is no `/metrics` route anywhere — the mux carries `/health`, `/ready`, the portal,
management and `/v1`. The operator this product names as its second reader has two booleans.

The series that decide operability, and no more: formation backlog depth **and age** per project
(age is the SLO; depth is a number that looks fine while the oldest item rots), recall latency by
surface, extraction and embedding provider latency and refusal rates by reason, admission refusals by
kind, pool saturation, erasure residuals, and ledger write failures (which today are logged and
swallowed).

### 3.2 A recall can explain itself — #160

`reach`, `degraded` and `controls` are already better than most of what was read on 2026-11-09, and
they stop short of what an operator debugging a bad answer needs: which arm proposed each anchor, how
many candidates each surface returned, what each scored, and what the cut dropped. An `explain`
control that returns that — never on by default, because it is a different cost — turns "memory gave
a bad answer" from a mystery into a lookup. It is also how 1.1, 1.2 and 1.3 get debugged while being
built, which is why it belongs with them rather than after them.

### 3.3 "Highly available" becomes testable

The chart is HA by default and no RTO or RPO has been stated, so the claim cannot fail. Two numbers,
then a test that kills a replica and a database and measures against them.

## Pass 4 — the seams

- **Pin the MCP protocol revision** (#146). A public interface whose shape is decided by an SDK
  default is one dependency bump from changing silently.
- **Refuse a predicate with no object kind** (#159). Every seeded predicate has one, so nothing is broken
  today. But D25 lets an operator add a predicate, `object_kind` is nullable, and the traversal's
  `step.other IS NOT NULL` means a fact with no object entity is never returned at all. An operator
  can therefore add a relation that extracts, stores, and is invisible to recall. Fail closed: refuse
  the predicate, or make the walk return objectless facts. The first is one constraint; the second is
  a change to the traversal.
- **Decide whether 29 person-centric predicates is the ceiling.** The vocabulary was written for what
  an agent learns about a person in conversation, and the AP run measured what that costs on other
  text. With `#141` opening operator-added predicates, the question of whether the shipped list grows
  is now a product decision rather than a schema one.

## What is deliberately not proposed

The most important list here, because each of these is what "state of the art" is usually taken to
mean, and each would cost more than it returns.

| Not proposed | Why |
|---|---|
| A vector database, graph engine, broker or object store | PostgreSQL being the only required dependency is a property, not a limitation: every addition is a second place data lives, a second thing to operate, and a second sweep an erasure has to prove it covered. None of pass 1 or 2 needs one. |
| A read-through cache in front of recall | Another copy of content, with its own erasure sweep and its own staleness, to solve a latency problem nobody has measured. Refused until 0.2 names it as the bottleneck — and 2.1 and 2.2 are the cheaper answers if it is. |
| A model reranker on by default | A model call on the critical path, paid by every caller, producing an order nobody can explain. Allowed as 1.5, behind a switch, after measurement. |
| A model consolidating the graph on the write path | Unbounded cost and a model deciding what memory believes. Extraction is already the one place a model touches the write path, and it is fenced, quoted and refused into a closed vocabulary. |
| Learned ranking as the primary order | The product sells a bundle a reader can audit. An embedding-derived order cannot be explained to the person asking why a fact was dropped. |
| Removing or raising the fanout cap | Hub entities are the first bottleneck a traversal has. The fix for a capped hub is ranking (1.1) and an honest signal (1.4), not a bigger cap. |
| Multi-tenancy beyond one project per credential | The deployment model is one instance, one tenant. Per-record labels and per-person credentials are a different product with a different threat model. |

## What must not change

Any of the above that threatens one of these is wrong, whatever it measures:

1. **Every fact carries the words that produced it**, as an inner join, to the byte.
2. **An erasure can count what is left**, in one transaction, with one filter.
3. **PostgreSQL is the only required dependency.**
4. **No read calls a generation model by default.**
5. **Nothing a model produces is trusted on the way to storage.**

These are also, on the 2026-11-09 market read, the only properties nobody else was found to hold
together. A programme that trades one of them for a retrieval score has traded the thing that is hard
to copy for the thing that is easy to.

## The order, as one journey

0.1 and 0.2 → 1.4 (independent, needs nothing) → 2.1 (independent, needs nothing) → 1.1 measured
against a recency-only baseline → 1.2 → 3.2 beside them → 1.3 → 2.2 → 3.1 → the rest.

Two of those — 1.4 and 2.1 — are work that can start today and need no number to justify. Everything
else waits on a measurement, and that is the finding, not an excuse.

Tracked as #161. The items carrying an issue number above are the roadmap; 1.3, 1.5, 2.3, 2.4 and
3.3 are designed here and named in #161 without one, because each is gated on a measurement that
does not exist yet and an issue opened before its gate is scope nobody can close.
