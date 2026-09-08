<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Inspecting a saved citation

Send `POST /v1/citations/resolve` with the same project credential used for recall:

```json
{"id":"the-fact-UUID-returned-by-recall","limit":8}
```

The response describes the retained fact and exact supporting messages. Byte offsets index the
original UTF-8 message, not characters or a concatenation of the conversation. `context` is a
bounded readable window; `context_complete` says whether it includes the entire message.
`occurred_at` belongs to that message. `valid` and `known` are distinct stored temporal intervals;
an absent endpoint is unbounded. `source_role` describes the fact's formation attribution, while
an evidence row's `role` comes from its source message.

If `next` is present, send it unchanged as `after` with the same fact ID. Continue until `next` is
absent. A page has at most the requested source count (default 8, maximum 32) and 256 KiB of decoded
text; serialization can expand escaped text. Pagination can therefore return fewer sources than
requested. Each page has a consistent database snapshot, but the whole sequence is live. Erasure
or newly recorded support can change subsequent pages. Use subject export for bulk transfer.

A retained superseded fact can return `validity_closed`; a closed knowledge interval returns
`knowledge_closed`. `current` means both stored upper bounds are open, not that the claim has been
independently verified or is valid at every instant. Recorded supersession links are available in `superseded_by` and
`supersession_source_observation_id`. An optional `history_available_from` identifies an upgrade
boundary; earlier knowledge queries are unavailable. [Historical recall](09-temporal-history.md)
selects preserved validity versions. Retraction relationships and rebuild-stable citation mappings
remain #89/#58. Do not infer a successor from similar text or matching predicates.

Unknown, erased and other-project fact IDs all return the same `404 not_found`. Invalid identifiers
or page parameters return `400 invalid_citation`. Oversized legacy source text returns
`422 citation_limit`; use the subject export. Inconsistent source bytes return a generic `500`
without partial evidence. Normal authentication, project suspension and admission responses also
apply. Audit records contain the operation and returned evidence count, not the citation content.

The generated [HTTP contract](02-contract.md) lists the full response shape. [D77](01-decisions.md)
records the implementation choices and remaining limits.
