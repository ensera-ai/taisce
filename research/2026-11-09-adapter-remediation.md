<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Adapter remediation: what each adapter does now, and how it was shown — 2026-11-09

What the audit of the same day ([audit](2026-11-09-adapter-audit.md),
[dependencies](2026-11-09-dependency-currency.md)) found, and what changed in each repository in
answer. Every number here was measured on 2026-11-09. All the runs below are correctness runs on a
development machine; none is a performance figure.

## How it was run

- **Deployment.** The quickstart's compose stack at the released v0.5.1 (`16ced9e`), under its own
  Compose project on port 18093, with a value that is not a model key and an allowlist that reaches
  nothing. This is the arrangement the adapters' `conformance` workflows use (D23).
- **Runner.** `taisce conformance`, built from #138, which carries twelve cases: the ten before, plus
  `different_turns_are_never_one_key` (#121, D25) and `erasure_hands_back_the_receipt` (#134, D24).
- **Keys.** The bootstrap's `read_write` key, plus a `read_only` key minted with the release's own
  binary: `docker compose run --rm --no-deps bootstrap credential issue live-reader --project default
  --read-only`. That key answers `403` to `POST /v1/erasures`.

## Results

| Adapter | Before: unchanged `main` | After | Default tests | Live tests | Advisories |
|---|---|---|---|---|---|
| Reference (service) | — | 12/12; the two new mutants (`LineJoinedKey`, `ClaimErasure`) are each refused by name | `go test ./internal/conformance ./cmd/taisce` pass | — | `make vuln`: none, after Go 1.27.3 |
| Python, Agent Framework | 12 run, 2 fail: `different_turns_are_never_one_key` (`409 idempotency_conflict`), `erasure_hands_back_the_receipt` (no receipt) | 12/12 | 49 passed | 4 passed; errors, not skips, with no deployment | `pip-audit`: none at newest; none at floors (pytest ≥ 9.0.3) |
| Python, LangGraph | — | 12/12 | (same run) | (same) | (same) |
| Python, both drivers at the floors | — | 12/12 each | 49 passed | — | — |
| .NET, Agent Framework | 12 run, 2 fail: the same two cases | 12/12 | 40 passed (16 before) | 4 passed; fail, not skip, with no deployment | `dotnet list package --vulnerable --include-transitive`: none |
| Go, Agent Framework (new) | — | 12/12, on its first run | `go test ./...` pass | 4 passed; fail, not skip, with no deployment | `govulncheck`: none |
| Java, LangChain4j and Spring AI | 12 run, 2 fail per driver: the same two cases | 12/12 per driver | `mvn -B verify`: client 42 (10 before), langchain4j 2, spring-ai 1 | 4 passed under `-Plive`; fail, not skip, with no deployment | osv-scanner v2.7.0 on the CycloneDX SBOM Maven resolved: none (run in CI; the local download was not permitted). An OSV query of the 50 resolved coordinates: 1 hit before the Jackson 3 BOM, 0 after |

**Fact.** In every repository, the unchanged code fails exactly the two new cases, and the changed
code passes all twelve. That is what shows the cases catch the defects they were written for, and
not only that the new code passes them.

## Dependencies moved

- **Service.** `golang.org/x/text` v0.39.0 → v0.43.0 and `golang.org/x/sys` v0.41.0 → v0.49.0 (#125).
  Go 1.27.1 → 1.27.3. The new `make vuln` found that 1.27.1's standard library carried advisories
  the code reaches: GO-2026-6809 (crypto/x509), and GO-2026-6617, -6613, -6612, -6611, -6610 and
  -6608 (net/http, http2, mime/multipart). Every published binary and image so far was built with
  1.27.1.
- **Python.** Floors raised to `langchain` 1.3.9, `langchain-core` 1.3.3 and `langgraph` 1.0.10 (#124).
  At `--resolution lowest-direct` these resolve `langchain-core` 1.4.6 and `langgraph` 1.2.4,
  because `langchain` 1.3.9 requires them. The newest installed were `langchain` 1.4.9,
  `langchain-core` 1.7.6 and `langgraph` 1.2.20.
- **.NET.**
  - Microsoft.Agents.AI 1.20.0 → 1.28.0
  - Microsoft.Extensions.AI 10.10.0 → 10.11.0
  - Microsoft.Extensions.* 10.0.11 → 10.0.14
  - Microsoft.NET.Test.Sdk 17.14.1 → 18.11.0
  - xunit.runner.visualstudio 3.1.3 → 4.0.1
- **Java.**
  - Jackson 2.20.0 → 2.23.0, one version in every module through `jackson-bom` (#123)
  - The Jackson 3 line that Spring AI 2 brings (`tools.jackson`), 3.0.3/3.1.7 → 3.3.0 through its own BOM, which cleared 6 advisories the audit had not looked for
  - LangChain4j 1.20.0 → 1.23.0
  - Spring AI 2.0.1 → 2.0.2 (2.1.0 is a release candidate only)
  - JUnit 5.13.4 → 6.1.3, and every Maven plugin to its latest

## What is still open, and where

- **The new cases are not in adapter CI yet.** Every adapter's `conformance` workflow pins service
  v0.5.1 and fetches cases from that release's commit (D23), so the two new cases run there only
  once a service release carries them and the pin moves. They were run locally, above.
- **The dependency graph is still off.** Turning it on is an organisation security setting, which
  the owner changes (#126).
- **Redirects.** #122 left open how each client treats a redirect. The Go client follows none,
  `httpx` follows none by default, and Java's `HttpClient` defaults to never. *Unverified:* whether
  .NET's `HttpClient`, which follows redirects by default, drops the `Authorization` header when it
  follows one to another host.
- **The Go repository has not been published.** `ensera-ai/taisce-go` has not been created yet; its
  code is committed locally.
