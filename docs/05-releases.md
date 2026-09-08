<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Preparing and publishing a release

Daily development lives in private `althunibat/taisce`. Public `ensera-ai/taisce` receives one source
snapshot commit per `x.y.0` release, on a separate linear history. The script validates the configured
`althunibat` and `origin` URLs against those roles. It never switches the developer's branch or pushes
private refs.

## Prepare

Start from a clean, pushed development branch. Write the public release message in a file outside the
checkout, then choose a new plan directory outside the checkout:

```sh
scripts/publish.sh prepare /path/to/release-message.txt /path/to/new-release-plan
```

Preparation runs the license gate and full test target, verifies that the source did not change,
and exports that source into an isolated Git repository under the plan. It stamps the released minor
line into `VERSION` and creates the public commit and annotated version tag locally. No remote refs
change during preparation.

The previous public tip must have a linear history with one public version tag per commit. A history
containing development commits, merge commits or unaccounted-for commits is refused. Resolve an
unexpected existing organization history deliberately before publication; the script never replaces it.

The export is refused if it contains private development issue URLs. Curating public documentation,
license notices and the release tree remains part of release preparation. A grep check is not a secret
scanner or a license review. Internal audit plans and tooling must not be shipped merely because they
are tracked in the development repository. The complete supply-chain gates remain development issue 37.

The plan contains the private source commit and branch, public parent, version, public commit and tree
digests, and repository destinations. Keep it private and retain it with the release record. Its
`repository` directory is the concrete public snapshot to review; the manifest is not a substitute
for reviewing that snapshot.

## Publish the reviewed plan

After completing the release review and supply-chain gates:

```sh
scripts/publish.sh publish /path/to/new-release-plan
```

Publication rechecks source, destinations, version, parent, tree, tag and checkout cleanliness. It
atomically publishes the public branch and the specific release tag. A lease compares the remote tip
against the reviewed parent; the new commit must descend directly from that parent, so the lease
cannot be used to replace public history. A changed remote refuses the push. Retrying the same plan
after both refs were published succeeds without changing refs.

Preparation and publication preserve the private checkout on success and failure. A failed plan is
left for inspection. Prepare a new plan when the source or public tip changes; do not edit its manifest
to bypass the checks.

## Continue the private version line

The public tag names the snapshot and is not an ancestor of daily development. In the private
repository, record an annotated `dev/vX.Y.0` tag on the plan's source commit, with the public commit
and tree digest in its release record. Advance `VERSION` to `X.Y` in a normal development commit.
Push that source marker and the development commit only to the private repository.

`scripts/version.sh current` counts private patches from the ancestor `dev/vX.Y.0` marker. In the
public repository it counts from the ancestor `vX.Y.0` tag, which makes the released snapshot `X.Y.0`.
An unrelated public tag cannot affect private patch counts. Before a line has a release marker,
versions count from the start of that history.

## Evidence and limits

The tests in `scripts/publish_test.go` use real disposable Git repositories with local URL rewriting.
They cover two consecutive releases, exclusion of private commit objects, public and private version
stamps, retry, changed source/destinations/snapshots, unaccounted-for public history, private issue-link
refusal, and a remote update during the push. The race test verifies that a failed atomic push leaves
no half-published version tag.

This script creates annotated tags; signing, registry artifacts, artifact provenance and public
contribution handling remain in the release/supply-chain backlog. It does not change repository
visibility. Its checks protect against accidental state changes, not a malicious operator who can
rewrite the script, Git configuration and prepared objects together.
