<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Message embedding generations

Message vectors retain the model name, operator-declared revision, endpoint digest, dimensions,
cosine metric and `message-text/v1` input contract. A dimension match alone does not make models
interchangeable. The revision must change when model weights or provider behavior change; an
OpenAI-compatible endpoint cannot attest unchanged weights. No endpoint credential is persisted.

The initial build captures a finite observation-log boundary. An explicitly started `follow` worker
can incrementally embed later messages under the same active model; ordinary API startup does not
start background embedding work.
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

## Application passage search

Set `TAISCE_INFERENCE_EMBEDDING_REVISION` on the API process to the exact revision used when starting
the active generation. Configure the embedding model and its allowlisted endpoint/key as for the
operator commands. API-only embedding does not require an extractor model. An empty revision leaves
the route unavailable; an explicitly configured but invalid provider refuses API startup.

A read-only or read-write project credential can call:

```http
POST /v1/passages/search
Authorization: Bearer <project-credential>
Content-Type: application/json

{"question":"Which database did we choose?","limit":10,"data_subject_id":"subject-1","source_role":"user"}
```

The result contains `generation_id`, `through_offset`, `covered_through_offset`, `build_state`,
`approximate`, and `passages`. The first offset is the current target; the second is the last fully
processed boundary. A partially processed range can already contribute passages above that boundary.
Each passage contains `chunk_id`, `source_id`, `ordinal`, `role`, `occurred_at`, `similarity`, `preview`,
`message_bytes`, `preview_byte_end` and `preview_complete`. The source id identifies the observation;
the chunk id identifies its message. These are source references, not asserted-fact citation ids.
There is no general message-download endpoint implied by these references. Previews start at byte
zero and include at most 512 Unicode characters; the exclusive end offset is in UTF-8 bytes.

`limit` defaults to 10 and is bounded to 1–32. `candidates` defaults to zero (exact retrieval); a
positive value, at least the limit and at most 1000, opts into approximate candidates with exact
reranking. Filtering happens after ANN candidate selection, so filtered ANN can return fewer matches.
`source_role` accepts user, assistant, system or tool. A missing subject filter searches the entire
credential project. The question is limited to 8192 UTF-8 bytes; subject filters to 1024 bytes.

This is source evidence, not a factual answer or safe merge decision. New messages enter the active
generation only when an explicit follower processes them. A missing active generation, configuration
mismatch or unavailable provider
returns `503 passage_unavailable`; invalid controls return `400 invalid_passage_query`. Read requests
share API admission and the 30-second request deadline. Read audit is content-free and best-effort,
matching other application reads; operator search keeps its own transactional audit. See D106.


## Incremental catch-up

After initial build and activation, use the runtime memory connection (`TAISCE_MEMORY_DSN`) to process
one page of new messages or keep an explicitly selected generation current:

```sh
taisce embeddings follow --project example --generation <generation-uuid> --revision weights-v1 --limit 32
taisce embeddings follow --project example --generation <generation-uuid> --revision weights-v1 --limit 32 --watch --interval 2s
```

The follower checks the configured model before inference and stops when its selected generation is
no longer active. It cannot allocate or activate a model. Each sequence captures a finite target;
later arrivals wait for the next sequence. Completed ranges are not re-embedded. Provider failures
leave progress retryable; watch mode backs off to at most one minute and stops promptly on cancellation.
Every page has a two-minute deadline and retains the existing 64-message/1-MiB text limits. Interval
is bounded to 100ms–1m; the default is 2s even with backlog, making resource use an operator choice.

Status is JSON with generation/audit identifiers, page counts, current target and completed coverage.
Unchanged idle polls do not print repeatedly. `retrying` with offsets -1 means that attempt could not
verify progress; provider error bodies and source content are omitted. The original build boundary
is retained as `initial_through_offset` in operator generation status. `repair` covers the current
captured target and conservatively clears completed coverage until its scan finishes. See D107/#134.
