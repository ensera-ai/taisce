<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Inspect the identity behind a record

Use `POST /v1/entities/list` with `{"limit":20}` to browse retained entities in the credential's
project. The response includes `entities` and an optional `next` cursor. Pass that cursor as `after`
in the next request. Limits are 1–100; names are previews of at most 512 Unicode characters, with
byte counts and a truncation flag. Pages are ordered by immutable UUID. Removing the cursor entity
does not shift the next page; new IDs behind it appear when you restart the inventory.

Use `POST /v1/entities/get` with `{"id":"ENTITY_UUID"}` to resolve a record's `subject_entity_id`
or `object_entity_id`. It returns the canonical name, source-owned alias cache, identity kind and,
for a speaker, the bound `speaker_subject_id`. A named entity and a speaker are different identity
kinds even if their display names match. The response does not infer equivalence or search descriptions.
Canonical names and each alias are at most 4096 UTF-8 bytes, there are at most 64 aliases, and speaker
subject IDs are at most 1 MiB. Unsupported stored identities refuse rather than return clipped IDs.

Read-only credentials can use both operations. Project selection comes from the credential; a body
cannot override it. Absent and foreign entity IDs both return `404 not_found`. Bad IDs, cursors and
limits return `400 invalid_entity_page`. Audit records contain no identity text. Erasure removes
unsupported entities and departing source-owned aliases; an identity supported by other surviving
sources remains available. These are inspection operations. Correcting records, changing identity
and previewing/executing an entity purge have separate semantics (D102/#89).
