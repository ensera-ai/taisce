<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Adapter audit: what they reach and what they trust — 2026-11-09

**Fact** — read or run; **Inference** — follows from that; **Unverified** — not confirmed. Repositories
read at the commits in [the inventory](2026-11-09-adapter-inventory.md); the service at `82d43b3`. Part
of #120.

## What this pass covered, and what it did not

It read the three clients and five framework adapters against the service's declared surface, and
worked the boundary each adapter sits on: what it sends, what it trusts, what credential it holds, and
how its defaults fail. It did not run the adapters beyond reading their conformance jobs, fuzz anything,
or review the portal, the MCP endpoint, the chart or any release workflow. Nothing here says those are
free of defects; they were not looked at.

"Full featured" had no definition before this. The capability table below is the definition: a service
capability an adapter cannot reach is a gap, and each gap is either built or deliberately rejected.

## Capability: what the clients reach

**Fact.** `internal/api/api.go` declares 35 operations. All three clients send exactly the same 9:
`observations`, `freshness`, `recalls`, `contexts`, `passages/search`, `artifacts/put|get|delete`,
`citations/resolve`. The three are in step with each other; together they cover the agent loop and
nothing outside it.

| Capability the product names | Reachable from a client |
|---|---|
| Answer from entities | Yes, through recall's facts and reports. Entity inspection (`entities/*`): no. |
| Time as a first-class property | Yes. `as_of` and `as_known_at` reach recall in all three clients — typed in .NET, open controls in Python and Java. The framework adapters' own automatic recall passes neither; it recalls the present. |
| The words behind a claim | Yes, `citations/resolve` in all three. |
| Prove what it deleted | **No.** `erasures` is in no client. |
| Export | **No.** `exports` is in no client. |
| Curated facts | **No.** `records/assert|correct|retract` are in no client. |

**Inference.** The two capabilities the product leans on hardest for governance are the two a developer
cannot reach from the library they already use. Whether that is right is a decision, not a defect: #127.

**Fact.** The service refuses unknown request fields (`internal/api/request.go:29`). So the Python
client's open `**controls` on recall cannot silently drop a misspelt control; the deployment rejects it.

## Security: the boundary

### What each adapter holds

**Fact.** A bearer credential bound to one project, held in memory, attached to every request. The
Python client, read in full, never writes or logs it. The .NET and Java constructors hold it the same
way; their error and logging paths were not traced (**Unverified** that no exception carries it).

**Fact.** None of them checks the address's scheme. `http://` to any host is accepted, and the
credential crosses the network in clear — #122. Loopback must stay allowed: the conformance runner and
every adapter's CI use `http://127.0.0.1` and `http://localhost:18080`.

### What it trusts from the model

**Fact.** Memory reaches the model as one `user` message whose first line is
`taisce-memory/v1 untrusted`, carrying the recall's arrays unchanged — never `system`, never
`assistant`. The conformance suite holds that.

**Fact.** The model's reply is stored as the turn's `assistant` message, and it is also an input to the
turn's idempotency key. The key joins subject, run, roles and contents with newlines and no length
prefix, so two different turns can produce one key:

| | user | assistant |
|---|---|---|
| Turn A | `Where does Marta work?\nassistant\nAt Ensera.` | `Noted.` |
| Turn B | `Where does Marta work?` | `At Ensera.\nassistant\nNoted.` |

Both hash to `be2dd158-12a0-54cc-8929-3c7f28d9cd58` for subject `alice`, run `run-1`, through the
Python derivation as written; the .NET and Java ones build the same string.

**Fact.** The deployment fingerprints the turn itself and answers `409` to a key reused for a different
turn (`internal/infra/pg/observationretry.go`). The collision is therefore loud, not silent. But the key
is deterministic, so the refused turn is refused on every retry and can never be stored — #121.

**Inference.** Subject and run are inside the key, so the effect stays within one subject's run. An
adapter configured with neither hashes both as empty, and the confinement widens to every such turn.

### What it trusts from the service

**Fact.** The clients parse the service's answers as JSON and hand recall arrays to the model unchanged.
The Java client parses with a Jackson version inside the range of 16 published advisories — #123.

**Inference.** A correct deployment emits nothing that reaches the `jackson-core` parser advisories; a
hostile network path does, which is one more reason #122 matters.

### Can one tenant reach another

**Fact.** One deployment is one tenant, and a credential opens one project. Inside a project, the
session stores (`SessionStore` in Python, its counterparts elsewhere) refuse to act without a subject.

**Unverified in this pass.** That the deployment answers another subject's object as one that does not
exist. The clients' documentation says so; the service's artifact handler was not read.

**Unverified.** Whether the project credential an agent holds is granted erasure. That decides the
design of #127 and was not checked.

## Dependency advisories

In [the dependency report](2026-11-09-dependency-currency.md): #123, #124, #125, and #126 for why none of
them was noticed.
