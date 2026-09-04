# Taisce

Memory for agents. An agent writes what it observes; later it asks a question and gets back what is
relevant, current and attributable — with the evidence for each claim, the history behind it, and a
guarantee that anything erased is gone and can be shown to be gone.

Entities are primary and passages are evidence: a question resolves to the entities it names, the
answer is assembled by expanding their neighbourhood, and passages are read to support what the
traversal found. Retrieval cost is proportional to a neighbourhood rather than to how much the
customer has stored.

A multi-tenant SaaS on one PostgreSQL substrate, hosted in the EU. The adapters and clients that
reach it are open source; the service is not.

## Where to start

| | |
|---|---|
| [`docs/00-goals.md`](docs/00-goals.md) | What this is for, and what would prove each goal |
| [`docs/01-decisions.md`](docs/01-decisions.md) | Every decision, closed, with what was rejected |
| [`CONTRIBUTING.md`](CONTRIBUTING.md) | How work is tracked |
| [`CLAUDE.md`](CLAUDE.md) | The working rules, in full |
| [issues](https://github.com/althunibat/taisce/issues) | The roadmap — the source of truth for scope |

## Running it

```
make db-up     # build the substrate image and start PostgreSQL on 55432
make test      # against that deployment; there is no mock path
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

Full text in [`CLAUDE.md`](CLAUDE.md).
