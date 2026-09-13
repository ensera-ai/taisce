<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Decisions

This is where the project's decisions are recorded, and it is the authority. If another document
appears to leave a choice open that is settled here, that is a defect in the other document.

**Every entry carries what it beat.** A decision written down without its alternatives gets reopened
by whoever has only heard the winning argument, and they are usually right to — nothing told them the
alternative had been considered. An entry that lists no rejected option is not finished.

**Every entry says how hard it would be to undo.** A choice that is one migration away from being
reversed and one that is baked into every row ever written are read differently by whoever is
considering reopening them, and only one of them deserves the benefit of the doubt.

**Every entry names the impact it actually moves** — security, performance, cost, operations — and
says nothing about the ones it does not. A template filled in with "not applicable" teaches every
later reader to skip the section.

An entry is written when a `decision`-labelled issue closes — that is what closes it, not implementing
one of its options — or when building forces a choice a later reader would otherwise have to
re-derive. If the reasoning would only ever have existed in a code comment, it was a decision.

Entries are numbered from D1 in the order they were written, and are never renumbered. Cite one as
`D12` in a comment, an issue or a document; that is what the number is for.

## D1 — Development happens in this repository

**Date.** 2026-09-12. **Issue.** [#1](https://github.com/ensera-ai/taisce/issues/1).

**Why it was open.** Taisce was built in a private repository and published here as a snapshot: one
commit per release, produced by a script that exported the tree, removed the documents that were not
to be published, and stripped every internal cross-reference from the text on the way out. That was a
bootstrapping arrangement and it had run its course.

**Decided.** Development happens here. Issues are opened here, pull requests are opened here against
`main`, and `CONTRIBUTING.md` is the path. The bootstrap repository is parked; nothing is tracked
outside this one.

**Why, in one sentence that is not about convenience.** A project that develops in private and
publishes results is asking to be trusted on the parts nobody can see — and the parts nobody could
see were the reasoning: why a boundary sits where it does, what was rejected, what the alternative
cost. That reasoning is the most valuable thing this codebase has and the hardest to reconstruct, and
publishing the code while withholding it is the arrangement this project exists to argue against.

**What it also removed.** Two trees that had to be kept in step; a rule that every reference in a
comment had to survive an automated rewrite on export; and a class of defect that could only be found
after publication, which is the one moment it cannot be fixed. One of those defects had already
happened: the first release commit was authored by an address no account held, so the repository
showed a release nobody had written.

**Rejected: keeping the split and publishing more of the private tree.** It answers the disclosure
question and keeps the cost — two trees, an export step, and an irreversible publication at the end
of it.

**Rejected: staying private and releasing snapshots.** That is the status quo, and its cost is above.

**What is deliberately not carried across.** The document whose subject is the market, and the
marketing material. Neither is reasoning a contributor needs, and the first names and grades other
systems, which is not something this project publishes. The register from the bootstrap repository is
parked rather than copied: an entry is carried across only when something being decided here depends
on it, and then it is re-derived and re-argued rather than transcribed, because a decision made under
constraints that no longer apply is not a decision anyone here can defend.

**Undo cost.** High, and asymmetric. Going back to private development means a tree nobody outside can
read, and every issue and pull request opened here would have to be abandoned in place. Nothing about
this is one setting away from reversal.

**Impact.** Governance and maintainability: the argument behind the code is now readable by the people
expected to change it. It moves no security boundary — what is published is what was already
published, plus reasoning — and no performance property.

## D2 — The build runs on every pull request

**Date.** 2026-09-12. **Issue.** [#2](https://github.com/ensera-ai/taisce/issues/2).

**Why it was open.** The suite ran on `workflow_dispatch` only. It had been turned off because the
account it ran under had finite Actions minutes and spent them — 1,997 of 2,000 in a month, a third
of them re-running on the default branch what had just passed on a pull request — and the gate moved
to a container on a maintainer's machine.

**Decided.** It runs on every pull request and on every push to `main`. GitHub-hosted runners are free
for public repositories, so the constraint that justified turning it off belonged to the account, not
to the project, and it did not survive the move D1 made.

**Why a pull request needs it and not just `main`.** Without it the only evidence a contributor could
offer was a description of what they ran on their own machine. That is a claim, and this project does
not accept claims as evidence — least of all from a stranger whose machine nobody can inspect.

**Why `main` as well, when the same commit just passed.** It is not the same commit. History is linear
and merges squash, so what lands is a tree that has never existed before: the change as reviewed,
replayed on whatever `main` had become in the meantime. That tree is what people clone and nothing
else would ever test it.

**Rejected: keeping the gate local.** `make gate` still exists and still runs the same target, which
is what makes a local green and a build green mean the same thing. But a gate only a maintainer can
run is not a gate an outside contribution passes through.

**What it costs.** Runner time, which is free here, and the duplication of a pull-request run and a
`main` run — bought deliberately, for the reason above.

**Undo cost.** Low. A trigger block in one workflow file.

**Impact.** Correctness and governance: a change nothing checked can no longer reach `main`. No
security or performance property moves.

## D3 — A citation in this repository points at something a reader can open

**Date.** 2026-09-12. **Issue.** [#3](https://github.com/ensera-ai/taisce/issues/3).

**Why it was open.** The documentation build refused any written page that cited a decision number, a
goal number, an issue number, or the development repository by name. The rule was right for what it
was built against: such a reference pointed into a tracker a public reader could not open, and a
reader who follows a pointer to nothing stops trusting the rest of the page.

**Decided.** The refusal is removed, along with the list of withheld paths, the export script that
consumed it, and the generator's notion that some of the tree is not for publication.

**The argument reversed rather than expired, which is the part worth recording.** After D1 a citation
points at this repository's own register and its own issues. It is now the mechanism by which a reader
finds the argument behind a boundary instead of being told there is one — the opposite of a dead
pointer. Keeping the refusal would have made the register uncitable from the documents it governs.

**Rejected: keeping the refusal and exempting the register.** An exemption list is a rule that is
about to be wrong again: the next document worth citing is not on it, and whoever adds it will not
know why the list exists.

**Rejected: keeping the export script unused.** Roughly 380 lines of script and test, a path list, and
a distinction threaded through four files of the site generator, none of which anything reads.
Machinery with no reader is indistinguishable from machinery that is merely untested, right up until
somebody uses it and finds it describes a world that no longer exists.

**What the build still refuses,** and this is the whole of it: a package with no doc comment, a link
that names nothing, and a document the navigation does not reach.

**Undo cost.** Low in code and high in fact. The deleted files are in the history and could be
restored; the reason to want them back would be a return to private development, which D1 records as
hard to undo.

**Impact.** Maintainability: the documents can now cite the register that governs them, and there is
less code. It moves no security boundary — nothing withheld from the public tree remains to be
protected, because there is no longer a private tree to withhold it from.

## D4 — The compose file pulls a published release, and building is a second file

**Date.** 2026-09-13. **Issue.** [#13](https://github.com/ensera-ai/taisce/issues/13).

**Why it was open.** `docker compose up` built the service from source and compiled pgvector from
source, on the machine of somebody who wanted to evaluate Taisce rather than build it. The release
workflow already pushed a service image, so the build was not even producing something unavailable —
it was producing something that existed and was not being used.

**Decided.** A release publishes both images the compose file needs — the service and its PostgreSQL
substrate — for `linux/amd64` and `linux/arm64`. `compose.yaml` names them and carries no `build:`
at all. A contributor running their working tree adds `compose.build.yaml`.

**Both images, because publishing one removes half a build.** The substrate is where the minutes are:
it clones and compiles pgvector. Publishing only the service would have left the slower half in place
and still required the repository, which is the thing being removed.

**Both architectures, because the alternative is worse than what it replaced.** The binaries in the
same job already build `linux/arm64`. An image that did not would hand every Apple Silicon and
Graviton operator an emulated container — slower than the build a published image exists to replace,
and slower invisibly, since nothing announces that a container is being emulated.

**Rejected: `image:` and `build:` on the same service.** Compose accepts both and builds when the
image is not in the local cache. That makes "am I running my change or a release?" a question about
cache state, and the wrong answer is the silent one — a contributor sees their change work when the
cache was cold and a release work when it was warm, with nothing in the output distinguishing them.
Two files cost a longer command and make the build something that was asked for.

**Rejected: pinning `latest`.** A getting-started file that follows a moving tag breaks under people
who changed nothing, and the failure looks like theirs. `latest` is published — for discovery, and
for anyone who has decided they want the moving one — and nothing this project ships points at it.

**Rejected: keeping the substrate on the stock PostgreSQL image and installing extensions at
migration time.** `CREATE EXTENSION` needs privileges the runtime is deliberately never granted, and
a missing extension should fail while the cluster comes up rather than part-way through a migration.
That reasoning has not changed; only where the image comes from has.

**Undo cost.** Low. Restoring `build:` to `compose.yaml` is a few lines, and the images are additive:
a deployment that ignores them is unaffected. What would be harder to undo is a published tag, which
is why the tags are immutable and only `latest` moves.

**Impact.** Adoption and operations: a first run is two pulls rather than two builds, and it needs
neither a checkout nor a Go toolchain. Supply chain: what an operator runs is now an artefact that
was signed and attested rather than one they compiled from a tree they did not read — which is a
stronger claim, and only true while the packages are public. It moves no boundary inside the service;
the images run what they always ran.

## D5 — A release has one name, and the chart asks for its image by it

**Date.** 2026-09-13. **Issue.** [#21](https://github.com/ensera-ai/taisce/issues/21).

**Why it was open.** The chart defaults its image tag to its `appVersion`. The release set
`appVersion` to the version with the tag's leading `v` stripped, and tagged the image with the `v`
kept. v0.3.0 and v0.3.1 both published a chart asking for `taisce:0.3.1` beside an image tagged
`v0.3.1`, so every install of either chart would have failed to pull. Lint passed and the chart
rendered, because each half was valid on its own.

**Decided.** A release is named by its tag, exactly as written — `v0.3.1` — on the git tag, on the
service and substrate images, and in the chart's `appVersion`. The only place the `v` is dropped is
the chart's own version, because Helm requires plain SemVer there, and that version names the chart
package rather than anything the chart installs. The workflow computes it once, in a `Version` step.
Before the chart is pushed, `scripts/chart-image-check.sh` renders the packaged chart and refuses it
unless every reference to the release's image is among the refs the same run pushed.

**Rejected: also pushing the image under `0.3.1`.** Two names for one image are two tags to keep
immutable, and two spellings for an operator to pin, a scanner to match and a changelog to mention —
the drift this defect came from, moved into the registry. It would have repaired the charts already
published, which is its one real advantage, and is not worth a second name on every release after.

**Rejected: prefixing `v` inside the template.** The default tag would then differ from what
`appVersion` says, and anyone overriding `image.tag` would have to know the template adds a letter to
the default but not to their value.

**What this gives up.** The charts published as 0.3.0 and 0.3.1 stay wrong; a published chart cannot
be edited. Installing either needs `--set image.tag=v0.3.1`, which `deploy/helm/README.md` states.

**What the check does not cover.** Images the chart names from other repositories, such as the
PostgreSQL operator's. This release does not produce them and cannot vouch for them.

**Undo cost.** Low: the `appVersion` is one argument in the workflow, and the check fails loudly if
the two names are ever split again.

**Impact.** Operations: a chart published from now on installs the image published beside it, and a
release that would break that fails before anything reaches the registry. It moves no boundary in the
service.

## D6 — A release claims SLSA Build Level 2, and proves its own artifacts before announcing them

**Date.** 2026-09-13. **Issue.** [#7](https://github.com/ensera-ai/taisce/issues/7).

**Why it was open.** The release signs and attests what it builds, and `SECURITY.md` called that
provenance without saying what level it reaches. A guarantee with no stated width is relied on at
whatever width the reader imagines.

**Established.** *Fact*, [SLSA v1.0 levels](https://slsa.dev/spec/v1.0/levels), read 2026-09-13:
Build Level 2 is a hosted build platform with provenance tied to it by a signature. Level 3 adds that
runs cannot influence each other and that the material used to sign provenance is out of reach of
the build's own steps. *Fact*,
[GitHub, Artifact attestations](https://docs.github.com/en/actions/concepts/security/artifact-attestations),
read 2026-09-13: artifact attestations by themselves provide Build Level 2, and a reusable workflow
isolated from its caller is what reaches Level 3. In this workflow the job that runs `go build`,
`docker buildx` and `helm package` holds the identity that signs and attests, so it reaches Level 2.

**Decided.** The release claims Build Level 2, in `SECURITY.md`, with what that does and does not
establish beside it. Before its release page is published, the release verifies every binary, both
images and the chart with the commands `SECURITY.md` gives consumers, pinned to this repository, this
workflow file and this tag.

**Rejected: Level 3 through a reusable workflow in this repository.** It would satisfy the letter and
not the purpose. The isolation Level 3 asks for protects the signing identity from build steps a
project cannot fully trust. When the reusable workflow lives beside its caller, the same write access
edits both, so the thing being isolated against can simply change the isolated half.

**Rejected: Level 3 through a reusable workflow in a separate, more tightly held repository.** That
does separate the two, but the separation is only as real as the difference in who can change each
repository. With one maintainer holding both, it moves the trust rather than reducing it, and it puts
a dependency on a second repository into the most sensitive step this project has. It becomes the
right answer when there is somebody else to hold the build repository.

**Rejected: keeping the word "provenance" without a level.** That is the defect this entry exists to
remove.

**Undo cost.** Low. Raising the level later is a restructuring of the release workflow that this
decision does not obstruct. The claim is one section of `SECURITY.md`.

**Impact.** Security: consumers can check a release against a pinned identity rather than an
organisation-wide or pattern match, and the release proves that check passes on every artifact before
announcing it. It changes nothing a release produces.
