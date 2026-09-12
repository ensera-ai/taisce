<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Inspecting a citation

Every fact that recall returns has an ID. Resolving that ID shows you the exact words the fact came
from, where they sit in the original message, and whether the fact is still current.

## Resolve a fact

Send the fact ID with the same project credential you used for recall:

```http
POST /v1/citations/resolve
Authorization: Bearer <credential>
Content-Type: application/json

{"id":"4b7f0c1e-2d9a-4e3b-8f61-0a5c9e2d7b14","limit":8}
```

A shortened response:

```json
{
  "id": "4b7f0c1e-2d9a-4e3b-8f61-0a5c9e2d7b14",
  "version": "c2e8…",
  "predicate": "lives_in",
  "statement": "I moved to Dublin in March.",
  "source_role": "user",
  "status": "current",
  "valid": {"from": "2026-03-01T00:00:00Z", "until": null, "from_inclusive": true, "until_inclusive": false},
  "known": {"from": "2026-03-04T10:12:00Z", "until": null, "from_inclusive": true, "until_inclusive": false},
  "evidence": [{
    "source_observation_id": "…",
    "source_ordinal": 0,
    "role": "user",
    "occurred_at": "2026-03-04T10:11:58Z",
    "quote": "I moved to Dublin in March",
    "byte_start": 0,
    "byte_end": 26,
    "context": "I moved to Dublin in March. Still settling in.",
    "context_complete": true,
    "extractor_version": "extract/v2:…"
  }]
}
```

The fields you'll use most:

- **`statement`** is the fact in full.
- **`evidence`** lists the supporting messages. `quote` is the exact text, and `byte_start` and
  `byte_end` locate it in the original UTF-8 message. They are byte offsets, not character offsets.
- **`context`** is a bounded window around the quote. `context_complete` tells you whether it is the
  whole message.
- **`occurred_at`** is when that message was sent.
- **`valid`** is when the claim is true in the world. **`known`** is when Taisce believed it. A
  missing `until` means the interval is still open.
- **`source_role`** is who the fact is attributed to. Each evidence row's `role` comes from its own
  message.
- **`authored_by`** appears on evidence a person wrote directly (see
  [authored records](17-authored-assertions.md)).

To find fact IDs without running a recall, browse [record inventory](11-record-inspection.md).

## Page through evidence

A fact with many supporting messages comes back in pages. If the response has `next`, send it back
unchanged as `after`, with the same `id`:

```json
{"id":"4b7f0c1e-2d9a-4e3b-8f61-0a5c9e2d7b14","limit":8,"after":{"…":"value of next"}}
```

Stop when `next` is missing. `limit` defaults to 8 and can go up to 32. A page also stops at 256 KiB
of text, so you may get fewer sources than you asked for.

## Status values

| `status` | Meaning |
|---|---|
| `current` | Both the validity and knowledge intervals are still open. |
| `validity_closed` | A later fact superseded it; it stopped being true at a point in time. |
| `knowledge_closed` | Taisce stopped believing it. |
| `retracted` | Someone withdrew it. See `retraction`. |
| `reinterpreted` | A fact rebuild replaced it. See `reinterpreted_by`. |

`current` means the stored intervals are open. It does not mean anyone checked the claim is true.

These fields tell you what happened to a fact:

- **`superseded_by`** and **`supersession_source_observation_id`** name the fact that replaced it
  and the message that caused the replacement.
- **`retraction`** holds who withdrew it and when, plus the replacement if it was a correction.
- **`generation`** and **`reinterpreted_by`** name the rebuild generation that admitted the fact and
  the one that retired it. Both [single-source and project fact rebuilds](20-generation-rebuild.md)
  keep these links.

Don't guess a successor from similar text or a matching predicate. Use the links.

## Errors

| Response | When |
|---|---|
| `404 not_found` | The ID is unknown, erased, or belongs to another project. All three look the same. |
| `400 invalid_citation` | The ID or page parameters are malformed. |
| `422 citation_limit` | The source text is too large to return. Use a subject export instead. |
| `500` | Stored source bytes don't match the evidence. No partial evidence is returned. |

The usual authentication, suspension and rate-limit responses also apply.

## Watch out for

- **Paging is live.** Each page is a consistent snapshot, but the sequence is not. An erasure or new
  supporting evidence can change later pages. Use subject export for a bulk copy.
- **The quote is verified.** If the stored bytes don't match the recorded offsets, you get an error,
  never a wrong quote.

## How it works

Evidence rows store byte offsets into the original message, and the resolver re-reads those bytes on
every request. The audit log records the operation and how many sources were returned, but not the
citation content.

To see a fact as it was at an earlier time, use [historical recall](09-temporal-history.md).
