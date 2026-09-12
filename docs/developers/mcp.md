<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Give a coding agent memory with MCP

Coding agents such as Claude Code reach tools through the Model Context Protocol (MCP). Taisce
serves MCP at `/mcp` on the same API you already run, with the same project token. There's nothing
extra to install on the server.

## Connect

Every MCP client needs two things: the URL `https://<your deployment>/mcp`, and the header
`Authorization: Bearer <project token>`.

Make a token for the agent first. Use a read-write token if the agent should save things, or a
read-only token if it only needs to ask:

```bash
taisce credential issue claude-code --project default
```

**Claude Code**, from the command line:

```bash
claude mcp add --transport http taisce https://memory.example/mcp \
  --header "Authorization: Bearer $TAISCE_TOKEN"
```

Or in a project's `.mcp.json`:

```json
{
  "mcpServers": {
    "taisce": {
      "type": "http",
      "url": "https://memory.example/mcp",
      "headers": { "Authorization": "Bearer ${TAISCE_TOKEN}" }
    }
  }
}
```

Keep the token in an environment variable, not in a file you commit. Run `/mcp` in a session and
you should see `taisce` with six tools.

**Other clients** name their settings differently, but all need the same three things: the
streamable HTTP transport, the `/mcp` URL, and the `Authorization` header.

If your deployment uses a certificate you issued yourself, Claude Code fails with
`UNABLE_TO_VERIFY_LEAF_SIGNATURE`. It runs on Node, which ignores your system's certificates.
Either of these fixes it:

```bash
export NODE_EXTRA_CA_CERTS=/path/to/your-ca.pem
export NODE_OPTIONS=--use-system-ca
```

## The tools

Each tool is one [HTTP API](http-api.md) call under another name. It takes the same fields and
returns the same JSON, and your token's permissions apply as usual.

| Tool | What it does | Inputs (required in bold) | Same as |
|---|---|---|---|
| `recall` | Ask memory a question. Facts come back with the words behind them | **`question`**, `data_subject_id`, `hops`, `max_characters`, `source_roles`, `surfaces`, `themes`, `as_of`, `as_known_at` | `POST /v1/recalls` |
| `observe` | Save what was said | **`idempotency_key`**, **`messages`** (each with **`role`**, **`content`**, and `group_ordinal`), `data_subject_id`, `occurred_at` | `POST /v1/observations` |
| `freshness` | How far behind memory is | none | `GET /v1/freshness` |
| `context` | One person's history under a size limit: recent turns word for word, older ones as summaries | **`data_subject_id`**, `max_characters` | `POST /v1/contexts` |
| `resolve_citation` | Turn a `fact_id` into the full record and every quote behind it | **`id`**, `limit`, `after` | `POST /v1/citations/resolve` |
| `report_feedback` | Flag a record as wrong without changing it | **`record_id`**, **`note`**, `proposed_object` | `POST /v1/feedback/record` |

See [the HTTP API](http-api.md) for what each field means and what comes back.

**What a tool returns.** A result is the call's JSON response, in two forms: as structured content
for hosts that read JSON, and as text for hosts that only show text to the model.

**When a tool is refused**, you get a tool error the model can read, not a protocol error. The text
reads like `400 invalid_question: a question is required`. It's `<status> <code>: <message>`, using
the same codes as [the HTTP API](http-api.md#errors). On `429 rate_limited`, wait and retry. Retry
`observe` with the same `idempotency_key`.

**Read-only tokens** can use `recall`, `freshness`, `context` and `resolve_citation`. `observe` and
`report_feedback` are refused with `403 forbidden`.

## What the agent can't do

Erasing and exporting a person's data aren't tools, because an agent shouldn't be able to delete or
copy out someone's data just because some text in its context told it to. A person does that with
their own token over [the HTTP API](http-api.md#forgetting-erasure-and-export).

Correcting a record isn't a tool either. An agent can flag one with `report_feedback`, and a person
decides what to do about it.

> [!WARNING]
> The tool list limits what the agent is offered. It doesn't limit the token. If a read-write token
> leaks, it can still erase over the HTTP API. Give an agent that only needs to ask a read-only
> token. Also treat what an agent saves with `observe` as input to review, since it can record
> anything under any role.

## The Claude Code plugin

The plugin in [plugins/claude-code](../../plugins/claude-code/) gives each Claude Code session the
memory of the sessions before it. It adds three slash commands, a skill that tells Claude when to
check memory, and two small hooks. It's configuration on top of the `/mcp` route, with no server of
its own.

### Set it up

1. **Check what you need.** You need a running Taisce deployment, `python3` on your machine (the
   hooks use only its standard library), and a clone of this repository.

2. **Make a read-write token.** The plugin saves with `observe`, so a read-only token won't do.

   ```bash
   taisce credential issue claude-code --project default
   ```

3. **Start Claude Code with the plugin**, from the repository root:

   ```bash
   claude --plugin-dir ./plugins/claude-code
   ```

   That loads it for one session. To keep it installed, add it to a plugin marketplace of your own,
   then use `/plugin marketplace add` and `/plugin install`. This repository doesn't ship a
   marketplace.

4. **Fill in the settings** when Claude Code asks:

   | Setting | What to enter |
   |---|---|
   | `endpoint` (required) | Your deployment's base URL, with no trailing slash. The plugin adds `/mcp` |
   | `token` (required) | The read-write token from step 2. Stored as sensitive |
   | `data_subject_id` | Who these sessions are about. Leave it empty for memory shared across the project |
   | `auto_capture` | Save your message from every completed turn. Off unless you turn it on |

5. **Check the connection.** Run `/mcp` and look for `taisce`.

   > [!NOTE]
   > The plugin's `.mcp.json` doesn't include `"type": "http"`, which Claude Code expects for an
   > HTTP server. If `taisce` doesn't show as connected, add it yourself with `claude mcp add`, as in
   > [Connect](#connect), using the same endpoint and token.

6. **Try it:**

   ```text
   /taisce-memory:remember We keep PostgreSQL as the only store, because every extra datastore is another place an erasure has to reach.
   /taisce-memory:memory-status
   /taisce-memory:recall What did we decide about PostgreSQL?
   ```

   `remember` saves your words. `memory-status` tells you when formation has caught up. Then
   `recall` answers with a citation like `[fact:<fact_id>]` and quotes the words it came from.

### What's inside

| Part | What it does |
|---|---|
| `/taisce-memory:recall <question>` | Calls `recall`, answers with a citation for every claim, and says so when memory has nothing or part of it didn't answer |
| `/taisce-memory:remember <text>` | Calls `observe` with your own words as a `user` message and a fresh key. It refuses to save secrets such as keys, tokens and passwords |
| `/taisce-memory:memory-status` | Calls `freshness` and tells you how many turns are held, whether formation has caught up, and whether any are parked |
| The `taisce-memory` skill | Tells Claude to check memory before deciding something the team may already have decided, and to save decisions when they're made. It also tells Claude not to use memory for what the code and `git log` already answer |
| Session start hook | Adds a short note that memory exists and whether it's up to date, for example: `Taisce memory for the whole project holds nothing yet.` |
| Stop hook | Saves your message from each completed turn, only when `auto_capture` is on |

About the hooks:

- They call the HTTP API directly and never interrupt your session. If the deployment is
  unreachable, they do nothing.
- With `auto_capture` on, only your message is saved, never Claude's reply. Messages that start
  with `/` are skipped. The key is made from the session and the text, so a hook that fires twice
  saves once.
- A failed automatic save isn't reported. For anything that must be kept, use
  `/taisce-memory:remember`, where you see the result.
- `TAISCE_PLUGIN_TIMEOUT` sets the hooks' request timeout in seconds. The default is 8.

## Calling it by hand

`/mcp` keeps no sessions, so every request stands alone. Send `POST` with an `Accept` header that
lists both JSON and event streams:

```bash
curl -sS -X POST "$TAISCE_URL/mcp" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -d '{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}'
```

Then call a tool:

```bash
curl -sS -X POST "$TAISCE_URL/mcp" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -d '{"jsonrpc": "2.0", "id": 2, "method": "tools/call",
       "params": {"name": "recall", "arguments": {"question": "Where does Marta work?"}}}'
```

Answers are always plain JSON, never an event stream. A request without a valid token is refused
with `401 unauthenticated` before any MCP happens. A `GET` gets `405`, because there's no stream to
open.

## Going deeper

- [Adapters](adapters.md): the same memory inside an agent framework.
- [The HTTP API](http-api.md): the calls behind each tool.
- [The CLI](cli.md): making and revoking tokens.
- [The security model](../architecture/security.md): why what comes back is treated as data, never
  as instructions.
- [Troubleshooting](troubleshooting.md#an-mcp-client-cant-connect): when a client won't connect.
- The code: [internal/api/mcp.go](../../internal/api/mcp.go).
