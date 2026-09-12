#!/usr/bin/env bash
#
# The coverage gate.
#
# Rule 6 makes coverage a requirement. This is what makes it one: without something that fails, the
# rule survives exactly as long as whoever remembers it.
#
# The floor is a ratchet, not a target. It sits at what the suite already achieves and moves up when
# a change earns it. A fixed percentage invites tests written to reach it; the only way to move a
# ratchet is to test something that was not tested.
#
# THE FLOOR SITS BELOW THE NOISE, NOT AT THE PEAK.
#
# The measurement is not deterministic. Some tests race deliberately — several writers against one
# exclusion constraint, several drivers against one advisory lock — and which branches run depends on
# who wins. Observed range across consecutive runs of an unchanged tree: about half a percentage
# point.
#
# So the floor is set from the LOWEST number a run produces, never the highest. A ratchet raised to a
# lucky peak fails on the next ordinary run, and a gate that fails for reasons nobody changed is one
# that gets bypassed — which costs more than the tenth of a percent it was protecting.
#
# Raising it after a real improvement means running the suite more than once and taking the smallest
# result.
#
# The number is computed across packages. `go test -cover` counts only what a package's own tests
# reach, which understates a codebase exercised end to end — the same suite reports 75.4% that way
# and 78.9% with -coverpkg=./... . The second is the honest one.
set -euo pipefail

profile="${1:?usage: coverage-gate.sh <coverage profile>}"
repo_root="$(cd "$(dirname "$0")/.." && pwd)"
floor_file="${repo_root}/coverage.floor"

floor="$(tr -d '[:space:]' < "$floor_file")"
total="$(go tool cover -func="$profile" | awk '$1=="total:"{print $NF}' | tr -d '%')"

if [ -z "$total" ]; then
  echo "coverage: the profile ${profile} has no total; the suite did not run" >&2
  exit 1
fi

# A build that prints "78.4 < 78.9" and stops teaches nothing. The percentage is how we noticed; the
# untested code is the defect, so the failure names it.
if awk -v t="$total" -v f="$floor" 'BEGIN{exit !(t < f)}'; then
  echo "coverage: ${total}% is below the floor of ${floor}%" >&2
  echo "" >&2
  echo "Functions no test reaches:" >&2
  go tool cover -func="$profile" | awk '$NF=="0.0%"{printf "  %s\n", $0}' >&2
  echo "" >&2
  echo "The floor is a ratchet. Cover what this change left untested rather than lowering" >&2
  echo "coverage.floor." >&2
  exit 1
fi

# Raising the floor has to be a deliberate edit, so this says the number rather than writing it.
if awk -v t="$total" -v f="$floor" 'BEGIN{exit !(t > f)}'; then
  echo "coverage: ${total}%, above the floor of ${floor}% — raise coverage.floor to ${total} to keep it"
else
  echo "coverage: ${total}%, at the floor"
fi
