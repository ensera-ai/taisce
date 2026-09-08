# Taisce

Memory for agents. An agent writes what it observes; later it asks a question and gets back what is
relevant, current and attributable — with the evidence for each claim, the history behind it, and a
guarantee that anything erased is gone and can be shown to be gone.

Entities are primary and passages are evidence: a question resolves to the entities it names, the
answer is assembled by expanding their neighbourhood, and passages are read to support what the
traversal found. Retrieval cost is proportional to a neighbourhood rather than to how much the
customer has stored.

Open source under Apache-2.0 — all of it, service and portal and adapters — on one PostgreSQL
substrate. You run it: `docker compose up` on a machine, or a Helm chart in a cluster where it is
highly available by default. Your data does not leave, and the proof that something was erased is
computed on your own substrate rather than asserted by somebody you have to trust.

## Where to start

| | |
|---|---|
| [`docs/00-goals.md`](docs/00-goals.md) | What this is for, and what would prove each goal |
| [`docs/01-decisions.md`](docs/01-decisions.md) | Every decision, closed, with what was rejected |
| [`docs/02-contract.md`](docs/02-contract.md) | Every operation, field and refusal on the wire |
| [`docs/03-market.md`](docs/03-market.md) | What is shipped elsewhere, dated, and what it obliges here |
| [`docs/04-feature-coverage.md`](docs/04-feature-coverage.md) | Verified feature coverage, gaps and execution order |
| [docs/05-releases.md](docs/05-releases.md) | Preparing public snapshots and preserving private development history |
| [`CONTRIBUTING.md`](CONTRIBUTING.md) | How work is tracked |
| [`CLAUDE.md`](CLAUDE.md) | The working rules, in full |
| [development issues](https://github.com/althunibat/taisce/issues) | The private roadmap — the source of truth for scope |

## Running it

One machine, one command.

```
docker compose up
```

On the laptop this was written on, that reached a serving instance in about fifteen seconds and
formed its first memory about half a minute later. **Those are setup timings, not performance
numbers** — the second is almost entirely a 35B model on a laptop GPU, and it says nothing about how
this system behaves under load. No latency target, throughput target or corpus size has been agreed,
and none will be quoted here until it has been measured somewhere that can measure it.

That brings up PostgreSQL, applies every migration, creates a project, mints a credential and serves
on `:8080`. The credential is printed once by the bootstrap step:

```
docker compose logs bootstrap | grep token:
```

### A model, which is the one thing that cannot be shipped

Extraction needs one. No key belongs in an open repository, and a hosted provider would put a signup
inside the first five minutes — so the default points at [Ollama](https://ollama.com) on your own
machine, which costs nothing and sends nothing anywhere.

```
OLLAMA_HOST=0.0.0.0 ollama serve        # it must listen beyond loopback, or the container sees nothing
ollama pull qwen3.6:35b-a3b-mxfp8
```

Two things that will otherwise cost you an hour, both of them mechanisms working rather than bugs:

- **`TAISCE_INFERENCE_ALLOWLIST` is host and port, not a URL.** `http://host.docker.internal:11434`
  matches nothing. It is closed when unset, deliberately — an allowlist that permits everything when
  empty is one forgotten variable from not being one — so the symptom of getting it wrong is an
  instance that starts, stores turns and never forms memory.
- **Model choice is not cosmetic.** Extraction asks for strict JSON against a closed vocabulary of 39
  relations, and models differ on whether they can hold that. Measured here with `make test-inference`,
  which is nineteen fixtures run three times each:

  | model | result |
  |---|---|
  | `qwen3.6:35b-a3b-mxfp8`, local | holds the contract |
  | `qwen/qwen3.8-flash`, hosted | holds the contract, and found one relation the local model missed |
  | `qwen3.8:27b-mxfp8`, local | returns valid JSON of the wrong shape; every turn refused |

  The two that hold it agreed on **every case both measured**: the same relation from the drift
  fixture, the same four refusals for negation and hedging, the same three answers on tense, and the
  same refusal of a tool result that instructs the extractor. `scripts/compare-corpus.sh` renders two
  runs side by side so a difference is visible without reading either log.

**Without a model it still runs.** Turns are stored, whatever is already formed is recalled, erasure
works — and it says so loudly at startup, because a system that silently stores and never extracts
looks like a memory that does not work.

**Where inference happens is a profile**, in [`deploy/inference/`](deploy/inference/):

| profile | generation | embedding |
|---|---|---|
| `local` | a model on your machine | the same machine |
| `openrouter` | a hosted provider | your machine |
| `gpu` | a host you rented, for the measurements a laptop cannot take | yours to point |

A profile carries an endpoint, a model and an allowlist. **It never carries a key** — a credential in
a committed file is a credential in the repository — so you export that yourself. Source one before
bringing the stack up, or name it when running the corpus:

```
set -a; . deploy/inference/local.env; set +a; docker compose up
make test-inference INFERENCE_PROFILE=openrouter
```

Anything already exported wins over the profile, so changing one setting for one run needs no edit.

**Generation and embedding can be different providers**, because they are different jobs: extraction
is slow, is the expensive part, runs on a background pass where nobody is waiting, and is where a
better model shows; embedding is a cheap forward pass over short text. Both endpoints are checked
against the one allowlist and a second endpoint inherits nothing from the first, because both send
somebody's words — generation sends a message, embedding sends text written from several of them.

**A hosted provider means conversation text leaves your machine.** The allowlist is where you decide
that, and it cannot be arrived at by default: it is closed when unset and it names every host, so
permitting one is always somebody writing the host down.

For retry-safe writes, send an `idempotency_key` UUID generated once per logical turn and reuse it
with the same payload when retrying. A matching retry returns the original `id` and `log_offset`.
Reusing the key for different input, or after its observation was erased or expired, returns HTTP 409
with `idempotency_conflict`. Keys are scoped to the credential's project and remain reserved there;
use a new UUID for a new logical write. Omit the key only when each call should create a new turn.

Observation messages may carry `group_ordinal`: use the same number for an assistant tool call and
its results, starting at zero and increasing by one between groups. Supply it on every message or
omit it on all. Omitted groups make ordinary messages independent; turns with tool messages require
explicit groups. Export preserves each message's observation ID, ordinal, and group ordinal.

A newly created project's freshness response has `stored: null` and `formed: null`. After its first
observation, `stored` is `0`; `formed` stays null until formation finishes. Erasure preserves the
highest accepted offset so an old write receipt does not become indistinguishable from a new project.

**Nothing on the read path calls a model.** Recall resolves entity names with a lookup, and freshness,
erasure and export call nothing at all — so a slow or absent provider costs you formation, never an
answer.

Each deployment uses one memory namespace (`memory` by default) and the fixed `control` credential
namespace. `TAISCE_SCHEMA` is an optional instance-wide override for an existing installation; keep
its value during upgrades. Changing it selects different storage and does not migrate data. Project
creation adds a project row and partition inside the existing memory namespace. Database grants
protect credential secrets; authenticated project filters control memory reads.

Migration 0023 validates project-consistent database relationships and refuses inconsistent existing
rows atomically. It also invalidates previously generated community reports because their source
registrations may be incomplete; the worker regenerates them from retained facts. Observations,
messages, facts and evidence are preserved. Review the regeneration cost before upgrading; see
[D68](docs/01-decisions.md#d68--relationships-agree-on-the-project-and-reports-register-exact-source-identities).

### The journey, over HTTP

```bash
TOKEN=$(docker compose logs bootstrap | grep -o 'tsk_[A-Za-z0-9_-]*' | head -1)

curl -X POST localhost:8080/v1/observations -H "Authorization: Bearer $TOKEN" \
  -d '{"data_subject_id":"alice",
       "messages":[{"role":"user","content":"I work at Ensera and I live in Dublin."}]}'

curl "localhost:8080/v1/freshness" -H "Authorization: Bearer $TOKEN"
# {"scope":"default","stored":0,"formed":null,"parked":0}   <- formed is absent until it forms

curl -X POST localhost:8080/v1/recalls -H "Authorization: Bearer $TOKEN" \
  -d '{"question":"What do we know about Ensera?"}'
```

which answers with the words behind the claim, not just the claim:

```json
{"anchors": [{"name": "Ensera", "type": "organisation", "matched": "ensera"}],
 "facts": [{"predicate": "works_at", "object": "Ensera",
            "statement": "The speaker works at Ensera.",
            "evidence": {"quote": "I work at Ensera", "byte_start": 0, "byte_end": 16}}]}
```

And erasure returns a counted residual rather than a claim that it worked:

```bash
curl -X POST localhost:8080/v1/erasures -H "Authorization: Bearer $TOKEN" \
  -d '{"data_subject_id":"alice","reason":"right to erasure"}'
# "deleted": {"observation":1,"fact":2,"entity":3,"chunk":1}
# "residual": {"fact":0,"entity":0,"chunk":0,"rejected_claim":0}, "clean": true
```

**Personal recall names the data subject explicitly.** For example:

```bash
curl -X POST localhost:8080/v1/recalls -H "Authorization: Bearer $TOKEN" \
  -d '{"question":"Where do I work?","data_subject_id":"alice","hops":2}'
```

This resolves Alice's speaker entity and filters anchors, every traversal hop, and evidence to
Alice's attributed observations. Without `data_subject_id`, recall remains project-wide and requires
an ordinary named anchor such as Ensera; an ambiguous `I` does not select everybody. The subject is
caller-supplied attribution, not an authenticated end-user identity. The project credential still
authorizes access to the whole project. Named shared organizations retain one identity.

Speaker references (`I`, `me`, `myself`, `the user`, `user`, `أنا`, `انا`, `نفسي`) bind only to the
stored user message and its nonempty data subject. Other pronouns and implicit references are not
inferred by this binding. Source text and quotes stay unchanged; protected identifiers are not added
to displayed names or statements. Migration 0022 refuses databases containing old name-only speaker
entities: those projections require rebuilding from retained observations before upgrading. It does
not guess whose supersession history should be restored. See D66 and the rebuild backlog.

### Developing on it

```
make db-up          # the substrate alone, on 55432
make test           # against that deployment; no mock path, coverage gated
make test-inference # extraction against a live model, and the regression corpus
make contract       # re-render docs/02-contract.md after changing the surface
make db-down
```

## The rules, in short

- **Everything has an issue**, opened before the work and closed with evidence — the commit, the
  plan, the test name, the number. Never *"implemented"*.
- **Nothing is brought in from another codebase** — not code, not decisions, not measurements —
  unless it is explicitly asked for.
- **"Migrate" means clone the idea**, rewrite the implementation, and validate it here. The check
  that catches a copy: every part must have a reader in this codebase.
- **Write as though nothing came before.** If the reasoning cannot be stated without naming another
  system, the reasoning is not finished.
- **Decisions go in the register with what was rejected**, and the register is the authority.
- **Proof, not assertion.** Tests run against a real deployment; a mock proves the mock matches the
  assertion.
- **Coverage is a requirement**, measured with `-coverpkg=./...` and ratcheted by the build. The only
  code allowed uncovered is the live-model path behind the `inference` tag, which has its own target.
- **Know what the market already does**, and take the capability rather than the implementation — a
  dated market document, kept out of the register.
- **Prompt text lives in embedded YAML**, and does not change without its regression corpus running.

Full text in [`CLAUDE.md`](CLAUDE.md).
