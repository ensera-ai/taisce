#!/usr/bin/env bash
# Copyright 2026 The Taisce Authors
# SPDX-License-Identifier: Apache-2.0
# Logical wipe of this run only, followed independently by provider termination.
# This does not claim forensic overwrite of provider-managed disks.
set -uo pipefail
cd /root/taisce-qualification || exit 0
status=0
if command -v docker >/dev/null && test -f .env; then
    docker compose -p taisce-qualification -f compose.yaml -f compose.gpu.yaml \
        --profile qualification down --volumes --remove-orphans --rmi all || status=1
    # A timed-out test runner can outlive its calling shell; remove only project-owned resources.
    ids=$(docker ps -aq --filter label=com.docker.compose.project=taisce-qualification)
    if test -n "$ids"; then docker rm -f $ids || status=1; fi
    volumes=$(docker volume ls -q --filter label=com.docker.compose.project=taisce-qualification)
    if test -n "$volumes"; then docker volume rm $volumes || status=1; fi
    test -z "$(docker ps -aq --filter label=com.docker.compose.project=taisce-qualification)" || status=1
    test -z "$(docker volume ls -q --filter label=com.docker.compose.project=taisce-qualification)" || status=1
fi
cd /root
rm -rf /root/taisce-qualification || status=1
rm -f /root/taisce-qualification-run.log || status=1
# A benchmark corpus is placed beside the source for a coverage run and is licensed to the operator,
# not to the node: it leaves with everything else.
rm -rf /root/corpus || status=1
test ! -e /root/taisce-qualification || status=1
test ! -e /root/corpus || status=1
printf 'qualification logical wipe exit=%s\n' "$status"
exit "$status"
