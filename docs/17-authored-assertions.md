<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Author a cited memory record

Use `POST /v1/records/assert` with a write-enabled project credential. The server accepts the
structured claim directly and records the authenticated credential as its author, without inference.
For example:

```json
{
  "records": [{
    "idempotency_key": "a2d0c96c-739b-4d42-8f8d-20c0a356b234",
    "data_subject_id": "person-42",
    "subject": "I",
    "predicate": "works_at",
    "object": "Ensera",
    "statement": "I work at Ensera.",
    "valid_from": "2026-09-01T00:00:00Z"
  }]
}
```

Generate a new UUID for each intended assertion and retain it across retries. Batches contain 1–20
records and commit atomically. A response contains each record's `id`, `version`,
`source_observation_id` and `replayed` flag, in request order. Repeat the same key and content after a
lost response. Different content or an erased source produces `409 idempotency_conflict`; never
replace a key automatically to bypass that response. A new key intentionally creates a new source.

The predicate must be in the service vocabulary. Its cardinality and object type come from that
vocabulary. Single-cardinality assertions can supersede earlier current validity; incompatible
out-of-order validity produces `409 record_conflict`. Use the correction route when editing a current
record with its expected version. `valid_from` is optional; the server records its knowledge event
separately. Source time follows that server event, including ordered logical time after clock rollback.

The statement becomes exact authored evidence. Citations name `curated/v1` and the author credential;
confidence zero means no model estimate. This establishes provenance, not independent verification of
truth or human identity. `data_subject_id` is the calling application's attribution. Relative speaker
references such as `I` require it. Named project facts may omit it, with the same attribution limits
as unowned observations.

Subject/object/data-subject fields have a 1,024-byte maximum, predicates 128 bytes and statements
16,384 bytes. Required fields must contain text. Invalid UTF-8, NUL, unknown fields, duplicate retry
keys in a batch and unsupported predicates are refused. Client fields cannot set author, role,
confidence, cardinality or project. Read-only credentials receive `403` before body parsing.

Authored sources are already formed. They consume no unfinished inference reservation, and freshness
still waits for any earlier pending observation. Export and erasure include their source, authored
input and formation receipt. Retrying a withdrawn record retains its withdrawal; retrying an erased
source conflicts. Missing facts can be recovered from their receipts under D88. D91 records the
transaction, time and storage tradeoffs.
