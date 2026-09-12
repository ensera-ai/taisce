<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# The HTTP API

Everything Taisce does for your agent is a JSON call over HTTP. This page starts with the three calls
every integration makes: save a turn, ask a question, and check whether memory has caught up. Then
come the rest, grouped by what you want to do. It ends with every error code and a retry loop you
can copy.

Using Python, Java or .NET? An [adapter](adapters.md) wraps these calls on your framework's own
seam. This page is for everyone else, and for anyone who wants to see what an adapter sends.

## Before you start

You need the API address and a project token, a secret key that starts with `tsk_`. The
[quickstart](quickstart.md) shows where both come from. The examples below build on this setup:

```bash
export TAISCE_URL=http://localhost:8080
export TOKEN=tsk_...   # a project token
```

```python
import os
import uuid

import httpx

client = httpx.Client(
    base_url=os.environ["TAISCE_URL"],
    headers={"Authorization": f"Bearer {os.environ['TOKEN']}"},
    timeout=35.0,  # a little longer than the server's own 30-second limit
)
```

```typescript
const base = process.env.TAISCE_URL!;
const headers = {
  Authorization: `Bearer ${process.env.TOKEN}`,
  "Content-Type": "application/json",
};

// The server said no. Branch on `code`.
class Refusal extends Error {
  constructor(readonly status: number, readonly code: string, readonly retryAfter: number | null) {
    super(`${status} ${code}`);
  }
}

// call sends one request and returns the parsed answer, or throws a Refusal.
async function call(method: string, path: string, body?: unknown): Promise<any> {
  const res = await fetch(base + path, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
    signal: AbortSignal.timeout(35_000), // a little longer than the server's own 30-second limit
  });
  const data = await res.json();
  if (!res.ok) {
    const retryAfter = res.headers.get("Retry-After");
    throw new Refusal(res.status, data?.error?.code, retryAfter ? Number(retryAfter) : null);
  }
  return data;
}
```

```go
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"
)

// A little longer than the server's own 30-second limit, so a slow answer is not mistaken for a lost one.
var httpClient = &http.Client{Timeout: 35 * time.Second}

// Refusal is the server saying no. Branch on Code.
type Refusal struct {
	Status     int
	Code       string
	Message    string
	RetryAfter time.Duration
}

func (r *Refusal) Error() string { return fmt.Sprintf("%d %s: %s", r.Status, r.Code, r.Message) }

// call sends one request and decodes a successful answer into out.
func call(method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, os.Getenv("TAISCE_URL")+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+os.Getenv("TOKEN"))
	req.Header.Set("Content-Type", "application/json")
	res, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		var e struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.NewDecoder(res.Body).Decode(&e)
		r := &Refusal{Status: res.StatusCode, Code: e.Error.Code, Message: e.Error.Message}
		if s, err := strconv.Atoi(res.Header.Get("Retry-After")); err == nil {
			r.RetryAfter = time.Duration(s) * time.Second
		}
		return r
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(res.Body).Decode(out)
}
```

A few things hold for every call:

- **Your token picks the project.** No request names a project, so you can't reach the wrong one.
- **Read-only tokens exist.** They can ask, inspect and export. Anything that changes memory gets
  `403 forbidden`.
- **Bodies are checked strictly.** Send one JSON object of up to 1 MiB. An unknown or misspelled
  field, a duplicate key or broken JSON gets `400 invalid_body`. Nothing is silently ignored.
- **Most reads are POST.** A question, or a person's id, is personal data, and URLs end up in logs.
  Only `GET /v1/freshness`, `GET /health` and `GET /ready` are GETs.
- **Version 1 only grows.** New response fields, new optional request fields and new error codes can
  appear. Ignore fields you don't know, and give unknown error codes a default branch.

## The three calls everyone needs

### Save a turn

A **turn** is one exchange: its messages, in order, each with the role of whoever said it.
`POST /v1/observations` stores the turn and answers `201` straight away. Taisce reads facts out of it
afterwards, in the background. That step is called **formation**.

```bash
curl -sS -X POST "$TAISCE_URL/v1/observations" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{
    "idempotency_key": "'"$(uuidgen)"'",
    "data_subject_id": "alice",
    "messages": [
      {"role": "user", "content": "I live in Cork now."},
      {"role": "assistant", "content": "Noted."}
    ]
  }'
```

```python
turn = {
    "idempotency_key": str(uuid.uuid4()),  # one per turn; reuse it if you retry
    "data_subject_id": "alice",
    "messages": [
        {"role": "user", "content": "I live in Cork now."},
        {"role": "assistant", "content": "Noted."},
    ],
}
receipt = client.post("/v1/observations", json=turn).raise_for_status().json()
print(receipt["log_offset"])
```

```typescript
const turn = {
  idempotency_key: crypto.randomUUID(), // one per turn; reuse it if you retry
  data_subject_id: "alice",
  messages: [
    { role: "user", content: "I live in Cork now." },
    { role: "assistant", content: "Noted." },
  ],
};
const receipt = await call("POST", "/v1/observations", turn);
console.log(receipt.log_offset);
```

```go
// uuid is github.com/google/uuid.

type Message struct {
	GroupOrdinal *int   `json:"group_ordinal,omitempty"`
	Role         string `json:"role"`
	Content      string `json:"content"`
}

type Turn struct {
	IdempotencyKey string    `json:"idempotency_key"`
	DataSubjectID  string    `json:"data_subject_id,omitempty"`
	Messages       []Message `json:"messages"`
}

type Receipt struct {
	ID        string `json:"id"`
	Scope     string `json:"scope"`
	LogOffset int64  `json:"log_offset"`
}

func saveTurn() (Receipt, error) {
	turn := Turn{
		IdempotencyKey: uuid.NewString(), // one per turn; reuse it if you retry
		DataSubjectID:  "alice",
		Messages: []Message{
			{Role: "user", Content: "I live in Cork now."},
			{Role: "assistant", Content: "Noted."},
		},
	}
	var receipt Receipt
	err := call(http.MethodPost, "/v1/observations", turn, &receipt)
	return receipt, err
}
```

**Request**

| Field | Required | What it is |
|---|---|---|
| `messages` | yes | 1 to 64 messages, in the order they were said |
| `messages[].role` | yes | `user`, `assistant`, `system` or `tool` |
| `messages[].content` | yes | The words. Not blank; up to 65,536 bytes per message and 262,144 bytes per turn |
| `messages[].group_ordinal` | with tool messages | Ties a tool call to its result. See [tool calls](#tool-calls-and-groups) |
| `messages[].occurred_at` | no | When this message was said (RFC 3339). Defaults to the turn's time |
| `idempotency_key` | no, but send one | A UUID naming this turn, so a retry can't store it twice. See [retries](#retries-and-idempotency-keys) |
| `data_subject_id` | no | Who the turn is about. Any string you choose, such as your own user id |
| `occurred_at` | no | When the turn happened (RFC 3339). Defaults to when the server stored it |

**Response** (`201`)

| Field | What it is |
|---|---|
| `id` | The stored turn's id |
| `scope` | Your project |
| `log_offset` | The turn's position in the project's log. Compare it with [freshness](#check-freshness) |

**About `data_subject_id`.** This is how "I" in a user's message comes to mean that user. It is a
label, not a login: anyone holding the project token can use any subject. It is compared exactly,
so `Alice` and `alice` are two people. A turn without a subject belongs to the whole project. That
suits documents, but a person's erasure won't reach it, so erase it by its `id` instead. If you'd
rather not use your own user ids, `POST /v1/subjects/register` issues a stable random one.

### Ask a question

`POST /v1/recalls` answers from **entities**: the people, places and things memory knows about. It
finds the names in your question, collects what is known about them, and returns each fact with the
exact words it came from. No model is called, so questions keep working when your model provider is
down.

```bash
curl -sS -X POST "$TAISCE_URL/v1/recalls" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"question": "Where does Alice live?", "data_subject_id": "alice"}'
```

```python
bundle = client.post(
    "/v1/recalls",
    json={"question": "Where does Alice live?", "data_subject_id": "alice"},
).raise_for_status().json()
for fact in bundle["facts"]:
    print(fact["statement"], "--", fact["evidence"]["quote"])
```

```typescript
const bundle = await call("POST", "/v1/recalls", {
  question: "Where does Alice live?",
  data_subject_id: "alice",
});
for (const fact of bundle.facts) {
  console.log(fact.statement, "--", fact.evidence.quote);
}
```

```go
type Evidence struct {
	ObservationID string `json:"observation_id"`
	Quote         string `json:"quote"`
	ByteStart     int    `json:"byte_start"`
	ByteEnd       int    `json:"byte_end"`
	Context       string `json:"context"`
}

type Fact struct {
	FactID     string   `json:"fact_id"`
	Predicate  string   `json:"predicate"`
	Object     string   `json:"object"`
	Statement  string   `json:"statement"`
	SourceRole string   `json:"source_role"`
	Evidence   Evidence `json:"evidence"`
}

type Bundle struct {
	Facts     []Fact `json:"facts"`
	Truncated bool   `json:"truncated"`
	Reach     struct {
		NamedNothingKnown bool `json:"named_nothing_known"`
	} `json:"reach"`
}

func ask(question, subject string) (Bundle, error) {
	var b Bundle
	err := call(http.MethodPost, "/v1/recalls",
		map[string]any{"question": question, "data_subject_id": subject}, &b)
	return b, err
}
```

**Request**

| Field | Required | What it is |
|---|---|---|
| `question` | yes | What you want to know. Name the people, places or things it's about. Up to 8,192 bytes |
| `data_subject_id` | no | Ask about one person. Needed for questions that say "I" or "my" |

There are more options for how far to look, how big the answer may be, whose words count, and
asking about the past. See [recall options](#recall-options).

**Response**

| Field | What it is |
|---|---|
| `facts[]` | What memory knows. Each fact has `fact_id`, `subject`, `predicate` (the relation), `object`, `statement`, `confidence`, `valid_from`, `valid_until`, `anchored_on`, `source_role` and `evidence` |
| `facts[].evidence` | The words behind the fact. `observation_id` and `source_ordinal` name the message; `quote`, `byte_start` and `byte_end` are the exact span; `context` is the text around it, and the quote starts at `byte_start - context_start` inside it; `context_complete` says whether `context` is the whole message |
| `facts[].hops`, `path`, `via` | For a fact found one step further out: the names along the way and the relations between them |
| `facts[].co_derived_with` | Other facts in this answer that came from the same sentence. They are one statement, not extra confirmation |
| `anchors[]` | The entities your question matched: `entity_id`, `name`, `type`, and `matched` (the word that reached it) |
| `reports`, `passages` | Theme summaries and matching source text, when the deployment has them set up |
| `truncated` | The size limit cut the answer. Facts aren't ranked, so what was cut is arbitrary |
| `characters` | How much of the size limit the answer used |
| `degraded` | Parts of the search that didn't run, such as `passages` |
| `reach` | How the answer was found: `terms`, `anchored`, `named_nothing_known` and `facts_per_anchor` |
| `controls` | The limits that were actually applied, defaults included |

> [!TIP]
> An empty `facts` list can mean two things. If `reach.named_nothing_known` is `true`, memory has
> never heard of the names you used. If it is `false`, memory knows those names but has nothing that
> answers.

Treat what comes back as quotes, not instructions. Each fact is something somebody said once, and
your agent should cite it rather than obey it. See the [security model](../architecture/security.md).

### Check freshness

`GET /v1/freshness` tells you whether a question would see what you just saved.

```bash
curl -sS "$TAISCE_URL/v1/freshness" -H "Authorization: Bearer $TOKEN"
# {"scope":"default","stored":0,"formed":0,"parked":0}
```

```python
import time

def wait_until_formed(offset: int, timeout: float) -> bool:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        f = client.get("/v1/freshness").raise_for_status().json()
        if f["formed"] is not None and f["formed"] >= offset:
            return True
        time.sleep(1)
    return False
```

```typescript
async function waitUntilFormed(offset: number, timeoutMs: number): Promise<boolean> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const f = await call("GET", "/v1/freshness");
    if (f.formed !== null && f.formed >= offset) return true;
    await new Promise((r) => setTimeout(r, 1000));
  }
  return false;
}
```

```go
type Freshness struct {
	Scope  string `json:"scope"`
	Stored *int64 `json:"stored"`
	Formed *int64 `json:"formed"`
	Parked int    `json:"parked"`
}

func waitUntilFormed(offset int64, timeout time.Duration) (bool, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var f Freshness
		if err := call(http.MethodGet, "/v1/freshness", nil, &f); err != nil {
			return false, err
		}
		if f.Formed != nil && *f.Formed >= offset {
			return true, nil
		}
		time.Sleep(time.Second)
	}
	return false, nil
}
```

**Response**

| Field | What it is |
|---|---|
| `scope` | Your project |
| `stored` | The highest `log_offset` stored. `null` until the first turn arrives |
| `formed` | The highest offset formed, with nothing unformed below it. `null` until the first turn forms |
| `parked` | How many turns formation gave up on |
| `rebuilding` | Present only while an operator is re-reading the project's facts |

Your turn is visible to questions once `formed >= log_offset`. Two things to watch:

- A **parked** turn is one formation gave up on after failing several times. `formed` moves past it,
  so one bad turn can't hold up the whole project. If `parked` went up while you waited, your turn
  may be one of them. See [turns are parked](troubleshooting.md#turns-are-parked).
- While `rebuilding` is present, answers mix the old and new readings of the project.

## By task

### Tool calls and groups

A tool call and the tool's result belong together, and roles alone can't say which result answers
which call. So a turn with a `tool` message must group its messages with `group_ordinal`:

- Give it on **every** message or on **none**. Without it, a `tool` message is refused.
- The first message is group `0`. Each next message stays in the same group or moves up by one. No
  gaps, and you can't go back to an earlier group.
- A call and its results share one number.

```json
{
  "idempotency_key": "4b7f3c1e-5a0d-4f7e-9a61-0c2d8e3b9f10",
  "data_subject_id": "alice",
  "messages": [
    {"group_ordinal": 0, "role": "user", "content": "What's the weather in Cork?"},
    {"group_ordinal": 1, "role": "assistant", "content": "{\"tool\":\"weather\",\"city\":\"Cork\"}"},
    {"group_ordinal": 1, "role": "tool", "content": "{\"sky\":\"overcast\"}"},
    {"group_ordinal": 2, "role": "assistant", "content": "It's overcast in Cork."}
  ]
}
```

Get it wrong and you'll see `400 invalid_turn` with a message such as
`group_ordinal must be supplied for every message or omitted for all` or
`turns containing tool messages require explicit group ordinals`.

### Retries and idempotency keys

If a response gets lost, you can't tell whether the turn was stored. An **idempotency key** fixes
that: make one random UUID per turn, and send the same key with the same body on every retry.

| What you send | What happens |
|---|---|
| A new key | A new turn, `201` |
| The same key and the same turn: the same person, messages, roles and order | The original receipt again, `201`. Nothing is stored or formed twice. The time a retry carries does not count, and the first stored time stands |
| The same key and a different turn | `409 idempotency_conflict` |
| The same key after that turn was erased | `409 idempotency_conflict`, from then on |
| No key | A new turn every time, even for identical words |
| A key that isn't a UUID | `400 invalid_turn` |

"The same body" means the same subject, the same messages in the same order, and the same
`occurred_at` values. So build the body once and resend exactly that. Don't set `occurred_at` to
"now" inside your retry loop. Timestamps you leave out are filled in after the comparison, so
leaving them out is safe.

Keys belong to the project and stay reserved, even after an erasure. That way a retry queued before
an erasure can't write the erased words back. Never reuse a key for new content.

### Recall options

Add any of these to a `POST /v1/recalls` body:

| Field | Default | What it does |
|---|---|---|
| `hops` | `1` | `1` returns facts about what the question names. `2` also follows one more relation and shows the chain in `path` and `via`. Other values are clamped to 1 or 2 |
| `max_characters` | the server's limit | A smaller size limit, counted in characters of fact text (not JSON, not tokens). Above the server's limit is `400 invalid_recall_controls` |
| `source_roles` | `["user"]` | Whose words may supply facts: any of `user`, `assistant`, `system`, `tool` |
| `surfaces` | all three | Which parts answer: any of `facts`, `reports`, `passages` |
| `themes` | `false` | Also include theme reports when the question matched entities |
| `as_of` | now | RFC 3339. What was true at that time |
| `as_known_at` | now | RFC 3339. What memory knew at that time |

The server's limit is `TAISCE_BUNDLE_CHARACTERS`, 16,000 characters unless the operator changes it.

> [!NOTE]
> `source_roles` defaults to `["user"]`. Facts drawn from assistant, system or tool messages are left
> out unless you ask for them, because a web page your agent fetched is not your user speaking. Each
> fact says where it came from in `source_role`.

Reports and passages need an embedding model set up on the deployment. Without one, only facts are
searched. If you list a surface in `surfaces` that can't run, the answer names it in `degraded`.

### A person's history for a prompt

`POST /v1/contexts` gathers one person's history under a size limit. The newest turns come back word
for word. Older turns come back as summaries that the background process has already written,
oldest first. No model is called.

```bash
curl -sS -X POST "$TAISCE_URL/v1/contexts" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"data_subject_id": "alice", "max_characters": 4000}'
```

```python
ctx = client.post(
    "/v1/contexts", json={"data_subject_id": "alice", "max_characters": 4000}
).raise_for_status().json()
summaries = [s["summary"] for s in ctx["segments"]]
recent = [m for t in ctx["turns"] for m in t["messages"]]
```

```typescript
const ctx = await call("POST", "/v1/contexts", { data_subject_id: "alice", max_characters: 4000 });
const summaries = ctx.segments.map((s: any) => s.summary);
const recent = ctx.turns.flatMap((t: any) => t.messages);
```

```go
type Context struct {
	Segments []struct {
		Summary string `json:"summary"`
	} `json:"segments"`
	Turns []struct {
		LogOffset int64 `json:"log_offset"`
		Messages  []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	} `json:"turns"`
	Truncated bool `json:"truncated"`
}

func history(subject string) (Context, error) {
	var c Context
	err := call(http.MethodPost, "/v1/contexts",
		map[string]any{"data_subject_id": subject, "max_characters": 4000}, &c)
	return c, err
}
```

| Request field | What it is |
|---|---|
| `data_subject_id` | Required. Whose history |
| `max_characters` | Optional. Same limit as recall |

| Response field | What it is |
|---|---|
| `segments[]` | Summaries of older turns: `segment_id`, `level`, `from_offset`, `to_offset`, `covered`, `summary` |
| `turns[]` | Recent turns: `log_offset`, `occurred_at`, and `messages` (each with `role` and `content`) |
| `watermark` | Freshness at the time of the answer, shaped like `GET /v1/freshness` |
| `characters`, `truncated` | How much of the limit was used, and whether it cut anything |

Only turns that have formed appear. Without a subject you get `400 invalid_context`.

### Citations: the words behind a fact

Every fact from a question has a `fact_id`. `POST /v1/citations/resolve` turns it into the full
record: its status, when it was true, and every quote behind it.

```bash
curl -sS -X POST "$TAISCE_URL/v1/citations/resolve" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"id": "<fact_id>"}'
```

```python
citation = client.post("/v1/citations/resolve", json={"id": fact_id}).raise_for_status().json()
for ev in citation["evidence"]:
    print(ev["role"], ev["quote"])
```

```typescript
const citation = await call("POST", "/v1/citations/resolve", { id: factId });
for (const ev of citation.evidence) console.log(ev.role, ev.quote);
```

```go
type Citation struct {
	ID       string `json:"id"`
	Version  string `json:"version"`
	Status   string `json:"status"`
	Evidence []struct {
		Role  string `json:"role"`
		Quote string `json:"quote"`
	} `json:"evidence"`
}

func cite(factID string) (Citation, error) {
	var c Citation
	err := call(http.MethodPost, "/v1/citations/resolve", map[string]string{"id": factID}, &c)
	return c, err
}
```

| Request field | What it is |
|---|---|
| `id` | Required. The `fact_id` |
| `limit` | Quotes per page: default 8, up to 32 |
| `after` | To get the next page, send back the `next` value from the previous answer |

| Response field | What it is |
|---|---|
| `id`, `version`, `status` | The record, its current version (you need this to correct or retract it), and its status |
| `predicate`, `statement`, `confidence`, `source_role`, `recorded_at` | What it claims, and whose words it came from |
| `subject_entity_id`, `object_entity_id` | The entities it links |
| `valid`, `known` | When it was true, and when memory knew it. Each has `from`, `until`, `from_inclusive` and `until_inclusive` |
| `superseded_by`, `retraction` | Present when the record was replaced or withdrawn |
| `evidence[]` | Every quote behind it, each with `source_observation_id`, `source_ordinal`, `log_offset`, `role`, `occurred_at`, `quote`, `byte_start`, `byte_end`, `context`, `context_byte_start`, `context_byte_end`, `context_complete` and `extractor_version` |
| `next` | Present when there are more quotes |

Errors: `400 invalid_citation` for a bad id or page, and `404 not_found` for an id that is unknown,
belongs to another project, or was erased; all three get the same answer on purpose. A source too
large to cite gets `422 citation_limit`; use an export for that one instead.

**Reading a whole message.** A passage points at its message with a `chunk_id` rather than quoting
all of it. `POST /v1/messages/get` returns the message a window at a time:

| Request field | What it is |
|---|---|
| `chunk_id` | Required. From the passage |
| `byte_start` | Where to start. Default 0 |
| `byte_limit` | Window size. Default and maximum 4,096 bytes |
| `expected_digest` | Required when `byte_start` is above 0: the `content_digest` from your first window |

The answer has `text`, `byte_start`, `byte_end`, `complete`, `next_byte_start`, `content_digest`,
`message_bytes`, `role` and `occurred_at`. Keep asking from `next_byte_start` until `complete` is
`true`. If the message changed in between, you get `409 source_changed`; start again from byte 0.

### Inspecting records

A **record** is one stored fact. The `fact_id` you get from a question is a record id.

```bash
curl -sS -X POST "$TAISCE_URL/v1/records/list" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"data_subject_id": "alice", "limit": 50}'
```

```python
page = client.post("/v1/records/list", json={"data_subject_id": "alice", "limit": 50}).raise_for_status().json()
while True:
    for rec in page["records"]:
        print(rec["id"], rec["version"], rec["statement_preview"])
    if "next" not in page:
        break
    page = client.post(
        "/v1/records/list", json={"data_subject_id": "alice", "limit": 50, "after": page["next"]}
    ).raise_for_status().json()
```

```typescript
let page = await call("POST", "/v1/records/list", { data_subject_id: "alice", limit: 50 });
for (;;) {
  for (const rec of page.records) console.log(rec.id, rec.version, rec.statement_preview);
  if (!page.next) break;
  page = await call("POST", "/v1/records/list", { data_subject_id: "alice", limit: 50, after: page.next });
}
```

```go
type RecordPage struct {
	Records []struct {
		ID               string `json:"id"`
		Version          string `json:"version"`
		StatementPreview string `json:"statement_preview"`
	} `json:"records"`
	Next *struct {
		ID string `json:"id"`
	} `json:"next"`
}

func listRecords(subject string) error {
	req := map[string]any{"data_subject_id": subject, "limit": 50}
	for {
		var page RecordPage
		if err := call(http.MethodPost, "/v1/records/list", req, &page); err != nil {
			return err
		}
		for _, r := range page.Records {
			fmt.Println(r.ID, r.Version, r.StatementPreview)
		}
		if page.Next == nil {
			return nil
		}
		req["after"] = page.Next
	}
}
```

| Call | Send | Get back |
|---|---|---|
| `POST /v1/records/list` | `data_subject_id` (optional), `limit` (1–100, default 20), `after` | `records[]`, each with `id`, `version`, `predicate`, `statement_preview`, `statement_bytes`, `preview_truncated`, `source_role`, `recorded_at`, `status`, `subject_entity_id`, `object_entity_id`; plus `next` |
| `POST /v1/records/history` | `id`, `limit`, `before` | `id`, `version`, `current`, `previous[]`, `retraction`, `next` |
| `POST /v1/entities/list` | `limit`, `after` | `entities[]` and `next` |
| `POST /v1/entities/get` | `id` | One entity: `id`, `identity_kind`, `canonical_name`, `aliases`, `first_seen_at` |

To page, send `next` back as `after`, or as `before` for history. A bad page or cursor gets
`400 invalid_record_page` (or `invalid_entity_page`).

### Correcting records

You never edit history. You add a new statement with your name on it, and the old one stays
available to inspect.

| You want to... | Call | Send |
|---|---|---|
| Flag a record as wrong, changing nothing yet | `POST /v1/feedback/record` | `record_id`, `note`, and optionally `proposed_object` (the value it should have) |
| See flagged records | `POST /v1/feedback/list` | Optional `record_id`, `open_only`, `limit`, `after` |
| Act on a flag | `POST /v1/feedback/promote` | `id` (the feedback) and `expected_version` (the record's). This becomes a correction if the feedback proposed a value, or a retraction if not |
| Replace a record's value | `POST /v1/records/correct` | `records`: 1 to 20 items, each with `id`, `expected_version`, `object`, `statement`, and optionally `valid_from` |
| Withdraw a record | `POST /v1/records/retract` | `records`: 1 to 20 items, each with `id` and `expected_version` |
| Add a fact your app knows | `POST /v1/records/assert` | `records`: items with `idempotency_key`, `subject`, `predicate`, `object`, `statement`, and optionally `data_subject_id` and `valid_from` |

```bash
curl -sS -X POST "$TAISCE_URL/v1/records/correct" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"records": [{"id": "<record id>", "expected_version": "<version>",
                    "object": "Galway", "statement": "Alice lives in Galway."}]}'
```

```python
fix = {"id": record_id, "expected_version": version,
       "object": "Galway", "statement": "Alice lives in Galway."}
result = client.post("/v1/records/correct", json={"records": [fix]}).raise_for_status().json()
new_id = result["records"][0]["id"]
```

```typescript
const result = await call("POST", "/v1/records/correct", {
  records: [{ id: recordId, expected_version: version, object: "Galway", statement: "Alice lives in Galway." }],
});
const newId = result.records[0].id;
```

```go
type Correction struct {
	ID              string `json:"id"`
	ExpectedVersion string `json:"expected_version"`
	Object          string `json:"object"`
	Statement       string `json:"statement"`
}

type Corrected struct {
	Records []struct {
		OriginalID string `json:"original_id"`
		ID         string `json:"id"`
		Version    string `json:"version"`
	} `json:"records"`
}

func correct(c Correction) (Corrected, error) {
	var out Corrected
	err := call(http.MethodPost, "/v1/records/correct", map[string]any{"records": []Correction{c}}, &out)
	return out, err
}
```

Good to know:

- `expected_version` is the `version` you read from a list, a history or a citation. If the record
  changed since, you get `409 record_conflict`. Read it again and decide again.
- A batch succeeds as a whole or not at all. A batch that touches too many quotes gets
  `413 record_mutation_limit`; split it.
- A correction returns `original_id`, `id`, `version` and `source_observation_id` for each item. An
  assertion returns `id`, `version`, `source_observation_id` and `replayed`.
- An assertion's `predicate` must be one memory already knows. Each item carries its own key, so a
  retry returns the same record with `replayed: true`.
- `409 feedback_already_promoted` means someone already acted on that flag. Stop; don't retry.

### Forgetting: erasure and export

`POST /v1/erasures` removes everything held about one person, or about a set of turns, and tells you
what is left.

```bash
curl -sS -X POST "$TAISCE_URL/v1/erasures" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"data_subject_id": "alice", "reason": "user request"}'
```

```python
receipt = client.post(
    "/v1/erasures", json={"data_subject_id": "alice", "reason": "user request"}
).raise_for_status().json()
assert receipt["clean"], receipt["residual"]
```

```typescript
const receipt = await call("POST", "/v1/erasures", { data_subject_id: "alice", reason: "user request" });
if (!receipt.clean) console.warn("left behind:", receipt.residual);
```

```go
type Erasure struct {
	RequestID string         `json:"request_id"`
	Deleted   map[string]int `json:"deleted"`
	Residual  map[string]int `json:"residual"`
	Clean     bool           `json:"clean"`
}

func erase(subject string) (Erasure, error) {
	var e Erasure
	err := call(http.MethodPost, "/v1/erasures",
		map[string]string{"data_subject_id": subject, "reason": "user request"}, &e)
	return e, err
}
```

| Request field | What it is |
|---|---|
| `data_subject_id` | The person to erase |
| `source_observation_ids` | Or: up to 10,000 turn ids to erase, for turns saved without a subject. Never both |
| `reason` | Optional. Kept with the receipt |

| Response field | What it is |
|---|---|
| `request_id`, `scope`, `completed_at` | The receipt |
| `data_subject_id`, `source_observation_ids` | What you asked to erase |
| `deleted` | How many rows of each kind were removed |
| `residual` | How many rows still match, counted right after the delete |
| `clean` | `true` when nothing is left |

`POST /v1/exports` returns everything held, as `sections` of raw rows plus `rows`, a count per
section you can compare with an erasure receipt. Name one of two things, as an erasure does:

- `data_subject_id` — everything held about that person.
- `source_observation_ids` — everything held about those turns, for a document observed
  project-wide, which has no person. At most 10,000 ids, and the response echoes them back.

An export is one answer at one instant, not a paged feed, so it is bounded: a person holding more
than 50,000 rows gets `422 export_too_large`, and you take their turns by source instead — the
`id`s are in an ingest manifest, or in a recall's citations.

Good to know:

- Read-only tokens can export but can't erase.
- An erasure or export that names nobody gets `400 no_subject`. Naming both a person and turns, or
  sending an id that isn't a UUID, gets `400 invalid_erasure` for an erasure and `400 invalid_export`
  for an export.
- Running the same erasure twice is harmless.
- A turn saved without a `data_subject_id` isn't reached by a person's erasure. Erase it by its `id`.

### Other calls

| Call | What it's for |
|---|---|
| `POST /v1/passages/search` | Find source text by meaning: `question`, `limit`, `candidates`, `data_subject_id`, `source_role`. Needs an embedding model |
| `POST /v1/entities/candidates`, `POST /v1/reports/candidates` | Find entities or theme reports by meaning. Need an embedding model |
| `POST /v1/subjects/register`, `/get`, `/list`, `/update` | Have Taisce issue a stable random id to use as `data_subject_id` |
| `POST /v1/artifacts/put`, `/get`, `/list`, `/delete` | Store files your agent produces, within a per-project allowance |
| `POST /v1/entities/purge` | Remove an entity. Send `confirm: true` to act; without it you get a preview |
| `POST /v1/notifications/endpoints/register`, `/list`, `/disable`, `POST /v1/notifications/deliveries/list` | Be told when memory forms, instead of polling |

**Notifications.** Register an HTTPS URL, and each time formation moves forward Taisce sends it a
signed message with the new offsets. The message never contains any content. Registering returns a
`secret`, shown once. Check each delivery's `Taisce-Signature` header with it. Deliveries can
repeat, so remember the highest offset you've handled. Notifications stay off until the operator
sets `TAISCE_NOTIFY_DESTINATIONS`; until then every notification call answers
`501 notifications_unavailable`.

## Errors

Every refusal has the same shape:

```json
{"error": {"code": "invalid_turn", "message": "group_ordinal must be supplied for every message or omitted for all"}}
```

Branch on `code`. The `message` is for people reading logs, and its wording may change.

| Code | Status | What it means | What to do |
|---|---|---|---|
| `invalid_body` | 400 | The JSON couldn't be read: unknown or misspelled field, duplicate key, bad timestamp, or over 1 MiB | Fix the request |
| `invalid_turn` | 400 | The turn broke a rule; the message says which | Fix the turn. Don't resend it unchanged |
| `invalid_question` | 400 | Empty question, or too long | Shorten or split it |
| `invalid_recall_controls` | 400 | A recall option is out of range | Check `max_characters`, `source_roles`, `surfaces` |
| `invalid_context` | 400 | No subject, or `max_characters` out of range | Send `data_subject_id`; lower the limit |
| `no_subject` | 400 | An erasure or export names nobody | Name the person, or the turns |
| `invalid_export` | 400 | An export names both a person and turns, or too many turns | Name one, at most 10,000 ids |
| `export_too_large` | 422 | One person holds more rows than an export returns | Export their turns by source |
| `invalid_erasure` | 400 | Both a person and turns, too many turns, or a turn id that isn't a UUID | Send one or the other |
| `invalid_citation`, `invalid_record_page`, `invalid_entity_page`, `invalid_message_window`, `invalid_passage_query`, `invalid_entity_candidate_query`, `invalid_report_candidate_query` | 400 | A bad id, cursor, window or search option | Fix the request |
| `invalid_record_mutation`, `invalid_feedback`, `invalid_subject`, `invalid_artifact` | 400 | A bad change request | Fix the request |
| `invalid_destination` | 400 or 409 | A notification URL the operator doesn't allow, or one already registered | Use an allowed HTTPS host |
| `unauthenticated` | 401 | Missing, wrong or revoked token, an operator token, or a suspended project. One answer for all, on purpose | Check the token. Don't retry |
| `forbidden` | 403 | A read-only token tried to change memory | Use a read-write token |
| `not_found` | 404 | Unknown, another project's, or erased. One answer for all, on purpose | Treat it as gone |
| `idempotency_conflict` | 409 | The key already names a different or erased turn | Fix the client. Never retry |
| `record_conflict`, `subject_conflict`, `artifact_conflict` | 409 | Your `expected_version` is out of date | Read again, decide again |
| `source_changed` | 409 | The message changed between windows | Start again from byte 0 |
| `feedback_already_promoted` | 409 | Someone already acted on this feedback | Stop |
| `record_mutation_limit` | 413 | The batch touches too much evidence | Split the batch |
| `citation_limit` | 422 | The source is too large to cite | Use an export |
| `rate_limited` | 429 | Too busy right now, or too many turns waiting to form. Comes with `Retry-After: 1` | Wait, then resend the same request with the same key |
| `storage_capacity` | 429 | The project's artifact allowance is full. No `Retry-After` | Delete artifacts, or ask the operator. Waiting won't help |
| `internal` | 500 | Something failed on the server | Retry a few times if it's safe, then report it |
| `notifications_unavailable` | 501 | Notifications aren't turned on | Poll freshness instead |
| `no_database_capacity` | 503 | The database had no free connection. Comes with `Retry-After: 1` | Wait, then resend the same request with the same key |
| `passage_unavailable`, `entity_candidates_unavailable`, `report_candidates_unavailable` | 503 | No embedding model is set up or reachable | A deployment setting. Don't retry in a loop |

## Writing a client

### What is safe to retry

- **Reads** change nothing. Retry them freely.
- **Saving a turn with a key.** After a network error, a timeout, `429 rate_limited`,
  `503 no_database_capacity` or `500 internal`, resend the same body with the same key. A turn saved
  without a key can't be retried safely.
- **Changes that carry `expected_version`**, such as corrections, retractions, subject updates and
  artifacts. If you don't know whether it worked, read the record again and decide again. Resending
  with a freshly read version could apply the change twice.
- **Feedback** has no key and no version. If you're unsure it landed, list the record's feedback
  before sending it again.
- **`409 idempotency_conflict` is never retryable.** Minting a new key for the same words after an
  erasure would write back what someone asked to remove.

Use `Retry-After` as the shortest wait, make each wait longer with a little randomness, and stop
after a few tries. How many tries and how long to wait is up to you.

```bash
# Build the body once, then resend exactly those bytes.
BODY='{"idempotency_key":"'"$(uuidgen)"'","data_subject_id":"alice","messages":[{"role":"user","content":"I live in Cork now."}]}'
OUT=$(mktemp)
for attempt in 1 2 3 4 5; do
  status=$(curl -sS -o "$OUT" -w '%{http_code}' -X POST "$TAISCE_URL/v1/observations" \
    -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" -d "$BODY")
  case "$status" in
    201) cat "$OUT"; break ;;
    429|500|503|000) sleep $((attempt * attempt)) ;;  # 000: no response at all
    *) cat "$OUT"; break ;;
  esac
done
```

```python
import random
import time

RETRYABLE = {"rate_limited", "no_database_capacity", "internal"}

def save_with_retries(turn: dict, attempts: int = 5) -> dict:
    """Send the same turn, with the same idempotency_key, until it is stored."""
    delay = 1.0
    for attempt in range(1, attempts + 1):
        try:
            r = client.post("/v1/observations", json=turn)
        except httpx.TransportError:  # network failure or timeout
            if attempt == attempts:
                raise
        else:
            if r.status_code == 201:
                return r.json()
            code = r.json().get("error", {}).get("code")
            if code not in RETRYABLE or attempt == attempts:
                r.raise_for_status()
            delay = max(delay, float(r.headers.get("Retry-After", "1")))
        time.sleep(delay + random.uniform(0, delay))
        delay *= 2
    raise RuntimeError("unreachable")
```

```typescript
const RETRYABLE = new Set(["rate_limited", "no_database_capacity", "internal"]);

// Send the same turn, with the same idempotency_key, until it is stored.
async function saveWithRetries(turn: object, attempts = 5) {
  let delay = 1000;
  for (let attempt = 1; ; attempt++) {
    try {
      return await call("POST", "/v1/observations", turn);
    } catch (err) {
      const network = err instanceof TypeError || (err as Error).name === "TimeoutError";
      const retry = err instanceof Refusal ? RETRYABLE.has(err.code) : network;
      if (!retry || attempt === attempts) throw err;
      if (err instanceof Refusal && err.retryAfter) delay = Math.max(delay, err.retryAfter * 1000);
    }
    await new Promise((r) => setTimeout(r, delay + Math.random() * delay));
    delay *= 2;
  }
}
```

```go
// Also import "errors" and "math/rand/v2".

var retryable = map[string]bool{"rate_limited": true, "no_database_capacity": true, "internal": true}

// saveWithRetries sends the same turn, with the same idempotency key, until it is stored.
func saveWithRetries(turn Turn, attempts int) (Receipt, error) {
	delay := time.Second
	for attempt := 1; ; attempt++ {
		var receipt Receipt
		err := call(http.MethodPost, "/v1/observations", turn, &receipt)
		if err == nil {
			return receipt, nil
		}
		var refusal *Refusal
		if errors.As(err, &refusal) {
			if !retryable[refusal.Code] {
				return Receipt{}, err
			}
			delay = max(delay, refusal.RetryAfter)
		}
		if attempt == attempts {
			return Receipt{}, err
		}
		time.Sleep(delay + rand.N(delay)) // add some randomness so clients don't retry in step
		delay *= 2
	}
}
```

### One request at a time per token

Each API process accepts only a few requests at once, split per project and then per token. Extra
requests are refused straight away with `429 rate_limited`; they don't wait in a queue. With the
compose file's default settings, that means **one request in flight per token** on each API process.
So send calls one at a time per token. If you need more, ask the operator to raise
`TAISCE_MEMORY_POOL`. Adding more tokens for the same project won't help, because the project's
share is the limit.

### Knowing when memory has formed

Formation happens after the save, so an agent that saves and immediately asks can miss its own turn.
Either keep the `log_offset` from the receipt and [poll freshness](#check-freshness) until `formed`
reaches it, or register for [notifications](#other-calls) and be told.

## Going deeper

- [How Taisce works](../start/how-it-works.md): the big picture in plain terms.
- [Troubleshooting](troubleshooting.md): each of these errors as you'll meet it, with the fix.
- [The write path](../architecture/write-path.md) and [the read path](../architecture/read-path.md):
  what happens inside a save and a question.
- [Adapters](adapters.md), and the [Python](python.md), [Java](java.md) and [.NET](dotnet.md)
  guides; [MCP](mcp.md) for coding agents; [the CLI](cli.md) for running a deployment.
- [Examples](../examples/overview.md): complete programs that use these calls.
