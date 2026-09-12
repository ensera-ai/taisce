<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Recall controls

Each recall can ask for a smaller answer, choose whose statements to trust, and choose which kinds of
memory to search. None of this needs a server change.

## Send controls with a recall

```http
POST /v1/recalls
Authorization: Bearer <credential>
Content-Type: application/json

{
  "question": "Where does Atlas work?",
  "max_characters": 4000,
  "source_roles": ["user", "tool"],
  "hops": 2,
  "surfaces": ["facts", "passages"]
}
```

Every successful response echoes what was actually used:

```json
{
  "controls": {
    "max_characters": 4000,
    "max_rows": 200,
    "source_roles": ["user", "tool"],
    "hops": 2,
    "surfaces": ["facts", "passages"],
    "themes": false
  },
  "characters": 1312,
  "truncated": false,
  "degraded": []
}
```

## The controls

**`max_characters`** caps the size of the answer. It must be positive and no larger than the server
budget, which is 16,000 by default. Operators change the budget with `TAISCE_BUNDLE_CHARACTERS`. You
can't raise the row limit (`max_rows`) from a request.

**`source_roles`** chooses whose statements count. Leave it out to get `user` statements only. You
can list any of `user`, `assistant`, `system` and `tool`, with no repeats. An empty list or an
unknown role is refused. Each fact keeps its own `source_role`, so turning on `tool` doesn't make a
tool's claim look like something the user said. Document text is usually stored with the `tool`
role.

**`hops`** is how far to follow links between entities. Values below 1 become 1, and values above 2
become 2.

**`surfaces`** chooses which kinds of memory to search: `facts`, `reports` and `passages`. Leave it
out to search all three. An empty list, a repeat or an unknown name is refused.

**`themes`** set to `true` also consults the report hierarchy when a name matched.

Any other invalid control returns `400 invalid_recall_controls`.

## How the budget is counted

`characters` counts Unicode code points in the text you get back: fact subjects, predicates, objects,
statements, source-role labels, quotes, contexts and path names. IDs, timestamps and JSON syntax
don't count. If you need a token count or a byte size, work it out on your side.

When the budget runs out, the response sets `truncated: true`. Taisce never goes over the budget and
never cuts a quote in half to fit. If even the first fact is too large, `facts` is empty and
`characters` is 0. An ordinary empty answer has `truncated: false`.

## What gets searched, in order

A recall tries up to four steps, all sharing one budget:

1. Exact entity names from the question.
2. Similar entity names, if no exact name matched.
3. Reports (short summaries of related facts), if nothing matched or `themes` is `true`.
4. Passages (short quotes from stored messages), with whatever budget is left.

Reports come back with a title, a summary and their source messages. Passages come back with their
role, source, offsets and a short quote. A passage is evidence, not a fact.

If the server has no embedding model configured, only step 1 runs. When you named a surface in
`surfaces` that couldn't run, `degraded` lists it.

## Watch out for

- **Results are not ranked by relevance.** Don't assume the first fact is the best one.
- **Selecting more roles doesn't loosen other rules.** Project access, subject filters, time filters
  and erasure all still apply.
- **The project comes from the credential.** You can't pick one in the request.

## How it works

The server has one budget and each request can only shrink it. That keeps the worst-case response
size under the operator's control. The budget counts characters rather than tokens because a token
count depends on your model's tokenizer, which the server can't know.

The fixture in [`internal/api/testdata/recall-controls.json`](../internal/api/testdata/recall-controls.json)
lists the requests and expected results. The server test suite runs it against a real database, and
adapter test runners can reuse it.
