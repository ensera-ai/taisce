<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Taisce

Taisce is open-source memory for AI agents. Your agent sends it each conversation turn. Taisce
stores the turn, pulls facts out of it in the background, and answers later questions with those
facts and the exact words each one came from. Every fact knows when it was true and when it was
learned, and when someone asks to be forgotten, Taisce erases them and hands you a receipt counting
what is left. It is one Go service on PostgreSQL that you run on your own machine or cluster, and
all of it is Apache-2.0.

## Run it

You need Docker Compose v2 and [Ollama](https://ollama.com), which runs the model that reads
conversations on your own machine. Start Ollama so the containers can reach it:

```bash
OLLAMA_HOST=0.0.0.0 ollama serve
```

Then, in a second terminal, from a checkout of this repository:

```bash
ollama pull qwen3.6:35b-a3b-mxfp8
docker compose up -d
export TOKEN=$(docker compose logs --no-log-prefix bootstrap | awk '/^token:/ {print $2}')
```

That starts PostgreSQL, sets up the database, creates a project called `default`, and serves the
API on `localhost:8080`. The last line saves the project token that setup printed once.

## Try it

Save something a user said. `data_subject_id` says whose words these are:

```bash
curl -sS -X POST localhost:8080/v1/observations \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"data_subject_id": "alice",
       "messages": [{"role": "user", "content": "I work at Ensera and I live in Dublin."}]}'
```

Check `formed` until it is no longer `null`. That means the turn has become facts:

```bash
curl -sS localhost:8080/v1/freshness -H "Authorization: Bearer $TOKEN"
```

Then ask:

```bash
curl -sS -X POST localhost:8080/v1/recalls \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"question": "Where do I work?", "data_subject_id": "alice"}'
```

The answer comes with the words behind it (trimmed):

```json
{"facts": [{"predicate": "works_at", "object": "Ensera",
            "evidence": {"quote": "I work at Ensera", "byte_start": 0, "byte_end": 16, …}, …}, …]}
```

The [quickstart](docs/developers/quickstart.md) walks through the same steps with what to expect at
each one, and ends by erasing Alice.

## Documentation

- [Introduction](docs/introduction.md): what Taisce is and where to start.
- [How it works](docs/start/how-it-works.md): memory in plain words, from a turn to an answer.
- [Quickstart](docs/developers/quickstart.md): running it and saving your first memory.
- [Examples](docs/examples/overview.md): short recipes for remembering, citing, forgetting, long
  conversations and coding agents.
- Guides: [the HTTP API](docs/developers/http-api.md), [adapters](docs/developers/adapters.md) for
  [Python](docs/developers/python.md), [Java](docs/developers/java.md) and
  [.NET](docs/developers/dotnet.md), [MCP](docs/developers/mcp.md) and the
  [Claude Code plugin](plugins/claude-code/README.md), [the CLI](docs/developers/cli.md), and
  [troubleshooting](docs/developers/troubleshooting.md).
- Going deeper: [architecture](docs/architecture/overview.md),
  [deployment](docs/architecture/deployment.md) including the [Helm chart](deploy/helm/README.md),
  [security](docs/architecture/security.md) and [PostgreSQL](docs/postgresql/overview.md).

## License

Apache-2.0; see [LICENSE](LICENSE). To report a vulnerability, see [SECURITY.md](SECURITY.md). To
contribute, see [CONTRIBUTING.md](CONTRIBUTING.md).
