<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Store agent state and files

Artifacts hold opaque application bytes within a project. Both `state` and `file` use the same
contract. They never enter extraction, embedding, recall or fact formation. A name is display
metadata, not a server filesystem path. Each object declares one data subject whose erasure removes
it. The application is responsible for that declaration: Taisce cannot discover another person
mentioned inside opaque bytes. Use separate artifacts when independently erasable ownership matters.

Create an object with `POST /v1/artifacts/put` and a new UUID:

```json
{
  "id": "ea1cef23-4bb7-4f29-84e5-e035bd6c0462",
  "data_subject_id": "opaque-subject-reference",
  "kind": "state",
  "name": "scratchpad",
  "content": "AQID"
}
```

`content` is canonical base64; this example stores the three bytes `01 02 03`. An empty string stores
zero bytes. Missing/null values, integer arrays and noncanonical base64 are refused. The response
contains ID, version, source observation, owner, kind/name, writer credential ID, byte count,
creation/update/expiry times and `replayed`. All four operations return 200 on success.

Use `POST /v1/artifacts/get` with `{"id":"<uuid>"}` to retrieve exact bytes and current metadata.
Use `POST /v1/artifacts/list` to browse metadata with optional `data_subject_id`, `after` UUID and
`limit` (default 20, maximum 100), `kind` (`state` or `file`) and `name_prefix`. Name prefixes are
case-sensitive literal text: `%`, `_` and backslashes are not wildcard syntax. Filters combine, and
search returns metadata without inspecting file contents. Continue using `next.id` as `after`. Pages are live, ordered by
immutable ID: deleting the cursor object does not skip later objects, but concurrent insertions
behind the cursor require restarting the browse. Cursors do not confer access to another project.

Overwrite by sending the complete put request with `expected_version` set to the current version.
Ownership and kind cannot change. The accepted response supplies a new version; retries of that same
last update are recognized without another write. Stale competing updates return 409
`artifact_conflict`. Retrying the original creation returns current metadata and never restores its
old bytes. Changing a creation request under an existing ID conflicts. Previous byte versions are
not archived. An update does not extend the original expiry.

Delete with `POST /v1/artifacts/delete`:

```json
{"id":"<uuid>","expected_version":"<current-version>"}
```

The response is `{"deleted":true}`. Unknown, foreign, expired and deleted objects return the same
404 `not_found` on reads/deletion. A completed delete repeated after a lost response returns 404.
Deleted/erased/expired identities cannot be reused by delayed creation retries; intentionally new
objects require new UUIDs. Mutation and success audit are atomic. A transient database interruption
rolls back the operation; retry the same request, and inspect current metadata on version conflict.

Project credentials determine access. Read-only credentials can get/list/export but cannot put or
delete. Subject filters help inventory and are not a separate authorization boundary. The project
contract is described in [content access](16-content-access.md).

The default allowance is 256 KiB per object, 16 MiB of current payload and 128 objects per project,
with a maximum 30-day lifetime. A shorter project retention policy wins. The request body remains
limited to 1 MiB, including base64 and JSON. The maximum configurable single object is 512 KiB.
Quota exhaustion returns 429 `storage_capacity`; repeating blindly cannot free capacity. Expired
objects become unreadable immediately but retain their allowance until the retention sweep deletes
them. A lower policy preserves accepted data and permits shrinking or deletion.

An operator inspects usage with `taisce artifact limits --project <project>`. Replace all allowances
in one audited operation:

```sh
taisce artifact limits --project example \
  --max-object-bytes 262144 --max-bytes 16777216 \
  --max-objects 128 --max-age-hours 720
```

The command uses the administrator connection. Runtime database credentials cannot edit limits or
usage. Limits count current payload bytes and object rows; indexes, metadata, WAL, backups and retry
tombstones require separate capacity planning. New expiry policies apply to newly created objects.

Export includes the `agent_artifact` section; PostgreSQL's JSON export represents binary `bytea` as
hexadecimal text prefixed with `\x`. Subject erasure and retention include artifact counts and release
quota through the shared projection registry. These opaque rows are authoritative storage and need
backup. Fact recovery does not reconstruct them. Session lifecycle, checkpoint indexes and framework
serialization protocols are separate work; this endpoint does not claim those contracts.
