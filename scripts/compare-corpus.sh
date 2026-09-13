#!/usr/bin/env bash
# Render two corpus runs side by side, case by case.
#
# Comparing providers is something this project does now that inference is a profile, and doing it by
# reading two thousand-line logs is how a difference gets missed. What matters per case is small: what
# was produced, what was refused, and whether the case passed — so that is what this shows, and the
# whole point is that a line differing between two providers is visible without reading either log.
#
#     make test-inference INFERENCE_PROFILE=deepseek   > /tmp/deepseek.txt
#     make test-inference INFERENCE_PROFILE=openrouter > /tmp/openrouter.txt
#     scripts/compare-corpus.sh /tmp/deepseek.txt /tmp/openrouter.txt
#
# It reports what the runs say and adds nothing: a case missing from one run is shown as missing rather
# than as a difference, because a run that did not measure something has not disagreed with anything.

set -euo pipefail

if [ $# -ne 2 ]; then
    echo "usage: $0 <run-a.txt> <run-b.txt>" >&2
    exit 2
fi

a=$1
b=$2
for f in "$a" "$b"; do
    [ -f "$f" ] || { echo "no such run: $f" >&2; exit 2; }
done

# One line per case: name, verdict, and what it produced. `produced` is the line the corpus prints for
# every case that completed; a case with no such line did not finish, which is a different thing from a
# case that produced nothing.
summarise() {
    awk '
        /^=== RUN   TestTheExtractionCorpus\// {
            name = $3
            sub(/^TestTheExtractionCorpus\//, "", name)
            current = name
        }
        /produced over [0-9]+ attempts:/ {
            line = $0
            sub(/^.*produced over [0-9]+ attempts: /, "", line)
            produced[current] = line
        }
        /^    --- (PASS|FAIL): TestTheExtractionCorpus\// {
            name = $3
            sub(/^TestTheExtractionCorpus\//, "", name)
            verdict[name] = ($2 == "FAIL:") ? "FAIL" : "pass"
        }
        END {
            for (c in verdict) {
                p = (c in produced) ? produced[c] : "(no measurement)"
                printf "%s\t%s\t%s\n", c, verdict[c], p
            }
            for (c in produced) if (!(c in verdict)) printf "%s\t%s\t%s\n", c, "?", produced[c]
        }
    ' "$1" | sort
}

summarise "$a" > /tmp/.corpus-a.$$
summarise "$b" > /tmp/.corpus-b.$$
trap 'rm -f /tmp/.corpus-a.$$ /tmp/.corpus-b.$$' EXIT

printf '%s\n' "── $a  vs  $b ──"
printf '\n'

differing=0
same=0
onlyone=0

# Every case either run mentions, so a case one run never reached is visible rather than absent.
cut -f1 /tmp/.corpus-a.$$ /tmp/.corpus-b.$$ | sort -u | while IFS= read -r case; do
    la=$(grep -P "^\Q$case\E\t" /tmp/.corpus-a.$$ 2>/dev/null || grep "^$case	" /tmp/.corpus-a.$$ 2>/dev/null || true)
    lb=$(grep -P "^\Q$case\E\t" /tmp/.corpus-b.$$ 2>/dev/null || grep "^$case	" /tmp/.corpus-b.$$ 2>/dev/null || true)
    va=$(printf '%s' "$la" | cut -f2); pa=$(printf '%s' "$la" | cut -f3)
    vb=$(printf '%s' "$lb" | cut -f2); pb=$(printf '%s' "$lb" | cut -f3)
    [ -z "$la" ] && { va="—"; pa="(this run did not reach it)"; }
    [ -z "$lb" ] && { vb="—"; pb="(this run did not reach it)"; }

    # Only where both runs measured it. A case one run never reached has not disagreed with anything,
    # and marking it as a difference is the instrument reporting its own incompleteness as a finding.
    mark=" "
    if [ -n "$la" ] && [ -n "$lb" ]; then
        [ "$pa" != "$pb" ] && mark="≠"
        [ "$va" != "$vb" ] && mark="≠"
    else
        mark="·"
    fi

    printf '%s %s\n' "$mark" "$case"
    printf '    a  %-5s %s\n' "$va" "$pa"
    printf '    b  %-5s %s\n' "$vb" "$pb"
done

printf '\n'
printf '≠  the two providers disagree, in verdict or in what came back.\n'
printf '·  only one run reached it, so nothing has been compared — a measurement not taken is not a\n'
printf '   difference, and an instrument that reports its own incompleteness as a finding is worse\n'
printf '   than one that says it is incomplete.\n'
