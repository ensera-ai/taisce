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
| [`CONTRIBUTING.md`](CONTRIBUTING.md) | How work is tracked |
| [`CLAUDE.md`](CLAUDE.md) | The working rules, in full |
| [issues](https://github.com/ensera-ai/taisce/issues) | The roadmap — the source of truth for scope |

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
  relations. Measured here: `qwen3.6:35b-a3b-mxfp8` holds the contract; `qwen3.8:27b-mxfp8` returns
  valid JSON of the wrong shape and every turn is refused. `make test-inference` runs a fixed corpus
  so the difference is measured rather than guessed at.

**Without a model it still runs.** Turns are stored, whatever is already formed is recalled, erasure
works — and it says so loudly at startup, because a system that silently stores and never extracts
looks like a memory that does not work.

**Generation and embedding can be different providers.** They are different jobs: extraction is slow,
is the expensive part, runs on a background pass where nobody is waiting, and is where a better model
shows; embedding is a cheap forward pass over short text. So a hosted extractor with a local embedder
is a reasonable arrangement:

```
TAISCE_INFERENCE_ENDPOINT=https://<provider>/v1
TAISCE_INFERENCE_EXTRACTOR_MODEL=<a model that provider serves>
TAISCE_INFERENCE_API_KEY=<their key>
TAISCE_INFERENCE_EMBEDDING_ENDPOINT=http://host.docker.internal:11434/v1
TAISCE_INFERENCE_EMBEDDING_MODEL=qwen3-embedding:4b-q8_0
TAISCE_INFERENCE_ALLOWLIST=<provider host>,host.docker.internal:11434
```

Both endpoints are checked against the one allowlist and a second endpoint inherits nothing from the
first, because both send somebody's words: generation sends a message, embedding sends text written
from several of them. **A hosted provider means conversation text leaves your machine** — the
allowlist is where you decide that, and it cannot be arrived at by default.

**Nothing on the read path calls a model.** Recall resolves entity names with a lookup, and freshness,
erasure and export call nothing at all — so a slow or absent provider costs you formation, never an
answer.

### The journey, over HTTP

```bash
TOKEN=$(docker compose logs bootstrap | grep -o 'tsk_[A-Za-z0-9_-]*' | head -1)

curl -X POST localhost:8080/v1/observations -H "Authorization: Bearer $TOKEN" \
  -d '{"scope":"default","data_subject_id":"alice",
       "messages":[{"role":"user","content":"I work at Ensera and I live in Dublin."}]}'

curl "localhost:8080/v1/freshness?scope=default" -H "Authorization: Bearer $TOKEN"
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
  -d '{"scope":"default","data_subject_id":"alice","reason":"right to erasure"}'
# "deleted": {"observation":1,"fact":2,"entity":3,"chunk":1}
# "residual": {"fact":0,"entity":0,"chunk":0,"rejected_claim":0}, "clean": true
```

**A question has to name something in memory.** Recall anchors on the entities a question mentions,
so *"What do we know about Ensera?"* works and *"Where does the user work?"* returns nothing — the
entity is recorded as `I`. Reaching an entity by meaning rather than by name is real work that is not
built yet.

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
