<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Exact anchoring: query plans

Recall starts by finding the entities your question names. It matches each term against an
entity's canonical name and its aliases. This page shows which query plans PostgreSQL picks for that
lookup, measured on 2026-09-08 on a synthetic dataset.

## What this means for you

- **The lookup uses indexes, not a table scan**, for a hit on a name, a hit on an alias, a missing
  term and 128 missing terms at once.
- **A question can match at most 256 entities.** A 257th match is refused before recall walks the
  graph.
- **This shows query shape, not speed.** It was measured on a local machine and says nothing about
  production latency, throughput or corpus size.

## Run it yourself

```sh
TAISCE_TEST_DSN=<test database> go test -count=1 -v ./internal/infra/pg -run '^TestAnchorQueryPlans$'
```

The test ([`anchorplans_test.go`](../internal/infra/pg/anchorplans_test.go)) creates an isolated
namespace with two projects, each holding 10,000 entities with 16 normalised aliases. It analyzes the
table, then runs the real production query under `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)`. It logs
the full plans, fails if a table scan comes back on this dataset, and checks the returned project and
match count. It does not turn off sequential scans to force a result.

## What the plans show

| Query case | One combined OR expression (earlier form) | Separate name lookup and per-term alias lookup (current) |
|---|---|---|
| Name hit | Sequential scan of entities; 10,000 rows passed the project filter and 10,000 were rejected | B-tree lookup on project and name, plus an alias GIN probe |
| Alias hit | Same sequential scan | Alias GIN probe finds two rows across projects; the project filter keeps one |
| Missing term | Same sequential scan | Empty B-tree and GIN probes |
| 128 missing terms | Parallel entity scan, matching every term across the project | 128 B-tree and 128 GIN probes, no entity table scan in the recorded plan |

A batched `normalized_aliases && $2` form used the indexes for single terms, but switched to a
sequential scan for 128 terms, reading and discarding all 20,000 rows. So Taisce keeps one alias
probe per term. Those repeated probes can read more buffers than a scan would, especially when the
GIN index has pending entries, so using an index is not a promise of speed.

## How matches are ordered

Matches are capped at 257 rows before the final sort. A 257th match refuses the request before the
graph is walked. With 256 or fewer, every matching entity and term pair is sorted by longest term,
then canonical name, then entity ID. A term listed twice, or a term that matches both a name and an
alias, does not produce a duplicate match. Entity identity and attribution are unchanged.

## What to watch out for

The alias index covers the whole instance. When projects share an alias, the lookup can read index
entries belonging to other projects before the project filter removes them. The test proves you only
get your own project's results. It does not prove the work done is independent of other projects.

Questions about one data subject also have to look up which sources support each entity. A separate
alias table keyed by project would need to be compared under realistic alias skew, concurrent writes,
erasure and index maintenance before it could replace the current layout. No such change has been
made.
