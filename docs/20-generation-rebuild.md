<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Fact generation and rebuild

Formation writes facts from a stored turn under a pinned extraction identity. When the model,
prompt, admission rules or vocabulary change, an operator can reinterpret one retained turn with:

```text
taisce rebuild facts --project <project> --source <observation-uuid> --key <operation-uuid>
```

The source must be an ordinary conversation turn, not curated or authored input. The operation reads
at most 256 messages and 1 MiB, performs model work before taking the cutover lock, and then checks
that the source and its fact revision are unchanged. Publication retires only model-derived facts
from that source and records a new generation with bounded per-fact dispositions. A human correction
is retained and blocks generated claims for the same message subject/relation slot. A source-backed
withdrawal continues to block its matching structural claim.

Missing current fact projections are restored from their surviving receipts inside cutover, then
retired along with retained facts. A damaged receipt refuses publication; later receipt recovery
cannot revive the replaced interpretation. This restoration has the same 100-fact bound.

The previous interpretation remains available as historical knowledge while the new one is current.
Citation responses expose the generation that admitted a fact and the generation that retired it;
exports and subject erasure include the same lineage. The JSON result reports counts and contains no
message text or provider diagnostics.

`--key` is an operator-owned idempotency key. Repeating it with the same source and extractor
identity returns the committed result and finishes the formed watermark without another model call.
A different identity for an already committed key, a changed source, a missing source or a failed
model attempt is refused without partial publication. This command does not rebuild entity resolution,
chunks, embeddings or reports, and it does not cache uncommitted model output.

## Rebuild a project's facts

```text
taisce rebuild project --project <project> --key <operation-uuid> --limit 10
taisce rebuild status --project <project> --key <operation-uuid>
taisce rebuild cancel --project <project> --key <operation-uuid>
```

Repeat `rebuild project` with the same key until status is `completed`. Each invocation processes at
most `--limit` eligible sources (default 10, maximum 100), with a five-minute budget per source.
The first invocation records the current log upper bound and extraction identity. Later appends are
excluded, and a different identity refuses resumption. Human-authored sources are excluded. The
operation publishes each source atomically; a project can contain both interpretations while a job
is in progress. It does not provide a project-wide atomic switch.

PostgreSQL stores the cursor, pending offset and acknowledged-source counters. If publication
succeeds but progress recording fails, resumption replays the source's stable operation key without
another model call. Failure before publication may require another inference attempt. An eligible
source erased before publication is skipped. Counters describe acknowledged sources, not a frozen
total: inputs erased before they are visited never enter those counts.

Cancellation prevents admitting a later source. An already admitted source may finish, so a running
job can temporarily report `cancel_requested: true` with `status: active`. If its runner disappears,
repeat `cancel`: it reconciles any committed pending result, finishes its freshness bookkeeping
and finalizes cancellation in one transaction, without the
original model configuration. Status and cancellation only require the operator database connection.
An orphaned uncommitted reservation counts as skipped. Neither path sends text to a model.

Only one active operation and one runner are permitted per project. A replacement runner changes a
persisted fencing token; an older runner cannot publish or acknowledge work afterwards. The operator
pool must allow at least two connections: one holds the session lock while source transactions use
another. A smaller pool is refused before work starts.

Job records contain project log positions and aggregate progress, not source IDs, subjects, message
text or model responses. They remain operational checkpoints after subject erasure, like the log
watermark. Start and cancellation are audited atomically. Database operator authority controls these
commands; the random invocation principal does not claim to identify an authenticated human.
