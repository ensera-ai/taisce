<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Retracting a record

Retraction withdraws a wrong claim so it stops showing up in recall. The original words stay on
record, and so does the fact that Taisce once believed the claim.

## Retract one or more records

First read the record's current `version` from [record inventory](11-record-inspection.md), record
history or [citation inspection](08-citation-resolution.md). Then, with a read/write credential:

```http
POST /v1/records/retract
Authorization: Bearer <credential>
Content-Type: application/json

{"records":[{"id":"4b7f0c1e-2d9a-4e3b-8f61-0a5c9e2d7b14","expected_version":"c2e8…"}]}
```

```json
{
  "records": [{
    "id": "4b7f0c1e-2d9a-4e3b-8f61-0a5c9e2d7b14",
    "version": "91ad…",
    "operation_id": "…",
    "principal_id": "…",
    "retracted_at": "2026-09-11T09:30:00Z"
  }]
}
```

`principal_id` is the credential that made the request, and `retracted_at` is server time. You can't
set either one. A request takes 1 to 20 distinct records, with at most 128 supporting sources across
the whole batch. The whole batch either commits or doesn't.

## What changes

- Current recall no longer returns the claim.
- A recall with an earlier `as_known_at` still returns it, because that is what Taisce knew then.
- The claim's validity interval and its exact citation text don't change.
- Inventory, history and citations show `status: "retracted"` with who withdrew it and when.
- The claim won't come back if the same message is formed again, even as a paraphrase. A new,
  independent message can still assert it.

## Errors

| Response | When |
|---|---|
| `409 record_conflict` | The version is stale, or the record is already withdrawn. |
| `404 not_found` | The record is unknown, erased, or in another project. |
| `400 invalid_record_mutation` | The IDs, versions or batch size are malformed. |
| `413 record_mutation_limit` | The batch has more than 128 supporting sources. |
| `403 forbidden` | The credential is read-only. |

## Watch out for

- **Check before retrying.** If you lose the response, look at the record before doing anything
  else. Replaying the old version returns a conflict and never withdraws twice.
- **Every change bumps the version.** Supersession, corrections and retractions all change it. That
  stops you from acting on a record that changed since you looked.
- **A stolen write credential can retract.** Any read/write credential for the project can withdraw
  its records. There are no finer-grained roles.
- **Retraction is not erasure.** The source message and the withdrawal record stay. To remove data
  for a person, erase their subject.

## Related tools

- [Correct](14-record-corrections.md) a record to withdraw it and state the right value in one step.
- [Author](17-authored-assertions.md) a record directly, with no message behind it.
- [Report a doubt](35-feedback.md) when someone else should decide.
- [Rebuild](20-generation-rebuild.md) facts under a new extractor. Withdrawals survive a rebuild.

## How it works

The withdrawal is stored against each supporting source message, as a normalized
subject/predicate/object match. Formation checks it before asserting a claim from that message, so
the block holds even if the derived fact is later lost and recovered. Retrying the same claim from
the same message returns the retained record without changing it.

Subject export includes the withdrawals in a `record_retraction` section. Erasure deletes them and
counts the residual, and retention expiry removes them with their source. The audit log records the
actor, project, operation and count, but no claim text or record IDs.
