<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Community-report embeddings

Community-report vectors answer thematic questions whose answer is distributed across several
messages. They are separate from passage vectors, which locate source text, and entity vectors, which
locate an identity. Equal provider settings do not make those inputs interchangeable.

The embedded input is community-report/v1: title, summary, importance reason and whole findings, in
persisted order, capped at 8,000 UTF-8 bytes. A title and summary must fit in full. Optional fields
that do not fit are omitted whole. The provider receives no source IDs, data-subject IDs or audit
metadata.

Each generation records model name, operator-declared revision, endpoint hash, dimensions, metric,
input version and a finite report count. Build pages contain at most 64 reports and one MiB of input.
Every stored vector records an input digest and a digest of ordered source/revision pairs. Publication
rechecks both after the model call. The generation can activate only when every captured report has a
vector.

Every contributing observation registers ownership of the aggregate. Removing any source removes the
community report and vector; retention uses the same path. A report inserted, changed or removed after
generation start makes that generation stale. Search then returns an availability error without
calling the provider. Start and activate a replacement generation before cancelling and pruning the
old one.

Operator lifecycle:

    taisce report-embeddings start --project p1 --key <uuid> --revision <revision> --dimensions <n>
    taisce report-embeddings build --project p1 --generation <uuid> --revision <revision>
    taisce report-embeddings activate --project p1 --generation <uuid>
    taisce report-embeddings search --project p1 --revision <revision> --query "What themes indicate growth?"

Search is exact by default. --candidates N opts into a bounded HNSW candidate set and must be at
least the result limit. The HTTP equivalent is POST /v1/reports/candidates; it accepts read-only
project credentials and returns bounded report previews. Similarity is a retrieval score, not a claim
that a report is true or complete. Follow the report's provenance to source evidence before presenting
an attributed answer.

## Verification

PostgreSQL tests cover deterministic bounded input, exact and optional ANN results, multi-source
ownership, export, erasure, retention, repair, finite-snapshot invalidation, model cutover and stale
model refusal. HTTP tests cover authentication, read-only access, input bounds and the generated
contract. CLI tests cover generation management without printing provider input. The configured
provider profile checks its actual vector width separately from quality claims.

The named `report-themes-v1` qualification exercised 1,152 reports across 24 topics against the
pinned `Qwen/Qwen3-Embedding-4B` revision on disposable GPU.ai infrastructure. Exact search put the
correct topic first for all 24 questions. Filtered HNSW search reproduced every member of the exact
top ten at candidate bounds 32, 64 and 128. The measured warm, sequential query latencies and build
time are recorded in `docs/26-report-quality-qualification.md`; they describe this corpus and host
rather than a production capacity target.
