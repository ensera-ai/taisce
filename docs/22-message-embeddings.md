<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Message embedding generations

Message embeddings let you search the words people actually said. You ask a question in plain
language, and Taisce returns the stored messages closest to it, with a reference you can use to read
the full text.

Use this when you need the source text: to show a quote, to find where something was said, or to
search material that did not become a fact. It does not answer questions and it does not merge
anything. For structured answers from entities and facts, use recall.

A set of vectors built by one model is called a **generation**. You build a generation, check it,
then switch it on. When you change models, you build a new generation beside the old one and switch
over when it is ready.

## Set it up

You need the operator database connection (`TAISCE_ADMIN_DSN`) and an embedding model configured
with its endpoint on the inference allowlist.

Pick two values before you start:

- **Dimensions**: the number your model actually returns. Taisce checks the first build against it
  and refuses a mismatch. Nothing is truncated or converted.
- **Revision**: a name you choose for this exact model version, such as `weights-v1`. Change it
  whenever the model's weights or the provider's behaviour change. An OpenAI-compatible endpoint
  cannot prove that its weights stayed the same, so the revision is how you say so.

Then start, build, check and activate:

```sh
taisce embeddings start    --project example --key <operation-uuid> --revision weights-v1 --dimensions 2560
taisce embeddings build    --project example --generation <generation-uuid> --revision weights-v1 --limit 32
taisce embeddings status   --project example --generation <generation-uuid>
taisce embeddings activate --project example --generation <generation-uuid>
taisce embeddings search   --project example --revision weights-v1 --query 'database choice' --limit 10
```

- `start` creates the generation. Run it again with the same `--key` and you get the same
  generation back; change the model details and it refuses.
- `build` embeds one page of messages. Repeat it until `status` shows `complete` is true. Progress is
  saved, so a crash does not lose work.
- `activate` checks that every surviving message in the generation's range has a vector, then makes
  it the one searches use. Until then, the old generation keeps answering.
- `status` without `--generation` shows the active generation.

`status`, `activate`, `repair`, `cancel` and `prune` do not call the model.

The generation records the model name, your revision, a hash of the endpoint, the dimensions, the
cosine metric and the input format `message-text/v1`. Matching dimensions alone do not make two
models interchangeable. The endpoint credential is never stored.

## Search from the command line

Search is exact by default: it compares your question with every eligible message. Options:

- `--subject` and `--role` narrow the results.
- `--candidates 100` switches to approximate search: the index picks up to that many candidates,
  and Taisce reranks them at full precision.

Filters, and messages removed since the build, can give you fewer results than you asked for.
Approximate search does not promise the exact top results, especially with filters. Each result
shows the generation, source IDs, the message's position and role, and a preview of up to 512
characters.

## Search over HTTP

Set `TAISCE_INFERENCE_EMBEDDING_REVISION` on the API process to the revision of the active
generation, and configure the embedding model and its allowlisted endpoint and key as for the CLI.
You do not need an extraction model for this. If the revision is empty, the route is unavailable. If
you configure a provider that is invalid, the API refuses to start.

Any project credential, read-only included, can search:

```http
POST /v1/passages/search
Authorization: Bearer <project-credential>
Content-Type: application/json

{"question":"Which database did we choose?","limit":10,"data_subject_id":"subject-1","source_role":"user"}
```

An abridged response:

```json
{
  "generation_id": "…",
  "through_offset": 418,
  "covered_through_offset": 412,
  "build_state": "ready",
  "approximate": false,
  "passages": [
    {
      "chunk_id": "…",
      "source_id": "…",
      "ordinal": 0,
      "role": "user",
      "occurred_at": "2026-09-01T10:00:00Z",
      "similarity": 0.83,
      "preview": "We went with PostgreSQL because…",
      "message_bytes": 1840,
      "preview_byte_end": 512,
      "preview_complete": false,
      "content_digest": "…"
    }
  ]
}
```

What the fields mean:

- `through_offset` is how far the generation is meant to reach. `covered_through_offset` is how far
  it has fully processed. A range still in progress can already return passages above the covered
  offset.
- `source_id` is the observation. `chunk_id` is the message inside it. These point at source text.
  They are not citation IDs for facts.
- `preview` starts at byte zero and holds at most 512 characters. `preview_byte_end` is where it
  stops, in UTF-8 bytes. `content_digest` pins the preview to that exact version of the message, so
  you can read on from where it stopped.

Request fields:

| Field | Rules |
|---|---|
| `question` | Required, up to 8192 UTF-8 bytes |
| `limit` | 1 to 32, default 10 |
| `candidates` | 0 (exact, the default), or from `limit` up to 1000 for approximate search |
| `data_subject_id` | Optional, up to 1024 bytes. Leave it out to search the whole project |
| `source_role` | Optional: `user`, `assistant`, `system` or `tool` |

With approximate search, filters apply after the index picks candidates, so you can get fewer
matches.

Errors:

- `503 passage_unavailable`: no active generation, the configuration does not match it, or the
  provider is down.
- `400 invalid_passage_query`: a field is out of bounds.

Searches share the API's admission limits and 30-second request deadline. The audit entry holds no
content.

## Keep a generation current

New messages are not embedded on their own. A follower does it, and you start it on purpose. It uses
the runtime memory connection (`TAISCE_MEMORY_DSN`):

The first command processes one page of new messages. The second keeps going until you stop it.

```sh
taisce embeddings follow --project example --generation <generation-uuid> --revision weights-v1 --limit 32
taisce embeddings follow --project example --generation <generation-uuid> --revision weights-v1 --limit 32 --watch --interval 2s
```

What to expect:

- The follower checks the configured model before each call, and stops when its generation is no
  longer active. It cannot create or activate a generation.
- Each round picks a fixed end point. Messages that arrive later wait for the next round. Nothing
  already covered is embedded again.
- A provider failure leaves progress ready to retry. In watch mode it backs off, up to one minute,
  and stops promptly when cancelled.
- Each page has a two-minute deadline. `--interval` is 100ms to 1m and defaults to 2s, even with a
  backlog, so resource use is your choice.

It prints JSON status with the generation, page counts, current target and covered range. It does
not repeat itself when nothing changes. `retrying` with offsets of -1 means that attempt could not
confirm progress. Provider error bodies and message text are never printed. Operator status shows the
original build boundary as `initial_through_offset`.

API startup never starts background embedding.

## Read a message from a search result

Any project credential can read a message by its `chunk_id`, without an active generation and
without a model call:

```http
POST /v1/messages/get
Authorization: Bearer <project-credential>
Content-Type: application/json

{"chunk_id":"<message-uuid>","byte_limit":4096}
```

The response carries the source and chunk IDs, the ordinal, role, time, `content_digest`,
`message_bytes`, `byte_start`, `byte_end` (exclusive), `text`, `complete` and, when there is more,
`next_byte_start`.

- `complete` means this window holds the whole message from byte zero.
- No `next_byte_start` means you have reached the end. It does not mean you also read the start.

To read on, send `byte_start` and `expected_digest`: either the last window's `next_byte_start` and
digest, or a search preview's `preview_byte_end` and `content_digest`. Any start above zero needs the
digest.

Rules:

- `byte_limit` is 4 to 4096, default 4096.
- `byte_start` must fall on a character boundary. The text returned also ends on one, so it can be
  shorter than the limit.

Errors:

- `409 source_changed`: the message changed. Start again at zero to read the new version.
- `404 not_found`: the message is missing, erased, or in another project. All three look the same.
- `400 invalid_message_window`: a field is out of bounds.

## Limits

- A project holds at most two generations that are not discarded. Prune one before you start
  another. Pruning keeps a content-free record of the operation key.
- A build page reads at most 64 messages, at most 64 KiB per message and at most 1 MiB of text in
  total. A message that is too large or blank is refused rather than clipped.
- Stored vectors have 1 to 4000 finite components and cannot be all zeros. Full precision is kept.
  Up to 2000 dimensions are indexed directly. Above that, the index uses a normalised half-precision
  copy and results are reranked at full precision.

These are bounds on storage and work per call, not capacity figures.

## Recovery and model changes

- `repair --project … --generation …` resets the cursor over the same range and model. Vectors that
  still match are kept. Missing vectors, chunks and ownership records are restored. An active
  generation stays active during repair, so searches can miss some messages until it finishes.
- `cancel` stops in-flight work from landing. It refuses to cancel the active generation.
- `prune --limit 64` removes one page from a generation that is neither active nor building. Repeat
  until status says `discarded`. You can prune the previous generation after a successful switch.

## How it works

A model call holds an advisory lock but no database transaction. When results come back, Taisce
checks the lease, the progress and the source hashes under locks before it writes anything.

- If a message is erased during the call, the erasure wins. The vector is not stored, and the message
  cannot come back.
- If a message's text changed during the call, the page is refused.

Every vector is tied to the exact observation it came from, in every generation, active or not.
Export, erasure and retention cover all of them, and erasure counts them in its receipt. The runtime
database roles cannot change a generation's model or activate one.

## How it is tested

Tests against PostgreSQL cover generation identity, source ownership, page bounds, cancellation,
repair, rollback, model mismatch and scoped retrieval. A local provider has passed storage and both
search modes with 2560-dimension vectors. `make test-inference` runs the same journey against each
configured provider.

The index-plan tests set planner costs by hand on small data. They prove the index can be used and
that a query touches one generation. They do not measure latency, throughput, capacity or recall.
