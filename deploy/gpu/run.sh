#!/usr/bin/env bash
# Copyright 2026 The Taisce Authors
# SPDX-License-Identifier: Apache-2.0
# Execute on the disposable node. The operator-local lifecycle controller owns termination.
# This script never receives the GPU provider key or any existing application credentials.
set -euo pipefail
cd /root/taisce-qualification
umask 077
install -m 700 deploy/gpu/wipe.sh wipe.sh
mkdir -p results
container_monitor_pid=
gpu_monitor_pid=
finish() {
    status=$?
    trap - EXIT
    for pid in "$container_monitor_pid" "$gpu_monitor_pid"; do
        [[ -n "$pid" ]] || continue
        kill "$pid" 2>/dev/null || true
        wait "$pid" 2>/dev/null || true
    done
    printf "%s\n" "$status" > results/run-exit
    exit "$status"
}
trap finish EXIT
echo 'Checking node hardware'
nvidia-smi --query-gpu=name,memory.total,driver_version --format=csv > results/gpus.csv
# The topology is sized by the cards the node exposes rather than asserted at eight: a provider does
# not always deliver eight, and a smaller VM of the same class is the same environment. Eight cards
# reproduce the reference in compose.gpu.yaml; fewer narrow it. Coverage mode runs several workers per
# replica because formation drains one project per worker while a replica batches many sequences.
gpus="$(nvidia-smi --query-gpu=name --format=csv,noheader | grep -c NVIDIA)"
test "$gpus" -ge 2
card_gb="$(nvidia-smi --query-gpu=memory.total --format=csv,noheader,nounits | sort -n | head -1 | awk '{printf "%d", $1/1024}')"
if [[ "${TAISCE_GPU_MODE:-qualification}" == coverage ]]; then per_replica="${TAISCE_GPU_WORKERS_PER_REPLICA:-4}"; else per_replica="${TAISCE_GPU_WORKERS_PER_REPLICA:-1}"; fi
eval "$(python3 deploy/gpu/topology.py "$gpus" "$per_replica" "$card_gb")"
printf 'gpus=%s card_gb=%s tp=%s replicas=%s workers=%s embedding_gpu=%s services=%s\n' "$TOPOLOGY_GPUS" "$card_gb" "$TOPOLOGY_TP" "$TOPOLOGY_REPLICAS" "$TOPOLOGY_WORKERS" "$TOPOLOGY_EMBEDDING_GPU" "$TOPOLOGY_SERVICES" | tee results/topology.txt
read -r -a generation_services <<< "$TOPOLOGY_SERVICES"
df -h . > results/disk.txt
free -h > results/ram.txt
docker info --format '{{.ServerVersion}} {{.Driver}}' > results/docker.txt
docker compose version >> results/docker.txt
bash deploy/gpu/tls.sh > results/tls-setup.log 2>&1
python3 - <<'PY'
from pathlib import Path
import secrets
p = Path('.env')
assert not p.exists(), 'refuse to reuse credentials from a previous run'
values = {
    'POSTGRES_PASSWORD': secrets.token_hex(24),
    'TAISCE_DATA_PASSWORD': secrets.token_hex(24),
    'TAISCE_CONTROL_PASSWORD': secrets.token_hex(24),
    'TAISCE_PROJECT': 'gpu_qualification',
    'TAISCE_PORT': '127.0.0.1:8080',
    'TAISCE_INFERENCE_ENDPOINT': 'https://generation:8000/v1',
    'TAISCE_INFERENCE_EXTRACTOR_MODEL': 'Qwen/Qwen3.8-27B',
    'TAISCE_INFERENCE_EMBEDDING_ENDPOINT': 'https://embedding:8000/v1',
    'TAISCE_INFERENCE_EMBEDDING_MODEL': 'Qwen/Qwen3-Embedding-4B',
    'TAISCE_INFERENCE_ALLOWLIST': 'generation:8000,embedding:8000',
}
p.write_text(''.join(k+'='+v+'\n' for k,v in values.items()))
p.chmod(0o600)
PY
dc=(docker compose -p taisce-qualification -f compose.yaml -f compose.gpu.yaml -f qualification-topology.yaml)
export TAISCE_COMPOSE="docker compose -p taisce-qualification -f compose.yaml -f compose.gpu.yaml -f qualification-topology.yaml"
"${dc[@]}" config --quiet
echo 'Pulling pinned vLLM and Go images'
timeout 1800 "${dc[@]}" --profile qualification pull \
    generation "${generation_services[@]}" embedding test > results/pull.log 2>&1
echo 'Starting vLLM model services and PostgreSQL'
timeout 3600 "${dc[@]}" up -d --build --wait --wait-timeout 3000 \
    generation "${generation_services[@]}" embedding postgres test-postgres > results/model-start.log 2>&1
echo 'Starting application components'
# Document-sized turns under a shared model server exceed the five-minute default.
if [[ "${TAISCE_GPU_MODE:-qualification}" == coverage ]]; then export TAISCE_FORMATION_TURN_BUDGET="${TAISCE_FORMATION_TURN_BUDGET:-15m}"; fi
timeout 1800 "${dc[@]}" up -d --build --scale worker="$TOPOLOGY_WORKERS" --wait --wait-timeout 300 \
    api worker > results/application-start.log 2>&1
"${dc[@]}" ps --format json > results/services.json
docker image inspect vllm/vllm-openai@sha256:fc120ece0a388cc0aa1caad4a9f1cd92113484ab7ec2fd0efadd62585be05bf8 \
    --format '{{json .RepoDigests}}' > results/generation-image-digest.json
docker image inspect vllm/vllm-openai:v0.28.0 \
    --format '{{json .RepoDigests}}' > results/embedding-image-digest.json
( while true; do
    timestamp="$(date -u +%Y-%m-%dT%H:%M:%S.%NZ)"
    containers="$(docker ps -q --filter label=com.docker.compose.project=taisce-qualification)"
    if [[ -n "$containers" ]]; then
        docker stats --no-stream --format "$timestamp {{.Name}} cpu={{.CPUPerc}} memory={{.MemUsage}} block={{.BlockIO}}" $containers
    fi
    sleep 2
done ) > results/container-stats.log 2>&1 &
container_monitor_pid=$!
( printf 'timestamp, index, name, memory.used [MiB], utilization.gpu [%%], power.draw [W]\n'
while true; do
    nvidia-smi --query-gpu=timestamp,index,name,memory.used,utilization.gpu,power.draw \
        --format=csv,noheader,nounits
    sleep 2
done ) > results/gpu-stats.csv 2>&1 &
gpu_monitor_pid=$!
echo 'Running deployed API/worker smoke test'
python3 deploy/gpu/smoke.py > results/deployed-smoke.log 2>&1
# TAISCE_GPU_MODE selects what the deployed stack is used for. `qualification` (the default) is the
# full gate set. `coverage` measures the relation vocabulary against a document corpus the operator
# placed at /root/corpus: it needs the models and the write path, not the regression suite, so
# it returns after the measurement rather than spending node hours on gates that ran elsewhere.
if [[ "${TAISCE_GPU_MODE:-qualification}" == coverage ]]; then
    echo 'Measuring vocabulary coverage over /root/corpus'
    set +e
    # Ten minutes without any watermark moving is the stall bound here rather than the harness's
    # thirty: an article parked by a defect blocks its project's watermark, and every idle
    # minute on this node is paid for.
    CORPUS=/root/corpus CORPUS_SHARDS="$TOPOLOGY_WORKERS" RESULTS=results FORMATION_STALL_SECONDS=600 \
        python3 deploy/gpu/vocabulary-coverage.py > results/vocabulary-coverage.log 2>&1
    result=$?
    set -e
    "${dc[@]}" logs --no-color worker > results/worker.log 2>&1 || true
    "${dc[@]}" logs --no-color generation > results/generation.log 2>&1 || true
    nvidia-smi --query-gpu=name,memory.used,utilization.gpu --format=csv > results/gpus-after.csv
    exit "$result"
fi
echo 'Benchmarking the seven-replica structured-output endpoint'
"${dc[@]}" exec -T generation-0 python3 - < deploy/gpu/generation-benchmark.py \
    > results/generation-benchmark.json
echo 'Running full regression and configured-provider tests'
set +e
timeout 7200 "${dc[@]}" --profile qualification run --rm -T test > results/test-runner.log 2>&1
result=$?
set -e
for service in generation "${generation_services[@]}"; do
    "${dc[@]}" logs --no-color "$service" > "results/$service.log" 2>&1 || true
done
"${dc[@]}" logs --no-color embedding > results/embedding.log 2>&1 || true
"${dc[@]}" ps --format json > results/services-after.json
nvidia-smi --query-gpu=name,memory.used,utilization.gpu --format=csv > results/gpus-after.csv
exit "$result"
