<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Browsing records

Record inventory lists the facts Taisce holds, without asking a question first. Record history shows
every earlier version of one fact. Both work with a read-only credential.

## List records

```http
POST /v1/records/list
Authorization: Bearer <credential>
Content-Type: application/json

{"data_subject_id":"subject-1","limit":20}
```

```json
{
  "records": [{
    "id": "4b7f0c1e-2d9a-4e3b-8f61-0a5c9e2d7b14",
    "version": "c2e8…",
    "subject_entity_id": "…",
    "object_entity_id": "…",
    "predicate": "lives_in",
    "statement_preview": "I moved to Dublin in March.",
    "statement_bytes": 27,
    "preview_truncated": false,
    "source_role": "user",
    "recorded_at": "2026-03-04T10:12:00Z",
    "status": "current"
  }],
  "next": {"id": "4b7f0c1e-2d9a-4e3b-8f61-0a5c9e2d7b14"}
}
```

Leave out `data_subject_id` to list the whole project. For the next page, send `next` back as `after`
with the same filter:

```json
{"data_subject_id":"subject-1","limit":20,"after":{"id":"4b7f0c1e-2d9a-4e3b-8f61-0a5c9e2d7b14"}}
```

`statement_preview` holds up to 512 characters. `statement_bytes` is the size of the full statement,
and `preview_truncated` tells you the preview was cut. For the full text and its sources, pass the ID
to [citation inspection](08-citation-resolution.md). The `version` is what you need to
[retract](13-record-retractions.md) or [correct](14-record-corrections.md) a record.

## See a record's history

```http
POST /v1/records/history
Authorization: Bearer <credential>
Content-Type: application/json

{"id":"4b7f0c1e-2d9a-4e3b-8f61-0a5c9e2d7b14","limit":20}
```

The response has:

- `current`: the record's `valid` and `known` intervals now.
- `previous`: earlier versions, newest first. Each has a `history_id`, its `valid` and `known`
  intervals, and `ended_by_observation_id`, the message that closed it.
- `version`, and `retraction` if the record was withdrawn.
- `next`: send it back as `before` to go further back.

A record that was never superseded has an empty `previous` list. To ask a question at an earlier
point in time instead, use [historical recall](09-temporal-history.md).

## Limits and errors

- Both routes take `limit` from 1 to 100, default 20.
- A bad limit or cursor returns `400 invalid_record_page`.
- An unknown, erased or other-project ID on the history route returns `404 not_found`.
- An empty project returns `"records": []`.
- The project always comes from the credential. You can't choose a different one in the request.

## Watch out for

- **No ranking.** Records come back in ID order, not by relevance or date.
- **Browsing is live.** Each page is consistent, but an erasure can remove a record between pages,
  and records written behind your cursor only appear if you start again.
- **Cursors are safe to reuse.** A cursor still works if the record it names is deleted, and it
  never grants access to that record.
- **A record is a claim, not a verified fact.** Inventory shows what was asserted and kept.
- **Read-only credentials see every subject** in their project.

## Changing records

Inventory only reads. To change what is held:

- [Retract](13-record-retractions.md) a wrong record.
- [Correct](14-record-corrections.md) a current record.
- [Author](17-authored-assertions.md) a new record directly.
- [Report a doubt](35-feedback.md) for someone else to review.
- Purge an entity: see [governance](architecture/governance.md).
- [Rebuild](20-generation-rebuild.md) facts under a new extractor.

## How it works

Pages are ordered by the record's immutable UUID, and history pages by when each version stopped
being known, so neither order shifts when rows are added or removed. Subject filtering follows the
record's registered sources, so a record with several sources for the same subject appears once.
Reads write content-free audit entries.
