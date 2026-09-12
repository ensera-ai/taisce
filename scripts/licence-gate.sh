#!/usr/bin/env bash
#
# The licence gate.
#
# A licence stated once at the root and nowhere else is one file move away from a package published
# with no licence header — and since publishing is irreversible, the moment that is noticed is after
# somebody has already depended on it.
#
# ── WHAT THE HEADER SAYS, AND WHY IT NAMES THE AUTHORS ───────────────────────────────────────────
#
# `Copyright <year> The Taisce Authors`, not the company. The contributor agreement is a LICENCE and
# not an assignment, so contributors keep the copyright in what they write — a blanket notice
# naming only Ensera is accurate today and stops being accurate the moment somebody else contributes.
# Naming the authors is true in both states and needs no editing when the second one arrives.
#
# ── WHAT IS EXEMPT, AND WHY EACH ONE ─────────────────────────────────────────────────────────────
#
# Test files. They are not published in any package and a header on each is noise in the files whose
# comments are doing the most work.
#
# Nothing else. Generated files would be exempt if there were any; there are none, and adding the
# exemption before there is something to exempt is the thing rule 10 is about.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"

expected="Copyright .* The Taisce Authors"
missing=()

while IFS= read -r file; do
  case "$file" in
    *_test.go) continue ;;
  esac
  # Only the first few lines: a licence header below the package declaration is not a header.
  if ! head -5 "$file" | grep -qE "$expected"; then
    missing+=("$file")
  fi
# Tracked AND new-but-not-ignored. `git ls-files` alone sees only what is already committed, which
# means a file added in the change being checked — the exact case this gate is for — passes.
done < <(git ls-files --cached --others --exclude-standard '*.go')

if [ ${#missing[@]} -gt 0 ]; then
  echo "licence: ${#missing[@]} published file(s) carry no copyright header" >&2
  printf '  %s\n' "${missing[@]}" >&2
  echo "" >&2
  echo "Add this above the package clause:" >&2
  echo "" >&2
  echo "    // Copyright 2026 The Taisce Authors" >&2
  echo "    // SPDX-License-Identifier: Apache-2.0" >&2
  echo "" >&2
  echo "The authors rather than the company: the contributor agreement is a licence and not an" >&2
  echo "assignment, so contributors keep copyright in what they write." >&2
  exit 1
fi

echo "licence: $(git ls-files --cached --others --exclude-standard '*.go' | grep -vc '_test\.go$') published files carry a header"
