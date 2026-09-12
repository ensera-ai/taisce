<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Reporting a doubt about a record

Feedback lets someone flag a record as wrong without changing it. The report waits in a queue until
someone with write access decides what to do. If they agree, one call turns the report into a
correction or a retraction.

Everything else you can tell Taisce (an assertion, a correction, a retraction) changes what it
believes as soon as it commits. Feedback does not. It sits beside the graph until somebody acts on it.

Use feedback when the person or agent who notices a problem is not the one who should fix it: an end
user flagging an old employer, an evaluation run disputing a claim, an agent that is unsure. If you
already know the answer, make a [correction](14-record-corrections.md) directly.

## Report a record

Find the record and its `version` with `POST /v1/records/list` or from a citation in a recall answer
(see [Inspecting retained memory](11-record-inspection.md)). Then:

```http
POST /v1/feedback/record
Authorization: Bearer <project-write-credential>
Content-Type: application/json

{"record_id":"<record-uuid>","note":"He moved to Oslo last spring.","proposed_object":"Oslo"}
```

| Field | Rules |
|---|---|
| `record_id` | Required |
| `note` | Required. 1 to 16384 UTF-8 bytes, not blank, no NUL |
| `proposed_object` | Optional. Up to 1024 bytes, same rules |

`proposed_object` decides what promoting the report will do:

| `proposed_object` | Promoting it |
|---|---|
| Present | Corrects the record to that object, using the note as the new record's statement |
| Absent | Retracts the record |

The response has the feedback `id`, the `record_id`, `recorded_by` and `recorded_at`.

**Nothing else changes.** No fact is written, no watermark moves, no recall answer changes, and no
report is invalidated. That is the whole point of the operation.

Errors:

- `400 invalid_feedback`: the request is malformed.
- `404 not_found`: the record is not in your project. Missing, erased and someone else's all look the
  same.

## Read the queue

```http
POST /v1/feedback/list
Authorization: Bearer <project-credential>
Content-Type: application/json

{"open_only":true,"limit":20}
```

Every field is optional:

- `record_id` shows reports about one record.
- `open_only` hides reports that were already promoted.
- `limit` is 1 to 100, default 20.
- `next` in the response is a cursor. Send it back as `after` for the next page.

It is a cursor, not a page number, because an erasure can remove rows behind you, and a page number
would shift every later page when that happens.

**A read-only credential can list feedback.** Seeing what is outstanding is inspection, and the
person who most needs to see the queue often holds a key that cannot change anything. Recording and
promoting are writes, and a read-only credential gets `403 forbidden`.

## Promote a report

```http
POST /v1/feedback/promote
Authorization: Bearer <project-write-credential>
Content-Type: application/json

{"id":"<feedback-uuid>","expected_version":"<target-record-version>"}
```

`expected_version` is the **record's** version, not the feedback's. Feedback never changes after it
is written, so it cannot be stale. The record can. Without this check you could apply someone's
judgement to a claim they never saw. If the version no longer matches, you get
`409 record_conflict` and the feedback stays open.

The response says what happened:

| Field | Meaning |
|---|---|
| `operation` | `record.correct` or `record.retract` |
| `replacement_record_id` | The new record a correction created; absent for a retraction |
| `version` | The new record's version, or the retracted record's closing version |
| `promoted_at` | When it committed |

What comes out is an ordinary record. It is credited to **whoever promoted it**, not whoever
reported it, because a report is not an assertion until somebody approves it. It resolves through
`POST /v1/citations/resolve`, shows up in history, replays under rebuild without a model call, and
can be retracted later like any other record.

**A report can be promoted once.** A second try returns `409 feedback_already_promoted`. That is a
different code from `record_conflict` on purpose: nothing in the request was wrong, and the right
response is to stop, not to re-read and retry. If two promotions race, they queue on the feedback row,
so exactly one wins and the record changes exactly once.

## Who can promote

Any read-write credential for the project. That credential can already assert, correct and retract
without review, so asking for more to promote feedback would guard the narrow path while the wide
one stayed open. There is no separate reviewer role.

## What the audit log records

Three operation names: `feedback.record`, `feedback.list` and `feedback.promote`. A promotion also
writes a `record.correct` or `record.retract` entry for what it did, so the log shows both who
complained and who decided. Refusals are logged as refusals. No note text, record ID or database
error reaches the audit log or the application log.

## Reporting from an agent

The MCP server offers `report_feedback`, so an agent holding a `fact_id` from a recall can say the
record is wrong. It is allowed because it changes nothing: no MCP tool may overwrite or delete, and
this one only adds a report. See [MCP and the Claude Code plugin](developers/mcp.md).

Promoting is deliberately **not** an MCP tool. Promotion is what turns a report into a belief, and a
model should not be able to decide that through a tool someone handed it. Listing feedback is not a
tool either: it would let an agent read every complaint in the project, which is a text-retrieval path
through a route meant to show what is outstanding.

## Erasure

Feedback is tied to the same sources and data subjects as the record it is about. The erasures that
reach the record reach the feedback, and the receipt counts it as `feedback`.

A record that two people contributed to is kept when one of them is erased, because it is also the
other person's. Feedback about that record is not kept: one person wrote those words, and a second
contributor to the record does not make them theirs.

## What it does not do

- **There is no dismiss.** A report that turns out to be wrong stays in the queue until it is erased
  with its record. Nothing reads the queue on a schedule, so an unpromoted report costs one row.
- **Promotion cannot create a new record.** It only corrects or retracts an existing one. To add
  something Taisce has never held, use `POST /v1/records/assert` (see
  [Authoring a cited record](17-authored-assertions.md)).

## How it works

Both list queries read an index and never sort the table. The open queue uses a partial index on
reports not yet promoted; a single record's reports use an index on the record. Both indexes end in
`(recorded_at DESC, feedback_id DESC)`, which is the page order, so the query does not have to sort
what it finds. Without those trailing columns, the sort would grow with how many reports one record
collects.

| Read | Plan | Index |
|---|---|---|
| The open queue | `Limit` over `Index Scan`, no sort | `memory_feedback_open_idx`, partial on unpromoted |
| One record | `Limit` over `Index Scan`, no sort | `memory_feedback_target_idx` |

This was checked on 10,000 reports over 500 records on a development machine. That machine cannot
give latency figures, so these are plan shapes only: neither read sorts, and neither grows with how
much feedback the project holds.

## How it is tested

In `internal/infra/pg`:

- `TestRecordingFeedbackChangesNoFactAndNoRecall`
- `TestFeedbackPagesSeekIndexesRatherThanScanningTheQueue`, which fails if either read becomes a
  sequential scan
- `TestOneFeedbackIsPromotedOnceEvenWhenTwoPromotionsRace`
- `TestFeedbackOnASharedRecordGoesEvenThoughTheRecordStays`
