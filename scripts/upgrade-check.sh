#!/usr/bin/env bash
# Copyright 2026 The Taisce Authors
# SPDX-License-Identifier: Apache-2.0
#
# Refuses a change whose migrations the previous release cannot serve on.
#
# An upgrade is a roll: the migration commits while processes of the previous release are still
# serving, and they go on serving against the new schema until the last of them is replaced. So a
# release makes a promise about its predecessor — that binary keeps working on this schema — and a
# promise about a binary nobody is editing any more is broken by a migration written by somebody who
# was not thinking about it (D58).
#
# This is what holds it. It takes the newest release before this commit, checks that release's source
# out beside this one, gives it this tree's migrations in place of its own, and runs its tests
# against a real database. The code and the assertions are the previous release's; only the schema
# is new. What the previous release needs from the schema is whatever its own suite exercises, so a
# migration that removes, renames or tightens something that release relies on fails here, in the
# pull request that adds it, with the previous release's own test naming what broke.
#
# It also refuses a change to a migration that has been released. A database that recorded a version
# never runs that file again, so an edit changes every new install and no existing one, and the two
# stop being the same schema with nothing to say so.
#
# What it does not cover:
#   - Anything the previous release's suite does not exercise. The suite is the definition of "needs";
#     it is wide, and it is not everything.
#   - The model path. No test here reaches a provider, in either release.
#   - A release older than the previous one. An upgrade is promised one release at a time.
#   - Locks. A migration that is compatible and holds a busy table for a minute passes.
#   - A break that only shows sometimes. A package that fails is run once more, and a failure that
#     does not repeat does not fail the check (below, where the suite runs).
set -euo pipefail

top="$(git rev-parse --show-toplevel)"
cd "$top"
planes=(internal/migrate/sql internal/migrate/control)

# The newest release strictly before this commit. HEAD^ and not HEAD: on the commit a release is cut
# from, the release to stay compatible with is the one before it, not itself.
if ! previous="$(git describe --tags --abbrev=0 --match 'v[0-9]*.[0-9]*.[0-9]*' 'HEAD^' 2>/dev/null)"; then
  echo "upgrade-check: no release tag is reachable from this commit, so there is nothing to compare against." >&2
  echo "upgrade-check: a shallow or tagless checkout is the usual cause — fetch the history and the tags (git fetch --tags --unshallow)." >&2
  exit 1
fi

# What changed under the migration directories since that release: committed, staged, unstaged, and
# files git has not been told about yet, because the next migration starts life as one of those.
changed="$(git diff --name-status "$previous" -- "${planes[@]}")"
untracked="$(git ls-files --others --exclude-standard -- "${planes[@]}")"

rewritten="$(printf '%s\n' "$changed" | grep -Ev '^(A|$)' || true)"
if [ -n "$rewritten" ]; then
  echo "upgrade-check: a migration released in ${previous} has been changed:" >&2
  printf '%s\n' "$rewritten" | sed 's/^/  /' >&2
  echo "upgrade-check: a database that already recorded that version will never run the file again, so new installs and upgraded ones would differ. Add a new migration instead." >&2
  exit 1
fi

added="$( { printf '%s\n' "$changed" | sed -n 's/^A[[:space:]]*//p'; printf '%s\n' "$untracked"; } | grep -v '^$' | sort -u || true)"
if [ -z "$added" ]; then
  echo "upgrade-check: no migration has been added since ${previous}; this schema is the one that release was built and tested on."
  exit 0
fi

# From here a database is needed, and there is no arm that goes without one: a compatibility check
# that skipped its tests is a green line that asserted nothing.
: "${TAISCE_TEST_DSN:?upgrade-check: migrations were added since the last release and TAISCE_TEST_DSN is not set; the previous release cannot be run against them without a database}"

echo "upgrade-check: since ${previous} this tree adds:"
printf '%s\n' "$added" | sed 's/^/  /'

work="$(mktemp -d "${TMPDIR:-/tmp}/taisce-upgrade-check.XXXXXX")"
cleanup() {
  git worktree remove --force "$work/previous" > /dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT

# A worktree and not an archive: some of that release's tests ask git about the tree they are in.
git worktree add --quiet --detach "$work/previous" "$previous"
for plane in "${planes[@]}"; do
  rm -rf "$work/previous/$plane"
  cp -R "$plane" "$work/previous/$plane"
done

cd "$work/previous"
# Every package of the previous release whose tests open the database, found and not listed, so a
# package that release had is not left out because nobody added it here. Three are set aside, each
# because its tests are about this repository's files and not about what a serving process needs:
#   internal/migrate  asserts the migration set itself, which has just been replaced;
#   internal/docsite  asserts the generated schema reference matches the schema;
#   scripts           asserts the repository's own scripts and workflows.
packages="$(grep -rl --include='*_test.go' 'TAISCE_TEST_DSN' . | xargs -n1 dirname | sort -u \
  | grep -Ev '^\./(internal/migrate|internal/docsite|scripts)$' || true)"
if [ -z "$packages" ]; then
  echo "upgrade-check: ${previous} has no package whose tests open the database, so there is nothing of it to run" >&2
  exit 1
fi

echo "upgrade-check: running ${previous}'s tests on this tree's schema:"
printf '%s\n' "$packages" | sed 's/^/  /'
# A package that fails is run once more before the check believes it.
#
# The suite being run is the previous release's, at its tag, and nobody can edit it. A test in it
# that depends on timing therefore fails this check at its own rate, on every change, until the next
# release replaces it — and each time the check would say a process of that release cannot serve on
# this schema, which is not what happened. A check that fails for a reason other than the one it
# names is retried without being read, and then it holds nothing.
#
# What a schema breaks, it breaks every time: a column that is gone is gone on the second run too.
# So a failure that repeats is the schema's and fails the check, and one that does not is reported,
# by package, and does not. What this gives up is a break that only shows sometimes — a race between
# a test and something the migration changed. That is rarer than a slow runner, and it is named in
# the output so that a line saying "on a second run" is one a reader looks at.
suite() {
  go test -count=1 -timeout 30m "$@" 2>&1 | tee "$work/suite.log"
  return "${PIPESTATUS[0]}"
}
refuse() {
  echo "upgrade-check: ${previous} does not pass on this tree's schema. A process of that release would be serving on it during an upgrade." >&2
  echo "upgrade-check: if the migration removes or tightens something, it waits a release: add now, remove once no supported release reads it (docs/postgresql/migrations.md)." >&2
  exit 1
}
# shellcheck disable=SC2086
if ! suite $packages; then
  # `FAIL <package> <time>` is how the toolchain names a package that failed. A run that failed and
  # named none did not get as far as running tests, and there is nothing to run again.
  failed="$(awk '$1 == "FAIL" && NF >= 2 { print $2 }' "$work/suite.log" | sort -u)"
  if [ -z "$failed" ]; then
    refuse
  fi
  echo "upgrade-check: failed, and run once more to see whether the failure repeats:"
  printf '%s\n' "$failed" | sed 's/^/  /'
  # shellcheck disable=SC2086
  if ! suite $failed; then
    refuse
  fi
  echo "upgrade-check: ${previous} passes on this tree's schema on a second run of the packages above."
  echo "upgrade-check: a failure that does not repeat is not the schema's: a test of ${previous} depends on timing. Read the first failure before trusting this line."
  exit 0
fi
echo "upgrade-check: ${previous} passes on this tree's schema"
