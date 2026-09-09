<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Disposable GPU qualification

Issue [#138](https://github.com/althunibat/taisce/issues/138) tracks the complete lifecycle:
create an eight-A100 80-GB VM, deploy this repository and its models with Compose, exercise the
system, collect sanitized evidence, wipe the run and terminate the VM. A valid result includes
termination verification even when a model or test fails.

`compose.gpu.yaml` overrides `compose.yaml`. It runs PostgreSQL, bootstrap, API, workers and two
vLLM services on the node. Integration fixtures use a second PostgreSQL cluster because they
change cluster-wide roles; the deployed API and worker retain their own credentials. The generation service uses the pinned BF16 Qwen3.6-35B-A3B revision
for extraction and reports, on four cards with tensor parallelism. The pinned BF16
Qwen3-Embedding-4B revision uses one card. The remaining cards are available for subsequent
workload experiments; merely renting eight GPUs is not evidence of eight-GPU utilization.
The vLLM image version is fixed and its resolved digest is captured in the results.

Only the API is published, on loopback. Model requests use Docker's service names and an explicit
allowlist. Native vLLM TLS uses a one-day test CA; clients verify its certificate rather than
bypassing the HTTPS requirement. The CA signing key is removed after issuing the server certificate,
and TLS private material is excluded from image builds and Git. No account-level provider keys, existing database passwords, private Git history or
unrelated local files belong in the source archive. `run.sh` creates fresh local test credentials.
The GPU.ai control-plane key and an ephemeral SSH private key remain on the operator machine.

The lifecycle controller must request an exact offering with `max_price_per_hour` and
`auto_terminate_hours`, retain its operation/instance IDs, and reconcile ambiguous responses using
the unique run name. The provider runtime limit is a fallback if the operator machine disappears;
normal termination happens immediately after collecting results. A remote wipe watchdog is also
scheduled ahead of the provider deadline, so losing the local controller does not intentionally leave
test data until termination. Never delete unrelated instances
or reuse an existing application environment for this run.

On a prepared disposable node, install the source snapshot at `/root/taisce-qualification`, copy
`deploy/gpu/wipe.sh` to `/root/taisce-qualification/wipe.sh`, then run:

```sh
bash deploy/gpu/run.sh
```

The runner verifies eight visible A100s and Docker, pulls the model/test images, waits for both
models and PostgreSQL, and starts the API and worker. The deployed journey checks observation,
actual asynchronous formation, recall, erasure and empty post-erasure recall. The test container
runs the complete Go regression suite, coverage gate, vet, live inference corpus, persisted
embedding/passage provider journeys and targeted race checks. Independent gates retain their
individual exit statuses. Ordinary regression/race gates clear live inference variables so that
fixtures retain their own endpoints and allowlists. A failed gate is reported, never silently converted to a pass.

Collect `results/` and the runner status before cleanup. Redact credentials from any diagnostic
logs; never copy `.env` or bootstrap logs. Run the root `wipe.sh` even after failure, then request
GPU.ai termination and verify both terminal status and absence from the active instance list.
Remove the run's registered SSH key and local private key after termination. Record failed wipe or
termination steps explicitly. Removing volumes and files is a logical wipe, not a claim of forensic
overwrite of provider-managed storage.

The default run is functional qualification. Throughput, latency, corpus size and failover targets
require separately specified workloads and acceptance limits; a passing GPU run does not establish
those claims.
