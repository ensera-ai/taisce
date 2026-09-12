<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Welcome to Taisce

Taisce is open-source memory for AI agents. Your agent tells it what people said, and later asks it
questions and gets back facts, each with the exact words it came from.

It is one Go service on PostgreSQL, and you run it yourself: on your laptop with
`docker compose up`, or in your own cluster. Your data stays on your infrastructure.

## Who it is for

Taisce is for developers building agents who want memory that outlasts one conversation, and for
whoever runs it, who is usually the same developer. You reach it the way you already work: through
an adapter for your agent framework in Python, Java or .NET, over plain HTTP, or over MCP from a
coding agent such as Claude Code.

## What you can do with it

- **Remember what people tell your agent.** Send each conversation turn. Taisce stores it at once
  and turns it into facts in the background.
- **Ask about people and things by name.** "Where does Alice work?" starts at Alice and follows her
  facts, instead of searching for similar-looking sentences.
- **Show the words behind every answer.** Each fact carries a quote and its exact position in the
  message it came from.
- **Keep time straight.** A fact records when it was true and when Taisce learned it, so a new job
  replaces the old one without losing the history.
- **Fix what is wrong.** Correct or retract a fact without rewriting what was said.
- **Forget a person, and prove it.** Erase everything someone said and get a receipt that counts
  what is left: zero.
- **Keep long conversations small.** Get one person's history back under a size limit, with the
  older part already summarised.
- **Answer without waiting on a model.** Asking never calls a model, so a slow model delays new
  memories, never answers.

## Where to go

| If you want to | Go to |
|---|---|
| run it and save your first memory | [Quickstart](developers/quickstart.md) |
| understand how memory works, in plain words | [How it works](start/how-it-works.md) |
| copy a recipe for a real task | [Examples](examples/overview.md) |
| connect your agent | Guides: [HTTP API](developers/http-api.md), [adapters](developers/adapters.md) for [Python](developers/python.md), [Java](developers/java.md) and [.NET](developers/dotnet.md), [MCP](developers/mcp.md), [the CLI](developers/cli.md) |
| fix a first run that went wrong | [Troubleshooting](developers/troubleshooting.md) |
| go deeper | [Architecture](architecture/overview.md), [security](architecture/security.md), [deployment](architecture/deployment.md) and [PostgreSQL](postgresql/overview.md) |

Taisce is licensed under Apache-2.0 and built by [Ensera](https://ensera.ai). Contributions are
welcome under the [contributor agreement](cla/individual.md).
