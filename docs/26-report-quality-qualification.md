<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Community-report quality qualification — 2026-09-09

Tracking: [#19](https://github.com/althunibat/taisce/issues/19). Application revision:
`c03efddde0d021607d9beb04083dcc53a92df597`.

This run measured community-report semantic retrieval and project/generation-filtered HNSW fidelity
on a fresh disposable GPU.ai VM. The test command ran inside the VM's Docker Compose `test`
container against the Compose PostgreSQL and vLLM services on that VM. The laptop acted only as the
lifecycle controller and evidence destination.

## Fixed measurement plan

`report-themes-v1` contains 1,152 reports across 24 themes, with 48 variants per theme and one
natural-language question per theme. It uses the production report input builder, embedding client,
generation lifecycle, vector storage, project filters, exact cosine query and optional HNSW candidate
query. Build pages contain 64 reports. Search uses one sequential client after one warm-up query.

The embedding service was `Qwen/Qwen3-Embedding-4B`, BF16, pinned to revision
`5cf2132abc99cad020ac570b19d031efec650f2b`, with native 2,560-dimensional output. The full
application profile also deployed `Qwen/Qwen3.6-35B-A3B`, BF16, pinned to revision
`995ad96eacd98c81ed38be0c5b274b04031597b0` across four GPUs. Both used
`vllm/vllm-openai:v0.28.0` at digest
`sha256:61fc8a896b0a4fbbbdc063bc4b0dbc25ce98e02b5050c24aeb7830ac02039b14`.

The provider supplied eight A100-SXM4-80GB GPUs in `us-east` at the selected whole-instance quote of
$10/hour, with a four-hour provider auto-termination guard. Generation used GPUs 0–3 and embedding
used GPU 4; three GPUs were intentionally unused. The host reported Docker 29.1.5, Compose 5.0.1,
driver 580.126.09, approximately 1.7 TiB RAM and 8.7 TiB disk.

## Results

| Measurement | Observed result |
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

ANN recall is the fraction of the exact cosine top ten returned by the filtered approximate query.
All 720 measured ANN result positions matched exact truth. At this corpus size, approximate queries
were slower than exact queries, so this evidence supports retrieval fidelity but does not justify
selecting ANN for small generations. Index selection remains an explicit caller choice.

The deployed API/worker smoke test passed, followed by all eight runner gates:

| Gate | Result |
|---|---|
| Complete uncached Go/PostgreSQL regression | Pass |
| Cross-package coverage | **91.6%, at the unchanged 91.6% floor** |
| `go vet ./...` | Pass |
| Full live inference suite | Pass |
| Persisted embedding-provider journey | Pass |
| Named report semantic/ANN qualification | Pass |
| Authenticated passage-provider journey | Pass |
| Targeted HTTP/PostgreSQL race checks | Pass |

These are warm, sequential measurements of a balanced English aggregate-report corpus on one host.
They do not establish concurrent throughput, saturation, multilingual quality, larger-corpus scaling
or a production latency objective.

## Evidence and teardown

Sanitized logs, coverage, service/hardware snapshots, model image identity and checksums were copied
to the operator host under
`/private/tmp/taisce-review/gpu-report-quality/taisce-19-quality-4690ce2192/evidence/`.
The evidence checksum manifest covers 27 files. Secret-pattern checks passed before teardown; no
application environment, provider credential or private key is part of the evidence directory.

The remote logical wipe returned zero. It removed the Compose project's containers, network, named
database/model/cache volumes, images requested by the qualification stack, source directory and
generated credentials. Termination was requested at 17:01:49 UTC and verified at 17:02:03 UTC. The
owned instance was terminal and absent from active inventory; an independent fresh paginated query
then reported zero active instances. The registered SSH key and local ephemeral private key were
deleted. Logical deletion does not claim forensic overwrite of provider-managed storage.

The first provisioning attempt never became SSH-reachable within its bounded deadline. No source,
credentials, models or tests reached it; termination and absence from active inventory were verified
before this measured attempt began.
