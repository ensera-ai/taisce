<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Is this a solid memory provider? — an architect's read, 2026-11-09

Read on 2026-11-09 against `docs/market-gap-2026-11` at 7512449, the architecture pages, the recall
and formation code, the migration set, and the CI state of this repository and the three adapter
repositories. No code was changed. Two defects below are new and have issues: #151 and #152. Part of #153.

## Verdict

**Solid as a system of record. Not yet solid as a retrieval system, and not yet operable.**

The write path, the governance path and the substrate are the strongest parts, and they are strong
in the way that is hardest to retrofit: the properties are enforced where a statement cannot get
around them. The read path is where the product's claim actually lands, and there the honest summary
is that **nothing ranks, and one of the two cuts that drops facts does not say it happened.**

Nothing here is a latency, throughput or corpus figure. None has been measured on hardware that
could produce one, and none should be quoted.

## What is solid, with what shows it

| Property | Evidence |
|---|---|
| A write either lands whole or not at all | One transaction covers observation, messages, chunks, erasure registrations, retention deadline, retry receipt, backlog reservation and log offset — `appendTurnTx`, `internal/infra/pg/observationstore.go` |
| A write is never blocked on a model | Formation runs after the commit; `GET /v1/freshness` returns `stored` and `formed` so a caller can tell a missing fact from an unformed one |
| Retries are safe | Idempotency key locked and the receipt read inside the same transaction — `AppendIdempotent` |
| Erasure is counted, not asserted | Delete and residual count share one filter in one transaction; `erasure_residual_with_completion_chk` in `0001_the_spine.sql` makes a residual without a completion impossible |
| Every fact carries its words | Evidence is an **inner** join on the fact walk: a fact whose receipt cannot be produced is not returned |
| An erased message degrades rather than lies | Message text is a **left** join; the quote survives, the context goes, and the bundle lists `citation_context` in `degraded` |
| Time is real, not a timestamp column | Two axes (`valid`, `known`), `fact_history`, and `supersedeSQL` closing the open interval at the new claim's start so any instant has one answer |
| The extractor refuses what it cannot verify | Closed relation vocabulary, exact quote located in the source, resolved subject; 19-case corpus passed 19/19 on three different model arrangements (`docs/36-extraction-models.md`) |
| The vocabulary's limits are measured, not assumed | 5.93% unmapped on 1,397 AP articles, and the larger refusal class is tense, not vocabulary (`docs/31-vocabulary-coverage-ap-news.md`) |
| Overload refuses instead of queueing | `Admission` builds its bounds from the real pool sizes; `429` with `Retry-After`, distinguished from `503 no_database_capacity` |
| Memory reaches the model as data | One `user`-role message prefixed `taisce-memory/v1 untrusted`, never `system`; checked by one shared conformance suite |
| The adapter contract is held across three languages | `conformance` green on `main` in taisce-python, taisce-java and taisce-dotnet, all three read 2026-11-09 |
| The gate is real | 858 named tests, 70 migrations, coverage floor 92.3 computed with `-coverpkg=./...`; `test` green on `main`, read 2026-11-09 |

## What is not solid

Ordered by what would hurt a developer who adopted this today.

### 1. The read path has no ranking, and one cut is silent — the sharpest gap

`ORDER BY f.fact_id LIMIT 64` runs per direction, per entity, per hop inside the traversal
(`factsAboutTemplate`, `internal/infra/pg/recallstore.go`). `fact_id` is a content hash of the claim
(`sourceClaimID`), so it is not time-ordered and carries no relevance. For an entity with more than
64 edges on a side, **which 64 are returned is decided by a SHA**. The final `LIMIT 201` and the
character budget then drop whatever sorted last.

The budget cut reports itself (`truncated`), and `docs/architecture/read-path.md` is candid that
nothing has ranked. The fanout cut does not report itself — see the first finding below.

This is defensible as a stated limitation and indefensible as a product property: the one question a
memory has to answer well is "what matters about this person", and at a hub entity this answers
"whatever hashed low".

### 2. No retrieval result exists

D26 says a retrieval result is published with its conditions or no number is published at all, and
#142 is the open decision. The consequence is that **the quality of the thing being sold has never
been measured.** Correctness of the mechanism is measured thoroughly; usefulness of what comes back
is not measured at all. LoCoMo, LongMemEval and MemBench were read on 2026-11-09 (`docs/03-market.md`)
and none has been run here.

### 3. The instance cannot be watched from outside it

No `/metrics` route exists anywhere — the mux carries `/health`, `/ready`, the portal, management and
`/v1` only. The stated second reader of this product is the operator, and that operator gets two
booleans. #143 is open.

### 4. Isolation is a convention, not a boundary

Rule 8 says a boundary expressed in code is a convention and one expressed in the substrate is a
boundary. Project separation is `scope = ANY($1)` written into every statement — the project's own
security page says so plainly. A statement that forgot it would not be refused by the database. Same
shape for `data_subject_id`: it narrows every surface and permits nothing, and any holder of a project
credential can name any subject.

### 5. Plain HTTP, and a vulnerable dependency reachable

The API and manage listeners serve plain HTTP; bearer tokens are protected only by whatever
terminates TLS in front. `golang.org/x/text` v0.39.0 carries GO-2026-6629 and `govulncheck` finds it
reachable (#125), and nothing in any of the four repositories checks dependencies against advisories
(#126).

### 6. The public interface is not pinned

`internal/api/mcp.go` sets no protocol revision, so the SDK's default decides what six MCP tools
present to a client and an SDK bump can change it silently (#146).

## New findings, needing issues

**F1 — a fanout-cut bundle reports no truncation (#151).** `truncated` is set only when the row bound
(`MaxRows+1`) or the character budget bites (`internal/recall/recall.go:293–310`). The 64-per-direction
fanout cut happens inside the SQL, so a hub entity can return 64 of 400 facts in a bundle whose row
count is well under 200 and whose `truncated` is `false`. `domain.Fanout`'s own comment claims "the
bundle says when it was made rather than pretending the neighbourhood was smaller than it was" — it
does not. `TestTheFanoutCapBoundsTheRowsAHubEntityIsReadFor` asserts the cap bounds the read; no test
asserts the caller is told. A caller cannot distinguish a complete neighbourhood from a truncated one,
which is the same failure `truncated` and `reach` exist to prevent.

**F2 — `VERSION` is stale at `0.3` while the published line is `v0.5.1` (#152).** `scripts/version.sh current`
therefore reports `0.3.48` for a working-tree build, and `next-release` computes `0.4.0`, a tag that is
already published. The release itself is driven by a pushed tag, so nothing publishes wrongly; the
dev-build label and the helper disagree with the history they describe, which is the exact failure the
script's own header says computing the patch was meant to avoid.

## What would change the verdict

In order:

1. Make the fanout cut visible (F1) — small, and it closes a silent-incompleteness hole.
2. Settle #142 and run one named benchmark with its conditions. Until then "answers from entities" is
   a mechanism claim, not a quality claim.
3. Give the operator something to scrape (#143).
4. Decide whether ranking is in scope. Recency alone — ordering the fanout and the budget cut by
   `lower(valid)` or `recorded_at` instead of by a hash — would be a large improvement over a SHA and
   needs an argument and a measurement, not a preference.
