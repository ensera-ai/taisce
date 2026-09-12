<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# GPU qualification runs

This page records two runs of the full test suite on rented eight-GPU machines, on 2026-09-09 and
2026-09-11. Each run deployed Taisce with real models, ran every gate against it, and then wiped and
terminated the machine.

## What this means for you

- **The whole system works end to end with real models.** Writing an observation, forming facts
  with a real model, recall, and erasure with zero residual all passed on a deployed stack.
- **The test suite passes off a laptop.** Coverage was 92.0% on the first run and 91.8% on the
  second. The one failure in the second run was a test that assumed it was not running as root. The
  test has been fixed.
- **Watch your connection budget when you scale workers.** In the second run, 28 workers plus the
  API needed more connections than `max_connections=80` allowed, and 10 documents failed with HTTP
  500. Taisce now checks this at startup, and a process that does not fit refuses to start. See
  [Production PostgreSQL](21-production-postgresql.md).
- **These are functional runs, not capacity figures.** The generation benchmark is how fast one model
  endpoint answered a fixed prompt on this hardware. It is not how fast Taisce forms memory.

---

## Run 1 — 2026-09-09

A fresh GPU.ai VM with eight A100 80 GB cards, about 787 GiB of host RAM and 4.9 TiB of disk. The
whole machine cost $10 an hour, set as the maximum rate, with a four-hour provider runtime limit.
This was a disposable functional run, not a production deployment and not a latency or throughput
test.

### What ran

Docker Compose ran the application PostgreSQL, bootstrap, API, worker, a separate PostgreSQL for
integration tests, a Go test container and two vLLM services. The integration tests change
cluster-wide roles on purpose, so they got their own database to keep the deployed credentials safe.
The API listened on host loopback only. Database and model ports were not published.

| Role | Artifact | Placement |
|---|---|---|
| Extraction and reports | `Qwen/Qwen3.6-35B-A3B`, BF16, revision `995ad96eacd98c81ed38be0c5b274b04031597b0` | Tensor parallelism across four A100s |
| Embeddings | `Qwen/Qwen3-Embedding-4B`, BF16, revision `5cf2132abc99cad020ac570b19d031efec650f2b` | One A100, native 2560-dimension output |
| Model runtime | `vllm/vllm-openai:v0.28.0` | Digest `sha256:61fc8a896b0a4fbbbdc063bc4b0dbc25ce98e02b5050c24aeb7830ac02039b14` |

Three cards were not used. vLLM served TLS with a temporary certificate authority, because Taisce
requires HTTPS to a model. Only the public CA certificate was mounted into the application
containers, which run as non-root. The CA signing key was deleted after the certificates were
issued. Private TLS material and results were kept out of Git and out of Docker build contexts. The
provider account credential stayed on the operator's machine.

### Results

The deployed API and worker passed observation, real-model formation, subject-specific recall,
erasure with zero residual, and empty recall after erasure. All seven gates passed:

| Gate | Result |
|---|---|
| Full uncached Go and PostgreSQL regression | Pass |
| Cross-package coverage | **92.0%**, floor **91.6%** |
| `go vet ./...` | Pass |
| Full live inference suite | Pass |
| Stored embedding provider journey | Pass |
| Authenticated passage provider journey | Pass |
| Targeted HTTP and PostgreSQL race checks | Pass |

The first attempts found problems in the test setup, not in Taisce: an outdated vLLM logging flag, a
project name that broke the naming rules, missing TLS, an OpenSSL serial-file assumption that stopped
certificate permissions being set, and live model settings leaking into ordinary test fixtures. All
were fixed before the final pass. The failed attempts were kept as separate evidence, not overwritten.

No application environment or provider key was copied to the VM.

### Cleanup

The remote wipe finished successfully (exit 0) at **10:57:46 UTC**. It removed the Compose
project's containers, named volumes (both databases and the model caches), the images Compose had
pulled, and the source and configuration directory with its test credentials. Its checks confirmed
none of them remained. This is logical deletion. It does not claim a forensic overwrite of the
provider's disks.

Termination was requested at **10:57:47 UTC** and confirmed at **10:58:01 UTC**: the instance was
terminated and gone from the active inventory. The SSH key registered for the run and the local
private key were deleted. The four-hour limit was only a fallback; cleanup finished well before it.

---

## Run 2 — 2026-09-11

This run had four goals: run the full suite and every model-dependent gate off the development
machine, which cannot hold a 27B model and the suite at once; benchmark the generation endpoint; run
a head-to-head comparison; and form the full news corpus a third time (see
[Vocabulary coverage](31-vocabulary-coverage-ap-news.md)).

### Where it ran

A GPU.ai VM in us-east: eight A100 80 GB cards, 1.7 TiB RAM, 8.7 TB disk, $10 an hour, with an
eight-hour auto-terminate as the fallback. A us-central VM at $9.84 was tried first and never left
`starting`, so the controller skipped that region and took the next offer. The provider key stayed
on the operator's machine. The VM received the repository snapshot, the pinned model names and a
throwaway SSH key.

| Role | Artifact | Placement |
|---|---|---|
| Extraction, reports, summaries | `Qwen/Qwen3.8-27B` BF16, revision `1d4bf0f2ff6012fd82039f2fa52739d0dd7c60c0`, guidance structured-output backend | Seven replicas, one per card (0–6), no tensor parallelism |
| Embeddings | `Qwen/Qwen3-Embedding-4B` BF16, revision `5cf2132abc99cad020ac570b19d031efec650f2b` | Card 7 |
| Model runtime, generation | `vllm/vllm-openai@sha256:fc120ece0a388cc0aa1caad4a9f1cd92113484ab7ec2fd0efadd62585be05bf8` | |
| Model runtime, embeddings | `vllm/vllm-openai:v0.28.0` (`sha256:61fc8a896b0a4fbbbdc063bc4b0dbc25ce98e02b5050c24aeb7830ac02039b14`) | |

Seven single-card replicas instead of one model spread across cards: the suite and the corpus send
many independent requests, and seven separate servers give seven times the concurrency with no
traffic between cards. Run 1 used four cards for one model and left three idle.

### Gates

Each gate ran `deploy/gpu/tests.sh` against the deployed stack, in this order:

| Gate | Result | Seconds |
|---|---|---|
| Regression, `go test -count=1 -coverpkg=./...` | 19 packages ok; `cmd/taisce` failed one test | 356 |
| Cross-package coverage | **91.8%**, floor 91.7% | 1 |
| `go vet ./...` | Pass | — |
| Live inference suite (`inference` tag) | 70 passed, 0 failed, including the live compaction case | 315 |
| Stored embedding provider journey | Pass | — |
| Report-quality qualification | Pass | 23 |
| Vector-layout qualification | Pass | 763 |
| Anchor-layout qualification | Pass | 29 |
| Authenticated passage provider journey | Pass | — |
| Targeted HTTP and PostgreSQL race checks | Pass | 37 |
| Head-to-head comparison | Exit 0 | — |

The slowest regression packages: `internal/infra/pg` 343 s, `internal/api` 118 s, `cmd/taisce` 65 s,
`internal/migrate` 62 s, `internal/formation` 59 s.

**The one failure was in the test, not the system.**
`TestIngestRefusesAMisconfiguredRunBeforeReadingAnything` made a manifest path unwritable with file
permissions. Root ignores file permissions, and the VM runs the suite as root. The test now puts a
directory where the file would go, which stops root too. Every other test in that package passed.

### Generation benchmark

`Qwen3.8-27B` across the seven replicas, structured output, 140 requests at a concurrency of 28:

| Measure | Value |
|---|---|
| Requests per second | 24.7 |
| Completion tokens per second, total | 618 |
| Prompt tokens per second, total | 569 |
| Latency p50 / p95 / p99 / max | 1.03 s / 1.22 s / 1.34 s / 1.36 s |
| Valid structured outputs | 140 of 140 |
| Elapsed | 5.7 s |

This is the most the extraction endpoint delivered at that concurrency on this machine, and nothing
more: no database, no formation, one fixed prompt. It is an upper bound on what formation can ask
for, not a formation throughput figure.

### The news corpus, third pass

The 1,397-article news corpus was ingested and formed on the same stack: 28 projects, 1,387 documents
stored, 10 refused at ingestion, formation finished in 5,067 seconds with all seven generation
replicas at 100% utilisation, 20,124 proposals and 13,711 facts. The full breakdown and the shape of
the resulting graph are in [Vocabulary coverage](31-vocabulary-coverage-ap-news.md).

### What it found

**PostgreSQL ran out of connection slots, and writes failed.** Ten of 1,397 documents came back HTTP
500. Over thirty minutes the workers logged 357 failures, all
`remaining connection slots are reserved for roles with the SUPERUSER attribute`. Twenty-eight
workers and the API asked for more connections than `max_connections=80` could give. Nothing at the
time related the number of workers to the number of connections the database had, so scaling up
silently cost writes, and a 500 gives the caller nothing to act on. Today each process checks the
server's free slots at startup and refuses to start if its pool will not fit, and a write that still
meets a full server gets `503 no_database_capacity` with `Retry-After: 1`.

**Bootstrap was not repeatable under a non-superuser administrator.** This was found on the Helm
chart's test cluster the same morning, not on this VM, but it is the same kind of problem: a
deployment shape nobody had run. It has been fixed.

**A test depended on a permission bit.** Described above, and fixed.

**The corpus graph is flat.** 19,965 entities over 13,516 current facts, median degree 1 and a
maximum of 58. That is below the traversal's fanout cap of 64, so the cap never came into play on
this corpus. How recall latency behaves when hubs exceed the cap needs a different graph, built for
that, on a machine that can be held steady.

**Everything else behaved as it does on the laptop.** That is the point of running it here: numbers
from the laptop were never evidence, and now the same tests have passed somewhere steady.

### Cleanup

The controller in `deploy/gpu/controller` collects the results, runs the wipe (`wipe.sh`),
terminates the instance and confirms it is gone from the inventory, as in Run 1. The eight-hour
provider limit is the fallback. The controller's log records the times when the run ends.
