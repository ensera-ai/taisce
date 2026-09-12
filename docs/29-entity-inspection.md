<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Inspect the identity behind a record

Every fact Taisce holds has a subject and often an object, and each is an entity: a person, a place,
an organisation, or a speaker in a conversation. These two operations let you look at those entities
directly: list what a project knows about, and look up the one behind a record.

Both are read-only. Read-only credentials can use them. The project always comes from your
credential, and a request body cannot change it.

## List the entities in a project

```http
POST /v1/entities/list
Authorization: Bearer <project-credential>
Content-Type: application/json

{"limit":20}
```

The response has `entities` and, when there is more, a `next` cursor. Send that cursor back as
`after` to get the next page.

- `limit` is 1 to 100.
- Names come as previews of up to 512 characters, with a byte count and a flag that says whether the
  name was cut.
- Pages are ordered by entity ID, which never changes. If the entity at your cursor is removed, the
  next page does not shift. Entities created with IDs before your cursor show up when you start the
  list again from the beginning.

## Look up one entity

Take a record's `subject_entity_id` or `object_entity_id` and send it:

```http
POST /v1/entities/get
Authorization: Bearer <project-credential>
Content-Type: application/json

{"id":"ENTITY_UUID"}
```

You get back the canonical name, the aliases collected from its sources, the kind of identity and,
for a speaker, the `speaker_subject_id` it is bound to.

- A named entity and a speaker are different kinds of identity, even when their names look the same.
- The response does not guess that two entities are the same, and it does not search descriptions.

## What to watch out for

- Canonical names and each alias are at most 4096 UTF-8 bytes, there are at most 64 aliases, and a
  speaker subject ID is at most 1 MiB. An entity stored outside these bounds is refused, not returned
  cut short.
- A missing entity and an entity in another project both return `404 not_found`, so you cannot tell
  them apart.
- A bad ID, cursor or limit returns `400 invalid_entity_page`.
- The audit log records the call but none of the identity text.

## After an erasure

Erasure removes an entity that no surviving source supports, and removes aliases that came only from
the erased sources. An entity that other sources still support stays.

## Changing an entity

These operations only inspect. To fix what a record says, see
[Correcting a record](14-record-corrections.md) and [Retracting a record](13-record-retractions.md).
Removing an entity the extractor should not have made is a separate operation with its own preview
and confirmation.
