<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Disposable GPU qualification — 2026-09-09

Tracking: [#138](https://github.com/althunibat/taisce/issues/138). Application revision: `56ff6bd`.
The qualification files were exercised from the working tree before their own commit.

A fresh GPU.ai VM provided eight A100 80-GB cards, approximately 787 GiB of host RAM and 4.9 TiB
of disk. The selected whole-instance quote was $10/hour, enforced as the maximum rate, with a
four-hour provider runtime limit. This was a disposable functional run, not a production deployment
or a latency/throughput acceptance measurement.

## Deployed topology

Docker Compose ran the application PostgreSQL cluster, bootstrap, API, worker, a separate integration
test PostgreSQL cluster, a Go test container and two vLLM services. The separate test cluster protects
the deployed credentials from integration fixtures that deliberately change cluster-wide roles.
The API was bound to host loopback; database/model ports were not published.

| Role | Artifact | Placement |
|---|---|---|
| Extraction and reports | `Qwen/Qwen3.6-35B-A3B`, BF16, revision `995ad96eacd98c81ed38be0c5b274b04031597b0` | Tensor parallelism across four A100s |
| Embeddings | `Qwen/Qwen3-Embedding-4B`, BF16, revision `5cf2132abc99cad020ac570b19d031efec650f2b` | One A100, native 2560-dimensional output |
| Model runtime | `vllm/vllm-openai:v0.28.0` | Digest `sha256:61fc8a896b0a4fbbbdc063bc4b0dbc25ce98e02b5050c24aeb7830ac02039b14` |

Three cards were unused in this functional topology. Native vLLM TLS and a temporary CA preserve
Taisce's HTTPS requirement. Only public CA material is mounted into the non-root application
containers. The CA signing key is deleted after issuance; private TLS material and generated results
are excluded from Git and Docker build contexts. Account-level provider credentials stayed local.

## Results

The deployed API/worker journey passed observation acceptance, real-model asynchronous formation,
subject-specific recall, erasure with zero residual and empty post-erasure recall. The final runner
returned zero, with all seven gates passing:

| Gate | Result |
|---|---|
| Complete uncached Go/PostgreSQL regression | Pass |
| Cross-package coverage | **92.0%**, unchanged floor **91.6%** |
| `go vet ./...` | Pass |
| Full live inference suite | Pass |
| Persisted embedding provider journey | Pass |
| Authenticated passage provider journey | Pass |
| Targeted HTTP/PostgreSQL race checks | Pass |

The initial attempts exposed qualification configuration defects: an obsolete vLLM logging flag,
a project name outside the identifier contract, missing TLS, an optional OpenSSL serial-file
assumption that prevented public-certificate permission setup, and live model environment variables
leaking into ordinary test fixtures. Those were corrected, and the final runner passed. Initial
failure evidence was retained separately rather than overwritten or described as a pass.

Sanitized logs, coverage, service/hardware snapshots and image identity were collected locally under
`/private/tmp/taisce-review/gpu-run/evidence/`. No existing application environment or provider key
was copied to the VM. Model-quality/scaling claims beyond the exercised corpus remain in their
existing roadmap issues; OpenRouter verification is separate from this GPU run.

## Cleanup

The local lifecycle guard ran the remote logical wipe successfully (exit 0) at **10:57:46 UTC**.
The wipe removed the Compose project's containers, named volumes (including both databases and
model caches), images requested by Compose, and the source/configuration directory with its test
credentials. Its checks confirmed no project containers or volumes and no source directory remained.
This is logical deletion, not a claim of forensic overwrite of provider-managed disks.

Termination was requested at **10:57:47 UTC** and verified at **10:58:01 UTC**: the owned instance
was terminal and absent from active inventory. The run's registered SSH key and local ephemeral
private key were removed. The four-hour provider deadline was a fallback; normal cleanup completed
before it. Local sanitized evidence remains available for review.
