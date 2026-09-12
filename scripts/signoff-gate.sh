#!/usr/bin/env bash
#
# The sign-off gate.
#
# D45 takes a contributor agreement rather than a Developer Certificate of Origin, and the agreement
# is checked where signatures live — a bot against a signature record, on a pull request. This checks
# the other half, which a bot cannot: that the commits in a change carry the author they claim.
#
# ── WHAT THIS CATCHES THAT A SIGNATURE BOT DOES NOT ──────────────────────────────────────────────
#
# A bot verifies that the person who OPENED a pull request has signed. It says nothing about the
# authorship of the commits inside it, and a change can carry commits authored by somebody who never
# signed anything — a cherry-pick, a rebase carrying somebody else's work, a squash that credits the
# wrong person. The agreement then covers the submitter and not the code.
#
# So this lists the distinct authors in a range and shows them. It cannot decide who has signed —
# that record lives with the signing service — and it makes the question answerable rather than
# invisible.
set -euo pipefail

range="${1:-origin/main..HEAD}"

authors=$(git log --format='%ae' "$range" 2>/dev/null | sort -u || true)
if [ -z "$authors" ]; then
  echo "sign-off: no commits in $range"
  exit 0
fi

echo "sign-off: authors in $range —"
printf '  %s\n' $authors
echo ""
echo "Each must have accepted docs/cla/individual.md, or be named on a corporate agreement."
echo "This check lists them; it does not decide. A signature record is not in the repository,"
echo "deliberately — it holds names and dates and belongs where that can be governed."
