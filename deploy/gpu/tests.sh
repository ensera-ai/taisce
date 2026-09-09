#!/usr/bin/env bash
# Copyright 2026 The Taisce Authors
# SPDX-License-Identifier: Apache-2.0
# Run independent gates even after one fails, preserving every exit status.
set -uo pipefail
mkdir -p results
failed=0
gate() {
    local name="$1"
    shift
    date -u +%Y-%m-%dT%H:%M:%S.%NZ >"results/$name.started"
    "$@" >"results/$name.log" 2>&1
    local result=$?
    date -u +%Y-%m-%dT%H:%M:%S.%NZ >"results/$name.finished"
    printf '%s %s\n' "$name" "$result" | tee -a results/gates.txt
    if (( result != 0 )); then failed=1; fi
}
# Ordinary fixtures supply their own fake endpoints. Live profile variables must not leak into
# those tests (especially a separate embedding endpoint overriding a fixture's local allowlist).
clean_inference=(env -u TAISCE_INFERENCE_ENDPOINT -u TAISCE_INFERENCE_API_KEY
    -u TAISCE_INFERENCE_ALLOWLIST -u TAISCE_INFERENCE_EXTRACTOR_MODEL
    -u TAISCE_INFERENCE_EMBEDDING_ENDPOINT -u TAISCE_INFERENCE_EMBEDDING_MODEL
    -u TAISCE_INFERENCE_EMBEDDING_API_KEY -u TAISCE_INFERENCE_EMBEDDING_REVISION)
gate regression "${clean_inference[@]}" go test -count=1 -timeout 30m -coverpkg=./... -coverprofile=results/coverage.out ./...
gate coverage bash scripts/coverage-gate.sh results/coverage.out
gate vet go vet ./...
gate inference go test -tags inference -count=1 -v -timeout 90m ./internal/infra/inference/
gate embedding-provider go test -tags inference -count=1 -v -timeout 5m ./internal/infra/pg/ -run '^TestConfiguredEmbeddingProvider'
gate report-quality go test -tags=inference,qualification -count=1 -v -timeout 30m ./internal/infra/pg/ -run '^TestReportEmbeddingNamedCorpusQualityAndANNRecall$'
gate vector-layout env TAISCE_VECTOR_LAYOUT_QUALIFICATION=1 go test -tags=qualification -count=1 -v -timeout 50m ./internal/infra/pg/ -run '^TestVectorLayoutOperatingEnvelope$'
gate anchor-layout env TAISCE_ANCHOR_LAYOUT_QUALIFICATION=1 go test -tags=qualification -count=1 -v -timeout 35m ./internal/infra/pg/ -run '^TestAnchorLayoutOperatingEnvelope$'
gate passage-provider go test -tags inference -count=1 -v -timeout 5m ./internal/api/ -run '^TestConfiguredPassageProvider'
gate race "${clean_inference[@]}" go test -race -count=1 -timeout 5m ./internal/api ./internal/infra/pg -run 'Test(MessageWindows|MessageReference|EveryOperation|ReadOnlyCredentials)'
exit "$failed"
