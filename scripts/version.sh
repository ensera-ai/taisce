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
    # Public snapshot tags are not ancestors of private development. A private dev/vX.Y.0
    # marker identifies the source commit; the public vX.Y.0 tag identifies the exported snapshot.
    # Only an ancestor can define a commit count on this history.
    base=""
    for candidate in "refs/tags/dev/v${line}.0" "refs/tags/v${line}.0"; do
      if git rev-parse --verify "$candidate^{commit}" >/dev/null 2>&1 &&
         git merge-base --is-ancestor "$candidate" HEAD; then
        base="$candidate"
        break
      fi
    done
    if [ -n "$base" ]; then
      z="$(git rev-list --count "$base..HEAD")"
    else
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
