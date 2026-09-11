#!/usr/bin/env bash
# Copyright 2026 The Taisce Authors
# SPDX-License-Identifier: Apache-2.0
# The head-to-head (#24) on the qualification node, after the stack is up: the demo in the .NET
# adapter repository, installed at /root/taisce-dotnet, run on the Compose network against the
# deployed API and the model services. Its output is the evidence and goes into results/.
set -euo pipefail
cd /root/taisce-qualification
: "${TAISCE_COMPOSE:?the compose command the runner exported}"
mkdir -p results
token="$($TAISCE_COMPOSE logs --no-color bootstrap | grep -o 'tsk_[A-Za-z0-9_-]*' | head -1)"
test -n "$token"
export TAISCE_HEADTOHEAD_TOKEN="$token"
timeout 3600 $TAISCE_COMPOSE --profile headtohead run --rm -T headtohead > results/headtohead.md 2> results/headtohead.err
status=$?
printf '%s\n' "$status" > results/headtohead-exit
exit "$status"
