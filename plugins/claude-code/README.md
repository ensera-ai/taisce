<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Taisce memory for Claude Code

A session forgets everything when it ends. `CLAUDE.md` holds what somebody remembered to write down.
This plugin gives a session the memory of every session before it, with the words behind every claim.

It is a thin plugin over a deployment's `/mcp` route. There is no second server to run: the route is
served by the same binary, under the same credential, with the same ledger, as the REST contract.

## Install

This repository is a plugin marketplace named `taisce`. From a clone of it, add the marketplace and
install the plugin from it:

```bash
claude plugin marketplace add ./path/to/taisce
claude plugin install taisce-memory@taisce
```

`claude plugin marketplace add` also takes the GitHub `owner/repo` form. To try the plugin for one
session without installing it, load it directly with `claude --plugin-dir ./plugins/claude-code`.

Claude Code asks for these values when the plugin is enabled:

| Setting | What it is |
|---|---|
| `endpoint` | The deployment's base URL, no trailing slash. The plugin appends `/mcp` |
| `token` | A write-enabled credential for one project, from `taisce credential issue`. Stored as sensitive |
| `data_subject_id` | Who these sessions are about, as registered. Empty means project-wide memory |
| `auto_capture` | Record the user's message of every completed turn. **Off by default** |

The credential is the deployment's own rather than an OAuth token: it is already bound to one
project, revocable, and attributed in the ledger on every act. A second identity system would end in
a bearer token that reaches exactly what the credential reaches.

## What you get

**Six tools** on the `taisce` server: `recall`, `observe`, `freshness`, `context`, `resolve_citation` and `report_feedback`. Each
is the v1 operation of the same name reached by another protocol: the same request, the same
response, the same refusal codes. Erasure and export are deliberately not tools.

**Three commands**, named after the plugin as Claude Code names every plugin's commands.
`/taisce-memory:recall <question>` asks memory before deciding something the team may have decided
already; `/taisce-memory:remember <thing>` records a decision in the user's own words;
`/taisce-memory:memory-status`
says how far behind memory is and whether anything is parked.

**A skill** that tells Claude when to reach for memory without being asked, and when not to: the
repository answers what the code is; memory answers what people decided.

**Two hooks.** At session start, one line saying that memory exists and whether it is current. On
every completed turn, when `auto_capture` is on and only then, the user's message is recorded under a
key derived from the session and the message, so a hook that fires twice records once. A turn that
used tools records the message that started it, not the tools' output. When the write fails, the
session shows `Taisce did not record this turn` and why; the session is never held open by it.

## A local deployment with a private certificate

Claude Code runs on Node, which ships its own certificate bundle and ignores the system keychain. A
deployment with a locally issued certificate fails to connect with `UNABLE_TO_VERIFY_LEAF_SIGNATURE`.
Either of these fixes it; a deployment with a public certificate needs neither:

```bash
export NODE_EXTRA_CA_CERTS=/path/to/your-ca.pem
export NODE_OPTIONS=--use-system-ca
```

## How it is proved

`internal/api` runs the tool journey against a live deployment: observe, freshness, recall and
resolve a citation through the MCP client, the same rows a REST caller would make; the tool list is
pinned; a stranger is refused at the door. The hook scripts run against the same deployment from a
test, with capture off and on.
