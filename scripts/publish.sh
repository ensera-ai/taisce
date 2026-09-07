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
# ── THE TWO VERSION LINES ────────────────────────────────────────────────────────────────────────
#
#   x.y.z   every commit on the working history, z counting commits since the last release. Computed,
#           so nobody maintains it and no two commits can claim the same version.
#
#   x.y.0   a release, and the only thing that exists publicly. The patch position is always zero
#           there, which is deliberate: a published version is an account somebody wrote, and there
#           is no such thing as an unaccounted-for public release.
#
# WHAT THAT COSTS, because it is not free: an urgent fix ships as the next MINOR rather than as a
# patch, so the version number cannot say "small, urgent" to somebody deciding whether to upgrade
# today. The release notes have to say it instead. That is a real loss and it is the price of every
# public version being a deliberate account rather than a build number.
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

release="$("$root/scripts/version.sh" next-release)"
if git rev-parse "v$release" >/dev/null 2>&1; then
  echo "publish: v$release already exists; a version is published once" >&2
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

echo "publish: $work ($("$root/scripts/version.sh" current)) -> ensera-ai/taisce main as v$release"

# Every check the working branch has to pass, before anything leaves.
make licence
make test

# main is rebuilt from the working tree rather than merged, so the published history is exactly one
# commit per release with no accumulation of merge shapes.
git checkout -B main-publish "$work" >/dev/null 2>&1
git checkout --orphan main-orphan >/dev/null 2>&1
git add -A
git commit -q -F "$message_file"

git tag -a "v$release" -m "$(head -1 "$message_file")"
git push -f origin main-orphan:main
git push origin "v$release"

git checkout "$work" >/dev/null 2>&1
git branch -D main-publish main-orphan >/dev/null 2>&1

# The working line moves to the released minor, so the next commit is x.y.1 rather than continuing to
# count against a line that has been published. Committed here rather than left to somebody, because
# forgetting it makes every subsequent version wrong in the same direction.
echo "${release%.*}" > VERSION
git add VERSION
git commit -q -m "The working line moves to ${release%.*} after publishing v$release

Every commit from here is ${release%.*}.z, counting from the release rather than from whatever came
before it. Written by scripts/publish.sh, because forgetting it makes every version after this one
wrong in the same direction and nothing would say so."
git push "$(git config --get branch."$work".remote)" "$work:main"

echo "publish: v$release is public; the working line is now $("$root/scripts/version.sh" current)"
