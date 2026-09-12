#!/usr/bin/env bash
# Copyright 2026 The Taisce Authors
# SPDX-License-Identifier: Apache-2.0
#
# Prepare a public release in a separate repository, then publish that reviewed snapshot explicitly.
# The developer checkout and its branches are never switched, reset or pushed by this script.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"

die() { echo "release: $*" >&2; exit 1; }
usage() {
  die "usage: scripts/publish.sh prepare <message-file> <new-plan-directory> | publish <plan-directory>"
}

# Read the configured URL, not a remote name's assumed meaning. Git URL rewriting remains available
# for an operator's transport configuration and for the disposable-repository integration tests.
check_remotes() {
  development_url="$(git config --get remote.althunibat.url)" || die "missing development remote althunibat"
  public_url="$(git config --get remote.origin.url)" || die "missing public remote origin"
  case "$development_url" in
    git@github.com:althunibat/taisce.git|https://github.com/althunibat/taisce.git) ;;
    *) die "althunibat must identify the private development repository" ;;
  esac
  case "$public_url" in
    git@github.com:ensera-ai/taisce.git|https://github.com/ensera-ai/taisce.git) ;;
    *) die "origin must identify the public release repository" ;;
  esac
}

check_clean() {
  [ -z "$(git status --porcelain)" ] || die "the development checkout must be clean"
}

# Every existing public commit must be an accounted-for release on a linear history. Refusing an
# unexpected history protects existing work; repairing or replacing it is a separate operator action.
# Every public commit is an accounted-for release, with one exception: the root.
#
# The repository was opened with a sync commit before any release existed, and that commit is what
# people who found the repository early have. Refusing it would mean rewriting history somebody may
# hold, to fix a bookkeeping mismatch; accepting it as the base costs nothing and rewrites nothing.
# An untagged commit that is NOT the root is still refused, because that is a release somebody
# pushed without recording it.
check_public_history() {
  local commit tag tags parents count
  for commit in $(git -C "$snapshot" rev-list "$parent"); do
    parents="$(git -C "$snapshot" rev-list --parents -n 1 "$commit")"
    count="$(printf '%s\n' "$parents" | wc -w | tr -d ' ')"
    [ "$count" -le 2 ] || die "public history contains a merge"
    tags="$(git -C "$snapshot" tag --points-at "$commit")"
    local releases=0
    while IFS= read -r tag; do
      if [[ "$tag" =~ ^v[0-9]+\.[0-9]+\.0$ ]]; then releases=$((releases + 1)); fi
    done <<< "$tags"
    if [ "$releases" -ne 1 ]; then
      [ "$count" -eq 1 ] && [ "$releases" -eq 0 ] || die "public history contains an unaccounted-for commit or release alias"
    fi
  done
}

# Nothing in the exported tree cites what only the development repository can open.
#
# Written in POSIX bracket boundaries rather than \b: the site's patterns are Go's RE2, and this runs
# through git grep -E, where \b is not a word boundary at all — it matched a literal "b", so every
# reference sailed through and the check passed a tree full of them. The test below is what caught
# that, and is why the patterns are asserted rather than assumed.
#
# The same four patterns the documentation site refuses, applied to code and everything else at the
# moment it is exported — which is where public.go says the rule for code belongs. A public reader
# following a decision or issue reference finds nothing, and the reference outlives every chance to
# remove it, because publication is irreversible. The patterns live below rather than in this
# comment: an example written here would be a reference in the exported tree, and this file is in it.
strip_private_references() {
  local files file changed
  # Stripped rather than refused, so a release is unattended. Scoped by FILE TYPE, and that scoping
  # is the whole safety argument rather than a convenience.
  #
  # A hex colour and an issue reference cannot be told apart by shape: every decimal digit is also a
  # hex digit, so `#235` is at once a valid CSS shorthand and a plausible issue number. Twenty-two
  # such tokens exist in this tree, and both readings occur for real — `#000` is black in a
  # box-shadow and `#235` is a citation. A strip keyed on shape would delete the colour and break
  # the rendering of a published site, silently. So this runs only over files that carry reasoning
  # and cannot carry a colour, and never over styling assets.
  #
  # The publisher, its test and the documentation site's test are excluded for the reason given
  # above: each has to name what it refuses in order to refuse it. .txt is out too: the only tracked
  # ones are a Helm NOTES template and two font licences, which carry no reasoning and do carry
  # third-party URLs this has no business rewriting.
  #
  # ── WHAT THIS REMOVES, AND WHAT IT DELIBERATELY LEAVES ───────────────────────────────────────
  #
  # Only a parenthetical made entirely of references, and the private repository's name. Measured on
  # real comments, that reads cleanly every time: "frozen (D126)." becomes "frozen."
  #
  # A reference that is the SUBJECT or OBJECT of a sentence is left alone, because no mechanical
  # rule repairs it. Removing the token from "Bounded, per G11." gives "Bounded, per." and from
  # "What #48 is about" gives "What is about" — a reader meets those as typos rather than as an
  # absence, which is worse than the citation they replaced. Those are rewritten by hand in the
  # development tree, where the sentence can be written properly.
  files="$(git -C "$snapshot" ls-files -- \
    '*.go' '*.sql' '*.md' '*.sh' '*.py' '*.yaml' '*.yml' '*.tpl' 'Makefile' 'Dockerfile' '*/Dockerfile' \
    ':(exclude)scripts/private-paths.txt' ':(exclude)scripts/publish.sh' ':(exclude)scripts/publish_test.go' \
    ':(exclude)internal/docsite/docsite_test.go')"
  [ -n "$files" ] || return 0
  printf '%s\n' "$files" | while IFS= read -r file; do
    [ -n "$file" ] && [ -f "$snapshot/$file" ] || continue
    perl -0777 -pi -e '
      # The private repository becomes the public one rather than vanishing. Deleting it would leave
      # a dangling sentence, and in one place an empty OCI source label on the published image.
      s{https://github\.com/althunibat/taisce}{https://github.com/ensera-ai/taisce}g;
      s{git\@github\.com:althunibat/taisce}{git\@github.com:ensera-ai/taisce}g;
      # A parenthetical made only of references goes whole, together with the space before it, so
      # "frozen (D126)." reads "frozen." rather than "frozen ().".
      s{[ \t]*\((?:D[0-9]{1,3}|G[0-9]{1,2}b?|\#[0-9]{1,4})(?:[,;][ \t]*(?:D[0-9]{1,3}|G[0-9]{1,2}b?|\#[0-9]{1,4}))*\)}{}g;
    ' "$snapshot/$file"
  done
  # What changed, said out loud. A release that rewrites its own source silently is one nobody can
  # review, and the point of stripping rather than refusing is speed, not secrecy.
  changed="$(git -C "$snapshot" diff --name-only)"
  if [ -n "$changed" ]; then
    printf 'stripped private references from:\n%s\n' "$changed" >&2
  fi
  git -C "$snapshot" add -A
}

case "${1:-}" in
  prepare)
    [ "$#" -eq 3 ] || usage
    message_file="$2"
    [ -s "$message_file" ] || die "a nonempty release message file is required"
    # Resolve before changing directories. The plan must be new; no existing files are overwritten.
    message_file="$(cd "$(dirname "$message_file")" && pwd)/$(basename "$message_file")"
    check_remotes
    check_clean
    source="$(git rev-parse HEAD)"
    branch="$(git symbolic-ref --quiet --short HEAD)" || die "prepare from a development branch"
    remote_source="$(git ls-remote "$development_url" "refs/heads/$branch" | cut -f1)"
    [ "$remote_source" = "$source" ] || die "push the development source branch before preparing a release"
    version="$(./scripts/version.sh next-release)"
    [[ "$version" =~ ^[0-9]{1,6}\.[0-9]{1,6}\.0$ ]] || die "public version must be x.y.0"
    [ -z "$(git ls-remote "$public_url" "refs/tags/v$version")" ] || die "public version already exists"

    make licence
    make test
    check_clean
    [ "$(git rev-parse HEAD)" = "$source" ] || die "development source changed during validation"

    # Plans contain private provenance and must be new directories outside the source checkout.
    [ ! -e "$3" ] && [ ! -L "$3" ] || die "plan directory already exists"
    plan="$(cd "$(dirname "$3")" && pwd)/$(basename "$3")"
    case "$plan/" in "$root/"*) die "release plans must be outside the development checkout" ;; esac
    mkdir "$plan"
    snapshot="$plan/repository"
    git init -q "$snapshot"
    git -C "$snapshot" config user.name "$(git config user.name)"
    git -C "$snapshot" config user.email "$(git config user.email)"
    git -C "$snapshot" remote add public "$public_url"
    parent="$(git ls-remote "$public_url" refs/heads/main | cut -f1)"
    if [ -n "$parent" ]; then
      git -C "$snapshot" fetch -q --tags public refs/heads/main
      parent="$(git -C "$snapshot" rev-parse FETCH_HEAD)"
      check_public_history
      previous="$(git -C "$snapshot" tag --points-at "$parent" | sed -nE 's/^v([0-9]+\.[0-9]+)\.0$/\1/p')"
      previous_major="${previous%%.*}"; previous_minor="${previous##*.}"
      next_major="${version%%.*}"; rest="${version#*.}"; next_minor="${rest%%.*}"
      [[ "$previous_major" =~ ^[0-9]{1,6}$ && "$previous_minor" =~ ^[0-9]{1,6}$ ]] || die "previous version is unsupported"
      if ! ((10#$next_major > 10#$previous_major || (10#$next_major == 10#$previous_major && 10#$next_minor > 10#$previous_minor))); then
        die "public version must advance beyond the previous release"
      fi
    fi

    git archive "$source" | tar -xf - -C "$snapshot"
    # Nothing named in the source's private list is published, and the list goes out empty: the
    # snapshot has nothing private left in it, and the names are not published either. A source with
    # no list is refused rather than treated as having nothing private, because a missing list is how
    # a private file would get out. The documentation generator reads the same list, so the site
    # cannot show what this snapshot leaves out.
    private_list="$snapshot/scripts/private-paths.txt"
    [ -f "$private_list" ] || die "the source has no scripts/private-paths.txt, so it cannot say what is private"
    while IFS= read -r entry || [ -n "$entry" ]; do
      entry="${entry%%#*}"
      entry="$(printf '%s' "$entry" | tr -d '[:space:]')"
      [ -n "$entry" ] || continue
      case "$entry" in
        /*|..|../*|*/../*|*/..) die "private path $entry must be relative and stay inside the tree" ;;
      esac
      rm -rf "${snapshot:?}/${entry%/}"
    done < "$private_list"
    printf '%s\n' "# What never leaves the development repository." \
      "# This is the public tree: nothing private is left in it, and what was left out is not listed." \
      > "$private_list"
    # The public tree carries the released minor line, so version.sh reports .0 at the public tag.
    printf '%s\n' "${version%.*}" > "$snapshot/VERSION"
    git -C "$snapshot" add -A
    # The list said what to remove; this says it is gone. A typo in an entry would otherwise remove
    # nothing and publish it.
    while IFS= read -r entry || [ -n "$entry" ]; do
      entry="${entry%%#*}"
      entry="$(printf '%s' "$entry" | tr -d '[:space:]')"
      [ -n "$entry" ] || continue
      [ ! -e "$snapshot/${entry%/}" ] || die "private path $entry survived removal from the snapshot"
    done < "$root/scripts/private-paths.txt"
    strip_private_references
    tree="$(git -C "$snapshot" write-tree)"
    if [ -n "$parent" ]; then
      commit="$(git -C "$snapshot" commit-tree "$tree" -p "$parent" -F "$message_file")"
    else
      commit="$(git -C "$snapshot" commit-tree "$tree" -F "$message_file")"
    fi
    git -C "$snapshot" update-ref refs/heads/release "$commit"
    git -C "$snapshot" symbolic-ref HEAD refs/heads/release
    git -C "$snapshot" tag -a "v$version" "$commit" -F "$message_file"
    # Plain data, never sourced as shell code. The prepared commit and manifest are independently
    # checked again at publication so accidental edits cannot redirect a release or its ancestry.
    printf '%s\n' "$source" > "$plan/source"
    printf '%s\n' "$branch" > "$plan/source-branch"
    printf '%s\n' "$parent" > "$plan/parent"
    printf '%s\n' "$version" > "$plan/version"
    printf '%s\n' "$commit" > "$plan/commit"
    printf '%s\n' "$tree" > "$plan/tree"
    printf '%s\n' "$public_url" > "$plan/public-url"
    printf '%s\n' "$development_url" > "$plan/development-url"
    echo "Prepared v$version at $plan"
    echo "Review the snapshot, commit, license notices and release provenance before running publish."
    ;;
  publish)
    [ "$#" -eq 2 ] || usage
    check_remotes
    check_clean
    plan="$(cd "$2" && pwd)"
    snapshot="$plan/repository"
    [ "$(cat "$plan/public-url")" = "$public_url" ] || die "public destination changed"
    [ "$(cat "$plan/development-url")" = "$development_url" ] || die "development destination changed"
    source="$(cat "$plan/source")"; parent="$(cat "$plan/parent")"
    version="$(cat "$plan/version")"; commit="$(cat "$plan/commit")"; tree="$(cat "$plan/tree")"
    [[ "$source" =~ ^[0-9a-f]{40}$ && "$commit" =~ ^[0-9a-f]{40}$ && "$tree" =~ ^[0-9a-f]{40}$ ]] || die "invalid prepared object ID"
    [[ -z "$parent" || "$parent" =~ ^[0-9a-f]{40}$ ]] || die "invalid prepared parent"
    [[ "$version" =~ ^[0-9]{1,6}\.[0-9]{1,6}\.0$ ]] || die "invalid public version"
    [ "$(git rev-parse HEAD)" = "$source" ] || die "development source changed since preparation"
    [ "$(git -C "$snapshot" rev-parse "$commit^{tree}")" = "$tree" ] || die "prepared tree changed"
    [ "$(git -C "$snapshot" show -s --format=%P "$commit")" = "$parent" ] || die "prepared ancestry changed"
    [ "$(git -C "$snapshot" rev-parse "refs/tags/v$version^{commit}")" = "$commit" ] || die "prepared tag changed"
    [ "$(git -C "$snapshot" show "$commit:VERSION")" = "${version%.*}" ] || die "public version stamp is wrong"
    [ -z "$(git -C "$snapshot" status --porcelain)" ] || die "prepared snapshot has unreviewed edits"
    remote_parent="$(git ls-remote "$public_url" refs/heads/main | cut -f1)"
    remote_tag="$(git ls-remote "$public_url" "refs/tags/v$version^{}" | cut -f1)"
    if [ "$remote_parent" = "$commit" ] && [ "$remote_tag" = "$commit" ]; then
      echo "v$version is already published; no refs changed"
      exit 0
    fi
    [ "$remote_parent" = "$parent" ] || die "public main changed; prepare and review a new release plan"
    [ -z "$(git ls-remote "$public_url" "refs/tags/v$version")" ] || die "public version already exists"
    # The lease is a compare-and-swap against the reviewed parent, including an absent first branch.
    # The prepared commit must name that exact parent above, so this cannot rewrite public history.
    # Atomic push prevents a released branch without its version tag, or the inverse.
    git -C "$snapshot" push --atomic "--force-with-lease=refs/heads/main:$parent" "$public_url" \
      "$commit:refs/heads/main" "refs/tags/v$version:refs/tags/v$version"
    echo "Published v$version. Private source-to-public mapping remains in $plan."
    echo "Advance the private VERSION line deliberately under the release workflow; no private refs were changed."
    ;;
  *) usage ;;
esac
