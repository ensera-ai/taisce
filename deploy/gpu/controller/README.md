<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# The operator-side lifecycle controller

Tracking: [#183](https://github.com/althunibat/taisce/issues/183). The node side of a disposable
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

```sh
export GPUAI_API_KEY=…
python3 deploy/gpu/controller/attempts.py --attempts 4 --workdir /path/outside/the/repo --gpus 8 --max-price 10 --hours 8 -- \
  --repo . --commit "$(git rev-parse HEAD)" --corpus /path/to/corpus \
  --dotnet-repo ../taisce-dotnet --dotnet-commit "$(git -C ../taisce-dotnet rev-parse HEAD)"
```

`phases.sh` runs the qualification gates, the head-to-head and the corpus measurement in turn, each
into its own results directory. `python3 guard.py --check` and `python3 deploy.py --check …`
validate arguments and, for the guard, the key against the provider's pricing endpoint, without
creating anything.

Proved by `deploy/gpu/controller_test.go`: the scripts compile, refuse bad arguments (no key, a
price of zero, a commit that is not a hash, a corpus that is not a directory) and accept good ones,
without a provider call.
