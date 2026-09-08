<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Recall budgets and source selection

Each client can request a smaller context allowance without changing server configuration:

```json
{
  "question": "Where does Atlas work?",
  "max_characters": 4000,
  "source_roles": ["user", "tool"],
  "hops": 2
}
```

Send this body to `POST /v1/recalls` with a project credential. Omitting `max_characters` uses the
server budget (16,000 by default, configurable through `TAISCE_BUNDLE_BUDGET`). A supplied value must
be positive and cannot exceed the configured budget. Clients cannot increase the server row limit.

Omitting `source_roles`, or sending null, selects only `user`. An explicit list can contain `user`,
`assistant`, `system`, and `tool`, without duplicates. Empty lists and unknown roles are refused.
Each returned fact retains its `source_role`; enabling tool or model assertions does not make them
user statements. Fetched document text uses its stored message role, commonly `tool`. This selector
does not add document ingestion or a separate `document` role.

Every successful response includes `controls` with `max_characters`, `max_rows`, `source_roles`, and
`hops`. Requested hops below 1 become 1; values above 2 become 2. Other invalid controls return
`400 invalid_recall_controls`. Project access always comes from the credential. Subject, temporal
and erasure filters still apply when a caller selects additional roles.

The `characters` field counts Unicode code points in fact subjects, predicates, objects, statements,
source-role labels, quotes, contexts, path names and path predicates. Quote and context both count.
Identifiers, anchor metadata, timestamps and JSON syntax are excluded. Clients must calculate their
own token usage and serialized-response size if those are their constraints.

A budget cut returns `truncated: true`. If the first fact is too large, `facts` is empty and
`characters` is zero; an ordinary empty recall has `truncated: false`. The service does not exceed
the allowance or split citation text to force a fact into it. Results are not relevance-ranked.

The [conformance fixture](../internal/api/testdata/recall-controls.json) is exercised against real
HTTP/PostgreSQL by `TestRecallControlConformanceOverHTTP`. SDK runners can reuse its requests and
expectations with the documented 100,000-character test ceiling and one seeded named fact for each
stored source role. [D82](01-decisions.md) records the accounting and trust decisions.
