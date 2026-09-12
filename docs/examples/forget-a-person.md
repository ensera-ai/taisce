<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Forget a person

**Goal:** erase everything one person said, and everything Taisce built from it, then read a
receipt that proves what is left.

You need Taisce running, plus `TAISCE_URL`, `TOKEN` and the Python `client` from
[Before you start](overview.md#before-you-start).

## 1. Two people mention the same company

Alice and Bob both work at Ensera. Keep Alice's idempotency key: you will use it again in step 5.

```bash
ALICE_KEY=$(uuidgen)
curl -sS -X POST "$TAISCE_URL/v1/observations" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"idempotency_key": "'"$ALICE_KEY"'", "data_subject_id": "alice",
       "messages": [{"role": "user", "content": "I work at Ensera and I live in Dublin."}]}'
OFFSET=$(curl -sS -X POST "$TAISCE_URL/v1/observations" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"idempotency_key": "'"$(uuidgen)"'", "data_subject_id": "bob",
       "messages": [{"role": "user", "content": "I work at Ensera too."}]}' | jq .log_offset)
until curl -sS "$TAISCE_URL/v1/freshness" -H "Authorization: Bearer $TOKEN" \
  | jq -e --argjson o "$OFFSET" '.formed != null and .formed >= $o' >/dev/null; do sleep 2; done
```

```python
alice_turn = {
    "idempotency_key": str(uuid.uuid4()),
    "data_subject_id": "alice",
    "messages": [{"role": "user", "content": "I work at Ensera and I live in Dublin."}],
}
print(client.post("/v1/observations", json=alice_turn).raise_for_status().json())
bob = client.post("/v1/observations", json={
    "idempotency_key": str(uuid.uuid4()),
    "data_subject_id": "bob",
    "messages": [{"role": "user", "content": "I work at Ensera too."}],
}).raise_for_status().json()
wait_formed(bob["log_offset"])
```

You should see Alice's receipt, and then the wait returns once both turns have formed:

```json
{"id":"…","scope":"default","log_offset":…}
```

## 2. See what is held about Alice

An **export** returns everything stored for one person: her messages, and every row built from them.

```bash
curl -sS -X POST "$TAISCE_URL/v1/exports" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"data_subject_id": "alice"}' | jq .rows
```

```python
export = client.post("/v1/exports", json={"data_subject_id": "alice"}).raise_for_status().json()
print(export["rows"])
```

You should see a count per kind of row (trimmed):

```json
{"message": 1, "fact": …, …}
```

`sections` in the same answer holds the rows themselves. `rows` is there so you can compare the
export with the erasure receipt in the next step without reading either in detail.

## 3. Erase Alice

```bash
curl -sS -X POST "$TAISCE_URL/v1/erasures" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"data_subject_id": "alice", "reason": "user asked to be forgotten"}' | jq
```

```python
receipt = client.post("/v1/erasures", json={
    "data_subject_id": "alice",
    "reason": "user asked to be forgotten",
}).raise_for_status().json()
print(receipt["deleted"], receipt["residual"], receipt["clean"])
```

You should see the receipt (trimmed; which kinds appear and their counts depend on what formed):

```json
{
  "request_id": "…",
  "scope": "default",
  "data_subject_id": "alice",
  "completed_at": "…",
  "deleted": {"observation": 1, "fact": 2, "entity": …, "chunk": 1, …},
  "residual": {"fact": 0, "entity": 0, "chunk": 0, …},
  "clean": true
}
```

How to read it:

- `deleted` counts what was removed, by kind: Alice's turn, the facts formed from it, the things
  only she mentioned, and so on.
- `residual` counts what still matches Alice **after** the delete, checked in the same database
  transaction. This is the proof.
- `clean` is `true` only when every residual count is zero.

You need both counts. `deleted` alone cannot tell a thorough erasure from one that found nothing,
and `residual` alone cannot tell a clean sweep from a person who was never there.

## 4. Check what is left

```bash
curl -sS -X POST "$TAISCE_URL/v1/recalls" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"question": "Where do I work?", "data_subject_id": "alice"}' | jq '.facts'
curl -sS -X POST "$TAISCE_URL/v1/recalls" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"question": "Who works at Ensera?"}' \
  | jq '[.facts[] | {predicate, object, quote: .evidence.quote}]'
```

```python
for body in ({"question": "Where do I work?", "data_subject_id": "alice"},
             {"question": "Who works at Ensera?"}):
    bundle = client.post("/v1/recalls", json=body).raise_for_status().json()
    print([(f["predicate"], f["object"], f["evidence"]["quote"]) for f in bundle["facts"]])
```

You should see nothing for Alice, and Bob's fact still there:

```text
[]
[{"predicate": "works_at", "object": "Ensera", "quote": "I work at Ensera too"}]
```

Ensera survives because Bob mentioned it too. Something that another person's words also support
is theirs as much as Alice's, so it is kept, and it does not count as residual.

## 5. Try to write Alice's words back

Resend Alice's first turn with its original idempotency key, as a retry queued before the erasure
would:

```bash
curl -sS -X POST "$TAISCE_URL/v1/observations" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"idempotency_key": "'"$ALICE_KEY"'", "data_subject_id": "alice",
       "messages": [{"role": "user", "content": "I work at Ensera and I live in Dublin."}]}'
```

```python
r = client.post("/v1/observations", json=alice_turn)
print(r.status_code, r.json())
```

You should see `409`:

```json
{"error":{"code":"idempotency_conflict","message":"idempotency key was already used for a different or erased observation"}}
```

An erased turn's key stays reserved for good, so a late retry cannot quietly bring back what
somebody asked you to remove. Do not work around this with a new key unless the person gives you the
information again.

## 6. Find the receipt later

Taisce keeps every erasure receipt, without any of the erased content. Operators list them on the
management surface, which the compose file serves on `127.0.0.1:8081` and which takes the
**operator** token that setup printed next to the project token:

```bash
export OPERATOR_TOKEN=$(docker compose logs --no-log-prefix bootstrap | awk '/^operator token:/ {print $3}')
```

Then:

```bash
curl -sS -X POST localhost:8081/manage/v1/erasures/list \
  -H "Authorization: Bearer $OPERATOR_TOKEN" -H "Content-Type: application/json" \
  -d '{"project": "default"}' | jq
```

```python
manage = httpx.Client(
    base_url="http://localhost:8081",
    headers={"Authorization": f"Bearer {os.environ['OPERATOR_TOKEN']}"},
)
print(manage.post("/manage/v1/erasures/list", json={"project": "default"}).raise_for_status().json())
```

You should see the receipts, newest first (trimmed):

```json
{"erasures": [{"id": "…", "project": "default", "selector": …, "reason": "user asked to be forgotten",
               "requested_at": "…", "completed_at": "…", "residual": {…}}]}
```

## Erasing a document instead of a person

Material with no person behind it, such as a document your agent read, is erased by the ids of the
turns it was saved as. Send `source_observation_ids` instead of `data_subject_id`, never both, with
up to 10,000 ids per request:

```json
{"source_observation_ids": ["…", "…"], "reason": "document withdrawn"}
```

## Good to know

- **Other people's words about Alice are theirs.** If Bob had said "Alice manages me", that fact
  comes from Bob's turn and survives Alice's erasure. To remove it, erase Bob's turn by its id.
- **Turns saved without a subject are not reached** by a person's erasure. Erase those by id.
- **Erasing twice is harmless.** The second receipt shows nothing deleted and nothing left.
- **Erasure covers your database.** If you use a hosted model provider, Taisce cannot tell you what
  the provider kept of the text it was sent.

## What next

- What a clean receipt does and does not promise: [the residual and the receipt](../architecture/governance.md#the-receipt-deleted-residual-and-clean)
  and [what erasure cannot establish](../architecture/governance.md#what-erasure-cannot-reach).
- Fix a single fact instead of erasing a person: [changing what memory believes](../developers/http-api.md#correcting-records).
- Show where a claim came from before you delete it: [answer with evidence](answer-with-evidence.md).
