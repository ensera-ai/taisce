<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Exact anchoring: query-plan diagnostics

2026-09-08. Development [#102](https://github.com/althunibat/taisce/issues/102). These local PostgreSQL
plan checks establish query behavior on a synthetic fixture. They do not establish production
latency, throughput or maximum corpus size. Controlled-host measurement remains open.

Run `TAISCE_TEST_DSN=<test database> go test -count=1 -v ./internal/infra/pg -run '^TestAnchorQueryPlans$'`.
The test provisions an isolated namespace, creates two projects with 10,000 entities each and
16 normalized aliases per entity, analyzes the table, then explains the actual production query
with `ANALYZE, BUFFERS, FORMAT JSON`. It logs full plans, refuses table-scan regressions for this
fixture and verifies the returned project and match count. It does not disable sequential scans.

| Query case | OR expression at `1309e72` | Separate canonical / per-term containment arms |
|---|---|---|
| Canonical hit | Entity sequential scan; 10,000 rows passed scope filter and 10,000 rejected | Project/name B-tree lookup plus alias GIN probe |
| Alias hit | Same entity sequential scan | Alias GIN probe finds two project rows; scope filter admits one |
| Missing term | Same entity sequential scan | Empty B-tree and GIN probes |
| 128 missing terms | Parallel entity scan and repeated term matching across the project | 128 B-tree and 128 GIN probes, no entity table scan in the recorded plan |

A batched `normalized_aliases && $2` alternative used the indexes for the single-term cases, but
selected an entity sequential scan for 128 terms, removing all 20,000 rows in this fixture. Separate
per-term containment is retained. Its repeated GIN probes can touch more buffers than a scan,
particularly with pending index entries; index selection alone is not a latency guarantee.

The returned match set is capped at 257 rows before final sorting. A 257th match produces a client
refusal before graph traversal. With at most 256 matches, all matching entity/term pairs are sorted
by longest term, canonical name and entity ID. Duplicate candidate terms and canonical/alias
overlap do not duplicate an identical match. Entity identity and attribution are unchanged.

The alias index is instance-wide. Shared aliases can require reading postings from other projects
before project filtering. The test proves result isolation, not a project-independent work bound.
Subject-specific predicates also require evidence dependency lookups. A normalized relation keyed
by project and alias must be compared under realistic alias skew, concurrent writes, erasure and
index maintenance before choosing that persisted representation. No alias-specific ownership
migration is included here.
