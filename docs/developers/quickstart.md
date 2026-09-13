<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Quickstart

In this guide you run Taisce on your machine, save one thing a user said, ask about it, and then
forget it. There are seven steps. Each one shows the command, what you should see, and what just
happened. You don't need an account anywhere, because the model runs on your own machine.

## What you need

- **Docker with the Compose plugin**, so `docker compose version` answers. The older standalone
  `docker-compose` will not do.
- **[Ollama](https://ollama.com), reachable from inside a container**, because that is where Taisce
  calls the model from.
- **`jq`**, to pull fields out of JSON, and **`uuidgen`**.
- **An empty directory** to work in. You do not need a checkout and you do not need Go: the compose
  file names images a release published, for `linux/amd64` and `linux/arm64`, and pulls them.

[Set up your machine](../start/your-machine.md) gets you all of these on Linux, macOS with Colima,
and Windows with WSL2, and ends with a check that a container can reach the model.

## 1. Pull the model

With Ollama running as [Set up your machine](../start/your-machine.md) describes for your system:

```bash
ollama pull qwen3.6:35b-a3b-mxfp8
docker run --rm --add-host host.docker.internal:host-gateway curlimages/curl:8.11.1 \
  -sS --max-time 5 http://host.docker.internal:11434/v1/models | jq -r '.data[].id'
```

You should see the model you pulled, among any others you have:

```text
qwen3.6:35b-a3b-mxfp8
```

Taisce uses this model to read each conversation turn and propose facts. The second command asks for
the list from inside a throwaway container, which is the way Taisce's worker reaches the model, so a
model listed here is one Taisce can use.

### Which model?

Taisce asks the model for strict JSON built from a fixed list of relations, and not every model can
keep to that. These were measured against the same 19-message test corpus
([extraction models, 2026-09-11](../36-extraction-models.md)):

| Model | Where it runs | Keeps to the format? | Set it up with |
|---|---|---|---|
| `qwen3.6:35b-a3b-mxfp8` | Ollama, on your machine | Yes, all 19 cases | the steps above (the default) |
| `Qwen/Qwen3.8-27B`, thinking off | vLLM, on a rented GPU | Yes, all 19 cases | [`demo-qwen3.8`](../../deploy/inference/demo-qwen3.8.env) |
| `deepseek-flash` | the provider's hosted API | Yes, all 19 cases | [`deepseek`](../../deploy/inference/deepseek.env) |
| `qwen3.8:27b-mxfp8`, thinking on | Ollama, on your machine | No answer: every call ran past the two-minute limit | not recommended |

With a hosted model, what people tell your agent leaves your machine and goes to that provider.
Taisce only sends text to hosts you have listed in the allowlist, so that is always your decision.

A model that is not in this table may still work. Check it with `make test-inference` before you
rely on it.

**Didn't work?** If `curl` prints `Failed to connect`, the container cannot reach Ollama: either it
is not running, or it listens where a container cannot reach it.
[When the check fails](../start/your-machine.md#when-the-check-fails) says which, per system. If the
model answers but nothing ever forms, see
[memory isn't forming](troubleshooting.md#memory-isnt-forming).
To use a hosted model instead, see [reaching a model](../architecture/deployment.md#reaching-a-model).

## 2. Start Taisce

Fetch the compose file for the release you want, then bring it up:

```bash
curl -fsSLO https://raw.githubusercontent.com/ensera-ai/taisce/v0.3.2/compose.yaml
docker compose up -d
docker compose ps -a --format '{{.Service}}\t{{.State}}\t{{.Health}}'
curl -s localhost:8080/ready
```

The first run pulls two images — the service and its PostgreSQL substrate — so give it a minute on a
cold cache. Once the health checks pass, you should see:

```text
api        running  healthy
bootstrap  exited
manage     running  healthy
postgres   running  healthy
worker     running  healthy
{"status":"ready"}
```

Then check that the worker found a model it is allowed to use:

```bash
docker compose logs --no-log-prefix worker | grep -c 'MEMORY WILL NOT FORM'
```

```text
0
```

A one-off `bootstrap` set up PostgreSQL, created a project called `default` and printed its tokens.
Now the `api` answers on port 8080, and the `worker` turns stored turns into facts in the
background.

**Didn't work?** If port 8080 is taken, run `TAISCE_PORT=18080 docker compose up -d` and use that
port below. If the count is `1` or more, see
[memory never forms](troubleshooting.md#the-worker-logs-memory-will-not-form).
If `api` or `worker` keeps restarting, see
[the serving process exits at start](troubleshooting.md#the-api-or-worker-exits-right-after-starting).

## 3. Get your token

```bash
docker compose logs --no-log-prefix bootstrap | grep 'token:'
```

You should see two tokens:

```text
operator token: tsk_…
token: tsk_…
```

Save the second one, the **project token**, and try it:

```bash
export TOKEN=$(docker compose logs --no-log-prefix bootstrap | awk '/^token:/ {print $2}')
curl -sS localhost:8080/v1/freshness -H "Authorization: Bearer $TOKEN"
```

```json
{"scope":"default","stored":null,"formed":null,"parked":0}
```

The project token opens memory for the `default` project, and `null` means nothing has been written
yet. The operator token is for running the instance and is refused on memory calls.

**Didn't work?** A `401` means the token is wrong, and the usual cause is picking up the operator
token by mistake ([every call answers 401](troubleshooting.md#every-call-answers-401-unauthenticated)).
Tokens are printed once, on the first start. If the line is gone, issue a new one with
`docker compose exec manage /taisce credential issue app --project default`.

## 4. Save a memory

```bash
export KEY=$(uuidgen)
curl -sS -X POST localhost:8080/v1/observations \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d @- <<EOF
{"idempotency_key": "$KEY",
 "data_subject_id": "alice",
 "messages": [{"role": "user", "content": "I work at Ensera and I live in Dublin."}]}
EOF
```

You should see a receipt, with HTTP status `201`:

```json
{"id":"…","scope":"default","log_offset":0}
```

Taisce stored Alice's turn and answered straight away, before any model looked at it. `log_offset`
is the turn's place in the project's log, and `data_subject_id` says the words are Alice's. Run the
same command again and you get the same receipt: the `idempotency_key` makes it a retry, not a
second turn.

**Didn't work?** A `400 invalid_turn` says what is wrong in its `message`, such as a key that is not
a UUID. A `409 idempotency_conflict` means the key was already used for something else, so make a
new one with `uuidgen` ([`409 idempotency_conflict`](troubleshooting.md#409-idempotency_conflict)).

## 5. Wait for it to form

```bash
while :; do
  F=$(curl -sS localhost:8080/v1/freshness -H "Authorization: Bearer $TOKEN"); echo "$F"
  case "$F" in *'"formed":null'*) sleep 2 ;; *) break ;; esac
done
```

You should see a few lines while the model works, then `formed` catches up:

```text
{"scope":"default","stored":0,"formed":null,"parked":0}
{"scope":"default","stored":0,"formed":null,"parked":0}
{"scope":"default","stored":0,"formed":0,"parked":0}
```

The worker had the model read Alice's message and kept only the facts whose words it could find in
it. `stored` is the last turn Taisce holds, and `formed` is the last turn it has turned into facts,
so your turn is in memory once `formed` reaches its `log_offset`
([stored and formed](../start/how-it-works.md#stored-and-formed)).

**Didn't work?** If `formed` stays `null`, see
[`formed` stays `null`](troubleshooting.md#formed-stays-null). If the loop ends with `"parked":1`,
the worker gave up on the turn after several tries; see
[turns are parked](troubleshooting.md#turns-are-parked).

## 6. Ask a question

```bash
curl -sS -X POST localhost:8080/v1/recalls \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"question": "Where do I work?", "data_subject_id": "alice"}' \
  | jq '{anchors, facts: [.facts[] | {predicate, object, quote: .evidence.quote}]}'
```

You should see Alice's facts, each with the words it came from (trimmed; the statements come from
the model, so wording can vary):

```json
{
  "anchors": [{"entity_id": "…", "name": "…", "type": "…", "matched": "speaker"}],
  "facts": [
    {"predicate": "works_at", "object": "Ensera", "quote": "I work at Ensera"},
    {"predicate": "lives_in", "object": "Dublin", "quote": "…"}
  ]
}
```

Because you named Alice as the `data_subject_id`, "I" meant Alice, so recall started from her and
returned her facts, without calling a model. Without a subject, ask by name instead, as in "What do
we know about Ensera?".

**Didn't work?** An empty `facts` usually means the subject is spelled differently from the one you
saved (`Alice` is not `alice`) or `formed` has not reached your turn yet
([recall returns nothing for "I"](troubleshooting.md#questions-about-i-return-nothing)).

## 7. Forget Alice

```bash
curl -sS -X POST localhost:8080/v1/erasures \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"data_subject_id": "alice", "reason": "right to erasure"}' | jq
```

You should see a receipt (trimmed; which kinds appear depends on what formed):

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

Ask again:

```bash
curl -sS -X POST localhost:8080/v1/recalls \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"question": "Where do I work?", "data_subject_id": "alice"}' | jq '.facts'
```

```text
[]
```

Taisce deleted Alice's turn and everything built only from it. Then, in the same transaction, it
counted what still matched her: that count is `residual`, and `clean` is `true` because every count
is zero.

**Didn't work?** A `400 no_subject` means the body named nobody, and an erasure never defaults to the
whole project. A receipt with `clean: false` names the kind of thing that survived; the
[forget a person](../examples/forget-a-person.md) recipe explains how to read one, and
[troubleshooting](troubleshooting.md) covers the rest of a first run.

## When you are done

`docker compose down` stops everything. Your data stays in a Docker volume, so
`docker compose up -d` brings it back, and your token still works.

## Next steps

- [How it works](../start/how-it-works.md): what happened in each step, in plain words.
- [Examples](../examples/overview.md): recipes for preferences, citations, forgetting, long
  conversations and coding agents.
- [The HTTP API, by task](http-api.md): every call, option and error.
- [Adapters](adapters.md) for [Python](python.md), [Java](java.md) and [.NET](dotnet.md), or
  [MCP](mcp.md) for coding agents: memory from the framework you already use.
- [Troubleshooting](troubleshooting.md): the problems a first run actually hits.
