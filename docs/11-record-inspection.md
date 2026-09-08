<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Inspecting retained memory

Use a project credential, including a read-only credential, to browse retained facts:

```http
POST /v1/records/list
Authorization: Bearer <credential>
Content-Type: application/json

{"data_subject_id":"subject-1","limit":20}
```

Omit `data_subject_id` for project-wide inventory. Subject filtering follows registered provenance;
a record with several supports for the same subject appears once. The response contains `records`
and an optional `next` cursor. Send that cursor as `after`, retaining the same subject filter:

```json
{"data_subject_id":"subject-1","limit":20,"after":{"id":"<last-record-uuid>"}}
```

Each summary includes its ID, entity IDs, predicate, source role, recording time and retained status.
`statement_preview` contains up to 512 Unicode characters; `statement_bytes` counts the full stored
statement's UTF-8 bytes, and `preview_truncated` identifies an incomplete preview. Use the ID with
[citation resolution](08-citation-resolution.md) to inspect the complete statement and verified
supporting source bytes. An inventory entry is a retained assertion, not an independent verification.

Inspect the fact's current and earlier temporal intervals:

```http
POST /v1/records/history
Authorization: Bearer <credential>
Content-Type: application/json

{"id":"<record-uuid>","limit":20}
```

The response has `id`, `current`, `previous` and optional `next`. Each previous version includes
`history_id`, `valid`, `known`, and `ended_by_observation_id`: the observation that ended that
knowledge version. Its claim content is the retained immutable claim. Send `next` as `before` to
continue through older intervals. A record with no supersession has an empty `previous` array.
[Historical recall](09-temporal-history.md) selects a knowledge view when answering a question.

Both page limits default to 20 and accept 1–100. Invalid limits/cursors return `400 invalid_record_page`.
Unknown, other-project and erased history IDs return the same `404 not_found`. An empty inventory
returns `records: []`. Requests cannot select a project independently of the credential.

Record pages use ascending UUID order, without relevance or recency ranking. History pages use newest
knowledge end first. Each page is consistent, but the whole browse is live: erasure may remove a
record and concurrent writes before the cursor appear only after restarting. Deleting a cursor row
does not invalidate continuation. A cursor never grants access to the record it names.

Read-only credentials can inspect all subjects within their project. These reads produce content-free
audit entries. Correction/retraction, batch mutation, entity purge and rebuild mapping remain planned.
[D81](01-decisions.md) records the storage and pagination tradeoffs.
