#!/usr/bin/env bash
# Copyright 2026 The Taisce Authors
# SPDX-License-Identifier: Apache-2.0
#
# Refuses a release page that would carry a file its checksums file does not vouch for.
#
# A release page is filled from a directory by a glob, and the checksums file is written from the
# same directory at one moment in the run. Nothing ties the two together: a file that reaches the
# directory after the checksums were written is uploaded beside them with no line of its own, and a
# reader who downloads it has nothing to compare it against. That is how a chart package came to be
# the one artifact of a release nobody could check (#253).
#
# So this takes the files the page will carry, by the same glob the upload uses, and fails unless
# each one is a line in the checksums file whose sum is the sum the file has now. It is run after
# everything is built and before the page is published.
#
# What it does not cover: who made the files. A checksums file vouches for bytes, not for origin;
# the provenance check that runs beside this one is what ties each file to the run that built it.
set -euo pipefail

dir="${1:?usage: release-page-check.sh <directory the page is filled from> <release tag>}"
tag="${2:?usage: release-page-check.sh <directory the page is filled from> <release tag>}"
sums="taisce-${tag}-checksums.txt"

cd "$dir"
if [ ! -s "$sums" ]; then
  echo "release-page-check: ${sums} is missing or empty, so nothing on the page could be checked" >&2
  exit 1
fi

# Every line of the checksums file names a file that is here and has that sum.
if command -v sha256sum > /dev/null; then
  sha256sum -c "$sums"
else
  shasum -a 256 -c "$sums"
fi

# And every file the page will carry has a line. The checksums file is the one exception: it
# cannot hold its own sum, and it is tied to the run by its provenance instead.
missing=0
for file in taisce-*; do
  [ "$file" = "$sums" ] && continue
  if ! grep -qE "^[0-9a-f]{64} [ *]${file//./\\.}\$" "$sums"; then
    echo "release-page-check: ${file} would be on the release page and is not in ${sums}" >&2
    missing=1
  fi
done
exit "$missing"
