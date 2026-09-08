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
check_public_history() {
  local commit tag tags parents
  for commit in $(git -C "$snapshot" rev-list "$parent"); do
    parents="$(git -C "$snapshot" rev-list --parents -n 1 "$commit")"
    [ "$(printf '%s\n' "$parents" | wc -w | tr -d ' ')" -le 2 ] || die "public history contains a merge"
    tags="$(git -C "$snapshot" tag --points-at "$commit")"
    local releases=0
    while IFS= read -r tag; do
      if [[ "$tag" =~ ^v[0-9]+\.[0-9]+\.0$ ]]; then releases=$((releases + 1)); fi
    done <<< "$tags"
    [ "$releases" -eq 1 ] || die "public history contains an unaccounted-for commit or release alias"
  done
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
    # The public tree carries the released minor line, so version.sh reports .0 at the public tag.
    printf '%s\n' "${version%.*}" > "$snapshot/VERSION"
    git -C "$snapshot" add -A
    private_tracker="https://github.com/althunibat/taisce/"'issues'
    if git -C "$snapshot" grep --cached -q -F "$private_tracker"; then
      die "export contains private issue links; curate the public tree before preparing a release"
    fi
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
