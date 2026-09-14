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

You need Docker with the Compose plugin and a [DeepSeek API key](https://platform.deepseek.com/api_keys):
Taisce reads conversations with DeepSeek's hosted model. [Set up your machine](docs/start/your-machine.md)
covers Linux, macOS with Colima and Windows with WSL2, and the [quickstart](docs/developers/quickstart.md)
walks through a first run. To keep conversation text on your own infrastructure, serve Qwen with vLLM
instead, as [choosing a model](docs/architecture/deployment.md#choosing-a-model) describes.

In an empty directory. You do not need a checkout — the compose file names published images and
pulls them:

```bash
export DEEPSEEK_API_KEY=…   # from platform.deepseek.com
curl -fsSLO https://raw.githubusercontent.com/ensera-ai/taisce/v0.5.1/compose.yaml
cat > .env <<EOF
TAISCE_INFERENCE_API_KEY=$DEEPSEEK_API_KEY
TAISCE_INFERENCE_ENDPOINT=https://api.deepseek.com/v1
TAISCE_INFERENCE_EXTRACTOR_MODEL=deepseek-flash
TAISCE_INFERENCE_ALLOWLIST=api.deepseek.com
EOF
chmod 600 .env
docker compose up -d
export TOKEN=$(docker compose logs --no-log-prefix bootstrap | awk '/^token:/ {print $2}')
```

That starts PostgreSQL, sets up the database, creates a project called `default`, and serves the
API on `localhost:8080`. The last line saves the project token that setup printed once. Compose reads
`.env` on every command, so keep that file out of version control. Each turn you save is sent to
DeepSeek to be read; only the hosts on the allowlist ever receive text.

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

## Operate it

The ops centre is the console for one instance: activity, instance health, projects, API keys,
parked turns and a ledger you can verify. It is off by default; `TAISCE_PORTAL=on docker compose up -d`
switches it on at `http://127.0.0.1:8081/portal/`, and it opens only for an operator token.

![The Taisce ops centre](site/static/img/ops-centre/overview.png)

[Operate an instance](docs/operate/overview.md) covers running Taisce from the ops centre, the CLI and
the management API.

## Documentation

The documents below are published, with search and the generated reference, at
[taisce.dev](https://taisce.dev/). Taisce's page on its maintainer's
site is [ensera.ai/#taisce](https://ensera.ai/#taisce).

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
