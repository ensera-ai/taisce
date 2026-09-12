<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# The subject registry

A subject is the person a piece of memory is about. The registry gives each subject a random ID for
use in memory, and keeps your own account reference in one place instead of in every row.

The registry is optional. You can keep sending your own opaque `data_subject_id` values.

## Register a subject

```http
POST /v1/subjects/register
Authorization: Bearer <credential>
Content-Type: application/json

{
  "idempotency_key": "de8bf4fa-5b6b-4310-a781-a58c46cc5bbd",
  "external_reference": "account-123",
  "label": "Support account"
}
```

```json
{
  "id": "0f6c…",
  "version": "b13e…",
  "external_reference": "account-123",
  "label": "Support account",
  "updated_by": "…",
  "created_at": "2026-09-11T09:00:00Z",
  "updated_at": "2026-09-11T09:00:00Z",
  "inactive_after": null,
  "replayed": false
}
```

Use the returned `id` as `data_subject_id` in observations, assertions, artifacts, recall, export and
erasure. Don't send the external reference there. Registering creates no memory, and makes no model
call.

Use one new UUID as `idempotency_key` per registration, and keep it for retries.

## Look a subject up

`POST /v1/subjects/get` takes exactly one of:

```json
{"external_reference":"account-123"}
```

```json
{"id":"0f6c…"}
```

References match exactly: case-sensitive, with no trimming or normalization. A reference is unique
within a project. The same text in another project is a different subject.

## Rotate a reference

`POST /v1/subjects/update` replaces the reference and label:

```json
{
  "id": "0f6c…",
  "expected_version": "b13e…",
  "external_reference": "account-456",
  "label": "Current support account"
}
```

If you leave out or empty a field, it's cleared. The old reference stops resolving, but existing
memory keeps the same subject ID. There's no alias history and no automatic merge. If the new
reference is already in use, you get a `409` and nothing changes.

## List subjects

```json
{"limit":20}
```

Send that to `POST /v1/subjects/list`. The response has `subjects` and `next` when there's more; pass
`next.id` as `after`. `limit` takes 1 to 100, default 20. Only registered subjects appear. Your own
IDs that you never registered don't.

## Errors

| Response | When |
|---|---|
| `409 subject_conflict` | The reference is taken, the version is stale, or a key was reused with a different registration. |
| `404 not_found` | The subject is unknown, erased, expired, or in another project. |
| `400 invalid_subject` | Bad UUID, both selectors or neither, invalid text, or a field is too long. |
| `403 forbidden` | A read-only credential tried to register or update. |

References can be up to 1,024 bytes and labels up to 256 bytes.

## Watch out for

- **A subject ID is not a login.** It doesn't prove anyone authenticated. Every credential for the
  project can still read every subject.
- **Random IDs are pseudonyms, not anonymization.** Anyone who can read the registry can map an ID
  back to its reference. Taisce also doesn't scan your messages or files for identifiers you put
  there.
- **Don't reuse a subject for a different person.** If an account now belongs to someone else,
  register a new subject instead of rotating the reference onto the old one's memory.
- **Retries return the current state.** Retrying a registration returns the subject as it is now,
  including any later rotation. It never restores the original values.
- **Erasure is not a ban.** After a subject is erased, a new write that uses the old ID is simply new,
  unregistered data. Resolve or register again before collecting, and stop sending erased IDs.
- **Back it up.** The registry is primary data. External references can't be rebuilt from facts.

## Retention and erasure

A registration gets an `inactive_after` deadline from the project's retention policy, or `null` for
no deadline. Updating the subject or changing the policy later doesn't move an existing deadline.

Once the deadline passes, the mapping is removed only after **all** of that subject's messages and
artifacts are gone. While any of them remain, you can still find the ID you need to export or erase
them. Expired entries are hidden from get, list and update right away, even before the cleanup
worker removes them.

Subject export includes `data_subject` and `subject_retry` sections. Erasure deletes the registry
entry, counts it, and clears its stored retry details in the same transaction, even for a subject
with no memory. A small, project-scoped digest of each retry key is kept afterwards so a delayed
retry can't bring an erased mapping back. A new registration after erasure needs a new key and gets a
new ID.

## How it works

A registration writes one registry row, one retry receipt and one audit entry. The audit entry never
holds the reference, the label or the subject ID. Linking a message to a registered subject adds one
indexed lookup. Recall doesn't touch the registry at all.

[Fact recovery](15-fact-recovery.md) and [rebuilds](20-generation-rebuild.md) leave the registry as
it is. Taisce has no tool to migrate existing IDs of your own into registered ones. Changing identity
would mean rewriting sources, which Taisce doesn't do automatically.
