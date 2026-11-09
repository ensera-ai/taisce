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

**`explain`** set to `true` adds an `explain` object beside the bundle: which arm proposed each
anchor, what each stage offered, and what the cuts dropped. Off by default, because it is a larger
response and most recalls do not need it. It reports and changes nothing — the bundle you get with it
is the bundle you would have got without it.

Any other invalid control returns `400 invalid_recall_controls`.

## Why a recall answered that way

```json
{
  "explain": {
    "anchors": [
      {"entity_id": "7f…", "name": "Marta", "arm": "name", "matched": "marta", "score": 0, "kept": true, "facts": 2},
      {"entity_id": "91…", "name": "Ensera", "arm": "meaning", "matched": "", "score": 0.77, "kept": false, "facts": 0}
    ],
    "stages": [
      {"surface": "facts", "ran": true, "offered": 7, "kept": 2,
       "dropped": [{"ref": "f3…", "cut": "budget", "characters": 180, "score": 0.9}]},
      {"surface": "reports", "ran": false, "offered": 0, "kept": 0, "dropped": []},
      {"surface": "passages", "ran": true, "offered": 3, "kept": 3, "dropped": []}
    ]
  }
}
```

**`arm`** is how an anchor got in: `name` for a term in your question that matched a stored name or
alias, `meaning` for the semantic surface's proposal. `score` is the meaning arm's similarity; the
name arm matched exactly and has nothing to score. **`kept: false`** is an anchor that was proposed
and then dropped — a name the meaning arm proposed that the person you asked about has no fact about.
That row is the one the bundle cannot show you, because `anchors` lists only what survived.

**`offered`** is what a stage produced before anything was cut; **`kept`** is what reached you;
**`dropped`** is the difference, in the order the candidates were offered. **`cut`** says which cut
took it: `rows` is the read's own row bound, which a larger `max_characters` does not change, and
`budget` is your character allowance, which it does. **`characters`** is what the candidate would
have cost — a first dropped candidate costing more than your whole remaining budget means ask for
more characters rather than doubt the retrieval.

**`ran: false`** is a stage that was selected and skipped: no anchor to walk from, no embedding model
configured, or a subject filter that withholds reports. `degraded` says which. A stage you did not
select is not in `stages` at all.

**It carries no text.** A fact your budget cut is a fact you were not given, so a dropped candidate
is a reference, a size and a score — never the statement, quote or passage. Resolve the reference
with [citation resolution](08-citation-resolution.md) or
[record inspection](11-record-inspection.md) if you need the words.

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
