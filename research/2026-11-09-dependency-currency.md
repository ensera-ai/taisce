<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Dependency currency — 2026-11-09

Two lists per ecosystem: what is behind, and what is behind **and** carries a published advisory. Only
the second justifies work, and each entry on it has an issue. Nothing was upgraded in this pass. Part of
#120.

Advisory data is OSV's, read 2026-11-09; OSV ingests the GitHub advisory database. Each ecosystem's own
tool was run as well, and where both answered they agreed.

## The finding underneath the lists

**Fact.** Nothing in any of the four repositories checks dependencies against advisories, and
Dependabot alerts — enabled on all four, zero open on all four — read a dependency graph that answers
`404`, where a control public repository answers `200`. Every advisory below went unnoticed for that
reason — #126.

## Behind and carrying an advisory

| Ecosystem | Dependency | Have | Advisories | Fixed from | Issue |
|---|---|---|---|---|---|
| Maven (shipped to every consumer, compile scope) | `jackson-databind` | 2.20.0 | 12, five high | 2.21.7 / 2.22.3 | #123 |
| Maven | `jackson-core` | 2.20.0 and 2.22.1 | 4 and 2, three high | 2.21.7 / 2.22.3 | #123 |
| PyPI (declared floor) | `langchain-core` | `>=1.0` | 6, one critical, three high | 1.3.3 covers all | #124 |
| PyPI (declared floor) | `langchain` | `>=1.0` | 1 moderate | 1.3.9 | #124 |
| PyPI (declared floor) | `langgraph` | `>=1.0` | 1 moderate | 1.0.10 | #124 |
| Go | `golang.org/x/text` | v0.39.0 | `GO-2026-6629`, reachable per `govulncheck` | v0.41.0 | #125 |
| Go | `golang.org/x/sys` | v0.41.0 | `GO-2026-5024`, Windows only, not called | v0.44.0 | #125 |

**Fact.** `dotnet list package --vulnerable --include-transitive` reports no vulnerable package in any
.NET project.

**Fact.** Python packages declare floors and no lockfile, which is right for a library: what installs
is the consumer's. That is why the floor, not a pinned version, is what was checked.

## Behind, no advisory

No issue: being behind is not, by itself, a reason to spend work.

**Go** (`go list -m -u all`, direct and indirect):

| Module | Have | Current |
|---|---|---|
| `github.com/jackc/pgx/v5` | v5.10.0 | v5.12.0 |
| `github.com/modelcontextprotocol/go-sdk` | v1.7.0 | v1.8.0 |
| `golang.org/x/oauth2` | v0.35.0 | v0.37.0 |
| `golang.org/x/sync` | v0.21.0 | v0.24.0 |
| `golang.org/x/time` | v0.15.0 | v0.16.0 |
| `golang.org/x/mod` | v0.37.0 | v0.42.0 |
| `golang.org/x/tools` | v0.47.0 | v0.52.0 |
| `github.com/stretchr/testify` | v1.11.1 | v1.12.1 |

Plus indirect test-only modules (`creack/pty`, `kr/pretty`, `stretchr/objx`, `segmentio/asm`,
`jackc/puddle/v2`, `cloud.google.com/go/compute/metadata`).

**.NET** (`dotnet list package --outdated`):

| Package | Have | Current |
|---|---|---|
| `Microsoft.Agents.AI`, `.Abstractions` | 1.20.0 | 1.28.0 |
| `Microsoft.Extensions.AI.Abstractions`, `.OpenAI` | 10.10.0 | 10.11.0 |
| `Microsoft.Extensions.*` (DI, Options, Logging) | 10.0.0–10.0.11 | 10.0.14 |
| `Microsoft.NET.Test.Sdk` | 17.14.1 | 18.11.0 |
| `xunit.runner.visualstudio` | 3.1.3 | 4.0.1 |

**Inference.** Eight minor releases behind on the framework the .NET adapter plugs into is the one worth
watching: the seam is the framework's, and a seam that moves is what breaks an adapter.

**Java** (`mvn versions:display-dependency-updates`):

| Artifact | Have | Current |
|---|---|---|
| `dev.langchain4j:langchain4j` (provided) | 1.20.0 | 1.23.0 |
| `org.springframework.ai:spring-ai-client-chat` (provided) | 2.0.1 | 2.1.0-RC2 — a release candidate, not a release |
| `org.junit.jupiter:junit-jupiter` (test) | 5.13.4 | 6.1.3 |

**Python.** The client's `httpx>=0.27` and the Agent Framework adapter's `agent-framework-core>=1.18.0`
carry no advisory at their floors.

## How to re-run this

```sh
govulncheck ./... && go list -m -u all
dotnet list Taisce.slnx package --vulnerable --include-transitive
dotnet list Taisce.slnx package --outdated
mvn -B -DskipTests package dependency:list -DincludeScope=runtime
mvn -B versions:display-dependency-updates
```

PyPI floors and resolved Maven versions were checked with OSV's `POST /v1/querybatch`, one query per
package and version. Run `mvn ... package`, not `install`: installing would put this checkout's
`taisce-client` into the local repository, where it would shadow the published artifact of the same
version.
