<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Disposable GPU qualification

The complete lifecycle:
create an eight-A100 80-GB VM, deploy this repository and its models with Compose, exercise the
system, collect sanitized evidence, wipe the run and terminate the VM. A valid result includes
termination verification even when a model or test fails.

`compose.gpu.yaml` overrides `compose.yaml`. It runs PostgreSQL, bootstrap, API, workers and two
vLLM services on the node. Integration fixtures use a second PostgreSQL cluster because they
change cluster-wide roles; the deployed API and worker retain their own credentials. Seven
independent servers run the pinned BF16 Qwen3.8-27B revision, one on each of GPUs 0 through 6. A
TLS-verifying nginx service distributes generation requests by least active connections and gives
Taisce one stable internal endpoint. The pinned BF16 Qwen3-Embedding-4B revision uses GPU 7.
Qwen3.8-27B needs 51.1 GiB for weights and fits one complete 32K-context replica on an A100 80 GB;
replication therefore uses every generation card without imposing a tensor or pipeline topology on
the model. The deployed application uses seven worker replicas so independent scopes can keep all
seven generation replicas busy; the database advisory lock still serializes formation within one
scope. Allocation alone is not evidence of sustained utilization or a scaling result; the
captured per-card measurements describe the exercised workload.
Each generation server selects the Guidance structured-output backend. The live structured-output
gates and bounded concurrent benchmark qualify this compatibility choice. Generation and embedding
image digests are fixed or captured in the results.

The qualification test PostgreSQL container is constrained to the documented eight-vCPU, 32-GiB
database-host starting specification. The runner samples container CPU, memory and block I/O while
gates execute. Gate timestamp files make workload-specific peaks distinguishable from model startup
and the ordinary regression suite.

Only the API is published, on loopback. Model requests use Docker's service names and an explicit
allowlist. Native vLLM TLS uses a one-day test CA; clients verify its certificate rather than
bypassing the HTTPS requirement. The CA signing key is removed after issuing the server certificate,
and TLS private material is excluded from image builds and Git. No account-level provider keys, existing database passwords, private Git history or
unrelated local files belong in the source archive. `run.sh` creates fresh local test credentials.
The GPU.ai control-plane key and an ephemeral SSH private key remain on the operator machine.

The lifecycle controller is `deploy/gpu/controller` (`guard.py`, `deploy.py`, `attempts.py`,
`phases.sh`; its README has the one command). It requests an exact offering with
`max_price_per_hour` and `auto_terminate_hours`, retains its operation/instance IDs, and reconciles
ambiguous responses using the unique run name. The provider runtime limit is a fallback if the operator machine disappears;
normal termination happens immediately after collecting results. A remote wipe watchdog is also
scheduled ahead of the provider deadline, so losing the local controller does not intentionally leave
test data until termination. Never delete unrelated instances
or reuse an existing application environment for this run.

On a prepared disposable node, install the source snapshot at `/root/taisce-qualification`, then run:

```sh
bash deploy/gpu/run.sh
```

The runner first installs its root-level cleanup entry point, then verifies eight visible A100s and
Docker, pulls the model/test images, waits for both
models and PostgreSQL, and starts the API and worker. The deployed journey checks observation,
actual asynchronous formation, recall, erasure and empty post-erasure recall. The test container
runs the complete Go regression suite, coverage gate, vet, live inference corpus, persisted
embedding/passage provider journeys and targeted race checks. Independent gates retain their
individual exit statuses. Ordinary regression/race gates clear live inference variables so that
fixtures retain their own endpoints and allowlists. A failed gate is reported, never silently converted to a pass.
The generation benchmark sends 140 schema-constrained requests at concurrency 28 through the shared
endpoint and records request rate, token throughput and latency percentiles. Per-GPU telemetry and
per-replica logs show whether the bounded workload reached every generation card.

Database layout runs add a separately named qualification gate. `vector-layout-v1` compares a shared
ordinary table, generation-list partitions and a bounded 32-way project hash across a fixed mix of
small, medium and large projects at the production 2,560-dimensional representation. It measures
exact and ANN recall, query plans, catalog/planning cost, build time, WAL, disk and erasure/vacuum
behavior. It records observations without creating a target after seeing them.

`anchor-layout-v1` compares the production canonical identity lookup with the retired array/GIN
shape and a normalized project-keyed relation. Its fixed corpus includes small, medium and large
projects, cross-project noise, 64 raw spelling variants and 128-term misses. It records warm latency,
plans, scanned rows, buffers, layout build time, WAL and storage. Raw spelling variants remain
source-owned evidence and semantic input; exact anchoring follows their one normalized canonical key.

Collect `results/` and the runner status before cleanup. Redact credentials from any diagnostic
logs; never copy `.env` or bootstrap logs. Run the root `wipe.sh` even after failure, then request
GPU.ai termination and verify both terminal status and absence from the active instance list.
Remove the run's registered SSH key and local private key after termination. Record failed wipe or
termination steps explicitly. Removing volumes and files is a logical wipe, not a claim of forensic
overwrite of provider-managed storage.

The default run is functional qualification. Throughput, latency, corpus size and failover targets
require separately specified workloads and acceptance limits; a passing GPU run does not establish
those claims.

## Sizing by the cards present

`compose.gpu.yaml` is the eight-card reference. The runner counts the A100s the node exposes and has
`deploy/gpu/topology.py` write `qualification-topology.yaml` and `qualification-generation-nginx.conf`
beside it: the load balancer depends on and routes to the first N−1 replicas, the embedding model
moves to card N−1, and only those services are started. Eight cards reproduce the reference exactly;
fewer narrow it; fewer than two is refused. Both generated files are ignored like the TLS material.
Card memory is read as well: the generation model's BF16 weights are 51.1 GiB, so on cards under
60 GB each replica spans two cards at tensor-parallel size two, the replica count halves, and the
replica command is regenerated from the reference arguments with only the parallel size changed.
Formation drains one project per worker while a replica batches several sequences, so coverage mode
runs `TAISCE_GPU_WORKERS_PER_REPLICA` (default 4) workers per replica and deals the corpus over that
many projects; qualification mode keeps one per replica, which is the arrangement D116 measured.

## Vocabulary coverage over a document corpus

`TAISCE_GPU_MODE=coverage bash deploy/gpu/run.sh` deploys the same stack, runs the smoke journey,
then measures the relation vocabulary against a corpus the operator placed at `/root/corpus`
(issue #43) and returns without the regression gates. `deploy/gpu/vocabulary-coverage.py` deals the
documents across one project per worker, because formation holds one advisory lock per project; observes each document once under the `tool` role with a deterministic retry key and, by
default, no data subject, because a claim from a non-user message is refused by role whenever the
observation names one and a corpus observed under a subject yields refusals only;
waits for every project's watermark; and reads admitted facts against every refusal reason from the
database, with unmapped refusals broken down by the relation the model tried to use. It writes
`results/vocabulary-coverage.json` and `.md`. AP-format JSON (`headline`, `body_nitf`,
`firstcreated`) and plain text files are both read.

The corpus is the operator's to license and never enters the repository, the image or the results.
It is uploaded beside the source and removed by `wipe.sh` with everything else. Claims refused by
role are reported by formation and not persisted, so they are absent from the denominator; a number
from this mode is a coverage measurement of one extractor on one corpus, not a retrieval quality
result.
