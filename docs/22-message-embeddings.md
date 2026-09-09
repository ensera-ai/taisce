<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Message embedding generations

Message vectors retain the model name, operator-declared revision, endpoint digest, dimensions,
cosine metric and `message-text/v1` input contract. A dimension match alone does not make models
interchangeable. The revision must change when model weights or provider behavior change; an
OpenAI-compatible endpoint cannot attest unchanged weights. No endpoint credential is persisted.

This is a bounded operator workflow over a captured observation-log boundary. It does **not**
automatically embed messages received after that boundary. A later generation captures those messages.
Entity descriptions and report embeddings remain separate work under #10/#19. Message matches are
source evidence, not extracted assertions and not a replacement for structured recall.

## Operator journey

Use the operator database connection (`TAISCE_ADMIN_DSN`) and the normal allowlisted inference
configuration. Choose the provider's actual dimension and an explicit model revision. The first build
validates provider output against the declared dimension; there is no truncation or silent conversion.

```sh
taisce embeddings start --project example --key <operation-uuid> --revision weights-v1 --dimensions 2560
taisce embeddings build --project example --generation <generation-uuid> --revision weights-v1 --limit 32
taisce embeddings status --project example --generation <generation-uuid>
taisce embeddings activate --project example --generation <generation-uuid>
taisce embeddings search --project example --revision weights-v1 --query 'database choice' --limit 10
```

Repeat `build` until `complete` is true. Its persisted cursor survives process loss. Retrying `start`
with the same operation key returns the same generation or refuses a changed model contract. A
candidate stays separate from the active generation until `activate` independently validates coverage
of every surviving source within its captured boundary. `status` without a generation shows the active
one. Status, activation, repair, cancellation and pruning need no model connection.

Search defaults to an exact baseline over eligible source rows. `--subject` and `--role` optionally
filter evidence. `--candidates 100` opts into bounded approximate candidates followed by full-precision
reranking. Filters and stale-source removal can produce fewer results than requested. Approximate
search does not guarantee exact top-k results; its filtered recall needs qualification under #101/#103.
Both modes return generation metadata, source IDs, message ordinal/role and a 512-character preview.

## Limits and recovery

- A project retains at most two non-discarded generations. Prune a non-active generation before
  allocating another; pruning keeps a content-free operation-key tombstone.
- A build page examines at most 64 messages, at most 64 KiB per message and at most 1 MiB of source
  text. Oversized or blank messages refuse rather than producing a clipped semantic representation.
- Persisted vectors have 1–4000 finite components and a nonzero direction. Full-precision values are
  retained. Indexes up to 2000 dimensions use vectors; higher dimensions use normalized half-precision
  index expressions and full-precision reranking. These are storage/work bounds, not capacity targets.
- `repair --project ... --generation ...` resets the cursor over the same captured range and model.
  Compatible retained vectors survive; missing vectors, chunks and registrations are restored. An
  active generation stays selected during repair, so reads may have partial coverage until it finishes.
- `cancel` fences in-flight results and refuses to cancel the active generation. `prune --limit 64`
  removes a bounded page from a non-active, non-building generation; repeat until status is `discarded`.
  A completed previous generation can be pruned after successful cutover.

A model call holds a session advisory lock but no database transaction. Publication checks its durable
lease, progress and source hashes under locks. Erasure during the call wins: deleted sources are skipped
and cannot be resurrected. Changed source text refuses the page. Every vector belongs to its exact
observation through the projection registry, including inactive generations; export, counted erasure
and retention operate on all of them. Runtime privileges cannot change model identity or activation.

## Verification and remaining qualification

The tests exercise generation identity, source ownership, bounded pages, cancellation, repair,
transaction rollback, model mismatch and scoped source retrieval against PostgreSQL. The configured
local provider has passed storage and both retrieval modes with 2560-dimensional vectors. The
inference-profile target includes that compatibility journey for each configured provider.

Index-plan fixtures explicitly control planner costs on small corpora to prove expression-index
eligibility and generation partition pruning. They do not measure latency, throughput, capacity or
approximate recall. Controlled-host targets and GPU configuration remain external qualification work.
See D104 and private issue #131 for scope and evidence.
