<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Storing agent state and files

Artifacts let an agent keep bytes in Taisce: a scratchpad, a checkpoint, a small file. Taisce stores
them as-is and never reads them. They don't feed extraction, embeddings, recall or facts, but they
are covered by export, erasure and retention like everything else.

## Create an artifact

Pick a new UUID and send:

```http
POST /v1/artifacts/put
Authorization: Bearer <credential>
Content-Type: application/json

{
  "id": "ea1cef23-4bb7-4f29-84e5-e035bd6c0462",
  "data_subject_id": "opaque-subject-reference",
  "kind": "state",
  "name": "scratchpad",
  "content": "AQID"
}
```

```json
{
  "id": "ea1cef23-4bb7-4f29-84e5-e035bd6c0462",
  "version": "5e2a…",
  "data_subject_id": "opaque-subject-reference",
  "kind": "state",
  "name": "scratchpad",
  "source_observation_id": "…",
  "bytes": 3,
  "authored_by": "…",
  "created_at": "2026-09-11T09:00:00Z",
  "updated_at": "2026-09-11T09:00:00Z",
  "expires_at": "2026-10-11T09:00:00Z",
  "replayed": false
}
```

- **`kind`** is `state` or `file`. Both behave the same way.
- **`name`** is a label for display, not a file path.
- **`content`** is standard base64. `"AQID"` is the three bytes `01 02 03`, and `""` is zero bytes.
- **`data_subject_id`** is the person whose erasure removes this artifact.

## Read, list, update and delete

Get the bytes and metadata:

```json
{"id":"ea1cef23-4bb7-4f29-84e5-e035bd6c0462"}
```

Send that to `POST /v1/artifacts/get`.

List metadata with `POST /v1/artifacts/list`. Every filter is optional:

```json
{"data_subject_id":"opaque-subject-reference","kind":"file","name_prefix":"report-","limit":20}
```

The response has `artifacts` and, when there's more, `next`. Pass `next.id` as `after`. `limit`
defaults to 20, maximum 100. `name_prefix` is plain case-sensitive text: `%`, `_` and `\` are not
wildcards. Listing never looks inside the content.

To update, send the full put request again with `expected_version` set to the current version:

```json
{"id":"ea1cef23-…","expected_version":"5e2a…","data_subject_id":"opaque-subject-reference","kind":"state","name":"scratchpad","content":"BAUG"}
```

To delete, send this to `POST /v1/artifacts/delete`:

```json
{"id":"ea1cef23-4bb7-4f29-84e5-e035bd6c0462","expected_version":"5e2a…"}
```

It returns `{"deleted":true}`.

## Limits

Each project starts with:

| Allowance | Default | Maximum you can set |
|---|---|---|
| Size of one artifact | 256 KiB | 512 KiB |
| Total current bytes | 16 MiB | 1 GiB |
| Number of artifacts | 128 | 100,000 |
| Lifetime | 30 days | 365 days |

A project retention policy that is shorter than the lifetime wins. Every request body is also capped
at 1 MiB, including base64 and JSON overhead.

Operators check and change allowances with the operator connection:

```sh
taisce artifact limits --project example
```

```json
{"max_object_bytes":262144,"max_bytes":16777216,"max_objects":128,"max_age_hours":720,"used_bytes":3,"used_objects":1}
```

To change them, pass all four limits together:

```sh
taisce artifact limits --project example \
  --max-object-bytes 262144 --max-bytes 16777216 \
  --max-objects 128 --max-age-hours 720
```

## Errors

| Response | When |
|---|---|
| `409 artifact_conflict` | The version is stale, or a create was changed under an existing ID. |
| `429 storage_capacity` | The project is out of artifact allowance. Retrying won't help. |
| `404 not_found` | The artifact is unknown, deleted, expired, or in another project. |
| `400 invalid_artifact` | The request is malformed, or the base64 isn't canonical. |
| `403 forbidden` | A read-only credential tried to put or delete. |

## Watch out for

- **One owner per artifact.** Taisce can't see names mentioned inside your bytes. If two people's
  data must be erasable separately, store it in separate artifacts.
- **No old versions.** An update replaces the bytes. Earlier versions are not kept.
- **Updates don't extend the expiry.** The lifetime counts from creation.
- **IDs are single-use.** A deleted, erased or expired ID can't be reused, even by a delayed retry.
  Use a new UUID for a new artifact.
- **Retries are safe.** Repeating the create returns current metadata; it never restores old bytes.
  Repeating the last update is recognized and not written twice. Repeating a completed delete
  returns `404`.
- **Expired artifacts still use allowance** until the retention sweep deletes them.
- **Back them up.** Artifacts are primary data. [Fact recovery](15-fact-recovery.md) can't rebuild
  them.
- **The allowances cover payload only.** Indexes, metadata, WAL and backups need their own capacity
  planning.

## How it works

Every write and its audit entry commit together, and a database error rolls the write back. Lowering
a limit keeps existing artifacts but lets you shrink or delete them. A new lifetime applies only to
artifacts created afterwards.

Read-only credentials can get, list and export. Subject filters help you find things, but they are
not an access boundary; see [content access](16-content-access.md). Subject export includes an
`agent_artifact` section, where the bytes appear as hex text starting with `\x`. Erasure and
retention remove artifacts, count them, and release their allowance.

Artifacts are plain storage. Taisce doesn't provide sessions, checkpoint indexes or framework
serialization on top of them.
