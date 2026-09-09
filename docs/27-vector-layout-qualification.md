<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Vector-layout and full-GPU qualification — 2026-09-10

Tracking: [#101](https://github.com/althunibat/taisce/issues/101) and
[#140](https://github.com/althunibat/taisce/issues/140). Application revision: `5c9edec`.

This run compared the production generation-partitioned vector layout with an ordinary shared table
and a bounded project-hash alternative. It also qualified the final eight-A100 Compose topology. The
test commands ran inside the disposable VM's Compose `test` container against the PostgreSQL and
vLLM services on that VM. The operator laptop handled only provisioning, evidence collection and
teardown.

## Fixed database workload

`vector-layout-v1` used 2,560-dimensional vectors across 128 projects and 13,824 rows: 112 projects
with 16 rows, 15 with 256 rows, and one with 8,192 rows. Twenty-four queries compared exact cosine
truth with filtered HNSW candidates at bounds 32, 64 and 128. The run captured provisioning and load
time, WAL, relation bytes, query latency and plans, generation removal, filtered deletion/vacuum, and
catalog/planning growth at 1, 100, 500 and 1,000 generations.

| Layout | Partitions / relations | Initial bytes | Provision / load | Exact p50 / p95 / p99 |
|---|---:|---:|---:|---:|
| Ordinary shared table | 0 / 3 | 286,965,760 | 0.006 / 211.065 s | 4.998 / 152.517 / 156.508 ms |
| List by immutable generation | 128 / 386 | 296,280,064 | 5.760 / 137.026 s | 5.175 / 151.930 / 152.394 ms |
| Hash by project, 32 children | 32 / 98 | 289,333,248 | 1.333 / 152.797 s | 5.341 / 168.694 / 171.466 ms |

Load WAL was similar: 253.30 MB for ordinary, 252.66 MB for generation-list and 254.22 MB for hash.
Generation-list added about 9.3 MB over the ordinary layout for this corpus. At 1,000 empty
generation partitions, the catalog fixture contained 3,002 relations, used 32,768,000 bytes, took
43.362 seconds and 30,906,472 WAL bytes to provision cumulatively, and planned the pruned query in
4.089 ms at p95. Every catalog query planned one leaf relation.

## Filtered retrieval

| Layout / candidates | Recall@10 vs exact | p50 / p95 / p99 | Representative plan |
|---|---:|---:|---|
| Ordinary / 32 | 1.0000 | 17.287 / 564.094 / 565.461 ms | No HNSW index; shared table scan |
| Generation / 32 | 0.8458 | 5.773 / 10.625 / 10.794 ms | `generation_p_0127_ann`, one child |
| Generation / 64 | 0.8458 | 4.724 / 10.799 / 10.858 ms | Pruned child HNSW |
| Generation / 128 | 1.0000 | 11.480 / 343.477 / 343.650 ms | Pruned child HNSW |
| Hash / 32 | 0.7958 | 6.273 / 11.162 / 11.227 ms | `hash_p_13_ann`, one child |
| Hash / 128 | 1.0000 | 12.014 / 359.591 / 361.121 ms | Pruned child HNSW |

The ordinary parent/shared HNSW was not selected for the production-shaped filtered query and its
representative execution took 402.074 ms. The generation child index was selected and took 6.063 ms.
Candidate bounds 32 and 64 lost exact top-ten results in both partitioned layouts. A bound of 128
restored full recall in this fixture but also exposed a large tail. These results do not justify a
universal candidate bound or latency target. Exact search remains the default; ANN remains explicit,
and every future tuning run must continue comparing it with exact truth.

The measured 1,152-report quality fixture also favored exact search at its small corpus size: exact
p95 was 45.562 ms, while ANN p95 was 96.940–105.390 ms despite 1.0 recall@10 at all three candidate
bounds. Small generations therefore receive no recommendation to opt into ANN merely because an
index exists.

## Lifecycle and layout decision

Removing one generation by detaching and dropping its child took 0.126 seconds, wrote 8,000 WAL
bytes, reclaimed about 170.1 MB immediately and required no vacuum. The ordinary and hash layouts
needed filtered deletion followed by vacuum: ordinary took 0.187 plus 113.977 seconds and wrote about
21.0 MB WAL; hash took 0.160 plus 31.536 seconds and wrote about 51.5 MB WAL.

Keep list partitioning by immutable embedding generation with one directly owned, dimension-specific
HNSW index on each generation child. A partitioned-parent index is virtual; no HNSW index is attached
to the vector parent. Project is the logical authorization and query boundary, while generation is
the physical vector boundary. This preserves exact model/revision/endpoint/dimension identity,
atomic activation and cheap retirement without multiplying vector storage by a separate project-hash
scheme. The direct catalog assertion in `embeddingpartition_test.go` protects that ownership model.

This decision does not reintroduce a vector column or HNSW index on the project-partitioned `chunk`
table. Tiny projects in the qualification corpus showed no reason to choose ANN, and they do not get
an extra project-specific vector partition. Their immutable generation child still carries its
small HNSW relation because all three semantic stores use one predictable cutover and repair
contract. At 1,000 generations that catalog cost remained 32.8 MB and p95 planning remained 4.089 ms;
adding a size-triggered index-build state machine would add publication races and a second search
contract without a measured operational benefit. Revisit that choice only when #103 measures a
material catalog, write or maintenance bottleneck at the intended production distribution.

Model and dimension mismatch is refused before vector publication. Supported changes create a new
finite generation, build and validate it under its immutable identity, activate it atomically, then
cancel and prune the old generation. The database tests cover incompatible retries, incomplete
activation, concurrent cutover, source change/erasure during provider work, repair and exact child
catalog ownership.

## Eight-A100 topology and generation benchmark

The final Compose topology used seven independent `Qwen/Qwen3.8-27B` BF16 vLLM replicas pinned
one-to-one to GPUs 0–6, a TLS-verifying least-connections nginx endpoint, seven Taisce workers, and
`Qwen/Qwen3-Embedding-4B` on GPU 7. Qwen3.8-27B was pinned to revision
`1d4bf0f2ff6012fd82039f2fa52739d0dd7c60c0`; its vLLM image digest was
`sha256:fc120ece0a388cc0aa1caad4a9f1cd92113484ab7ec2fd0efadd62585be05bf8`. The embedding revision
remained `5cf2132abc99cad020ac570b19d031efec650f2b` in the pinned vLLM 0.28.0 image digest
`sha256:61fc8a896b0a4fbbbdc063bc4b0dbc25ce98e02b5050c24aeb7830ac02039b14`.

The controlled generation benchmark sent 140 JSON-schema requests at concurrency 28 through the
shared endpoint. All 140 returned valid structured output in 5.342 seconds: 26.205 requests/s and
655.137 completion tokens/s. Latency was 1.025/1.212/1.222 seconds at p50/p95/p99, with a 1.226-second
maximum. Every generation GPU reached 100% observed utilization. Generation replicas held about
75,725 MiB each; the embedding GPU held 40,299 MiB and reached 41% utilization. Per-replica logs
recorded 31–34 total chat-completion requests across the complete qualification, proving traffic
reached every generation replica without claiming perfect balance for all workloads.

`Qwen/Qwen3.8-Flash-Next` was evaluated and rejected for this topology. Its official serving recipe
does not support pipeline parallelism for the n-gram embedding layer, so it cannot occupy the seven
generation GPUs while leaving GPU 7 dedicated to embeddings. Seven independent 27B replicas provide
a supported topology and useful request concurrency.

## Gates, evidence and teardown

The deployed observation → formation → recall journey passed, as did erasure with zero residual and
an empty post-erasure recall. All nine runner gates passed: regression, coverage, vet, live inference,
embedding-provider, report-quality, vector-layout, passage-provider and targeted race checks. Coverage
was 91.6%, at the unchanged floor. At completion the stack had one API, seven workers, seven generation
replicas behind one proxy, one embedding service, and separate deployed and test PostgreSQL services.
The test database is required because integration fixtures alter cluster-wide roles; it is isolated
from deployed application state.

Sanitized evidence is stored locally under
`/private/tmp/taisce-review/gpu-vector-layout/taisce-101-layout-254040e752/evidence/`. Its checksum
manifest covers 61 files and has SHA-256
`a87432defc54eae12530a75c69ff1acfcb966ef992f0d1b40846283ce5d6f777`; the secret scan found zero
hits. The remote logical wipe returned zero and removed the Compose resources, volumes, images,
source and generated credentials. Instance `gpu-9fb87f87` was terminated and independently absent
from paginated active inventory; the ephemeral provider SSH key and local private key were absent.
The account-level provider credential remains only in the operator environment.

Slow or unhealthy provisioning attempts are not retained for debugging on a paid node. The controller
terminates them after the bounded readiness deadline, verifies absence, and starts a fresh instance.
Logical deletion does not claim forensic overwrite of provider-managed storage.
