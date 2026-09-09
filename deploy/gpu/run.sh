#!/usr/bin/env bash
# Copyright 2026 The Taisce Authors
# SPDX-License-Identifier: Apache-2.0
# Execute on the disposable node. The operator-local lifecycle controller owns termination.
# This script never receives the GPU provider key or any existing application credentials.
set -euo pipefail
cd /root/taisce-qualification
umask 077
mkdir -p results
trap 'status=$?; printf "%s\n" "$status" > results/run-exit; exit "$status"' EXIT
echo 'Checking node hardware'
nvidia-smi --query-gpu=name,memory.total,driver_version --format=csv > results/gpus.csv
test "$(nvidia-smi --query-gpu=name --format=csv,noheader | wc -l)" -eq 8
test "$(nvidia-smi --query-gpu=name --format=csv,noheader | grep -c A100)" -eq 8
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
    'TAISCE_INFERENCE_EXTRACTOR_MODEL': 'Qwen/Qwen3.6-35B-A3B',
    'TAISCE_INFERENCE_EMBEDDING_ENDPOINT': 'https://embedding:8000/v1',
    'TAISCE_INFERENCE_EMBEDDING_MODEL': 'Qwen/Qwen3-Embedding-4B',
    'TAISCE_INFERENCE_ALLOWLIST': 'generation:8000,embedding:8000',
}
p.write_text(''.join(k+'='+v+'\n' for k,v in values.items()))
p.chmod(0o600)
PY
dc=(docker compose -p taisce-qualification -f compose.yaml -f compose.gpu.yaml)
"${dc[@]}" config --quiet
echo 'Pulling pinned vLLM and Go images'
timeout 1800 "${dc[@]}" --profile qualification pull generation embedding test > results/pull.log 2>&1
echo 'Starting vLLM model services and PostgreSQL'
timeout 3600 "${dc[@]}" up -d --build --wait --wait-timeout 3000 generation embedding postgres test-postgres > results/model-start.log 2>&1
echo 'Starting application components'
timeout 1800 "${dc[@]}" up -d --build --wait --wait-timeout 300 api worker > results/application-start.log 2>&1
"${dc[@]}" ps --format json > results/services.json
docker image inspect vllm/vllm-openai:v0.28.0 --format '{{json .RepoDigests}}' > results/model-image-digest.json
echo 'Running deployed API/worker smoke test'
python3 deploy/gpu/smoke.py > results/deployed-smoke.log 2>&1
echo 'Running full regression and configured-provider tests'
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
set +e
timeout 7200 "${dc[@]}" --profile qualification run --rm test > results/test-runner.log 2>&1
result=$?
kill "$container_monitor_pid" "$gpu_monitor_pid" 2>/dev/null
wait "$container_monitor_pid" 2>/dev/null
wait "$gpu_monitor_pid" 2>/dev/null
set -e
"${dc[@]}" ps --format json > results/services-after.json
nvidia-smi --query-gpu=name,memory.used,utilization.gpu --format=csv > results/gpus-after.csv
exit "$result"
