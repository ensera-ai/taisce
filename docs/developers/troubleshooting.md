<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Troubleshooting

Find what you're seeing, then read why it happens and how to fix it. The messages below are copied
from the code, so you can search for the exact text.

The logs are JSON, one object per line. To read one service's logs without the compose prefix:

```bash
docker compose logs --no-log-prefix worker
```

Most checks use the project token (`TOKEN`), and a few use the operator token (`OPERATOR_TOKEN`).
The [quickstart](quickstart.md) exports both.

## Starting up

### Bootstrap exits: "password authentication failed"

**What you see.** `docker compose up` stops and reports that `bootstrap` didn't complete successfully,
although `postgres` is healthy. The bootstrap log ends like this:

```text
{"level":"ERROR","msg":"taisce failed","error":"database unreachable: failed to connect to `user=postgres database=taisce`: …: failed SASL auth: FATAL: password authentication failed for user \"postgres\" (SQLSTATE 28P01)"}
```

**Why.** PostgreSQL takes the `postgres` password from `POSTGRES_PASSWORD` once, when the volume is
created. After that the variable changes nothing, and the volume keeps asking for the password it
was created with. `postgres` still shows as healthy because its health check does not log in. So
either `POSTGRES_PASSWORD` has changed since the volume was made, or this stack is using a volume
another install made. Compose files from v0.3.0 to v0.4.0 set `name: taisce`, which makes every
directory on the machine that uses one of them share one project and one volume.

**Fix.** Find out whose volume it is before you delete anything:

```bash
docker compose ls -a
docker volume ls --filter label=com.docker.compose.project
```

- **The volume belongs to another install:** give this one its own project. Put
  `COMPOSE_PROJECT_NAME=` and a name nothing else uses in `.env` beside `compose.yaml`, then run
  `docker compose up -d` again.
- **The volume is this install's:** set `POSTGRES_PASSWORD` back to the value it was created with.
- **`docker compose down -v` deletes the volume and every memory in it.** Run it only when that data
  is disposable, and only from the install that owns it.

### Memory is empty after updating `compose.yaml`

**What you see.** You fetched a newer `compose.yaml` and ran `docker compose up -d`. Bootstrap
printed new tokens, and `/v1/freshness` answers `"stored":null`, as if nothing had ever been saved.

**Why.** Compose files from v0.3.0 to v0.4.0 set `name: taisce`. An install they created is the
project `taisce`, with its data in the volume `taisce_postgres-data`, whatever its directory is
called. Newer files take the project name from the directory. In a directory not called `taisce`,
the same command now starts a new, empty project beside the old one. Nothing was deleted.

**Fix.** Stop the new project, point this directory back at the old one, and start again:

```bash
docker compose down
echo 'COMPOSE_PROJECT_NAME=taisce' >> .env
docker compose up -d
```

Run `docker compose down` without `-v`, and before you edit `.env`, so that it stops the new, empty
project. The old tokens work again, and bootstrap mints none, because the project can already be
reached. The empty project's volume is left behind, named after your directory; remove it with
`docker volume rm` once you are sure it is the empty one.

### The api or worker exits right after starting

**What you see.** The container restarts over and over. Its last log line looks like this:

```text
{"level":"ERROR","msg":"taisce failed","error":"memory database unreachable: unsafe runtime database privileges: memory: elevated role attributes or reachable privileged role"}
```

**Why.** A serving process connects to PostgreSQL with two limited logins: one that serves memory,
and one that reads tokens. Each new connection is checked, and the process refuses to run with a
login that has more power than it needs. The usual cause is pointing `TAISCE_MEMORY_DSN` or
`TAISCE_REGISTRY_DSN` at the `postgres` superuser, for example when running `taisce serve` outside
compose.

**Fix.** Use the logins that bootstrap creates: `taisce_data` for `TAISCE_MEMORY_DSN` and
`taisce_control` for `TAISCE_REGISTRY_DSN`, as [compose.yaml](../../compose.yaml) does. Only
`bootstrap`, the `manage` role and operator commands use the admin connection (`TAISCE_ADMIN_DSN`).

The last part of the error tells you exactly what's wrong:

| Reason | Meaning |
|---|---|
| `login does not exist` or `instance namespaces are missing` | Bootstrap hasn't run, or `TAISCE_SCHEMA` differs from the one it used |
| `elevated role attributes or reachable privileged role` | The login is a superuser, can create roles or databases, or can switch to a role that can |
| `runtime owns application database objects` | The migrations ran as this login. Give ownership back to the admin role |
| Any other reason | Someone granted this login extra rights by hand. Revoke them |

[Roles and grants](../postgresql/roles-and-grants.md) explains the full setup.

### A process exits: "connection demand exceeds the server's slots"

**What you see.** After adding workers or raising a pool size, a process exits with:

```text
memory pool: the deployment's connection demand exceeds the server's slots: …
```

**Why.** Each process checks at startup that PostgreSQL has enough free connections for the pool it
asks for. That way it fails now, instead of failing later in the middle of someone's write.

**Fix.** Run fewer processes, lower `TAISCE_MEMORY_POOL` or `TAISCE_WORKER_MEMORY_POOL`, or raise
PostgreSQL's `max_connections`. Each process that starts logs a `"msg":"connection budget"` line
with `wanted`, `available` and `max_connections`, so you can see how much room is left.

## Memory isn't forming

### The worker logs `MEMORY WILL NOT FORM`

**What you see.** Turns are stored (`stored` goes up) but `formed` stays `null`. The worker logged
this once at startup:

```text
{"level":"WARN","msg":"MEMORY WILL NOT FORM: no model endpoint is configured, so turns will be stored and never extracted. …","reason":"…"}
```

**Why.** The headline always says "no model endpoint is configured", whatever went wrong. The real
problem is in `reason`. Most often it's the allowlist, `TAISCE_INFERENCE_ALLOWLIST`. That's the list
of hosts allowed to receive your users' words. It must match the endpoint's host **exactly**: the
hostname, plus `:port` only when the endpoint URL has a port. It's never a URL. The process keeps
running without a model, because saving, answering from what's already formed, and erasing all
still work. It just can't form anything new.

| `reason` starts with | Fix |
|---|---|
| `TAISCE_INFERENCE_ENDPOINT is "api.deepseek.com", which TAISCE_INFERENCE_ALLOWLIST does not list (https://api.deepseek.com)` | You wrote a URL. Write just `api.deepseek.com` |
| `... which TAISCE_INFERENCE_ALLOWLIST does not list` | The port is on one side and not the other. Make them match |
| `TAISCE_INFERENCE_ALLOWLIST is empty, so no endpoint may receive message content` | Set the allowlist |
| `... is not https, and only a loopback address may be plaintext` | Use `https`, or a local address: `localhost`, `127.0.0.1`, `::1` or `host.docker.internal` |
| `TAISCE_INFERENCE_EMBEDDING_ENDPOINT: ...` | The embedding endpoint needs its own entry in the same allowlist |
| `TAISCE_INFERENCE_ENDPOINT is not set` or `TAISCE_INFERENCE_EXTRACTOR_MODEL is not set` | Set it |

**Fix.** List hosts exactly as the endpoint has them, separated by commas:

```bash
sed -i.bak 's/^TAISCE_INFERENCE_ALLOWLIST=.*/TAISCE_INFERENCE_ALLOWLIST=api.deepseek.com/' .env && rm .env.bak
docker compose up -d
docker compose logs --no-log-prefix worker | grep -c 'MEMORY WILL NOT FORM'   # should print 0
```

Compose reads `.env` from the directory you run it in, and recreates the containers whose settings
changed. The [deployment guide](../architecture/deployment.md) has the
full rules.

### `formed` stays `null`

**What you see.** `GET /v1/freshness` keeps answering `"stored":0,"formed":null` long after you
saved a turn.

**Check these in order:**

1. **Is anything forming at all?** Run `docker compose ps` and look for a running `worker`. The
   `api` container never forms anything; it logs `"msg":"formation is not running in this process"`
   on purpose. Also check for `MEMORY WILL NOT FORM` (above).
2. **Is the model failing?** The worker logs `"msg":"formation pass"` lines with `"errored":1`.
   Each failure is retried after a longer wait. After six tries the turn is parked (next section).
   Common causes:
   - A container can't reach the model's host. Run the check in
     [set up your machine](../start/your-machine.md#check-that-a-container-reaches-the-model).
   - The key is wrong. `curl -sS https://api.deepseek.com/models -H "Authorization: Bearer <key>"`
     answers `401` for a key DeepSeek does not accept.
   - The model name is one the endpoint doesn't serve. `TAISCE_INFERENCE_EXTRACTOR_MODEL` must be in
     that same `/models` list.
   - You sourced a file from [deploy/inference/](../../deploy/inference/) before running compose.
     Those are for tools on the host, and the demo ones say `localhost`, which inside a container
     means the container itself. Unset those variables and let `.env` and compose's defaults decide.
3. **Is it just slow?** A large model on a laptop takes a while per message, and nothing is logged
   while it works. A turn that runs past `TAISCE_FORMATION_TURN_BUDGET` (default `5m`) fails that
   attempt. Wait, or raise the budget for document-sized turns.
4. **Are you asking the right project?** Freshness answers for your token's project.

### Turns are parked

**What you see.** Freshness shows `parked` above zero. `formed` has moved on, but facts from those
turns are missing.

**Why.** Formation failed on the turn too many times, so it stopped trying. That keeps one bad turn
from blocking the whole project. The turn itself is kept, untouched. It still counts against the
backlog of turns waiting to form.

**Fix.** Fix the cause first: the model settings, or a model that can't follow the extraction
format (next section). Then list the parked turns and retry them. The easiest way is the CLI inside
the `manage` container:

```bash
docker compose exec manage /taisce formation parked --project default
docker compose exec manage /taisce formation unpark --project default <observation_id>
```

Or call the management API with the operator token. It listens on `127.0.0.1:8081` in compose:

```bash
curl -sS -X POST localhost:8081/manage/v1/formation/parked \
  -H "Authorization: Bearer $OPERATOR_TOKEN" -H "Content-Type: application/json" \
  -d '{"project": "default"}'
# {"items":[{"observation_id":"…","log_offset":0,"attempts":6,"parked_at":"…"}]}

curl -sS -X POST localhost:8081/manage/v1/formation/unpark \
  -H "Authorization: Bearer $OPERATOR_TOKEN" -H "Content-Type: application/json" \
  -d '{"project": "default", "observation_id": "<observation_id>"}'
# {"observation_id":"…","unparked":true}
```

Unparking puts the turn back in line, so `formed` may briefly go **down** until it forms. If the
cause is still there, the turn parks again. `404 not_found` means that id isn't parked in that
project.

### Turns form, but questions find nothing

**What you see.** One of two things:

- **Turns park**, and the error stored on each failed turn reads:

  ```text
  model did not return the requested JSON: no {"claims": [...]} object in the reply (…)
  ```

- **Turns form but nothing comes back.** `formed` moves on, yet questions return no facts about
  what was said.

**Why.** Formation asks the model for strict JSON, using a fixed list of relations. Every quote the
model returns must appear word for word in the message. Some models can't do that reliably. Claims
that fail the check are refused and counted. You can see the counts with the operator token:

```bash
curl -sS -X POST localhost:8081/manage/v1/refusals/summary \
  -H "Authorization: Bearer $OPERATOR_TOKEN" -H "Content-Type: application/json" \
  -d '{"project": "default", "limit": 50}'
```

The reasons are `unmapped_relation`, `unlocatable_quote`, `duplicate_claim`, `not_asserted`,
`unresolvable_subject`, `not_current`, `conflicting_value`, `not_spoken_by_principal` and
`entity_name_limit`. A few
refusals are normal. If nearly everything is `unlocatable_quote` or `unmapped_relation`, the model
is paraphrasing instead of quoting, or making up relations.

**Fix.** Use a model that follows the format. Before relying on a new one, test it with
`make test-inference`. Then unpark any parked turns. Turns that already **formed** won't re-form on
their own; re-reading them is a rebuild (`taisce rebuild`, see [the CLI](cli.md#rebuild)).

## Calling the API

### Every call answers `401 unauthenticated`

```json
{"error":{"code":"unauthenticated","message":"a credential is required"}}
```

**Why.** A missing, malformed, unknown or revoked token all get the same answer, on purpose. So do
two cases that catch people out: an **operator** token on a memory call, and a token whose project
has been suspended. Bootstrap prints the operator token first, so a script that grabs the first
`tsk_` it sees gets the wrong one.

**Fix.** Take the line that starts with `token:`, not `operator token:`:

```bash
export TOKEN=$(docker compose logs --no-log-prefix bootstrap | awk '/^token:/ {print $2}')
```

Send it as `Authorization: Bearer <token>`. If the log no longer has it, make a new one with
`docker compose exec manage /taisce credential issue app --project default`.

### `400 invalid_body`

```json
{"error":{"code":"invalid_body","message":"the request body could not be read"}}
```

**Why.** Taisce reads request bodies strictly. An unknown or misspelled field, a duplicate key, a
timestamp that isn't RFC 3339, broken JSON, or a body over 1 MiB is refused rather than ignored.

**Fix.** Check your field names against [the HTTP API](http-api.md). A typo such as `data_subject`
for `data_subject_id` is the usual cause.

### `403 forbidden`

```json
{"error":{"code":"forbidden","message":"the credential does not permit this operation"}}
```

**Why.** You're using a read-only token for something that changes memory, such as saving a turn,
correcting a record or erasing.

**Fix.** Use a read-write token. Make one with `taisce credential issue <name> --project <project>`
and leave off `--read-only`.

### Questions about "I" return nothing

**What you see.** "Where do I work?" comes back with no facts, and `reach.named_nothing_known` is
`true`.

**Why.** "I" isn't a name. When a turn forms, "I" in its user messages is linked to that turn's
`data_subject_id`. A question can only reach that link if it names the same subject.

**Fix.** Send the subject with the question:

```bash
curl -sS -X POST "$TAISCE_URL/v1/recalls" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"question": "Where do I work?", "data_subject_id": "alice"}'
```

Still empty? Check:

- The turn was saved with **the same** `data_subject_id`. `Alice` and `alice` are different.
- The turn was saved **with** a subject at all. Without one, there's nobody for "I" to mean, and
  those claims are refused.
- The statement came from a **user** message. "I" in assistant or tool messages is refused as
  `not_spoken_by_principal`.
- The fact's role is in `source_roles`, which defaults to `["user"]`.
- `formed` has reached the turn's `log_offset`.

### `503 passage_unavailable`

```json
{"error":{"code":"passage_unavailable","message":"passage search is unavailable for the configured model generation"}}
```

You may also see `entity_candidates_unavailable` or `report_candidates_unavailable`. Or a question
that lists `passages`, `reports` or `semantic_anchors` in `degraded`.

**Why.** Searching by meaning compares embeddings (number lists a model makes from text). The API
serves it only when an operator has built a set of embeddings and told the API which one to use,
with `TAISCE_INFERENCE_EMBEDDING_REVISION`. Without that, only exact name matching runs.

**Fix.** Build and activate a set with `taisce embeddings start`, `build` and `activate` (see
[the CLI](cli.md#all-commands)). Then give the API the revision, the embedding model, and an
allowlisted endpoint. The compose file doesn't pass the revision, so add an override file, which
`docker compose` reads automatically:

```yaml
# compose.override.yaml
services:
  api:
    environment:
      TAISCE_INFERENCE_EMBEDDING_REVISION: weights-v1
      TAISCE_INFERENCE_EMBEDDING_MODEL: qwen/qwen3-embedding-4b
```

The revision must match the one you used with `embeddings start`. Once it's set, a broken embedding
setup stops the API at startup with `configure passage search: …`, so you'll know.

### `429 rate_limited`

```json
{"error":{"code":"rate_limited","message":"request capacity is busy; retry with backoff and the same observation idempotency key"}}
```

or, when saving a turn:

```json
{"error":{"code":"rate_limited","message":"unfinished observation capacity is full; retry after formation or erasure with the same idempotency key"}}
```

Both come with `Retry-After: 1`.

**Why.** There are two different limits:

- **Requests at once.** Each API process takes only a few requests at a time, per project and per
  token, and refuses the rest instead of queueing them. With the compose defaults that's one request
  at a time per token, so parallel calls on one token are refused straight away.
- **The backlog.** Turns waiting to form, parked ones included, each hold a place. When the worker
  falls behind or turns are parked, new turns are refused until places free up.

**Fix.** Wait for `Retry-After`, back off, and resend the same request with the same key. There's a
[retry loop](http-api.md#writing-a-client) you can copy. Send one call at a time per token. For more
throughput, the operator can raise `TAISCE_MEMORY_POOL`; more tokens for the same project won't
help. For the backlog, check `parked` first: parked turns keep their places until you unpark them,
erase them, or they expire.

A `429` with code `storage_capacity` is different. It means the project's artifact allowance is full,
and waiting won't clear it.

### `409 idempotency_conflict`

```json
{"error":{"code":"idempotency_conflict","message":"idempotency key was already used for a different or erased observation"}}
```

**Why.** That key already belongs to a turn in this project, and this request isn't that turn:

- **The turn changed between tries.** A changed subject, message, role or order counts. The time
  doesn't: a retry that carries another `occurred_at` gets the original receipt.
- **The key was reused** for a new turn, for example a key made from a session id.
- **That turn was erased.** Its key stays reserved for good, so an old retry can't bring the erased
  words back.

**Fix.** Make one random UUID per turn, build the body once, and resend exactly that. Don't retry a
`409`. A genuinely new turn needs a new key.

## MCP

### An MCP client can't connect

- **`405` from `/mcp`.** The route only takes `POST`. A client that tries to open a separate event
  stream with `GET` gets `405`. Use the streamable HTTP transport.
- **`400` on a request you wrote by hand.** Send `Accept: application/json, text/event-stream`.
  Both types have to be listed.
- **`UNABLE_TO_VERIFY_LEAF_SIGNATURE` in Claude Code.** Claude Code runs on Node, which ignores your
  system's certificates. Point it at yours:

  ```bash
  export NODE_EXTRA_CA_CERTS=/path/to/your-ca.pem   # or: export NODE_OPTIONS=--use-system-ca
  ```

- **The plugin's `taisce` server doesn't show as connected.** See the note in
  [the plugin setup](mcp.md#the-claude-code-plugin).

## Going deeper

- [Quickstart](quickstart.md): the whole journey, step by step.
- [The HTTP API](http-api.md): every error code and a correct retry loop.
- [Formation](../architecture/formation.md): what happens to a turn between saving and the first
  answer that includes it.
- [Deployment](../architecture/deployment.md): every setting, and what compose sets for you.
- [The CLI](cli.md): the operator commands used above.
