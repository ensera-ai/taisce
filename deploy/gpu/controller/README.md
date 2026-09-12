<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# The operator-side lifecycle controller

The node side of a disposable
GPU.ai qualification is `deploy/gpu` (`run.sh`, `tests.sh`, `topology.py`, `headtohead.sh`,
`wipe.sh`); this directory is the operator side, which used to be rewritten per run.

Three scripts, standard library only:

- `guard.py` owns one instance for the life of the process: an ephemeral SSH key, the cheapest
  eligible VM under `--max-price` with a provider-side `--hours` guard, and on any exit the remote
  wipe, termination and verified absence from active inventory. A pool that errored during boot
  this run is tried again only when nothing else is eligible.
- `deploy.py` waits for the guard's `ready.json`, uploads `git archive` of `--commit` (and a corpus
  and the .NET adapter repository when given), arms a remote wipe watchdog, runs `phases.sh`
  detached on the node, polls until it is done, and collects every results directory.
- `attempts.py` runs the two together and retries on a bad node, a bounded number of times.

The provider key is read from `GPUAI_API_KEY` and reaches the provider only. No credential, no
source outside the named commits and no corpus content is written under the results.

`--fetch-corpus` lets the node download the benchmark corpus instead of the operator uploading one.
It is off by default and it is an acceptance, not a convenience: the AP News set ships under a licence
Microsoft grants for research, so the node fetches one pinned commit, checks the archive's digest
before unpacking anything, and records the source, the digest and the article count under
`results/corpus-provenance.txt`. The articles themselves are never written under results and never
collected — the licence is the operator's to accept, not ours to redistribute. Without either an
upload or this flag, the extraction-coverage phase skips rather than fails.

```sh
export GPUAI_API_KEY=…
python3 deploy/gpu/controller/attempts.py --attempts 4 --workdir /path/outside/the/repo --gpus 8 --max-price 10 --hours 8 -- \
  --repo . --commit "$(git rev-parse HEAD)" --corpus /path/to/corpus \
  --dotnet-repo ../taisce-dotnet --dotnet-commit "$(git -C ../taisce-dotnet rev-parse HEAD)"
```

`phases.sh` runs the qualification gates, the head-to-head, the corpus measurement and the read-path
measurement in turn, each into its own results directory.

The read-path phase (`deploy/gpu/readpath-scale.py`, #220) is last and writes a synthetic graph in a
project of its own, at the sizes the scaling claim names, with hubs deliberately past the traversal's
fanout cap. It records recall latency percentiles and throughput per size and per concurrency, the
anchor query's plan at each size, the degree distribution either side of the cap, and the connections
and refusals the deployment produced while it ran. Those numbers describe the read path against a
graph of a given shape and say nothing about extraction or answer quality — the corpus measurement is
what speaks for extraction, and answer quality is a separate harness that does not exist yet. `python3 guard.py --check` and `python3 deploy.py --check …`
validate arguments and, for the guard, the key against the provider's pricing endpoint, without
creating anything.

## A demo node

`demo.py` is the same lifecycle at the size of a demo: one GPU in a named region family (EU by
default), serving the extraction model to this machine rather than running the stack. It launches the
provider's base container, exposes no port, installs a pinned vLLM over SSH and starts it with the
qualified arguments — thinking off — bound to the container's loopback, then holds an SSH tunnel on
`127.0.0.1:18000`. The qualified image itself cannot run there: container capacity refuses an image
that defines an ENTRYPOINT (`docs/36-extraction-models.md`). Before it reports ready it proves, through the tunnel,
that the model refuses a request without the per-run key, that the qualified model answers, and that
it answers without a reasoning trace. `down`, an interrupt or the runtime deadline terminates the node
and verifies it is gone. `--model` picks Qwen3.8-27B (`3.8`, the measured one) or Qwen3.6-35B-A3B
(`3.6`, not yet run on this path), and `deploy/inference/demo-qwen3.8.env` and `demo-qwen3.6.env` are
the profiles that use them.

```sh
export GPUAI_API_KEY=…
python3 deploy/gpu/controller/demo.py up --workdir ~/.taisce-demo --check   # eligible offerings, nothing created
python3 deploy/gpu/controller/demo.py up --workdir ~/.taisce-demo
python3 deploy/gpu/controller/demo.py down --workdir ~/.taisce-demo
```

Proved by `deploy/gpu/controller_test.go`: the scripts compile, refuse bad arguments (no key, a
price of zero, a commit that is not a hash, a corpus that is not a directory) and accept good ones,
without a provider call.
