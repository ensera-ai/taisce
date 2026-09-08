<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Retracting a retained record

A project credential with write access can withdraw an erroneous claim without deleting the
source words or pretending the system never knew it. Read its current `version` through record
inventory, history or citation resolution, then send:

```http
POST /v1/records/retract
Authorization: Bearer <project-write-credential>
Content-Type: application/json

{"records":[{"id":"<record-uuid>","expected_version":"<version-uuid>"}]}
```

The response contains each record's `id`, new `version`, `operation_id`, authenticated
`principal_id` and `retracted_at`. The caller cannot supply attribution or a withdrawal date.
All records in one request either commit together or remain unchanged, including their source
instructions, report invalidation and success audit. A request accepts 1–20 distinct records and
at most 128 supporting evidence rows across the batch. Repeated spans count against that bound.

A stale version or already withdrawn record returns `409 record_conflict`. Inspect the record
before deciding what to do next, including after a lost success response; replaying its old version
does not create another withdrawal. Unknown, foreign-project and erased records return the same
`404 not_found`. Invalid identifiers, versions or batch sizes return `400 invalid_record_mutation`;
excess support returns `413 record_mutation_limit`. Read-only credentials receive `403 forbidden`.

Withdrawal ends the current knowledge interval. Current recall excludes the claim; earlier
`as_known_at` reads can still return it. Its validity interval and exact citation bytes remain
unchanged. Inventory, history and citation resolution expose `retracted` status and attributable
withdrawal metadata. Supersession and every other fact update change the version, so an editor
cannot unknowingly act on a record changed since inspection.

The withdrawal instruction is owned by each supporting source message. Formation checks it before
asserting a claim with the same normalized subject, predicate and object from that message. A
different paraphrase or date cannot bypass it, even if the derived fact has been lost. An independent
later source may assert the claim again. Retained source claims have deterministic record identities:
replaying the same structural claim returns its retained record without changing its evidence,
version or temporal ranges. This is retry behavior; source reinterpretation is separate rebuild work.

Subject export includes `record_retraction` instructions. Erasure counts their deletion and residual;
source retention expiry cascades their deletion. Matching hashes are sensitive source-owned metadata,
not anonymized data. The audit stores actor, project, operation and count, without claim text or
record/source identifiers. A stolen write credential can retract its project's records; this endpoint
does not provide finer human roles than the project credential boundary.

Corrections, feedback, entity purge and a supported rebuild workflow remain tracked by #89/#58.
Recovery tests currently prove source replay, stable claim identity and withdrawal preservation under
derived-row loss; they do not establish a complete generation or citation migration policy.
