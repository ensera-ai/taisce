<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Community-report quality — 2026-09-09

This run measured how well report search finds the right theme, and whether approximate (HNSW)
search returns the same results as exact search, on a fresh rented GPU machine.

## What this means for you

- **Report search found the right theme every time on this corpus.** Exact search ranked the
  correct topic first for all 24 questions.
- **Approximate search matched exact search completely**, at every candidate bound tested.
- **On a corpus this small, exact search is faster.** Exact p95 was 47.234 ms; approximate p95 was
  98.489 to 101.188 ms. Keep the default (exact) unless you have measured a larger corpus where
  approximate search wins.
- **These are warm, one-at-a-time queries on one machine.** They say nothing about concurrent load,
  other languages or larger corpora.

## How it was run

The test command ran inside the VM's Docker Compose `test` container, against the PostgreSQL and
vLLM services on the same VM. The laptop only started the machine, collected the evidence and tore
it down.

The corpus, `report-themes-v1`, has 1,152 reports across 24 themes: 48 variants per theme and one
plain-language question per theme. The run used the production code for building report input, the
embedding client, the generation lifecycle, vector storage, project filters, the exact cosine query
and the optional HNSW query. Build pages held 64 reports. Search used one client, one query at a
time, after one warm-up query.

Models and runtime:

- Embeddings: `Qwen/Qwen3-Embedding-4B`, BF16, revision `5cf2132abc99cad020ac570b19d031efec650f2b`,
  native 2,560-dimension output.
- The full application stack also ran `Qwen/Qwen3.6-35B-A3B`, BF16, revision
  `995ad96eacd98c81ed38be0c5b274b04031597b0`, across four GPUs.
- Both used `vllm/vllm-openai:v0.28.0` at digest
  `sha256:61fc8a896b0a4fbbbdc063bc4b0dbc25ce98e02b5050c24aeb7830ac02039b14`.

Hardware: eight A100-SXM4-80GB GPUs in `us-east` at $10 an hour for the whole machine, with a
four-hour provider auto-terminate. Generation used GPUs 0–3 and embeddings used GPU 4; three GPUs
were left idle on purpose. The host reported Docker 29.1.5, Compose 5.0.1, driver 580.126.09, about
1.7 TiB RAM and 8.7 TiB disk.

## Results

| Measurement | Result |
|---|---:|
| Reports / questions | 1,152 / 24 |
| Build and activation | 13.325 s |
| Exact topic recall@1 | **1.0 (24/24)** |
| Exact latency p50 / p95 / p99 | 45.492 / 47.234 / 52.153 ms |
| ANN recall@10, 32 candidates | **1.0** |
| ANN latency p50 / p95 / p99, 32 candidates | 95.857 / 98.489 / 98.503 ms |
| ANN recall@10, 64 candidates | **1.0** |
| ANN latency p50 / p95 / p99, 64 candidates | 97.114 / 99.765 / 99.980 ms |
| ANN recall@10, 128 candidates | **1.0** |
| ANN latency p50 / p95 / p99, 128 candidates | 99.645 / 101.188 / 102.018 ms |

ANN recall is the share of the exact top ten that the filtered approximate query also returned. All
720 measured result positions matched. Because approximate search was slower here, this shows the
index returns the right results, not that you should turn it on for small generations. Choosing the
index stays an explicit choice by the caller.

The deployed API and worker smoke test passed, then all eight gates:

| Gate | Result |
|---|---|
| Full uncached Go and PostgreSQL regression | Pass |
| Cross-package coverage | **91.6%, at the 91.6% floor** |
| `go vet ./...` | Pass |
| Full live inference suite | Pass |
| Stored embedding provider journey | Pass |
| Report search and ANN qualification | Pass |
| Authenticated passage provider journey | Pass |
| Targeted HTTP and PostgreSQL race checks | Pass |

The corpus is balanced, in English, and made of aggregate reports, all on one host. The run does not
establish concurrent throughput, saturation, quality in other languages, scaling to larger corpora,
or a production latency target.

## Evidence and teardown

Logs with secrets removed, coverage, service and hardware snapshots, model image identity and
checksums were copied to the operator's machine. The checksum manifest covers 27 files. A scan for
secrets passed before teardown. The evidence holds no application environment, provider credential
or private key.

The remote wipe returned zero. It removed the Compose project's containers, network, database, model
and cache volumes, the images the run pulled, the source directory and the generated credentials.
Termination was requested at 17:01:49 UTC and confirmed at 17:02:03 UTC. The instance was terminated
and gone from the active inventory, and a separate fresh query then showed zero active instances. The
registered SSH key and the local private key were deleted. This is logical deletion. It does not
claim a forensic overwrite of the provider's storage.

The first machine requested never became reachable over SSH within its deadline. No source,
credentials, models or tests reached it. It was terminated and confirmed gone before the measured
run began.
