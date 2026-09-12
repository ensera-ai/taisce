<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Vocabulary coverage on a news corpus — 2026-09-10

Taisce stores facts using a fixed list of relations, such as `works_at`, `lives_in` and `has_role`.
A claim whose relation is not on the list is refused as `unmapped_relation`. The list was written for
what an agent learns about people in conversation. This measurement asks how much of a real news
corpus that list can hold.

## What this means for you

- **The relation list covers most of what news asserts.** On the full corpus, 5.93% of proposed
  claims used a relation that is not on the list. Most of those were one-offs.
- **The biggest reason claims are refused is tense, not vocabulary.** Taisce stores only relations
  that hold now. A news story reports finished events ("voters adopted an amendment"), and those are
  refused as `not_current`. On the full corpus that was 2,651 claims against 1,194 unmapped.
- **Reporting verbs are not relations.** The most common unmapped words were `reported`, `said` and
  `denied`. They describe who said what, which Taisce keeps as provenance, not as a fact.
- **Every stored fact has a quote that was found in the source and a resolved subject.**
- **If you ingest news or other third-party documents, expect fewer facts per document than from a
  conversation.** Use passage search ([Message embedding generations](22-message-embeddings.md)) to
  find the source text itself. Recall does not fall back to passages on its own.

## The setup

The extractor was `Qwen/Qwen3.8-27B` BF16 at revision `1d4bf0f2ff6012fd82039f2fa52739d0dd7c60c0`,
with the guidance structured-output backend and the extraction prompt built into Taisce. That
identity is stamped on every row the run produced. The harness is `deploy/gpu/vocabulary-coverage.py`.

The corpus is 1,397 Associated Press health articles from 2023-11 to 2024-04: the corpus that
BenchmarkQED ships for its own evaluation. It is licensed to the operator under the Microsoft
Research License and is not in the repository.

**What the number is, and is not.** It is one extractor's admission rate on one corpus at one point
in time, broken down by refusal reason and, for unmapped claims, by the relation the model tried to
use. It is not a retrieval result, not a latency or throughput figure, and says nothing about the
extractor on conversations, which is what the relation list was written for.

## Where passes 1 and 2 ran

The models ran on a 4×H100 PCIe container in GPU.ai's asia-pacific region: the pinned vLLM image,
tensor parallelism across the four cards, 32 sequences, and the embedding model beside it on the
last card. The Taisce stack (PostgreSQL, API and twelve workers) ran on the operator's machine, with
its model endpoints reached through SSH tunnels to the container. The harness ran beside it. Article
text left the machine only as model input; nothing was stored remotely. Latency across that link
does not affect an admission rate and is not reported.

This was the eleventh machine tried that day. The provider's eight-A100 VM pool, which had run the
2026-09-09 qualification, gave nothing usable in eight attempts (boot errors, stalls in `starting`,
two machines with only seven cards visible), and its four-card VM stalled three more times. The
topology is sized by the cards actually present, so a day like that is survivable, and the container
was used when the VM pool stopped booting altogether.

## Pass 1 — under a data subject: zero facts

Every article was stored as one `tool`-role message under a made-up data subject, the way the recall
controls describe fetched document text. All 1,397 were accepted in eleven seconds across twelve
projects. Formation was stopped at 535 articles once the result was clear:

| Outcome | Count |
|---|---|
| articles formed | 535 |
| facts | **0** |
| refused, `not_current` | 1,918 |
| refused, `not_asserted` | 593 |
| refused, `unmapped_relation` | 319 |
| refused, `unlocatable_quote` | 121 |
| refused, `unresolvable_subject` | 5 |
| refused, `duplicate_claim` | 3 |
| refused by role | counted per attempt, never stored; one rebuilt article showed 21 of 28 |

At the time of this run, the fact store refused every claim from a non-user message whenever the
observation named a data subject, not only claims about that subject. So a document under a subject
could not form any fact. Today the rule is narrower: only a claim that would become a fact about the
data subject, from a message the data subject did not speak, is refused.

The vocabulary check runs first in the extractor, so the 319 unmapped refusals are a complete count.
What this pass could not give was the denominator, because claims that passed the extractor and were
then refused by role were not stored.

This pass also measured two other things. Eleven articles, averaging 10.7 KB against 4.5 KB for the
rest, hit the extraction client's two-minute timeout at least once while twelve workers shared one
server. The timeout is now an operator setting, and the run continued at fifteen minutes. Changing it
meant editing the extractor configuration, which changes the extractor's identity, so fourteen
sources with an earlier attempt were refused as "configuration changed" until they were explicitly
rebuilt. That is the identity pin doing its job, and it is why a rebuild command exists.

## Pass 2 — without a data subject

The same corpus, twelve fresh projects, no data subject on the observations, everything else the
same. Without a subject, the role rule does not apply, `tool`-role facts are stored under their own
role, and the denominator is complete: facts plus every stored refusal.

The operator stopped the run at 537 formed articles. The model server was the provider's container
at about eight articles a minute, and the eight-card VM pool that would have run the reference setup
gave nothing bootable all day. The figures below are a partial run over 537 of 1,397 articles, spread
in corpus order across twelve projects. The harness's own report is kept as `pass2-interim.json` with
the run's evidence.

| Outcome | Count |
|---|---|
| articles formed | 537 of 1,397 (partial; run stopped) |
| proposals (facts + stored refusals) | 7,482 |
| facts | 4,022 |
| refused, `not_current` | 2,092 |
| refused, `not_asserted` | 781 |
| refused, `unmapped_relation` | 430 |
| refused, `unlocatable_quote` | 147 |
| refused, `unresolvable_subject` | 9 |
| refused, `duplicate_claim` | 1 |
| **unmapped rate (unmapped / all proposals)** | **5.75%** |
| unmapped share of refusals | 12.4% |
| formed articles with no fact | 53 of 537 (9.9%) |
| facts per formed article | median 6, maximum 36 |
| distinct unmapped predicates | 228, of which 166 appear once |
| parked by the exclusion defect (below) at the stop | 40 |

**Unmapped predicates.** `reported` 25, `denied` 24, `said` 23, `is` 14, `died` 12, `announced` 8,
`told` 8, `killed` 7, `asserts` 6, `was` 6, `has` 5, `says` 5, then a tail of four or fewer:
`agreed to`, `filed`, `found`, `lost`, `sent`, `works_for`, `approved`, `called_on`, `caused_by`,
`directed`, `refused`, `repealed`, `violate` and 200 more. The head is attribution, the middle is
events, and the rest are one-offs.

**What the corpus did use.** Thirty-seven of the thirty-nine relations; only `has_timezone` and
`learning` never appeared. The most used: `has_role` 1,094, `intends_to` 393, `works_at` 371,
`located_in` 267, `has_status` 234, `lives_in` 164, `related_to` 122, `member_of` 111, `created` 109,
`participated_in` 108, `owns` 99, `prefers` 96.

**The decision.** No relation was added. A 5.75% unmapped rate, with a tail that is three-quarters
single occurrences, is what a list that is right looks like when the tail is noise. The head of the
tail is attribution, which is provenance rather than a relation.

**A defect this pass found.** Nine articles were parked after six attempts each, because inserting a
fact broke the rule that a single-valued relation has one current value per subject. Two claims in
one message share one occurrence time, so neither can replace the other. The second insert overlapped
the first, the store returned a raw database error, and formation treated it as a transport failure
and retried until the turn was parked with nothing stored. The right outcome is to refuse the second
claim with a reason of its own. That is what happens now: pass 3 shows it as `conflicting_value`.
Those nine articles are left out of the counts above and listed in the run's results.

## What the first two passes showed

**The biggest refusal on news is tense, not vocabulary.** `not_current` outnumbered
`unmapped_relation` six to one. The prompt defines `past` as a relation that held and no longer
does. That is right for a state and wrong for a reported event: "voters adopted an amendment" is
complete and stays true. In one case "is the Governor" was refused because its quote sat in a
past-tense sentence. Today the extraction prompt still stores only relations in the present tense,
so a reported event is refused as `not_current`.

**The unmapped tail is reporting verbs.** The most frequent unmapped predicates in the first pass
were `denied` (27), `reported` (26), `said` (17), `died` (9), `is` (9), `says` (8), `increased` (6),
`was` (6), `transferred` (5). Attribution verbs are the shape of news, and there is no relation for
them in a list written for what an agent learns about people.

## Limits

Claims refused by role are counted in formation reports and not stored, so the first pass's
denominator cannot be recovered from the database; that is why the second pass exists. Eleven long
articles needed a longer model timeout than the default, which the harness's results record. Every
number here is one extractor's behaviour at one point in time. A change to the prompt or the relation
list means running the corpus again before quoting a new number.

---

## Pass 3 — the whole corpus on eight A100s, 2026-09-11

Run on the eight-A100 machine described in [GPU qualification runs](23-gpu-qualification.md), with
the same extractor identity as pass 2: seven single-card replicas of `Qwen/Qwen3.8-27B` behind the
API, twenty-eight worker shards, and the corpus ingested with `taisce ingest` under the `tool` role
with no data subject. Formation finished in 5067 seconds.

| | |
|---|---|
| Documents offered | 1397 |
| Stored | 1387 |
| Refused at ingestion | 10 (see below — a defect, not a refusal) |
| Projects | 28 |
| Formed / parked | 1387 / 0 |
| Turns needing a second attempt | 1 |
| Proposals | 20124 |
| Facts | 13711 |
| Refused claims | 6413 |
| Unmapped rate | 5.93% of proposals, 18.6% of refusals |
| Documents yielding no fact | 99 |
| Median facts per document | 9 |

### Why claims were refused

| reason | claims |
|---|---|
| `not_current` | 2651 |
| `not_asserted` | 1904 |
| `unmapped_relation` | 1194 |
| `unlocatable_quote` | 354 |
| `conflicting_value` | 282 |
| `duplicate_claim` | 15 |
| `unresolvable_subject` | 13 |

### The unmapped tail, again

| predicate | claims |
|---|---|
| `reported` | 66 |
| `said` | 56 |
| `denied` | 55 |
| `died` | 36 |
| `announced` | 18 |
| `is` | 18 |
| `told` | 16 |
| `says` | 15 |
| `killed` | 14 |
| `found` | 11 |

### What the vocabulary did admit

| predicate | facts |
|---|---|
| `has_role` | 2925 |
| `participated_in` | 2224 |
| `created` | 1325 |
| `works_at` | 978 |
| `intends_to` | 937 |
| `located_in` | 665 |
| `has_status` | 623 |
| `lives_in` | 405 |

### What pass 3 settles

**The finding of passes 1 and 2 holds on the whole corpus.** Tense outnumbers vocabulary:
`not_current` is 2651 claims against 1194 unmapped, and the unmapped tail is still reporting verbs:
`reported`, `said`, `denied`, `told`, `announced`. A list written for what an agent learns about a
person does not carry attribution, which is the grammar of news.

**Nothing was stored that the system cannot stand behind.** 354 claims were dropped because their
quote could not be found in the source text, 282 because they conflicted with a current value, and 13
because the subject could not be resolved. Every fact that was stored has a quote found in the source
and a resolved subject.

**Ten documents were lost to a defect, not a refusal.** Seven came back `the request could not be
served` and three `the turn could not be stored`, all HTTP 500. The worker log shows why: PostgreSQL
refused new connections for thirty minutes (`remaining connection slots are reserved for roles with
the SUPERUSER attribute`), because twenty-eight workers and the API asked for more connections than
`max_connections=80` allowed. The part that was scaled was not the part that broke; the write path
was. Today each process checks free connection slots at startup and refuses to start if it will not
fit, and a write that still meets a full server gets a retryable `503 no_database_capacity`. See
[Production PostgreSQL](21-production-postgresql.md).

### The shape of the graph it built

Read from the same run by `deploy/gpu/hubskew`: 19965 entities over 13516 current facts.

| | degree |
|---|---|
| minimum | 1 |
| median | 1 |
| p90 | 2 |
| p95 | 3 |
| p99 | 5 |
| p99.9 | 11 |
| maximum | 58 |
| mean | 1.35 |

The biggest hub in the corpus has 58 edges, below the traversal's limit of 64 per direction per hop.
So the limit never comes into play on this corpus, and nothing measured here says what happens when
it does. That needs a graph with hubs built past the limit, on a machine that can be held steady.
