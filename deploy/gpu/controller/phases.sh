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
# and a fifteen-minute turn budget, as coverage mode sizes them.
# The corpus, when the operator did not upload one.
#
# A corpus is never in this repository: the AP News set ships under a licence Microsoft grants for
# research, and accepting it is the operator's act rather than a file we vendor. Fetching it here is
# that acceptance made explicit and bounded — one pinned commit, one archive, a digest checked before
# anything is unpacked, and the source and digest recorded under results so a later reader can say
# exactly which bytes produced a number.
#
# Off by default. TAISCE_FETCH_CORPUS=1 is the operator saying to do it.
fetch_corpus() {
    [ "${TAISCE_FETCH_CORPUS:-0}" = "1" ] || return 1
    commit=799b78b6716a8f24fcd354b89a37b429ba1e587a
    digest=2f70dda22a9f261f285c94f3ac13a8f0df60b69fe3df1b5853b47b372065a66f
    url="https://raw.githubusercontent.com/microsoft/benchmark-qed/$commit/datasets/AP_news/raw_data.zip"
    curl -fsSL -o /root/corpus.zip "$url" || return 1
    # Verified before unpacking, not after: an archive that is not the one this was written against
    # is one nobody has read, and unpacking it first would already have written its contents.
    echo "$digest  /root/corpus.zip" | sha256sum -c - || return 1
    mkdir -p /root/corpus && unzip -q -o /root/corpus.zip -d /root/corpus || return 1
    rm -f /root/corpus.zip
    # The provenance, never the content. The corpus itself is not written under results and not
    # collected: the licence is the operator's to accept, not ours to redistribute.
    printf 'source %s\nsha256 %s\narticles %s\nlicence AP News, licensed by Microsoft for research purposes\n' \
        "$url" "$digest" "$(find /root/corpus -type f -name '*.json' | wc -l | tr -d ' ')" \
        > results/corpus-provenance.txt
    return 0
}

coverage() {
    # A corpus is the operator's to supply, or to say may be fetched. Skipping is not failing — a run
    # measuring the read path without one should not report a failed gate for work nobody asked it to
    # do, because a failure nobody meant makes every real failure beside it harder to believe.
    if [ ! -d /root/corpus ] && ! fetch_corpus; then
        echo 'no corpus was uploaded or fetched; extraction coverage is skipped' > results/vocabulary-coverage.log
        return 0
    fi
    workers="$(( $(grep -o 'replicas=[0-9]*' results-qualification/topology.txt | cut -d= -f2) * 4 ))"
    TAISCE_FORMATION_TURN_BUDGET=15m timeout 1800 $dc up -d --scale worker="$workers" --wait --wait-timeout 300 api worker > results/application-rescale.log 2>&1 || return $?
    CORPUS=/root/corpus CORPUS_SHARDS="$workers" RESULTS=results FORMATION_STALL_SECONDS=600 python3 deploy/gpu/vocabulary-coverage.py > results/vocabulary-coverage.log 2>&1
    code=$?
    $dc logs --no-color worker > results/worker.log 2>&1 || true
    nvidia-smi --query-gpu=name,memory.used,utilization.gpu --format=csv > results/gpus-after.csv
    return $code
}
run coverage coverage
# The read path at size. Last, because it writes a synthetic graph of its own and the gates
# and the corpus measurement must not be reading a database it is loading a million rows into. Its
# own project, so nothing it writes is mistaken for corpus material.
readpath() {
    SCALE_PROJECT=readpath_scale RESULTS=results \
        python3 deploy/gpu/readpath-scale.py > results/readpath-scale.log 2>&1
    code=$?
    $dc exec -T postgres psql -v ON_ERROR_STOP=1 -U postgres -d taisce -tAc \
        "SELECT count(*) FROM pg_stat_activity WHERE backend_type='client backend'" \
        > results/connections-after.txt 2>&1 || true
    return $code
}
run readpath readpath
echo done >> /root/phases.txt
