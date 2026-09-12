<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Answer with evidence

**Goal:** every claim your agent makes from memory shows the words it came from, and any claim can
be traced back to the message that said it.

You need Taisce running, plus `TAISCE_URL`, `TOKEN` and the Python `client` from
[Before you start](overview.md#before-you-start).

## 1. Save a turn and wait for it to form

```bash
OFFSET=$(curl -sS -X POST "$TAISCE_URL/v1/observations" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"idempotency_key": "'"$(uuidgen)"'", "data_subject_id": "priya",
       "messages": [{"role": "user", "content": "Northwind uses PostgreSQL for billing, and I manage the payments team there."}]}' \
  | jq .log_offset)
until curl -sS "$TAISCE_URL/v1/freshness" -H "Authorization: Bearer $TOKEN" \
  | jq -e --argjson o "$OFFSET" '.formed != null and .formed >= $o' >/dev/null; do sleep 2; done
```

```python
receipt = client.post("/v1/observations", json={
    "idempotency_key": str(uuid.uuid4()),
    "data_subject_id": "priya",
    "messages": [{"role": "user", "content": "Northwind uses PostgreSQL for billing, "
                                             "and I manage the payments team there."}],
}).raise_for_status().json()
wait_formed(receipt["log_offset"])
```

When it returns, the turn has been read and its facts are in memory.

## 2. Recall, and show each quote in its sentence

Ask about Northwind by name. No subject is needed, because the question names what it is about.

```bash
curl -sS -X POST "$TAISCE_URL/v1/recalls" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"question": "What does Northwind use?"}' \
  | jq '.facts[] | {fact_id, statement, quote: .evidence.quote, context: .evidence.context}'
```

```python
bundle = client.post("/v1/recalls", json={"question": "What does Northwind use?"}).raise_for_status().json()
for fact in bundle["facts"]:
    ev = fact["evidence"]
    ctx = ev["context"].encode()                  # positions count UTF-8 bytes, not characters
    at = ev["byte_start"] - ev["context_start"]   # where the quote starts inside the context
    end = at + (ev["byte_end"] - ev["byte_start"])
    marked = (ctx[:at] + b"[" + ctx[at:end] + b"]" + ctx[end:]).decode()
    print(f'{fact["statement"]}  [fact:{fact["fact_id"]}]')
    print(f'    "{marked}"')
```

You should see the claim, its id, and the exact words that support it (the Python version marks
them in brackets):

```text
Northwind uses PostgreSQL.  [fact:…]
    "[Northwind uses PostgreSQL] for billing, and I manage the payments team there."
```

Every fact carries an `evidence` object:

| Field | What it tells you |
|---|---|
| `observation_id`, `source_ordinal` | Which turn, and which message in it, the fact came from |
| `quote` | The words that support the fact, copied from the message |
| `byte_start`, `byte_end` | Where the quote sits in that message, in UTF-8 bytes |
| `context`, `context_start` | The words around the quote, and where they start, so you can see whether the sentence stated it, hedged it or denied it |
| `context_complete` | `true` when the context is the whole message |

The quote is not the model's word for it. The model proposes a fact and a quote, and Taisce keeps the
fact only if it finds that quote in the message itself, then records the position it found.

## 3. Trace a claim back to its source

A `fact_id` resolves to the stored fact, its time ranges, and every piece of evidence behind it,
read back from the stored message.

```bash
FACT=$(curl -sS -X POST "$TAISCE_URL/v1/recalls" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"question": "What does Northwind use?"}' | jq -r '.facts[0].fact_id')
curl -sS -X POST "$TAISCE_URL/v1/citations/resolve" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d "{\"id\": \"$FACT\"}" | jq
```

```python
fact_id = bundle["facts"][0]["fact_id"]
citation = client.post("/v1/citations/resolve", json={"id": fact_id}).raise_for_status().json()
print(citation["status"], citation["statement"])
for ev in citation["evidence"]:
    print(ev["role"], ev["log_offset"], ev["occurred_at"], repr(ev["quote"]))
```

You should see (trimmed):

```json
{
  "id": "…",
  "predicate": "uses",
  "statement": "…",
  "status": "current",
  "source_role": "user",
  "recorded_at": "…",
  "valid": {"from": "…", …},
  "known": {"from": "…", …},
  "evidence": [
    {"source_observation_id": "…", "source_ordinal": 0, "log_offset": …, "role": "user",
     "occurred_at": "…", "quote": "Northwind uses PostgreSQL", "byte_start": 0, "byte_end": 25,
     "context": "Northwind uses PostgreSQL for billing, and I manage the payments team there.",
     "context_complete": true, …}
  ]
}
```

This is the full record behind the claim:

- `status` is `current` while Taisce still believes it. `validity_closed` means it stopped being
  true, usually because a newer fact replaced it, and `superseded_by` then names that fact.
  `retracted` means someone withdrew it, and `retraction` says who and when. `knowledge_closed`
  means Taisce stopped believing it without a retraction record.
- `valid` is when the fact was true in the world. `known` is when Taisce believed it.
- `evidence` lists every message that supports the fact, with who said it (`role`), where it sits
  in the log (`log_offset`) and when it was said (`occurred_at`). A fact said many times has many
  entries; page through them with `limit`, and pass the `next` value you get back as `after`.

## 4. When a citation is gone

Resolve an id that does not exist, belongs to another project, or was erased:

```bash
curl -sS -X POST "$TAISCE_URL/v1/citations/resolve" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"id": "00000000-0000-4000-8000-000000000000"}'
```

```python
r = client.post("/v1/citations/resolve", json={"id": "00000000-0000-4000-8000-000000000000"})
print(r.status_code, r.json())
```

You should see `404`:

```json
{"error":{"code":"not_found","message":"citation not found"}}
```

All three cases get the same answer on purpose, so a token for one project learns nothing about
another. In your interface, show a claim whose citation no longer resolves as "source removed".

> [!TIP]
> Put `[fact:<fact_id>]` markers in your agent's answers and resolve them when a user clicks. The
> Claude Code plugin's recall command asks Claude to cite memory in exactly this form.

## What next

- Change a claim that is wrong: [changing what memory believes](../developers/http-api.md#correcting-records).
- Remove a person's words, and everything built from them: [forget a person](forget-a-person.md).
- Every field on a citation: [showing the words behind a claim](../developers/http-api.md#citations-the-words-behind-a-fact).
- How facts get their time ranges: [how it works](../start/how-it-works.md).
