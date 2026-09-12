<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Vector layout and full-GPU qualification — 2026-09-10

This run compared three ways to store vectors in PostgreSQL, and tested the final eight-A100 Docker
Compose setup with real models.

## What this means for you

- **Vectors are stored one partition per embedding generation.** That layout used its HNSW index
  for filtered queries, where a single shared table did not, and it drops an old generation almost
  for free: 0.126 seconds, 8,000 bytes of WAL, no vacuum.
- **Exact search is the default, and should stay yours unless you measure otherwise.** Lower
  candidate bounds (32 and 64) missed some of the exact top ten. A bound of 128 found them all but
  had a long tail (p95 343.477 ms).
- **Many generations are cheap.** 1,000 empty generation partitions took 32.8 MB of catalog and
  4.089 ms p95 planning time.
- **Seven single-GPU model replicas worked well for formation.** 140 structured requests at
  concurrency 28 all came back valid, at 26.205 requests a second.

All test commands ran inside the VM's Compose `test` container, against the PostgreSQL and vLLM
services on that VM. The laptop only started the machine, collected the evidence and tore it down.

## The database workload

`vector-layout-v1` used 2,560-dimension vectors across 128 projects and 13,824 rows: 112 projects
with 16 rows, 15 with 256 rows, and one with 8,192 rows. Twenty-four queries compared exact cosine
results with filtered HNSW results at candidate bounds 32, 64 and 128. The run recorded setup and
load time, WAL, table sizes, query latency and plans, dropping a generation, filtered delete and
vacuum, and catalog and planning growth at 1, 100, 500 and 1,000 generations.

| Layout | Partitions / relations | Initial bytes | Setup / load | Exact p50 / p95 / p99 |
|---|---:|---:|---:|---:|
| Ordinary shared table | 0 / 3 | 286,965,760 | 0.006 / 211.065 s | 4.998 / 152.517 / 156.508 ms |
| List by immutable generation | 128 / 386 | 296,280,064 | 5.760 / 137.026 s | 5.175 / 151.930 / 152.394 ms |
| Hash by project, 32 children | 32 / 98 | 289,333,248 | 1.333 / 152.797 s | 5.341 / 168.694 / 171.466 ms |

Load WAL was about the same: 253.30 MB for the shared table, 252.66 MB for generation partitions and
254.22 MB for hash partitions. Generation partitions used about 9.3 MB more than the shared table for
this corpus. With 1,000 empty generation partitions, the catalog held 3,002 relations, used
32,768,000 bytes, took 43.362 seconds and 30,906,472 bytes of WAL to create in total, and planned the
pruned query in 4.089 ms at p95. Every catalog query planned against one leaf partition.

## Filtered search

| Layout / candidates | Recall@10 vs exact | p50 / p95 / p99 | Representative plan |
|---|---:|---:|---|
| Ordinary / 32 | 1.0000 | 17.287 / 564.094 / 565.461 ms | No HNSW index; shared table scan |
| Generation / 32 | 0.8458 | 5.773 / 10.625 / 10.794 ms | `generation_p_0127_ann`, one child |
| Generation / 64 | 0.8458 | 4.724 / 10.799 / 10.858 ms | Pruned child HNSW |
| Generation / 128 | 1.0000 | 11.480 / 343.477 / 343.650 ms | Pruned child HNSW |
| Hash / 32 | 0.7958 | 6.273 / 11.162 / 11.227 ms | `hash_p_13_ann`, one child |
| Hash / 128 | 1.0000 | 12.014 / 359.591 / 361.121 ms | Pruned child HNSW |

The planner did not pick the shared table's HNSW index for the production-shaped filtered query; a
representative run took 402.074 ms. It did pick the generation partition's index, and that run took
6.063 ms. Bounds of 32 and 64 lost some of the exact top ten in both partitioned layouts. A bound of
128 found them all on this data but showed a long tail. None of this supports one candidate bound
or one latency target for everyone. Exact search stays the default, approximate search stays
opt-in, and every tuning run must keep comparing it against exact results.

The 1,152-report quality corpus also favoured exact search at its small size: exact p95 was
45.562 ms, while approximate p95 was 96.940–105.390 ms, even with 1.0 recall@10 at all three bounds.
Having an index is not, on its own, a reason to use approximate search on a small generation.

## Dropping a generation, and the layout chosen

Dropping one generation by detaching and dropping its partition took 0.126 seconds, wrote 8,000
bytes of WAL, freed about 170.1 MB at once and needed no vacuum. The other two layouts needed a
filtered delete and then a vacuum. The shared table took 0.187 plus 113.977 seconds and wrote about
21.0 MB of WAL. The hash layout took 0.160 plus 31.536 seconds and wrote about 51.5 MB of WAL.

So Taisce partitions vectors by embedding generation, with one HNSW index on each generation's
partition, built for that generation's dimension. An index on a partitioned parent is only virtual,
so the parent has none. The project is the boundary for access and queries; the generation is the
physical boundary for vectors. This keeps each generation's model, revision, endpoint and dimension
fixed, makes switching generations atomic, and makes retiring one cheap, without also splitting
storage by project hash. A catalog test, `embeddingpartition_test.go` in `internal/infra/pg`,
checks that each partition owns its own index.

The project-partitioned `chunk` table still has no vector column and no HNSW index. Small projects
in the test gave no reason to use approximate search, so they do not get a partition of their own.
Their generation partition still carries a small HNSW index, because all three kinds of embedding
share one predictable switch-over and repair process. At 1,000 generations that costs 32.8 MB of
catalog and 4.089 ms p95 planning. Building indexes only above some size would add race conditions
at publish time and a second search path, with no measured benefit. That choice should be revisited
only if a measurement at a real production distribution shows a catalog, write or maintenance
bottleneck. That measurement has not been done.

A model or dimension mismatch is refused before any vector is saved. To change models, you create a
new generation, build and check it, activate it in one step, then cancel and prune the old one. The
database tests cover retries that do not match, activating before a build is complete, switching
generations concurrently, a source changing or being erased while the model is working, repair, and
each partition owning its own index.

## Eight-A100 setup and generation benchmark

The final Compose setup ran:

- seven separate `Qwen/Qwen3.8-27B` BF16 vLLM replicas, one per GPU (0–6), at revision
  `1d4bf0f2ff6012fd82039f2fa52739d0dd7c60c0`, image digest
  `sha256:fc120ece0a388cc0aa1caad4a9f1cd92113484ab7ec2fd0efadd62585be05bf8`;
- an nginx endpoint in front of them, verifying TLS and sending each request to the least busy
  replica;
- seven Taisce workers;
- `Qwen/Qwen3-Embedding-4B` on GPU 7, at revision `5cf2132abc99cad020ac570b19d031efec650f2b`, in the
  vLLM 0.28.0 image at digest `sha256:61fc8a896b0a4fbbbdc063bc4b0dbc25ce98e02b5050c24aeb7830ac02039b14`.

The benchmark sent 140 JSON-schema requests at concurrency 28 through the shared endpoint. All 140
came back as valid structured output in 5.342 seconds: 26.205 requests a second and 655.137
completion tokens a second. Latency was 1.025 / 1.212 / 1.222 seconds at p50 / p95 / p99, with a
maximum of 1.226 seconds. Every generation GPU reached 100% utilisation. Each generation replica held
about 75,725 MiB; the embedding GPU held 40,299 MiB and peaked at 41% utilisation. Each replica's log
showed 31–34 chat-completion requests across the whole run, so traffic reached every replica. That
does not mean the balance is perfect for every workload.

`Qwen/Qwen3.8-Flash-Next` was tried and rejected for this setup. Its official serving recipe does not
support pipeline parallelism for its n-gram embedding layer, so it cannot use the seven generation
GPUs while leaving GPU 7 for embeddings. Seven separate 27B replicas are a supported setup with good
request concurrency.

## Gates, evidence and teardown

The deployed observe, form and recall journey passed, as did erasure with zero residual and an empty
recall afterwards. All nine gates passed: regression, coverage, vet, live inference, embedding
provider, report quality, vector layout, passage provider and targeted race checks. Coverage was
91.6%, at the floor. At the end the stack had one API, seven workers, seven generation replicas behind
one proxy, one embedding service, and separate deployed and test PostgreSQL services. The test
database exists because integration tests change cluster-wide roles, and it is kept apart from the
deployed data.

Evidence with secrets removed was kept on the operator's machine. Its checksum manifest covers 61
files and has SHA-256 `a87432defc54eae12530a75c69ff1acfcb966ef992f0d1b40846283ce5d6f777`. The secret
scan found nothing. The remote wipe returned zero and removed the Compose resources, volumes, images,
source and generated credentials. The instance was terminated and confirmed gone from the active
inventory. The provider SSH key and the local private key were removed. The provider account
credential stays only on the operator's machine.

A machine that is slow or unhealthy during setup is not kept around for debugging at an hourly rate.
The controller terminates it after its readiness deadline, confirms it is gone, and starts a fresh
one. This is logical deletion. It does not claim a forensic overwrite of the provider's storage.
