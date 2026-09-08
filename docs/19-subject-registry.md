<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Subject inventory and external references

A subject is application-declared attribution within a project. Register it once, then use its
returned random UUID as `data_subject_id` in observations, assertions, artifacts, recall, export
and erasure. A credential supplies the project. A subject ID is neither a credential nor evidence
that a person was authenticated. Project credentials retain project-wide access under D90.

The optional registry keeps personal account references out of the many rows carrying attribution.
It does not inspect source text or opaque files for identifiers that the application put there.
Random IDs are pseudonymous metadata, not anonymization; an authorized mapping resolves them.

## Register, resolve and rotate

Every operation is an authenticated POST with one bounded JSON object. The generated
[wire contract](02-contract.md) describes the fields and responses.

`/v1/subjects/register`:

```json
{
  "idempotency_key": "de8bf4fa-5b6b-4310-a781-a58c46cc5bbd",
  "external_reference": "account-123",
  "label": "Support account"
}
```

Generate one random UUID retry key per registration intent and preserve it across retries.
The response includes `id`, `version`, current metadata, timestamps, `updated_by` and `replayed`.
Use the returned `id` for subsequent memory operations; do not send the external reference as
`data_subject_id`. Registration creates no observation, inferred entity, extraction work or claim.

`/v1/subjects/get` accepts exactly one selector:

```json
{"external_reference":"account-123"}
```

Or send `{"id":"<returned subject UUID>"}`. References are exact, case-sensitive UTF-8 strings:
no case folding, trimming, Unicode normalization or hashing. Applications define their own identifier
semantics before registering. A reference is unique within a project; the same text in another
project identifies a separate entry. Omit a reference to use only the random ID.

`/v1/subjects/update` replaces the two mutable fields:

```json
{
  "id": "<returned subject UUID>",
  "expected_version": "<current version UUID>",
  "external_reference": "account-456",
  "label": "Current support account"
}
```

Omitted or empty reference/label clears that field. IDs, creation time and retention deadline are
immutable. The old reference stops resolving; existing memory keeps its original subject UUID.
There is no alias archive and no automatic merge. A collision returns 409 and changes nothing.
Changing which real person an account represents calls for a new subject, not rotating the mapping
onto another person's existing memory. The caller is responsible for that distinction.

An identical immediate overwrite retry returns the accepted version without another mutation.
Other stale writes return 409; inspect current state before deciding on a new edit. Retrying the
original registration returns current metadata, including subsequent rotation, instead of restoring
its original label/reference. Changed registration intent under an existing key returns 409.
Successful mutations and their content-free audit entry commit together.

## Inventory and refusals

`/v1/subjects/list` accepts `limit` (default 20, range 1–100) and `after` (a previous `next.id`).
Results are ordered by immutable subject ID. A deleted cursor remains usable. Pages are live views,
not a transaction spanning requests; concurrent registrations below the cursor require a new pass.
Only registered subjects appear. Application-managed attribution without a registration is not
silently imported or inferred into this inventory.

Read-only project credentials may get/list/export but cannot register/update/erase. Unknown,
foreign, erased and inactive unused entries return the same 404. Invalid UUIDs, conflicting selectors,
NUL/invalid UTF-8, blank nonempty metadata or excessive fields return 400. Reference and label limits
are 1,024 and 256 UTF-8 bytes respectively. The shared HTTP body and admission budgets also apply.
Storage failures return a generic 500 and no partial mutation. External references, labels, subject
IDs and payload fingerprints are excluded from permanent audit entries and metric labels.

## Retention, erasure and recovery

Registration stamps `inactive_after` from the project policy. Null means indefinite retention until
explicit erasure. A finite deadline is not extended by metadata updates or later policy changes.
Once due, the mapping expires only after **all attributed observations are gone**, including retained
artifact sources. Keeping it while source bytes survive allows the application to discover the ID
needed for export or erasure. Registry-only expiry is scheduled even in a quiet project whose current
policy has become indefinite. A delayed worker can leave expired unused rows physically present;
get/list/update already refuse them, while export still reports everything physically held.

The existing subject export includes `data_subject` and linked `subject_retry` sections as well as
sources and projections. Erasure counts the registry deletion and residual, and clears retry payload
metadata in the same transaction. This also works for a registered subject with no observations.
Physical expiry follows the same retry-clearing rule. Minimal project-scoped retry-key digests remain
as tombstones so a delayed registration cannot recreate an erased mapping. The original request
fingerprint is retained while its registration lives, even after a reference rotates; it is not a
hash-based anonymization guarantee. It is removed with the mapping, as is the latest update digest.

New collection after erasure requires a new registration key and receives a new ID. Existing
application-managed opaque `data_subject_id` values remain supported. A registry entry does not
install a permanent ban on future writes using a known ID: if deletion completes before new source
acceptance, that source is new unregistered collection. Applications should resolve/register again
before collecting and stop sending deleted IDs. Observation/assertion/artifact retry receipts still
prevent reusing their own erased operation identities.

The registry is authoritative bookkeeping and must be backed up. Fact recovery uses retained source
attribution and leaves the current mapping intact. Projection rebuilds must preserve the registry;
external references cannot be reconstructed from facts. There is no automatic migration or merge of
existing application-managed IDs: changing identity would require an explicit attributable source
rewrite and temporal/erasure policy. D94 records this boundary. Full projection generations remain
#58, independently of the subject contract.

## Cost

A registration writes one bounded registry row, one retry receipt and one audit entry, without a
model call. Reference rotation updates one indexed mapping. Source attribution adds an indexed
optional-registry lookup and row lock; ordinary recall adds no registry join. Metadata reads and
expiry check surviving sources through the existing project/subject index. Expired mappings with
long-lived sources increase those checks; returned/deleted pages are bounded, examined rows need
not be. Total metadata/tombstone capacity and controlled workload measurements remain #93/#103.
This implementation does not claim production latency or throughput.
