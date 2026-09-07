#!/usr/bin/env bash
#
# Publish the working history to the public repository as one commit.
#
# ── THE TWO REPOSITORIES, AND WHY THEY DIFFER ────────────────────────────────────────────────────
#
#   althunibat/taisce   the working history. Every commit, with its measurements, its arguments and
#                       the things that turned out to be wrong. This is where the reasoning lives and
#                       where the work happens.
#
#   ensera-ai/taisce    the published repository. One commit per release, so somebody arriving reads
#                       a project rather than an excavation.
#
# The working history is not noise and is not hidden for being embarrassing — it is where a decision
# can be traced to the run that produced it. It is separated because a stranger evaluating this needs
# to read what the software IS, and fifty-five commits of how it came to be is a different document
# for a different reader.
#
# ── WHAT THIS SCRIPT WILL NOT DO ─────────────────────────────────────────────────────────────────
#
# It does not squash silently. The commit message is written by a person, because the published
# history is one line per release and that line is the only account a reader gets.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"

message_file="${1:-}"
if [ -z "$message_file" ] || [ ! -f "$message_file" ]; then
  echo "usage: scripts/publish.sh <file containing the release commit message>" >&2
  echo "" >&2
  echo "The message is the whole account a reader of the public repository gets of this release." >&2
  exit 1
fi

if [ -n "$(git status --porcelain)" ]; then
  echo "publish: the working tree is not clean" >&2
  exit 1
fi

work="$(git rev-parse --abbrev-ref HEAD)"
if [ "$work" = "main" ]; then
  echo "publish: run this from the working branch, not from main" >&2
  exit 1
fi

echo "publish: $work -> ensera-ai/taisce main, as one commit"

# Every check the working branch has to pass, before anything leaves.
make licence
make test

# main is rebuilt from the working tree rather than merged, so the published history is exactly one
# commit per release with no accumulation of merge shapes.
git checkout -B main-publish "$work" >/dev/null 2>&1
git checkout --orphan main-orphan >/dev/null 2>&1
git add -A
git commit -q -F "$message_file"

git push -f origin main-orphan:main
git checkout "$work" >/dev/null 2>&1
git branch -D main-publish main-orphan >/dev/null 2>&1
echo "publish: done"
