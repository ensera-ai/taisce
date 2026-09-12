<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Correcting a record

A correction replaces the value of a current record with the right one. The original stays citable,
and the new record says who made the change.

## Correct one or more records

Read the record's current `version` from [record inventory](11-record-inspection.md) or
[citation inspection](08-citation-resolution.md). Then, with a read/write credential:

```http
POST /v1/records/correct
Authorization: Bearer <credential>
Content-Type: application/json

{"records":[{
  "id": "4b7f0c1e-2d9a-4e3b-8f61-0a5c9e2d7b14",
  "expected_version": "c2e8…",
  "object": "Oslo",
  "statement": "I live in Oslo.",
  "valid_from": "2026-03-01T00:00:00Z"
}]}
```

```json
{
  "records": [{
    "original_id": "4b7f0c1e-2d9a-4e3b-8f61-0a5c9e2d7b14",
    "id": "e61a…",
    "version": "0b3f…",
    "source_observation_id": "…"
  }]
}
```

`id` and `version` belong to the new record. `source_observation_id` is the message Taisce created to
hold your statement.

## What you can change

A correction keeps the record's subject, predicate and data subject. You supply:

- **`object`**: the new value, up to 1,024 bytes.
- **`statement`**: the sentence that says it, up to 16,384 bytes.
- **`valid_from`** (optional): when the new value became true. If you leave it out, server time is
  used.

Both text fields are required. They must be valid UTF-8, not blank, and contain no NUL characters. A
batch takes 1 to 20 records, with at most 128 supporting sources across the batch. The whole batch
either commits or doesn't.

## What happens to the original

- It is withdrawn, and its retraction details name who did it and the replacement.
- It stays citable, with its original text and source bytes.
- It stops being known at exactly the moment the replacement starts being known. A recall with an
  earlier `as_known_at` still returns the original.
- Backdating `valid_from` doesn't rewrite what was known earlier.
- Reports built on the old fact are cleared in the same transaction.

The new record's evidence shows your credential in `authored_by` and `extractor_version:
curated/v1`. Its confidence is 0, meaning no model estimated it. That records what the editor said,
not that it's true.

## Errors

| Response | When |
|---|---|
| `409 record_conflict` | The version is stale, or the record is no longer current. |
| `400 invalid_record_mutation` | The request is malformed. |
| `413 record_mutation_limit` | The batch has more than 128 supporting sources. |
| `404 not_found` | The record is unknown, erased, or in another project. |
| `403 forbidden` | The credential is read-only. |

## Watch out for

- **Current records only.** A correction needs a record whose validity and knowledge intervals are
  both still open, with a resolved subject. You can't edit closed history or change a record's
  subject or relation. To add a new claim instead, [author a record](17-authored-assertions.md).
- **After a lost response, check the original.** Its retraction details name the replacement.
  Retrying the old version conflicts and creates nothing new. A version you fetch afterwards is a
  fresh edit, not a retry.
- **Corrections skip the formation queue.** A correction is already formed, so it works even when
  the [ingestion backlog](06-ingestion-budget.md) is full. It can't move freshness past an older
  turn that is still waiting.

## How it works

Your statement is stored as a new user-role message, plus a structured record of the claim. That
claim replays exactly, with no model call, if the fact ever needs [recovering](15-fact-recovery.md).
If the stored claim is missing or changed, replay fails rather than asking a model to invent one.

A later retraction of the corrected record still applies. Subject export includes the claim in a
`curated_claim` section. Erasure deletes it and counts the residual. If retention removes the
replacement, the original stays withdrawn, so an expiry can never bring back the old value.
