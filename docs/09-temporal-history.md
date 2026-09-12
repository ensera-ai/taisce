<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Historical recall

Taisce keeps two kinds of time for every fact: when it was true, and when Taisce knew it. You can ask
a question at any point on either axis and get the answer as it stood then.

## Ask about the past

Add one or both timestamps to a recall (RFC 3339 format):

```http
POST /v1/recalls
Authorization: Bearer <credential>
Content-Type: application/json

{
  "question": "Where did I live?",
  "data_subject_id": "subject-1",
  "as_of": "2026-08-01T00:00:00Z",
  "as_known_at": "2026-03-10T00:00:00Z"
}
```

- **`as_of`** picks the moment the claim was true.
- **`as_known_at`** picks what Taisce knew at that moment.
- Leave out `as_known_at` to use current knowledge.
- Leave out both to get what is true now.

## An example

Suppose you record "lives in Dublin" in March, then "lives in Amman from June" in July.

| Question | `as_of` | `as_known_at` | Answer |
|---|---|---|---|
| Where did I live in August? | August | before July | Dublin, no end date |
| Where did I live in August? | August | now | Amman |
| Where did I live in April? | April | now | Dublin, ending in June |
| Where did I live in April? | April | before July | Dublin, no end date |

The first row is useful for audits: it shows exactly what an agent would have been told before the
move was recorded.

## Look at one fact's history

Fact IDs stay the same when a newer fact supersedes them. To see every interval one fact has had,
use [record history](11-record-inspection.md). [Citation inspection](08-citation-resolution.md) shows
the fact as it is now, with `superseded_by` and `supersession_source_observation_id` when it was
replaced. Subject exports include every stored interval in their `fact_history` section.

## Watch out for

- **History starts at install time.** A query for knowledge from before anything was recorded
  returns no facts, even if an entity with that name exists now.
- **Erasure removes history too.** Erasing a subject or letting retention expire their data removes
  earlier versions as well. If the message that set a fact's end date is erased, that fact can become
  unavailable even though its original message survives.
- **You can't take back an answer.** A response already sent to a client stays sent.
- **Only facts are versioned.** Historical recall keeps each fact's intervals. It does not keep old
  entity names, old alias lookups, or an exact copy of an earlier API response.
- **Corrections apply to current records only.** You can [correct](14-record-corrections.md) or
  [retract](13-record-retractions.md) a record that is still current, and history keeps the
  original. You can't edit a record that has already been closed.

## How it works

Each fact has a validity interval and a knowledge interval. When a new fact supersedes an old one,
Taisce closes the old fact's intervals and copies the closed version into a history table. The live
fact keeps its ID. A historical recall reads whichever version was open at the times you asked for.

If a fact's closing message is erased, Taisce won't guess the end date. The fact stays unavailable
until a new source establishes it again.
