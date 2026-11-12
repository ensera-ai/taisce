#!/usr/bin/env bash
# Copyright 2026 The Taisce Authors
# SPDX-License-Identifier: Apache-2.0
#
# Judge a release that is already published against the advisories known today.
#
# `make vuln` judges the source tree, so when an advisory lands the next build goes red and somebody
# moves a pin. That says nothing about what was published before it landed: the image a chart
# installs and a compose file pulls was built by a toolchain and against modules that are fixed for
# as long as that release exists, and it is the release an operator is running. This reads the
# binary out of the published image, and the binaries attached to the release, and asks the same
# scanner about them.
#
# The binary, not a checkout of the tag. A Go binary records the toolchain and every module version
# it was linked from, so the thing judged is the thing that was shipped — including a toolchain the
# build picked that the source did not name. It is also the only form of a release this needs no
# trust in: a tag can be checked out and rebuilt by anybody, differently.
#
# What this does not do:
#   - It does not execute what it downloads. The image is created and never started; a file is
#     copied out of it and read.
#   - It is not the reachability check `make vuln` is. From a binary the scanner sees which
#     vulnerable symbols were linked in, which is wider than which this code calls. A finding here
#     is a reason to look and to cut a patch release, not proof of exposure.
#   - It judges the service's binaries. The PostgreSQL image a release publishes is another vendor's
#     software with its own advisories, and a different tool's to read.
#
# usage: scripts/released-advisories.sh <release tag> [<directory to keep the binaries in>]
#
# Exits 0 when no binary is affected, 3 when one is (the scanner's own code for "affected"), and
# anything else when the release could not be read — which is not the same as clean and must not
# look like it.
set -euo pipefail

tag="${1:?usage: released-advisories.sh <release tag> [<directory>]}"
keep="${2:-$(mktemp -d)}"
image="${IMAGE:-ghcr.io/ensera-ai/taisce}"
repository="${REPOSITORY:-ensera-ai/taisce}"
mkdir -p "$keep"

echo "== $image:$tag"
docker pull --quiet --platform linux/amd64 "$image:$tag" >/dev/null
container="$(docker create --platform linux/amd64 "$image:$tag")"
trap 'docker rm "$container" >/dev/null 2>&1 || true' EXIT
docker cp "$container:/taisce" "$keep/taisce-image-linux-amd64"

# The binaries on the release page, where there is a way to fetch them. Without one the image is
# still judged, and the output says the rest was not.
if command -v gh >/dev/null 2>&1 && [ "${RELEASE_ASSETS:-1}" = "1" ]; then
  if ! gh release download "$tag" --repo "$repository" --pattern 'taisce-*' --dir "$keep" --clobber 2>/dev/null; then
    echo "   the release page's binaries could not be fetched; only the image is judged"
  fi
else
  echo "   the release page's binaries were not fetched; only the image is judged"
fi

affected=0
judged=0
for binary in "$keep"/taisce-*; do
  # A checksum file or a signature sits beside the binaries and is not one.
  go version -m "$binary" >/dev/null 2>&1 || continue
  judged=$((judged + 1))
  echo
  echo "== $(basename "$binary"): $(go version "$binary" | awk '{print $NF}')"
  status=0
  go tool govulncheck -mode binary "$binary" || status=$?
  case "$status" in
    0) ;;
    3) affected=$((affected + 1)) ;;
    *) echo "the scanner could not read $(basename "$binary") (exit $status)" >&2; exit "$status" ;;
  esac
done

if [ "$judged" -eq 0 ]; then
  echo "no binary of release $tag could be read, so nothing was judged" >&2
  exit 1
fi
echo
if [ "$affected" -gt 0 ]; then
  echo "release $tag: $affected of $judged binaries carry a published advisory"
  exit 3
fi
echo "release $tag: $judged binaries judged, none carries a published advisory"
