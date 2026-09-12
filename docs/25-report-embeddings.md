<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Community-report embeddings

Some questions have no single source. "What themes point to growth?" is answered by a pattern across
many messages, not by one message. Taisce groups related entities into communities and writes a
report for each. Report embeddings let you search those reports by meaning.

Each kind of embedding has its own job, and you cannot swap one for another even when the model
settings match:

- **Passage** embeddings find source text ([Message embedding generations](22-message-embeddings.md)).
- **Entity** embeddings find an identity.
- **Report** embeddings find a theme.

## Set it up

The lifecycle is the same as for messages: start, build, activate, then search.

```sh
taisce report-embeddings start    --project p1 --key <uuid> --revision <revision> --dimensions <n>
taisce report-embeddings build    --project p1 --generation <uuid> --revision <revision>
taisce report-embeddings activate --project p1 --generation <uuid>
taisce report-embeddings search   --project p1 --revision <revision> --query "What themes indicate growth?"
```

Repeat `build` until the generation is complete. `activate` only succeeds once every report the
generation captured has a vector.

## Search

Search is exact by default. `--candidates N` switches to approximate search over the HNSW index. `N`
must be at least the number of results you ask for.

Over HTTP, use `POST /v1/reports/candidates` with a project credential. Read-only credentials are
allowed. Send `question`, and optionally `limit` (1 to 32, default 10) and `candidates` (up to 1000).
You get back short report previews.

Similarity is a search score. It does not mean a report is true or complete. Before you show an
answer with attribution, follow the report's provenance back to the source messages.

## What to watch out for

- **A report change makes the generation stale.** If a report is added, changed or removed after
  the generation started, search returns an availability error and does not call the model. Start
  and activate a new generation, then cancel and prune the old one.
- **Erasure removes the report.** Every observation that fed a report is registered as one of its
  owners. Erasing any of them removes the report and its vector. Retention takes the same path.

## How it works

The text sent to the model is built by the `community-report/v1` format: title, summary, the reason
the report matters, and whole findings, in stored order, capped at 8,000 UTF-8 bytes.

- The title and summary must fit in full.
- An optional field that does not fit is left out whole, never cut.
- The model never sees source IDs, data-subject IDs or audit data.

Each generation records the model name, your revision, a hash of the endpoint, dimensions, metric,
input version and the number of reports it covers. A build page holds at most 64 reports and 1 MiB
of input. Every vector stores a digest of its input and a digest of the ordered list of sources and
their revisions. Both are checked again after the model call, before anything is saved.

## How it is tested

Tests against PostgreSQL cover the input bounds, exact and approximate results, reports with several
sources, export, erasure, retention, repair, stale generations, model changes and refusing a stale
model. HTTP tests cover authentication, read-only access and input bounds. CLI tests cover managing
generations without printing what was sent to the model. The provider profile checks the real vector
width separately from any quality claim.

A quality run used 1,152 reports across 24 topics with the pinned `Qwen/Qwen3-Embedding-4B` model on
a rented GPU machine. Exact search put the right topic first for all 24 questions. Filtered
approximate search returned every one of the exact top ten at candidate bounds of 32, 64 and 128. The
latencies and build time are in [Report quality, 2026-09-09](26-report-quality-qualification.md).
They describe that corpus and that machine, not a production target.
