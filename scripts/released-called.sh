#!/usr/bin/env bash
# Copyright 2026 The Taisce Authors
# SPDX-License-Identifier: Apache-2.0
#
# Says whether a release's own code calls the vulnerable code its binaries were found to carry.
#
# The scan of a published binary reports what was linked in. That is the right first question — it
# judges the bytes an operator runs — and it is wider than the one a patch promise needs: a symbol
# linked in and never called is a reason to rebuild at the next release, and one the service calls
# is a reason to cut a release now. The patch policy in SECURITY.md has a date on the second and
# none on the first, so which of the two a finding is has to be decided by something that runs, on
# the public record, and not by whoever happens to look.
#
# So this checks the release's tag out and asks the scanner the reachability question of that
# source, with the toolchain the published binary says it was built by. The toolchain is named
# exactly and not left to the machine: a newer one on the runner would analyse a newer standard
# library, and report a release clean of an advisory that is in the one it shipped with.
#
# What it does not do:
#   - It does not execute the release. The source is read by the scanner and never built to run.
#   - It is not the binary. It answers for the source at the tag; the binary scan beside it is what
#     says the shipped bytes carry the advisory at all.
#
# usage: scripts/released-called.sh <release tag> <toolchain the binary was built by, e.g. go1.27.3>
#
# Exits 3 when the release's code calls vulnerable code, 0 when it does not, and anything else when
# that could not be decided — which the policy reads as "calls", because the shorter clock is the
# safe one to start by mistake.
set -euo pipefail

tag="${1:?usage: released-called.sh <release tag> <toolchain>}"
toolchain="${2:?usage: released-called.sh <release tag> <toolchain>}"

if ! git rev-parse --quiet --verify "refs/tags/${tag}^{commit}" > /dev/null; then
  echo "released-called: tag ${tag} is not in this checkout, so its source cannot be read (git fetch origin tag ${tag})" >&2
  exit 2
fi

work="$(mktemp -d "${TMPDIR:-/tmp}/taisce-released-called.XXXXXX")"
cleanup() {
  git worktree remove --force "$work/source" > /dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT
git worktree add --quiet --detach "$work/source" "refs/tags/${tag}"

cd "$work/source"
status=0
GOTOOLCHAIN="$toolchain" go tool govulncheck ./... || status=$?
case "$status" in
  0|3) exit "$status" ;;
  *) echo "released-called: the scanner could not judge the source of ${tag} with ${toolchain} (exit ${status})" >&2; exit "$status" ;;
esac
