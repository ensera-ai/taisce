<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Correcting a current record

Inspect the current record and its `version`, then use a project write credential:

```http
POST /v1/records/correct
Authorization: Bearer <project-write-credential>
Content-Type: application/json

{"records":[{"id":"<record-uuid>","expected_version":"<version-uuid>","object":"Oslo","statement":"I live in Oslo.","valid_from":"2026-03-01T00:00:00Z"}]}
```

Each correction preserves the target's subject, predicate and data-subject attribution. It changes
the object and statement, with an optional explicit validity start. Omitted `valid_from` uses server
time. This operation requires a resolved subject and records whose valid and known intervals are still open. It does not
rewrite closed historical records or change their subjects or relations. Independent record creation
and broader historical editing remain tracked by #89.

The batch accepts 1–20 records and at most 128 supporting evidence rows. Each object is at most 1024
UTF-8 bytes; each statement is at most 16384. Both are required, valid UTF-8, nonblank and without NUL.
Corrections commit atomically: original withdrawals, authored sources, replacement facts, provenance,
report invalidation, freshness and success auditing either all persist or all roll back. Stale
versions or closed records return `409 record_conflict`; malformed requests return
`400 invalid_record_mutation`; excess evidence returns `413 record_mutation_limit`. Foreign, unknown
and erased IDs share `404 not_found`. Read-only credentials receive `403 forbidden`.

The response's `records` contains `original_id`, replacement `id`, replacement `version` and
`source_observation_id`. The original remains citable with its unchanged text and exact source bytes.
Its retraction metadata identifies the actor and replacement. The new record cites a separate
user-role message containing the submitted statement; evidence identifies the authenticated editor
in `authored_by` and reports `extractor_version: curated/v1`. This records what the editor asserted.
It does not verify truth or claim a model confidence; confidence remains zero for these human inputs.

The original's knowledge end equals the replacement's knowledge start. Earlier knowledge reads
therefore retain the original assertion, and reads at that boundary see the replacement. Valid time
is an independent choice, so backdating the replacement does not rewrite what was known earlier.
Reports derived from the old fact are invalidated in the correction transaction.

A stored authored claim is authoritative input and replays without a model call, retaining the original
server-assigned knowledge start. A correction commits already formed, consumes no pending-work
reservation and remains available at model-backlog capacity; it cannot advance freshness past an
older pending turn. If that input is
missing or its identity changed, replay fails instead of asking a model to invent a replacement.
A later retraction still applies to the authored source. Source export includes `curated_claim`;
subject erasure counts deletion and residual. Expiring a replacement removes its replacement link
from the old record while keeping the old withdrawal, so expiry cannot resurrect the original.

After a lost response, inspect the original's retraction metadata to find the result. Retrying its
old version conflicts and creates no second source. A newly fetched version is a new editing decision,
not a retry token. Generic rebuild and historical generation mapping remain #58/#116; the available
recovery tests cover a retained authored source whose derived fact is lost.
