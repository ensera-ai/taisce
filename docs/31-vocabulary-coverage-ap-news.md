<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Vocabulary coverage on a news corpus — 2026-09-10

Tracking: [#43](https://github.com/althunibat/taisce/issues/43). Harness: `deploy/gpu/vocabulary-coverage.py`
at `5b3775b` (D119). Extractor: the qualified configuration, `Qwen/Qwen3.8-27B` BF16 at revision
`1d4bf0f2ff6012fd82039f2fa52739d0dd7c60c0` with the guidance structured-output backend and the
embedded extraction prompt, so the identity stamped on every row is the one D96 pins.

**The question.** The relation vocabulary is closed (D15) and anything outside it is refused as
`unmapped_relation` (D16), on the untested assumption that the set covers what a real corpus asserts.
This measures that assumption on a corpus the vocabulary was not written for: 1,397 Associated Press
health articles from 2023-11 to 2024-04, the corpus BenchmarkQED ships for its own evaluation. The
corpus is licensed to the operator under the Microsoft Research License and is not in the repository.

**What the number is, and is not.** It is one extractor's admission rate on one corpus at one commit,
broken down by refusal reason and, for the unmapped ones, by the relation the model tried to use. It
is not a retrieval result, not a latency or throughput figure, and not a claim about the extractor
on conversational turns, which are what the vocabulary was written for.

## Where it ran

Models on a 4×H100 PCIe container in GPU.ai's asia-pacific region: the pinned vLLM image with the
generation model's arguments as its command, tensor parallelism across the four cards, 32 sequences;
the embedding model started beside it on the last card. The Taisce stack — PostgreSQL, API and twelve
workers — ran on the operator's machine with its inference endpoints on SSH tunnels to the container,
and the harness ran beside it. Article text left the machine only as model input; nothing was stored
remotely. Latency across that hop is irrelevant to an admission rate and is not reported.

This was the eleventh node of the day. The provider's eight-A100 VM pool, which ran the 2026-09-09
qualification, delivered nothing usable in eight attempts (errors during boot, stalls in starting,
two nodes with seven cards visible) and its four-card VM stalled three times more; D117 sized the
topology by the cards present so the next such day is survivable, and the container path was taken
when the whole VM pool stopped booting.

## Pass 1 — under a data subject: zero facts

Every article was observed as one `tool`-role message under a synthetic data subject, which is how
the recall controls describe fetched document text. All 1,397 were admitted in eleven seconds across
twelve projects. Formation was paused at 535 articles once the result was clear:

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

The fact store refuses every claim from a non-user message whenever the observation names a data
subject, not only claims about that subject, so a document under a subject cannot form a fact by
construction. The vocabulary check runs first in the extractor, so the 319 unmapped refusals are a
complete count over every proposal; what the pass could not supply was the other side of the ratio,
because the claims that passed the extractor died uncounted. Filed as
[#153](https://github.com/althunibat/taisce/issues/153), with the decision it forces for documents.

Two more things this pass measured. Eleven articles, averaging 10.7 KB against 4.5 KB for the rest,
were cut off by the extraction client's two-minute timeout at least once under twelve-way sharing of
one server; D118 made the budget an operator setting and the run continued at fifteen minutes.
Raising it meant editing the extractor file, which changed the D96 identity, and fourteen sources
with an earlier attempt were refused as "configuration changed" until explicitly rebuilt: the pin
working as designed, and the reason a rebuild command exists.

## Pass 2 — without a data subject

The same corpus, twelve fresh projects, no data subject on the observations, everything else equal.
Without a subject the role gate does not apply, tool-role facts are admitted under their own role,
and the denominator is complete: facts plus every stored refusal.

_In progress at the time of writing; the table below is filled in from `results/vocabulary-coverage.json`
when formation completes._

| Outcome | Count |
|---|---|
| articles formed | — |
| facts | — |
| refused, by reason | — |
| unmapped rate (unmapped / all proposals) | — |
| unmapped share of refusals | — |
| documents with no fact | — |

Top unmapped predicates, the breakdown #43 asks for, follow the table when the run completes.

This pass surfaced a third defect. Nine articles parked after six attempts each with the fact insert
violating the single-cardinality exclusion constraint. That constraint is G4 working: one current
value per single-valued relation per subject. Two claims in one message share one occurrence time,
so neither can supersede the other, the second insert overlaps the first, and the store returns the
raw error, which formation treats as a transport failure and retries until the turn is parked with
nothing stored. The right outcome is a refusal of the second claim with a reason of its own. Filed as
[#155](https://github.com/althunibat/taisce/issues/155); those nine articles are absent from the
counts below and are listed in the run's results.

## What the first pass already decides

**The largest refusal on news is tense, not vocabulary.** `not_current` outnumbers `unmapped_relation`
six to one. The prompt defines `past` as a relation that held and no longer does, which is right for a
state and wrong for a reported event: "voters adopted an amendment" is complete and remains true, and
"is the Governor" was refused because its quote sat in a past-tense sentence. Filed as
[#154](https://github.com/althunibat/taisce/issues/154).

**The unmapped tail is reporting verbs.** The most frequent unmapped predicates in the first pass
were `denied` (27), `reported` (26), `said` (17), `died` (9), `is` (9), `says` (8), `increased` (6),
`was` (6), `transferred` (5). Attribution verbs are the shape of news and have no relation in a
vocabulary written for what an agent learns about people; whether they belong there or belong to the
passage fallback in #149 is the decision the second pass's full breakdown informs.

## Limits

Claims refused by role are counted in formation reports and not persisted, so the first pass's
denominator is unknowable from the database; the second pass exists because of that. Eleven long
articles needed a longer budget than the default, which the harness's per-run results record. Every
number here is one extractor's behaviour at one commit; a prompt or vocabulary change re-runs the
corpus (rule 14) before a new number is quoted.
