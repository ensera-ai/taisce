set -u
# Copyright 2026 The Taisce Authors
# SPDX-License-Identifier: Apache-2.0
cd /root/taisce-qualification
dc="docker compose -p taisce-qualification -f compose.yaml -f compose.gpu.yaml -f qualification-topology.yaml"
export TAISCE_COMPOSE="$dc"
run() { name="$1"; shift; "$@"; code=$?; printf '%s %s\n' "$name" "$code" >> /root/phases.txt; rm -rf "results-$name"; mv results "results-$name" 2>/dev/null; mkdir -p results; return $code; }
run qualification env TAISCE_GPU_MODE=qualification bash deploy/gpu/run.sh
run headtohead bash deploy/gpu/headtohead.sh
# The corpus measurement over the stack the gates left up: four workers per generation replica
# and a fifteen-minute turn budget, as coverage mode sizes them (D117, D118).
coverage() {
    workers="$(( $(grep -o 'replicas=[0-9]*' results-qualification/topology.txt | cut -d= -f2) * 4 ))"
    TAISCE_FORMATION_TURN_BUDGET=15m timeout 1800 $dc up -d --scale worker="$workers" --wait --wait-timeout 300 api worker > results/application-rescale.log 2>&1 || return $?
    CORPUS=/root/corpus CORPUS_SHARDS="$workers" RESULTS=results FORMATION_STALL_SECONDS=600 python3 deploy/gpu/vocabulary-coverage.py > results/vocabulary-coverage.log 2>&1
    code=$?
    $dc logs --no-color worker > results/worker.log 2>&1 || true
    nvidia-smi --query-gpu=name,memory.used,utilization.gpu --format=csv > results/gpus-after.csv
    return $code
}
run coverage coverage
echo done >> /root/phases.txt
