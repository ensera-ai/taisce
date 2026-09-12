<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# The taisce command

`taisce` is one program. It runs the server, and it carries every admin command: projects, keys,
health, stuck turns, the audit log and rebuilds. This page gives you a quick tour by task, then the
complete list of commands.

In the container image the program is `/taisce`. With compose, the easiest place to run a command
is the `manage` container, which already has the database connection it needs:

```bash
docker compose exec manage /taisce project list
```

## How commands connect

Commands reach Taisce in one of three ways. Which one depends on the command and the variables you
set.

| Way | Set | Commands |
|---|---|---|
| The management API, using an operator token | `TAISCE_MANAGE_API` (such as `http://127.0.0.1:8081`) and `TAISCE_OPERATOR_TOKEN` | `project`, `credential`, `audit` |
| Straight to PostgreSQL, as the admin login | `TAISCE_ADMIN_DSN` (falls back to `TAISCE_MEMORY_DSN`) | Everything else, and `project`, `credential` and `audit` when `TAISCE_MANAGE_API` isn't set |
| The memory API, using a project token | `TAISCE_API` and `TAISCE_TOKEN` | `ingest`, `conformance` |

There are two kinds of token. An **operator token** can manage projects and keys but can't read any
memory. A **project token** can use one project's memory and nothing else. Both start with `tsk_`.

Tokens are read from environment variables only, never from flags, because flags end up in your
shell history. If you set `TAISCE_MANAGE_API` but forget the token, the command stops rather than
falling back to the database:

```text
TAISCE_MANAGE_API is set and TAISCE_OPERATOR_TOKEN is not; the management surface needs an operator credential
```

Compose publishes the management API on `127.0.0.1:8081` but doesn't publish PostgreSQL. So from
your own machine, use the management API, or run the command inside the `manage` container.

## Quick tour

The examples call a project `research`. A `…` marks output that changes from run to run.

### Start

With no arguments, `taisce` runs the server. `taisce serve` does the same. `TAISCE_ROLE` decides
what the process does:

| `TAISCE_ROLE` | What runs | Listens on |
|---|---|---|
| `all` (default) | The memory API and formation (the background step that reads facts out of turns) | `TAISCE_ADDR`, default `:8080` |
| `api` | The memory API only | `TAISCE_ADDR`, default `:8080` |
| `worker` | Formation only, plus health checks | `TAISCE_HEALTH_ADDR`, default `127.0.0.1:8082`; loopback only |
| `manage` | The management API, plus the portal when `TAISCE_PORTAL=on` | `TAISCE_MANAGE_ADDR`, default `127.0.0.1:8081` |

Compose starts all of this for you with `docker compose up -d`. It also runs `bootstrap` first,
which turns an empty database into one that can serve. It creates the database logins, the
first project, an operator token and a project token:

```bash
export TAISCE_ADMIN_DSN='postgres://…'     # a login that may change the schema
export TAISCE_CONTROL_PASSWORD='…'         # password for the login that reads tokens
export TAISCE_DATA_PASSWORD='…'            # password for the login that serves memory
taisce bootstrap -project default -credential bootstrap
```

You'll see something like this:

```text
operator credential operator (…) reaches the management surface and no project
operator token: tsk_…

credential bootstrap (…) reaches project default
token: tsk_…

This token is shown once. Only its digest is stored, so it cannot be recovered — mint another if it is lost.
```

Only a hash of each token is stored, so save them now. Running bootstrap again is safe: it creates
only what's missing. When the project already has a key, it logs
`this project can already be reached; no credential minted`.

### Create a project

A project is a separate memory with its own keys.

```bash
taisce project create research
taisce project list
```

You'll see something like this:

```text
project "research" created
default              surfaces=none                     retention=indefinite
research             surfaces=none                     retention=indefinite
```

Names start with a lowercase letter and use lowercase letters, digits and `_`, up to 63 characters.
Creating a project twice is safe. To take a project offline without losing anything, use
`taisce project suspend research`, and bring it back with `taisce project resume research`. While a
project is suspended, its keys are refused. There's no `project delete`. Removing memory is an
erasure, with a receipt.

### Make a key

```bash
taisce credential issue research-app --project research
taisce credential issue research-reader --project research --read-only
```

You'll see something like this:

```text
credential research-app (…) reaches project research with read_write access
token: tsk_…

This token is shown once and cannot be recovered.
```

Put flags **after** the name. `--project` defaults to `default`. A `--read-only` key can ask,
inspect and export, but can't change memory. Each run makes a new key.

To see and revoke keys (listing works only over the management API):

```bash
taisce credential list --project research   # without --project: the operator tokens
taisce credential revoke <credential id>
```

You'll see something like this:

```text
…  research-reader          tsk_…… project research read_only
…  research-app             tsk_…… project research read_write
credential … revoked
```

The list shows each key's id, name, the start of its token and what it reaches. Revoked keys stay in
the list, marked `[revoked <time>]`. To make another operator token, run `taisce operator issue <name>`.
It always goes straight to the database, because you need an operator token before you can use the
management API.

### Check health

```bash
TAISCE_ADMIN_DSN='postgres://…' taisce health
```

In a terminal, you get a panel showing whether the worker is responsive, how full the backlog is,
how many turns are parked, and how busy the database connections are. Otherwise you get one JSON
object. It never includes project names, people or message content. `health` needs
`TAISCE_ADMIN_DSN` set explicitly.

To check a single process, use `probe`. It prints nothing; the exit status is the answer:

```bash
taisce probe            # is the API ready? (GET /ready on TAISCE_ADDR)
taisce probe --live     # is it alive? (GET /health)
taisce probe --worker   # the worker's health listener
taisce probe --manage   # the management listener
```

`probe` only talks to the local machine. It's what the container image uses for its own health
checks.

### Look at stuck turns

A turn that failed to form too many times is **parked**. List the parked turns, fix the cause (usually
the model settings), then retry them one at a time:

```bash
taisce formation parked --project research
taisce formation unpark --project research <observation id>
```

You'll see something like this:

```text
{"items":[{"observation_id":"…","log_offset":…,"attempts":6,"parked_at":"…"}],"next_after":…}
{"changed":true,"audit_principal":"…"}
```

The list shows metadata only, never content. When there's another page, pass `next_after` back with
`--after`. Unparking the same turn again returns `"changed":false`. If the model is still failing,
the turn parks again. See [troubleshooting](troubleshooting.md#turns-are-parked).

### Verify the audit log

Every operation is written to an audit log. Sealing chains everything new together and gives you a
**head**, a fingerprint of the whole log so far. Verifying recomputes the chain.

```bash
taisce audit seal
taisce audit verify
```

You'll see something like this:

```text
sealed … entries (…-…)
head: …

Record that head somewhere this database cannot reach. …
verified: … seals covering … entries
head: …

… entries written since the last seal are covered by nothing.
```

Keep the head somewhere outside the database. A rewritten log can't produce a head that matches one
you wrote down earlier. If verification fails, the command prints `FAILED: …` and exits 1.

> [!WARNING]
> Over the management API (`TAISCE_MANAGE_API` set), `audit seal` and `audit verify` print the raw
> JSON answer, such as `{"Valid":…,"Seals":…,"Entries":…,"Unsealed":…,"Head":"…","Failure":…}`, and
> exit 0 **even when verification fails**. In a script, read `Valid` yourself.

### Load documents

`ingest` sends files through the memory API as ordinary turns, using a project token:

```bash
export TAISCE_API=http://127.0.0.1:8080
export TAISCE_TOKEN='tsk_…'             # a read-write project token
taisce ingest --wait 5m notes.md report.json
```

You'll see something like this:

```text
{"document":"notes.md","segments":…}
{"document":"report.json","segments":…}
{"documents":2,"refused":0,"segments":…,"max_log_offset":…,"parked":…,"formed_up_to":…}
```

It accepts `.txt`, `.md` and `.json` files. A `.json` file has exactly `title`, `text` and
`occurred_at`. Each file is cut into pieces of up to `--segment-bytes` (default 8 KiB), and each
piece becomes one turn. A manifest per file, with offsets and receipts but no text, goes into
`--manifest-dir`. Running the same command again is safe and picks up where it stopped. `--wait`
waits for formation to catch up.

> [!WARNING]
> Put every flag before the first file. In `taisce ingest notes.md --wait 5m`, the words `--wait`
> and `5m` are read as file names.

### Rebuild

A **rebuild** re-reads turns you already have with the model you have configured now. Use it after
changing to a better model. The commands that call a model need `TAISCE_INFERENCE_ENDPOINT`,
`TAISCE_INFERENCE_EXTRACTOR_MODEL` and `TAISCE_INFERENCE_ALLOWLIST`. Keys are UUIDs you choose, and
reusing a key is how you resume.

```bash
KEY=$(uuidgen)
taisce rebuild project --project research --key "$KEY" --limit 10   # run again until status is completed
taisce rebuild status  --project research --key "$KEY"
taisce rebuild cancel  --project research --key "$KEY"
```

Each `rebuild project` run re-reads up to `--limit` sources and saves its progress. Run it again
until `status` is `completed`. `status` and `cancel` don't need a model, so they work while your
provider is down. To re-read a single turn, use `rebuild facts` with `--project`, `--source` (the
observation id) and `--key`. To write missing theme reports now instead of waiting for the worker,
use `rebuild reports --project research`.

To restore derived rows from what's stored, without calling a model, use `recover facts` and
`recover chunks`. Each run handles one page; keep passing the `next` cursor until it's absent.

## All commands

Every command exits 0 on success and 1 on failure. Flags take one dash or two (`-project` and
`--project` are the same), and they stop at the first argument that isn't a flag.

### Serving and setup

| Command | What it does | Flags and notes |
|---|---|---|
| `taisce`, `taisce serve` | Run the server | The role comes from `TAISCE_ROLE` |
| `bootstrap` | Prepare an empty database and mint the first tokens | `-project` (default `default`), `-credential` (default `bootstrap`). Needs `TAISCE_CONTROL_PASSWORD` and `TAISCE_DATA_PASSWORD`. Safe to repeat |
| `operator issue NAME` | Mint another operator token | Always straight to the database |
| `version`, `--version` | Print the version | `unknown` for a plain `go build`; `make build` stamps it |
| `help`, `-h`, `--help` | Print the command list | `taisce help COMMAND`, or `--help` after any command, prints that command's lines |

### Projects, keys and audit

These use the management API when `TAISCE_MANAGE_API` is set, and the database otherwise.

| Command | What it does | Flags and notes |
|---|---|---|
| `project create NAME` | Create a project | Safe to repeat |
| `project list` | List projects | Shows surfaces, retention and `[suspended]` |
| `project suspend NAME`, `project resume NAME` | Take a project offline, or bring it back | Refused if it's already in that state |
| `project retention NAME DAYS` | Set how long new turns are kept | `indefinite` clears it. Whole days, 1–36500. Deadlines already stamped on stored turns are not rewritten |
| `credential issue NAME` | Mint a project token | Then `--project` (default `default`), `--read-only`. Refused for a suspended project |
| `credential list` | List tokens | `--project`; without it, the operator tokens |
| `credential revoke ID` | Stop a token working | Refused if already revoked |
| `audit seal` | Seal new audit entries | Prints `nothing to seal` when there's nothing new |
| `audit verify` | Check the audit chain | Exits 1 on failure only when talking straight to the database |

### Formation, recovery and storage

These print JSON, and `--project` is required. `formation parked` and `formation unpark` use the
management API when `TAISCE_MANAGE_API` is set; the rest go straight to the database.

| Command | What it does | Flags |
|---|---|---|
| `formation parked` | List parked turns | `--after OFFSET`, `--limit` (1–200, default 100) |
| `formation unpark` | Retry one parked turn | Then the observation id, after the flags. Prints `observation_id` and `unparked`. A turn that is no longer parked prints `unparked: false` from the database and is refused as not found by the management API |
| `recover facts` | Restore missing facts from stored turns | `--source`, `--after`, `--limit` (1–100, default 100) |
| `recover chunks` | Restore missing message chunks | `--source`, `--after-offset` with `--after-ordinal`, `--limit` |
| `artifact limits` | Show or set a project's artifact allowance | None to show; all four of `--max-object-bytes`, `--max-bytes`, `--max-objects`, `--max-age-hours` to set |
| `health` | Content-free health summary | Needs `TAISCE_ADMIN_DSN` |

### Rebuilds

These go straight to the database. `--project` is required.

| Command | What it does | Flags |
|---|---|---|
| `rebuild facts` | Re-read one turn | `--source`, `--key`, both required. Needs a model |
| `rebuild project` | Re-read a whole project, a page per run | `--key` required, `--limit` (1–100, default 10). Needs a model |
| `rebuild status` | Show a project rebuild | `--key` |
| `rebuild cancel` | Stop a project rebuild | `--key` |
| `rebuild reports` | Write missing theme reports now | `--limit` (1–100, default 4). Needs a model |

### Embeddings

Embeddings let the API search by meaning. Each set of embeddings is called a **generation**, and
building one never changes what answers questions; only `activate` does.

| Command | What it does | Flags |
|---|---|---|
| `embeddings OP` | Manage message embeddings. `OP` is `start`, `build`, `status`, `activate`, `cancel`, `prune`, `repair` or `search` | `--project` (required); `--generation`; `--key` and `--dimensions` for `start`; `--revision`; `--limit` (default 32); `--candidates`; `--query`; `--subject` and `--role` for `search` |
| `embeddings follow` | Keep the active generation up to date | `--project`, `--generation` (required); `--revision`; `--limit`; `--watch`; `--interval` (default `2s`). Uses `TAISCE_MEMORY_DSN` |
| `entity-embeddings OP`, `report-embeddings OP` | The same for entities and theme reports | Same operations and flags, without `follow`, `--subject` or `--role` |

A typical order is `start`, then `build` until `complete` is `true`, then `activate`. After that, set
`TAISCE_INFERENCE_EMBEDDING_REVISION` on the API so it uses the generation. The commands that embed
text need `TAISCE_INFERENCE_EMBEDDING_MODEL`, an endpoint, and an allowlist entry for it.

### Clients and checks

| Command | What it does | Flags |
|---|---|---|
| `ingest FILE...` | Load documents as turns | Flags first: `--api`, `--role` (`tool` or `user`, default `tool`), `--data-subject`, `--occurred-at`, `--segment-bytes` (default 8192), `--max-file-bytes`, `--manifest-dir` (default `taisce-ingest`), `--capacity-wait` (default `5m`), `--wait`. One manifest per file, named after it (`a.txt.manifest.json`); two inputs with the same file name are refused before anything is sent |
| `conformance` | Run the adapter test suite against a deployment | `--api`, `--cases` (default `conformance/cases.json`), `--driver "COMMAND"` or `--reference`, `--serve-reference` |
| `probe` | Check a local process | `--live`, `--worker`, `--manage` |

## Output and scripts

A few commands draw a panel when they run in a terminal: `help`, `health`, `rebuild project`,
`rebuild status`, `rebuild cancel`, `rebuild reports`, and error messages. Everything else prints
plain lines or JSON. To control this:

| Setting | Effect |
|---|---|
| `TAISCE_OUTPUT=json` or `plain` | Never draw panels |
| `TAISCE_OUTPUT=fancy` | Always draw panels |
| `NO_COLOR` (any value) | No colour |
| `TAISCE_ASCII` (any value), or `TERM=dumb` | Plain ASCII, no colour |

In scripts, set `TAISCE_OUTPUT=json`. A failure is then written to **stderr** as one JSON line, so it never mixes into the output on stdout:

```text
{"time":"…","level":"ERROR","msg":"taisce failed","error":"…"}
```

In a terminal, the error panel leaves out the error text, so a raw database or provider error never
lands on your screen. Rerun with `TAISCE_OUTPUT=json` to see it.

Good to know:

- `taisce <command> --help` (or `-h`) prints that command's usage lines and exits 0, without
  connecting to anything.
- `bootstrap` mixes JSON log lines with the token lines, so pick out lines rather than parsing it as
  JSON.

## Environment variables

These are the variables the commands read. [Deployment](../architecture/deployment.md#environment-variables)
lists every setting the server reads.

| Variable | Used by | What it is |
|---|---|---|
| `TAISCE_ADMIN_DSN` | `bootstrap`, `operator`, direct database commands, `health`, the `manage` role | The login that may change the schema. Falls back to `TAISCE_MEMORY_DSN`, except for `health` |
| `TAISCE_MEMORY_DSN`, `TAISCE_REGISTRY_DSN` | The server; `embeddings follow` | The two limited logins the server uses |
| `TAISCE_SCHEMA` | Every command that connects | The memory schema. Default `memory` |
| `TAISCE_CONTROL_PASSWORD`, `TAISCE_DATA_PASSWORD` | `bootstrap` | Passwords for the two limited logins. Required; there's no default |
| `TAISCE_MANAGE_API`, `TAISCE_OPERATOR_TOKEN` | `project`, `credential`, `audit` | Use the management API instead of the database |
| `TAISCE_API`, `TAISCE_TOKEN` | `ingest`, `conformance` | The memory API and a project token |
| `TAISCE_INFERENCE_ENDPOINT`, `TAISCE_INFERENCE_EXTRACTOR_MODEL`, `TAISCE_INFERENCE_API_KEY` | Rebuilds | The model that reads facts |
| `TAISCE_INFERENCE_EMBEDDING_ENDPOINT`, `TAISCE_INFERENCE_EMBEDDING_MODEL`, `TAISCE_INFERENCE_EMBEDDING_API_KEY` | Embedding commands | The embedding model. The endpoint defaults to the one above |
| `TAISCE_INFERENCE_ALLOWLIST` | Anything that calls a model | Hosts allowed to receive text. Empty allows nothing |
| `TAISCE_FORMATION_TURN_BUDGET` | `rebuild facts` | Time limit per turn. Default `5m` |
| `TAISCE_ADDR`, `TAISCE_HEALTH_ADDR`, `TAISCE_MANAGE_ADDR` | `probe` | Where each local listener is |
| `TAISCE_OUTPUT`, `TAISCE_ASCII`, `NO_COLOR`, `COLUMNS` | Terminal output | See [output and scripts](#output-and-scripts) |

## Going deeper

- [Quickstart](quickstart.md): a deployment running end to end.
- [Troubleshooting](troubleshooting.md): what the errors mean and how to fix them.
- [Deployment](../architecture/deployment.md): the roles as compose services and a Helm chart.
- [Formation](../architecture/formation.md) and [governance](../architecture/governance.md): what
  rebuild, recovery and the audit log act on.
- [Roles and grants](../postgresql/roles-and-grants.md): the database logins these commands use.
- The code: [cmd/taisce](../../cmd/taisce/).
