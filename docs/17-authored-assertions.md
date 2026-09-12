<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Authoring a record

You can write a structured fact straight into memory, with no model involved. It gets the same
citations, history and erasure handling as a fact Taisce extracted from a conversation.

## Assert one or more records

With a read/write credential:

```http
POST /v1/records/assert
Authorization: Bearer <credential>
Content-Type: application/json

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

```json
{
  "records": [{
    "id": "3f9e…",
    "version": "a71c…",
    "source_observation_id": "…",
    "replayed": false
  }]
}
```

Results come back in request order. A batch holds 1 to 20 records and commits all or nothing.

## The fields

- **`idempotency_key`**: a new UUID for each record you mean to create. Keep it for retries.
- **`subject`**, **`predicate`**, **`object`**: the claim. The predicate must be in the service
  vocabulary, which decides whether it allows one value or several and what type the object is.
- **`statement`**: the sentence that becomes the record's quoted evidence.
- **`data_subject_id`**: the person the record is about. It's required when `subject` is a relative
  reference like `I`, and optional for named facts.
- **`valid_from`** (optional): when the claim became true. When Taisce learned it is always server
  time.

Size limits: subject, object and data subject 1,024 bytes; predicate 128 bytes; statement 16,384
bytes. Required fields must contain text.

## Retrying

If you lose the response, send the same key and the same content again. You get the original record
back, with `replayed: true`.

- The same key with different content returns `409 idempotency_conflict`.
- A key whose source has been erased also returns `409 idempotency_conflict`.
- Never swap in a new key automatically to get past a conflict. A new key always creates a new
  record.

## Errors

| Response | When |
|---|---|
| `409 idempotency_conflict` | The key was reused with different content, or its source was erased. |
| `409 record_conflict` | A single-value claim's validity clashes with an existing one out of order. |
| `400 invalid_record_mutation` | Unknown predicate, blank or oversized field, invalid UTF-8, NUL, or a key repeated in the batch. |
| `400` | The body has a field the route doesn't accept. |
| `403 forbidden` | The credential is read-only. |

## Watch out for

- **Single-value predicates supersede.** Asserting a new employer for a `works_at` subject closes
  the current one, just like extraction would.
- **Editing a record is a different call.** To change an existing record, use
  [corrections](14-record-corrections.md) with its expected version.
- **Provenance is not proof.** The evidence names your credential in `authored_by` and
  `extractor_version: curated/v1`, with confidence 0 (no model estimate). That shows who asserted
  the claim, not that it's true, and not which human was behind the credential.
- **You can't set attribution.** Author, role, confidence, cardinality and project come from the
  server and the credential. Requests can't set them.

## How it works

Each assertion creates a source message holding your statement, a structured record of the claim, and
a formation receipt, all in one transaction. The record is formed immediately. It doesn't use a slot
in the [ingestion backlog](06-ingestion-budget.md), but freshness still waits for any earlier turn
that hasn't formed yet.

Source time follows the server's event order, even if the clock goes backwards. Export and erasure
include the source, the claim and the receipt. If the fact is ever lost, it can be
[recovered](15-fact-recovery.md) from the receipt without a model call. Retrying a record that was
later withdrawn keeps it withdrawn.
