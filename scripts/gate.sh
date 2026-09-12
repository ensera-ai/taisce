#!/usr/bin/env bash
# Copyright 2026 The Taisce Authors
# SPDX-License-Identifier: Apache-2.0
#
# The gate a change passes before it merges.
#
# It is what the hosted runner was for, run here. That runner gave this project two things the
# developer's machine does not: Linux, and a clean checkout of the commit rather than a working tree
# that might hold something uncommitted. It never gave enforcement — this repository's plan has no
# branch protection — so replacing it loses a check mark somebody else can see and nothing else.
#
# So this clones the commit, starts a fresh substrate from the project's own image, and runs exactly
# what the workflow ran, in a Linux container under colima: the licence gate, the full suite with the
# coverage floor, and vet. Whether the contract document is current is a test kept with the document
# in the development repository (internal/api/contractdoc_test.go), so it runs in the suite here.
#
# What it does not reproduce, stated so nobody relies on it at a wider width: the hosted runner is
# x86-64 and this laptop is arm64. Nothing here builds with cgo, which is where the architecture
# usually shows, so the gap is small — small, not zero.
set -euo pipefail

top=$(git rev-parse --show-toplevel)
sha=$(git -C "$top" rev-parse HEAD)
short=${sha:0:7}
image=${GATE_IMAGE:-taisce-gate:1.27.1}
substrate=${SUBSTRATE_IMAGE:-taisce-postgres:0.1.0}

# Under the home directory, deliberately. colima shares the home directory with the VM and does not
# share the system's temporary directory: a container that bind-mounts a path colima does not share
# sees an empty directory, and the gate would fail against no code for a reason that looks like a
# broken build. Measured, not assumed — the probe that found it is in #228.
root="$HOME/.cache/taisce-gate"
work="$root/$short-$$"
log="$root/$short.log"
net="taisce-gate-$$"
db="taisce-gate-db-$$"

cleanup() {
    # -v: the substrate image declares its data directory a volume, and without it every run left a
    # full test database behind on Docker's disk until the disk filled.
    docker rm -f -v "$db" >/dev/null 2>&1 || true
    docker network rm "$net" >/dev/null 2>&1 || true
    rm -rf "$work"
}
trap cleanup EXIT

# The gate tests the commit. Uncommitted changes are not in it, and saying so is the difference
# between "green" and "green for something other than what you are about to push".
if [ -n "$(git -C "$top" status --porcelain)" ]; then
    echo "gate: the working tree has uncommitted changes; this tests commit $short, not them" >&2
fi

# Room on Docker's disk before anything starts, measured from a container because that is the disk
# the test database writes to. A database that runs out mid-run fails every package after it, and
# hundreds of failures that are one full disk read as hundreds of broken tests.
min_free_gb=${GATE_MIN_FREE_GB:-6}
free_kb=$(docker run --rm --entrypoint df "$substrate" -Pk / | awk 'NR == 2 { print $4 }')
if [ -z "$free_kb" ] || [ "$free_kb" -lt $(( min_free_gb * 1024 * 1024 )) ]; then
    echo "gate: Docker's disk has $(( ${free_kb:-0} / 1024 / 1024 )) GiB free and a run needs ${min_free_gb}; free some (docker volume ls --filter dangling=true lists what nothing uses) before gating" >&2
    exit 1
fi

mkdir -p "$root"
# A standalone clone rather than a worktree: a worktree's .git is a pointer to an absolute host path,
# which does not exist inside the container, and the licence gate needs git to list the files.
git clone --quiet --no-local "$top" "$work"
git -C "$work" checkout --quiet "$sha"

# A fresh substrate on a private network, never on a published port, so the gate cannot collide with a
# database the developer already has running and cannot be reached by anything but the test container.
docker network create "$net" >/dev/null
docker run -d --name "$db" --network "$net" \
    -e POSTGRES_PASSWORD=taisce -e POSTGRES_DB=taisce "$substrate" >/dev/null
for attempt in $(seq 1 60); do
    if docker exec "$db" psql -U postgres -d taisce -c 'SELECT 1' >/dev/null 2>&1; then
        break
    fi
    if [ "$attempt" -eq 60 ]; then
        echo "gate: the substrate did not answer within sixty seconds" >&2
        exit 1
    fi
    sleep 1
done

dsn="postgres://postgres:taisce@$db:5432/taisce?sslmode=disable"
started=$(date +%s)
set +e
# The module and build caches persist between runs in named volumes; the source never does.
docker run --rm --network "$net" -v "$work:/src" -w /src \
    -v taisce-gate-gomod:/go/pkg/mod -v taisce-gate-gobuild:/root/.cache/go-build \
    "$image" bash -c "make test TEST_DSN='$dsn' COVER_PROFILE=coverage.out \
        && go vet ./..." 2>&1 | tee "$log"
code=${PIPESTATUS[0]}
set -e
elapsed=$(( $(date +%s) - started ))
coverage=$(grep -E 'coverage: [0-9.]+%(,| is)' "$log" | tail -1 || true)

if [ "$code" -eq 0 ]; then
    echo "gate: $short green in ${elapsed}s — ${coverage:-no coverage line} (log: $log)"
else
    echo "gate: $short FAILED with exit $code after ${elapsed}s — ${coverage:-no coverage line} (log: $log)" >&2
fi
exit "$code"
