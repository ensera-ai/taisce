<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Exact anchoring: query plans

Recall starts by finding the entities your question names. It matches each term of the question
against an entity's normalised name, inside the projects you may read. This page shows the query
plan PostgreSQL picks for that lookup, read on 2026-11-13 on PostgreSQL 18.6 from a synthetic
dataset.

## What this means for you

- **The lookup is one probe of a unique index per term, and nothing else.** A hit, a miss and 128
  misses at once all use `entity_named_identity_uniq`, and none scans the entity table.
- **The index leads with the project.** A probe for a name in your project descends to your
  project's part of the index; another project's entities of the same name are not read.
- **A question can match at most 256 entities.** A 257th match is refused before recall walks the
  graph.
- **This shows query shape, not speed.** It was read on a development machine and says nothing
  about latency, throughput or corpus size.

## Run it yourself

```sh
TAISCE_TEST_DSN=<test database> go test -count=1 -v ./internal/infra/pg -run '^TestAnchorQueryPlans$'
```

The test ([`anchorplans_test.go`](../internal/infra/pg/anchorplans_test.go)) creates an isolated
namespace with two projects, each holding 10,000 entities of the same names. It analyzes the table,
then runs the production statement under `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)`. It logs the full
plans, fails if the entity table is scanned or the index is not used, and checks the returned
project and match count. It does not turn off sequential scans to force a result.

## What the plans show

| Query case | Plan | Probes | Rows returned | Rows discarded by a filter |
|---|---|---|---|---|
| A name that is stored | Index scan on `entity_named_identity_uniq`, condition on project and name | 1 | 1 | 0 |
| A name stored under many spellings | The same | 1 | 1 | 0 |
| A name that is not stored | The same | 1 | 0 | 0 |
| 128 names that are not stored | The same, once per term | 128 | 0 | 0 |

Every case has the project and the name in the index condition and nothing in a filter, so no row
is read and then thrown away. The second project's 10,000 entities carry the same names and none of
them is touched: the index is on `(scope, normalized_name)`, and the probe names both.

Work grows with the number of terms in the question, one probe each, and not with the number of
entities stored.

## Why there is one index and one probe

An entity has one normalised name, and it is the key. The spellings a source used — different case,
different spacing — are kept in `entity_name_receipt`, and a trigger requires every one of them to
normalise to that same key ([0043](../internal/migrate/sql/0043_entity_name_variants_have_source_owners.sql),
[0051](../internal/migrate/sql/0051_canonical_names_are_the_exact_anchor_key.sql)). So the probe on
the name already reaches every spelling, and there is no second structure to search.

What that leaves out is everything that is not the same name after normalisation: a misspelling, an
inflection, a different word order. Those are not found by this statement. Whether they should be
is an open decision ([#157](https://github.com/ensera-ai/taisce/issues/157)).

## How matches are ordered

Matches are capped at 257 rows before the final sort. A 257th match refuses the request before the
graph is walked. With 256 or fewer, every matching entity and term pair is sorted by longest term,
then canonical name, then entity ID. A term listed twice does not produce a duplicate match. Entity
identity and attribution are unchanged.

## When the question is about one person

A recall for one data subject adds two things to the statement: the entity that stands for the
speaker, found by a probe of `entity_speaker_identity_uniq` on project and subject, and a check that
each named entity has a source belonging to that subject, read from `projection_dependency`. The
test above plans the statement without a subject; the per-subject form is not in the table.
