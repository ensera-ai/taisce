<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# What the adapters are — 2026-11-09

Established from the repositories and the organisation, not from what the adapters are said to be.
Every claim is labelled: **Fact** — read directly; **Inference** — follows from what was read;
**Unverified** — plausible, not confirmed. Part of #120.

## Repositories

**Fact.** `gh repo list ensera-ai` lists three adapter repositories beside the service, each described
as "Taisce adapter":

| Repository | Read at | Client | Framework adapters |
|---|---|---|---|
| `ensera-ai/taisce-python` | `00b6e3b` | `taisce` (async, `httpx` only) | `taisce-agent-framework` (Microsoft Agent Framework), `taisce-langgraph` |
| `ensera-ai/taisce-dotnet` | `87ddf40` | `Taisce.Client` | `Taisce.AgentFramework` (Microsoft Agent Framework) |
| `ensera-ai/taisce-java` | `a56e8bd` | `taisce-client` | `taisce-langchain4j`, `taisce-spring-ai` |

Five framework adapters on three clients.

## Go

**Fact.** There is no Go adapter repository, and no Go client a Go application can import: every package
in this module sits under `internal/`, and the Go toolchain refuses to import an `internal/` package from
another module. The only Go-facing surfaces are the HTTP contract and the `taisce` binary.

**Fact.** The reference conformance driver is Go (`internal/conformance`), run as
`taisce conformance --reference`. It proves the deployment and the suite; it is not a library.

Whether to build one is #128.

## Microsoft Agent Framework

**Fact.** An integration exists in two languages: `taisce-agent-framework` (Python) and
`Taisce.AgentFramework` (.NET). Both are context providers: memory goes in as one untrusted `user`
message, and the turn is stored afterwards.

**Fact, read 2026-11-09.** The framework's own repository holds `dotnet/`, `python/` and a `go/` directory
whose README points at `microsoft/agent-framework-go`. That repository was last pushed 2026-11-09, its
newest tag is `v0.3.0`, and at that tag `agent/context.go` declares a `ContextProvider` with `Invoking`
and `Invoked` hooks — the same two seams the .NET and Python adapters use.

**Fact, read 2026-11-09.** The vendor's documentation disagrees with itself: its FAQ says the framework
supports .NET and Python; its OpenAI provider page says C#, Python and Go.

**Inference.** There is no Java Agent Framework to integrate with, so the absence of a Java one here is
not a gap. A Go one is buildable on a pre-1.0 seam; that is what #128 decides.

## The conformance suite, per language

**Fact.** One suite, `conformance/cases.json`, ten cases, run unchanged by every adapter against a live
deployment through a recording proxy. The cases cover: memory as one untrusted user message; recall
failure not fatal; write failure never silent; storing only on success; filtering to external messages;
never observing a synthetic message; compaction handing the model the deployment's context; context
failure not fatal; a retried turn stored once; a turn retried a second later stored once.

**Fact.** Each adapter repository's `conformance.yml` stands up a deployment, runs the reference first to
prove the stack, then drives every framework adapter it ships:

| Language | Drivers run in CI |
|---|---|
| Python | `taisce_agent_framework.conformance`, `taisce_langgraph.conformance` |
| .NET | the Agent Framework adapter |
| Java | LangChain4j, Spring AI |

So all five framework adapters are held to the same ten cases.

**Fact.** The suite asserts that the same turn gets the same idempotency key. It does not assert that
different turns get different keys, and they can collide — #121.

**Fact.** The suite is about the agent loop. It does not exercise anything the clients do not send —
erasure, export, records — because no client sends them (#127).
