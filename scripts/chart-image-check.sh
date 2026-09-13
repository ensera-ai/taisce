#!/usr/bin/env bash
# Copyright 2026 The Taisce Authors
# SPDX-License-Identifier: Apache-2.0
#
# Refuses a chart that asks for an image tag its own release did not push.
#
# The chart and the image are published by the same run, and nothing else ties them together: the
# chart names its image by repository and a tag it defaults from its appVersion, and the image is
# pushed under whatever tags the workflow lists. When those two conventions drifted apart the
# published chart named a tag that did not exist, and every install of it would have sat in
# ImagePullBackOff with nothing wrong in the chart that a lint could see (#21).
#
# So this renders the packaged chart exactly as an operator would get it, collects every reference
# to the release's image repository, and fails unless each one is among the refs the run pushed.
#
# What it does not cover: images from other repositories the chart names, such as the PostgreSQL
# operator's image, which this release does not produce and cannot vouch for.
set -euo pipefail

chart="${1:?usage: chart-image-check.sh <packaged chart .tgz> <image repository> <pushed refs, comma-separated>}"
repo="${2:?usage: chart-image-check.sh <packaged chart .tgz> <image repository> <pushed refs, comma-separated>}"
pushed="${3:?usage: chart-image-check.sh <packaged chart .tgz> <image repository> <pushed refs, comma-separated>}"

# The colon right after the repository keeps a sibling such as taisce-postgres out of the match.
wanted="$(helm template check "$chart" | grep -oE "${repo//./\\.}:[^\"'[:space:]]+" | sort -u || true)"
if [ -z "$wanted" ]; then
  echo "chart-image-check: the chart names no ${repo} image, so there is nothing it could install" >&2
  exit 1
fi

missing=0
while IFS= read -r ref; do
  case ",${pushed}," in
    *",${ref},"*) echo "chart-image-check: ${ref} was pushed by this release" ;;
    *) echo "chart-image-check: the chart asks for ${ref}, which this release did not push" >&2; missing=1 ;;
  esac
done <<< "$wanted"
exit "$missing"
