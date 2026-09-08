<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Historical knowledge

`POST /v1/recalls` accepts two independent RFC 3339 timestamps:

```json
{
  "question": "Where did I live?",
  "data_subject_id": "subject-1",
  "as_of": "2026-08-01T00:00:00Z",
  "as_known_at": "2026-03-10T00:00:00Z"
}
```

`as_of` selects when the claim was valid. `as_known_at` selects the knowledge version. Omitting
knowledge time uses current knowledge. Omitting both uses the still-open current relation.

For example, first record Dublin valid from March. Later record Amman valid from June. A query
for August using knowledge from before the move was recorded returns the then-open Dublin claim.
The same August query using current knowledge returns Amman. An April query using current knowledge
returns Dublin ending in June; using the earlier knowledge version returns Dublin with no end.

Preservation starts with migration 0029. Projects that already contained facts receive a historical
availability boundary because overwritten versions cannot be recovered. Earlier knowledge queries
return `422 history_unavailable`; no result is guessed. Fresh projects have no such missing-history
interval. Current citation inspection reports `history_available_from` when a boundary exists.

Fact UUIDs survive supported supersession. Citation inspection returns `superseded_by` and
`supersession_source_observation_id` where retained. Subject export includes `fact_history` rows and
their temporal boundaries. Citation inspection returns the current retained record, while the
recall timestamps select a historical view. A separate record-history API remains planned.

Erasure and retention remove historical derivatives too. If the source that established a closing
date is erased, the affected fact can become unavailable even when its original observation remains.
Reopening that fact would require reconstruction under the future rebuild workflow. This preserves
the erasure boundary without guessing at truth. An already returned client response cannot be revoked.

The historical view preserves fact validity/knowledge, not prior entity labels, alias lookup state,
or an exact replay of an earlier API response. Arbitrary retroactive corrections, mutable-record
versioning and rebuild mappings remain separate roadmap work. [D78](01-decisions.md) records the
storage, indexing, upgrade and governance decisions.
