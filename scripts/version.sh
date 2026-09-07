#!/usr/bin/env bash
#
# What version this is.
#
# ── TWO LINES, AND WHY THE PATCH NUMBER IS COMPUTED ──────────────────────────────────────────────
#
#   x.y.0     a release. Only these exist in the published repository, and each is an account of
#             what changed that somebody wrote deliberately.
#
#   x.y.z     a commit on the working history, z counting commits since the last release. Every
#             commit has one, and nobody maintains it.
#
# Computed rather than stored, because a number kept in a file is a number somebody forgets to bump —
# and the failure is silent: two commits claiming the same version, discovered when a bug report names
# one of them. Counting commits since a tag cannot disagree with the history it describes.
#
# VERSION holds only the minor line, `x.y`, because that is the part a person decides. The patch is a
# fact about the repository.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"

line="$(tr -d '[:space:]' < VERSION)"
case "${1:-current}" in
  line)
    echo "$line"
    ;;

  current)
    # Commits since this line was released. The release tag itself is z=0, so the first commit after
    # it is z=1 — which keeps `.0` meaning "released" and nothing else.
    if git rev-parse "v${line}.0" >/dev/null 2>&1; then
      z="$(git rev-list --count "v${line}.0..HEAD")"
    else
      # No release on this line yet: count everything, so a version exists before the first release
      # rather than the tooling refusing to say what this is.
      z="$(git rev-list --count HEAD)"
    fi
    echo "${line}.${z}"
    ;;

  next-release)
    # The next release bumps the minor and zeroes the patch. A release is never a patch bump, because
    # the published line has no patch numbers to bump — see the note above about what x.y.0 means.
    major="${line%%.*}"
    minor="${line##*.}"
    echo "${major}.$((minor + 1)).0"
    ;;

  *)
    echo "usage: version.sh [current|line|next-release]" >&2
    exit 1
    ;;
esac
