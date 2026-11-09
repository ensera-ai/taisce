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

## D7 — The coverage floor is absolute, and a change that only deletes code can fail it

**Date.** 2026-09-13. **Issue.** [#15](https://github.com/ensera-ai/taisce/issues/15).

**Why it was open.** A change merged without the suite ever running on it and deleted 469 lines of
covered Go. Nothing became untested — the gate's list of functions no test reaches was empty — but a
smaller numerator over a smaller denominator took `main` from at or above 92.0% to 91.9%, and every
pull request after it failed a gate it had done nothing to fail. The question left behind: should a
change that only removes code be able to fail the coverage gate at all?

**Decided.** Yes. The floor stays an absolute number checked on every run. When a deletion lowers the
ratio, the remedy is to cover behaviour nothing exercises — there is always some; on 2026-09-13 there
were 1,019 uncovered statements — and never to lower the floor to meet the suite. The floor moves up
only when the lowest of several full runs clears it, as `scripts/coverage-gate.sh` says, so noise
cannot raise it to a peak the next ordinary run misses.

**Rejected: a gate relative to the base branch.** "Coverage must not fall below `main`" lets coverage
erode in steps each smaller than run-to-run noise, which here is around a tenth of a point, so it
would also fail unrelated changes on a bad draw. It needs the base branch's number for every pull
request, which means a second suite run or a stored artefact, and it answers the wrong question: the
property wanted is that the codebase is well covered, not that it is no worse than yesterday.

**Rejected: exempting changes that only delete.** "Only deletes" is a property of a diff, and a move
looks like a deletion from where it was. The deletions most worth catching — tests removed along with
the code they held — are exactly what an exemption would wave through.

**Rejected: lowering the floor to the new ratio after a deletion.** That turns a ratchet into a target
that follows the suite down.

**Measured.** 91.962% before #17; 92.063% after it. A full run on `ace3f29` measured 92.055%
(11,807 of 12,826 statements). The tests in this change cover 41 more statements: a full local run measured
92.382% (11,848 of 12,825) and the pull request's run reported 92.4%. The floor rises from 92.0 to
**92.3** — below the lower of the two runs by more than run-to-run noise, rather than at 92.4, where
one run happened to land and the next ordinary run could miss.

**Undo cost.** Low: one number in `coverage.floor` and a few lines of script.

**Impact.** Process: a change that deletes well-covered code can be held until a test is added, which
costs time on that change. That is accepted — the time is spent on coverage, not on arguing a number
down. It moves no boundary in the service.

## D8 — The documentation site publishes documentation, and records for contributors stay in the repository

**Date.** 2026-09-13. **Issue.** [#45](https://github.com/ensera-ai/taisce/issues/45).

**Why it was open.** The site build published every markdown file under `docs/` and refused one the
navigation did not list, so being in `docs/` meant being on the site. That put this register, and six
dated measurement runs under a heading called *Measurements*, on taisce.dev beside the quickstart. The
register is written for whoever is about to change the project, not for someone adding memory to an
agent. The measurement runs are findings from rented machines and synthetic data, and no latency,
throughput or corpus target has been agreed for a published number to be read against. A reader quotes
a number from a page titled *Measurements* whatever else the page says.

**Decided.** A document in `docs/` is either listed in `docs/SUMMARY.md`, and published, or named in
that file's kept list, and not. The build refuses a document that is neither, one that is both, and a
kept name that is no document. A kept document is not staged, and a link to it from a published page
goes to the file on GitHub at the commit the site was built from. Kept from 2026-09-13: this register,
and documents `23`, `26`, `27`, `28`, `31` and `36`. The site's footer links the register on GitHub.
Measurements return to the site as a benchmark run against an agreed target, on hardware that can
speak for the system, published as that.

**Rejected: moving kept documents out of `docs/`.** `docs/01-decisions.md` is the path the project's
rules, the contributor guide and code comments cite. What a website shows is not a reason to break
every one of those references.

**Rejected: deleting the measurement records.** They are findings, and several of them changed a
decision. What changes is where they are shown, not whether they exist.

**Rejected: keeping them on the site under a "not a benchmark" notice.** The notice is read after the
number.

**Rejected: dropping them from the navigation and relaxing the check.** The check exists so that a page
is not half-published by omission. Relaxing it re-opens that for every document written after this
one; a list keeps each exclusion deliberate and visible in review.

**Undo cost.** Low: moving a name from the kept list into the navigation publishes it again.

**Impact.** Operations: what the public site shows is a reviewed list rather than whatever is in a
directory. It moves no boundary in the service.

## D9 — The generated v1 contract stays in the repository

**Date.** 2026-09-13. **Issue.** [#49](https://github.com/ensera-ai/taisce/issues/49).

**Why it was open.** `docs/02-contract.md` is generated by `make contract` so that a test can fail the
build when the API drifts from the frozen v1 surface. D8 decided that the site publishes documentation
and keeps contributor records in the repository; the contract was still listed in the navigation,
published as a 1,211-line generated page beside the guide written for people calling the API.

**Decided.** It joins D8's kept list. The file, `scripts/contract.go` and the drift test are
unchanged. A reader of the site learns the API from *The HTTP API, by task*; a reader of the contract
is someone checking the frozen surface, who reads it where the test does.

**Rejected: moving it out of `docs/`.** The generator and the drift test name its path, and what the
site shows is not a reason to change either.

**Rejected: publishing it under a generated-reference heading.** It is a check artefact rather than
reference prose, and the HTTP API guide already describes the same surface for a human.

**Undo cost.** Low: moving its name from the kept list back into the navigation publishes it again.

**Impact.** Operations: one generated page leaves the public site. It moves no boundary in the service
and does not change the contract or its check.

## D10 — The documentation recommends a hosted model, and names the Qwen models for self-hosting

**Date.** 2026-09-13. **Issue.** [#56](https://github.com/ensera-ai/taisce/issues/56).

**Why it was open.** The quickstart started every reader with a 38 GB local model before memory could
form, and the tag it named is an Apple silicon build that Linux and Windows cannot serve (#58). The
owner asked for DeepSeek to be the preferred way, for OpenRouter to carry embeddings, and for the
self-hosted Qwen models to be documented for production.

**Decided.** The documentation presents three arrangements, in order:
- **Hosted:** DeepSeek `deepseek-flash` for extraction and for an agent's model, and OpenRouter
  `qwen/qwen3-embedding-4b` for embeddings.
- **Qwen on vLLM**, for production on your own GPUs.
- **Qwen on Ollama**, for one machine, with the build named per platform.

The quickstart and the README show the hosted path first and say what text goes where; the allowlist
is where the operator consents to it. `compose.yaml` keeps pointing at a local model. Models are named
only where they are measured, and anything else is marked as not measured.

**Rejected: Ollama as the only path.** It keeps every word local, at the cost of a first run most
laptops cannot finish quickly. As written, it did not work on Linux or Windows at all.

**Rejected: DeepSeek as `compose.yaml`'s default endpoint.** A default that needs a key fails for
everyone without one, and a default that sends text to a third party is consent given by omission.

**Rejected: `openai/text-embedding-3-small`.** It has closed weights and no self-hosted counterpart.
`qwen3-embedding-4b` is the model family and 2,560 dimensions of the self-hosted options, so moving
between arrangements changes where the model runs rather than what it is.

**Rejected: a gateway on the first run.** LiteLLM would be a second moving part for nothing a first
run needs.

**Rejected: recommending models the corpus has not seen.** A named model is a measured one; the Linux
Ollama build is named because a reader needs a tag that runs, and it is marked as not yet measured.

**Undo cost.** Low: the order in which documentation presents models, and a paragraph per path.

**Impact.** Security: the recommended path sends conversation text to two third parties, which the
documentation states and the allowlist enforces. Operations: a first run needs API keys rather than a
local model.

## D11 — Compose defaults to DeepSeek and asks for its key, and the documentation names no local model runner

**Date.** 2026-09-13. **Issue.** [#63](https://github.com/ensera-ai/taisce/issues/63), which supersedes
[#58](https://github.com/ensera-ai/taisce/issues/58). **Supersedes** D10's rejection of DeepSeek as
compose's default endpoint.

**Why it was open.**
- **The default did not run everywhere.** `compose.yaml` defaulted to a model on the host, under a tag
  that runs only on Apple silicon (#58). Fixing the tag would still have left a first run depending on
  a 38 GB model most machines serve slowly.
- **The owner decided the direction:** a hosted default, and documentation that describes one way to
  start rather than two.

**Decided.**
- **Compose defaults generation to DeepSeek.** The endpoint is `https://api.deepseek.com/v1`, the model
  is `deepseek-flash`, and the allowlist is `api.deepseek.com`.
- **The key has no default.** `${TAISCE_INFERENCE_API_KEY:?…}` makes Compose refuse to run the file,
  and name the variable, until it is set. The documentation keeps the key in `.env`.
- **The published documentation and the README describe two ways to run a model.** DeepSeek, with
  OpenRouter's `qwen/qwen3-embedding-4b` for semantic search; and, for conversation text that must
  stay on the operator's infrastructure, Qwen served with vLLM.
- **`make test-inference` defaults to the `deepseek` profile.** Every profile embeds through
  OpenRouter, and the profile for a model on the host is removed.

**D10's objections, and why they no longer hold.**
- **"A default that needs a key fails for everyone without one."** It still fails, but loudly and at
  the first command, naming the variable. The alternative, a default that starts and never forms, fails
  silently.
- **"A default that sends text to a third party is consent given by omission."** Nothing is sent until
  an operator supplies a key, and supplying a DeepSeek key is the act of choosing DeepSeek. The
  allowlist still bounds every host.

**Rejected: a Linux-capable local tag as the default.** It fixes #58's symptom and keeps a first run
that most machines finish slowly, and the tag has not been through the corpus.

**Rejected: a default endpoint with an optional key.** An instance without a key would start, store
turns and form none. That is the failure `MEMORY WILL NOT FORM` exists to shout about, and a refusal
before anything starts is clearer.

**Rejected: a wrapper script that prompts for the key.** It would be a second way to start the stack,
and Compose itself would still need the key on every later command that reads the file.

**Rejected: OpenRouter as the default extractor.** Its shared pool answered `429` minutes apart
throughout a corpus run (`deploy/inference/openrouter.env`), which a formation worker would hit
continuously.

**Rejected: keeping the local-runner pages as a secondary path.** Owner's decision: two first-run paths
double what a reader has to choose between and what has to be kept true on three platforms.
Self-hosting remains documented through vLLM.

**What this does not change.**
- **The binary's own defaults.** With no endpoint it forms nothing and says so.
- **The Helm chart,** whose endpoint is whatever the operator sets.
- **Measurement records** that ran a local model; they describe what was run.

**Undo cost.** Low: the defaults in `compose.yaml` and `.env.example`, one profile file, and the pages
that describe them. A deployment that set its own variables is unaffected either way.

**Impact.** Security: by default, conversation text leaves the operator's infrastructure for DeepSeek
once a key is supplied, and the allowlist bounds where it goes. Cost: formation spends provider tokens
per stored turn. Operations: every Compose command that reads the file needs the key, which is why it lives in `.env`.

## D12 — The compose file names no project, so each directory is its own install

**Date.** 2026-09-14. **Issue.** [#73](https://github.com/ensera-ai/taisce/issues/73).

**Why it was open.** From v0.3.0 to v0.4.0, `compose.yaml` set `name: taisce`, and nothing recorded
why. The project name decides which containers and which volume every `docker compose` command acts
on. Fixed in the file, it made every copy of the file on one machine a single install:
- **A takeover:** `up` in a second directory recreated the first install's containers against the
  first install's volume.
- **A misleading failure:** bootstrap failed on that volume's password with SQLSTATE `28P01`, while
  `postgres` reported healthy, because its health check does not log in.
- **Data loss:** `down -v` in either directory deleted the other's data. This happened on a
  development machine, and it destroyed an install's volume.

**Decided.** No shipped compose file sets `name`. Compose then takes the project name from the
directory holding the file. `COMPOSE_PROJECT_NAME` remains the way to choose one deliberately. The
measurement overlays and the GPU qualification scripts already pass their own names, and are
unchanged. `TestNoShippedComposeFileFixesTheProjectName` holds this for all four files.

**Measured on 2026-09-14,** with this file, Docker Compose 5.5.0, Colima on macOS:
- **Two directories:** different `POSTGRES_PASSWORD` values gave two projects and two volumes. Both
  bootstraps exited 0, and the first install's containers were unchanged after the second started.
- **The same directory again:** `up -d` reused its volume. Bootstrap minted no credential, and the
  first token read what had been stored.
- **An install made by a file with a fixed name:** the new file in the same directory started a new,
  empty project. Setting `COMPOSE_PROJECT_NAME` to the old name brought back the original project,
  its token and its stored turn.

**Rejected: keep the name and document it.** The takeover and the data loss happen before anyone
reads a troubleshooting page.

**Rejected: keep the name and make bootstrap explain a password mismatch.** A clearer error fixes the
symptom, not the hazard: the containers are still taken over and the volume is still shared.

**Rejected: a version-specific name, such as `taisce-v0-4`.** Two installs of the same version still
collide, and every upgrade would start an empty project and look like data loss.

**What this does not change.** An install created by a v0.3.0–v0.4.0 file keeps its data in
`taisce_postgres-data`. From a directory called `taisce`, which is what a repository checkout is,
nothing changes. From a directory with another name, the next `up` with a newer file starts an empty
project instead; `COMPOSE_PROJECT_NAME=taisce` in `.env` points it back, and troubleshooting says so.
The Helm chart has no equivalent: each Helm release is already named by whoever installs it.

**Undo cost.** Low to put the line back, and a repeat of the same one-time move for anyone who
upgraded in between: installs made by a file without a name would become the shared project `taisce`
on their next `up`.

**Impact.** Operations: running two installs on one machine becomes safe by default. The cost is a
one-time step for anyone who created an install with a v0.3.0–v0.4.0 file in a directory not called
`taisce`.


## D13 — A working-tree build is tagged as a local build, never as a release

**Date.** 2026-09-14. **Issue.** [#95](https://github.com/ensera-ai/taisce/issues/95). **Refines** D4,
which put the build in a second file and did not say what the build is called.

**Why it was open.** `compose.build.yaml` tagged its build with the release tag `compose.yaml` names,
so that the two files would agree about what is running. The local image cache is shared by every
Compose install on a machine, so that agreement replaced the release for all of them: an install in
another directory, recreated afterwards, ran the working tree under the release's name, and only the
binary's own version line said otherwise. D4 rejected `image:` beside `build:` because it made "am I
running my change or a release?" a question about cache state. The shared tag reintroduced exactly
that question, across directories instead of within one.

**Decided.**
- Every service in the overlay is tagged `taisce:dev` or `taisce-postgres:dev`, with
  `pull_policy: build`. A name with no registry host is one no release pushes and no registry serves,
  so a build cannot replace a release, and Compose builds it rather than looking it up.
- `deploy/composebuild_test.go` refuses an overlay that leaves a published service untagged, tags it
  with a name a registry could serve, or lets Compose pull it.

**Rejected: the release tag, which is what the overlay did.** The argument for it was that the base
file and the overlay would name the same image. They then name the same image for every install on
the machine, which is the defect.

**Rejected: letting Compose name the build.** Compose's default is a project-scoped name, which would
not collide with a release. It also changes with the directory, so one build has a different name in
every checkout, and nothing in the file tells a reader what to look for in `docker image ls`.

**Rejected: a tag per commit or per build.** It would keep every build apart, but Compose cannot
compute it from the file, so every run would need a wrapper or an exported variable before the command
D4 settled on works. Two checkouts sharing `:dev` is the smaller cost, since both are working-tree
builds.

**What this does not cover.**
- Two checkouts on one machine share the `:dev` tags, so the last build is what both run.
- The binary's version still comes from `TAISCE_VERSION` when it is set, so a build run with
  `TAISCE_VERSION=v0.5.1` reports that version while tagged `:dev`.
- A machine that already holds a build under a release tag keeps it until `docker compose pull`
  replaces it. This prevents the next one and repairs none.

**Undo cost.** Low: two lines per service in one overlay file, and a test. No release artefact and no
data depend on the local tag.

**Impact.** Operations: an install started from a release keeps running that release whatever is built
on the same machine, and `docker image ls` shows builds and releases under different names.

## D14 — Whether formation is failing is read from two stamps, not from a clock or a counter reset

**Date.** 2026-11-08. **Issue.** [#97](https://github.com/ensera-ai/taisce/issues/97).

**Why it was open.** `formation_health.failed_attempts` only ever rises, and nothing resets it. The
ops centre marked its tile on any non-zero value, so the first failure an instance ever suffered
marked the page for the life of the instance, and the page could not tell a failing instance from one
that recovered an hour ago. The operator guide asked the reader to watch "whether it is still rising",
which is a question a page read once cannot answer. Building the answer forced a choice about what
"now" means on a page that holds no clock of its own.

**Decided.**
- Failures are current when `last_failure_at` is more recent than `last_progress_at`, and when
  `last_progress_at` is absent while `last_failure_at` is not: nothing has ever formed, which is the
  worst case the tile exists for, so an absent stamp counts as older rather than as nothing to report.
- The tile is marked on that, and carries `last_failure_at` whether or not it is marked, so an
  operator can see when the failures stopped as well as that they did.
- The section header takes the same answer, between **Connections are low** and **Turns are parked**,
  so a header saying nothing needs attention can no longer appear beside a marked tile.
- `internal/api/portalhealth_test.go` holds the four states against a real deployment: failing,
  recovered with the total intact, never formed, and never failed.

**Rejected: a recency window.** Marking the tile while `last_failure_at` is within the last N minutes
is what a monitoring system would do. It needs a duration nobody here has measured, and it is wrong in
both directions on a quiet instance: a worker whose attempts are further apart than the window reads
as healthy in the gaps between its failures, and an instance that failed once inside the window reads
as failing when the next turn has already formed. It also needs a wall clock the page trusts, where
the two stamps come from one clock in the substrate and need no agreement with this process.

**Rejected: resetting the counter on success.** It would make a non-zero value mean "failing now"
directly. It also destroys the only record that failures ever happened, which is the number the health
report publishes and the operator guide tells people to watch, and it would make the page's reading
depend on write-path behaviour rather than on what the row says.

**Rejected: counting failures in a window from the ledger.** The ledger holds operations, not failed
formation attempts, so this would mean writing a row per failure — a write on the failure path, in a
table whose chain is verified, to answer a question two columns in a row already hold.

**What this does not cover.**
- One health row covers the instance, so a turn forming in any project clears the mark while another
  project may still be failing. Per-project currency needs a per-scope row, which this does not add,
  and a project stuck on its own is visible as *Behind by* and *Parked* in the projects table.
- The mark says a failure has not been recovered from, not how many failures there were or how fast
  they are arriving. The count beside it is still a lifetime total.
- A clock moved backwards on the database server can order the two stamps wrongly. Both come from
  `clock_timestamp()` on one server, so this is one clock being wrong rather than two disagreeing.

**Undo cost.** Low: one derived boolean in the portal's view model, one template condition, and a
test. No schema change, no stored value, and nothing a release or a row depends on.

**Impact.** Operations: an operator reading the page once can tell whether formation is failing now,
and an instance that recovered stops asking for attention while keeping the evidence that it failed.

## D15 — The first credentials are never stored, and a lost one is replaced rather than recovered

**Date.** 2026-11-08. **Issue.** [#102](https://github.com/ensera-ai/taisce/issues/102).

**Why it was open.** Bootstrap mints the first operator credential and the first project credential
and prints them once; only their digests are stored. In the Helm deployment that output lives in the
log of a Job named by the release revision, and the next upgrade takes the previous Job out of the
release, so Helm deletes it and its log. The following revision's bootstrap finds the instance
already reachable, mints nothing, and prints nothing. Changing a replica count is enough to destroy
both tokens, and they go together: minting a replacement project credential through the management
surface needs the operator credential that was in the same log. Measured on a cluster rather than
reasoned about.

The question it forced is whether the deployment should keep a copy of a token so that an upgrade
cannot destroy it.

**Decided.**
- No. Nothing in the chart or the service stores a token. A digest stays the only thing written, and
  a bootstrap Job's output stays as perishable as the Job.
- The way out of a lost token is a replacement minted through the administrative database
  connection, which outlives every credential: `taisce operator issue <name>` and
  `taisce credential issue <name> -project <project>`. The management surface already carries that
  connection, so on a default install this is one `kubectl exec`.
- That only helps an operator who knows it, so the commands are printed where the loss is
  discovered: beside the tokens when bootstrap mints them, in the log line of every later bootstrap
  that mints nothing, in the chart's notes, and in the chart README beside the step that reads them.
- Held by `TestBootstrapSaysHowToReplaceTheTokensItShowsOnce`,
  `TestABootstrapThatMintsNothingShowsNoTokenAndStillNamesTheWayToMintOne`,
  `TestTheNotesSayTheFirstTokensAreTheOnlyCopyAndHowToReplaceThem` and
  `TestTheNotesDoNotOfferAnExecRecipeWhenNoWorkloadHoldsTheAdminConnection`.

**Rejected: write the first credentials into a Secret annotated `helm.sh/resource-policy: keep`.**
This is the obvious repair, and it works: the credential survives upgrades, survives uninstall
deliberately, and an operator reads it with `kubectl get secret` whenever they get to it. It also
converts the two most powerful credentials in the system from something that exists for the minutes
an operator is looking at an install into something that exists for the life of the deployment,
readable by anyone who can read a Secret in that namespace and present in every cluster backup taken
afterwards. What it buys is one command, because the minting command already exists. A credential
that must exist is a credential that can be stolen, and this one would have to exist for no reason
beyond saving that command.

**Rejected: keep printing once and annotate the Job `resource-policy: keep`.** The token stays out
of a Secret, which is the better half of the argument. It is still at rest, in a pod log, for as
long as the cluster's log retention and whatever ships those logs keep it — and it leaves a
completed Job behind on every upgrade for somebody to prune by hand. The same security property is
traded for worse operations.

**Rejected: mint nothing at bootstrap and make the operator mint explicitly.** Then nothing is
printed that could be lost. It also means a fresh install serves nobody until a second command has
run, so installing stops being one command. A loss that two commands repair is the smaller cost than
an install that does not work when it finishes.

**What this does not cover.**
- The tokens themselves. Nothing brings back what was shown once — that is the property being
  defended, not a shortfall in defending it.
- Minting a replacement does not revoke what was lost. A token nobody can find still works for
  whoever read the log before it was deleted, or for whatever shipped that log elsewhere; closing it
  is `taisce credential revoke <id>`, and doing so is part of recovering.
- A deployment with `manage.enabled=false` has no workload holding the administrative connection, so
  there is nothing to exec into. The management surface has to be enabled to mint, and the chart's
  notes say that instead of printing a command that would fail.
- Anyone who can read the bootstrap Job's log before the next upgrade holds both tokens. That log is
  the trust boundary for as long as the operator leaves it, which is the window this decision makes
  short rather than closes.

**Undo cost.** Low. Reversing it is a Secret template and a line in the Job; nothing is written to
the database, no row records the choice, and no deployment carries state that depends on it. The
cost of having chosen wrongly is paid once, by whoever has to rotate credentials that were stored.

**Impact.** Security: the credential that reaches the management surface and the credential that
reaches the first project exist at rest nowhere, so reading a Secret, listing a namespace or
restoring a cluster backup yields neither. Operations: losing them stops being a dead end, and the
two commands that end it are named at each of the places an operator meets the problem.

## D16 — A context carries every turn the subject said, and a summary stands in only for the turns it was written from

**Date.** 2026-11-08. **Issue.** [#111](https://github.com/ensera-ai/taisce/issues/111).

**Why it was open.** `POST /v1/contexts` read the same turns the compaction pass reads: formed, not
parked. The pass needs that gate — it writes a summary from a model's reading of turns and ties it to
each one, and a turn still inside the formation window may yet park or be erased. Applied to the read
as well, it meant a subject whose turns were stored but not yet formed got `{"segments": [], "turns":
[], "characters": 0, "truncated": false}`, and the adapter that asked for it replaced the history its
framework held with nothing and reported success. Formation is asynchronous by design, so the gap was
as wide as the worker backlog, which for a subject mid-conversation covers exactly the newest turns
the next turn is about. For a parked turn the gap never closes: it exhausted its attempts, the
watermark moved past it, and it will never be formed and so never summarised.

Fixing the read then forced a second choice. A segment names a *contiguous* range because erasure has
to reach through it, and the pass plans that range over the turns that had formed — so a range closes
across a turn the pass skipped. Choosing a segment by offset alone would let a summary that never read
a turn's words stand in for them, permanently and invisibly.

**Decided.**
- The read path and the write path have separate readers. `SegmentStore.Formed` keeps the gate and
  stays the pass's; `SegmentStore.History` returns every stored turn of the subject and is the
  context's.
- A context replaces a turn with a segment only when that segment was written from it. The store
  reports this per turn from the registration the segment write made against the turn — the same row
  an erasure of that turn follows to delete the segment. So a turn counts as summarised exactly when
  a segment would be destroyed by erasing it: the substrate's own answer, not a range comparison.
- Every other turn is returned as it was said, in its place in the log, and costs the budget like any
  other. The loss a budget forces is therefore reported as `truncated`, and how far formation is
  behind is reported as the watermark. A short answer is one of those two and never silence.
- The registration is matched on the scope and the subject passed to the read, not on the turn's own
  columns. Correlating the subquery to `o.scope` makes it unindexable, and the planner answers it by
  scanning every segment registration in the project — so one person's context would cost more as
  other people were added. With both as parameters it uses
  `projection_dependency_subject_idx (scope, data_subject_id)`. On a project seeded with 45,960 turns
  across 81 subjects, the 5,000-turn subject's context reads 9,966 registration rows through that
  index rather than 50,286 through a sequential scan — 191 buffers against 877 — and what it reads is
  bounded by that subject's own history. That is a query plan, read on 2026-11-08; no latency figure
  is claimed, and none can be taken on a development machine.
- `TestAContextCarriesTheTurnsFormationHasNotReachedAndTheOnesItParked` holds both halves against a
  real deployment, and `TestATurnNoSegmentWasWrittenFromIsKeptVerbatimInsideTheRangeThatSpansIt`
  holds the assembly rule without one. Each fails on its own when its half is reverted.

**Rejected: keep the gate and document it.** Saying a context carries only formed turns would make
the implementation honest at the cost of the operation. An adapter's compaction replaces the
framework's history with the answer, so the answer has to be the history; a caller told to compare
`stored` against `formed` and decide for itself has been handed the problem the operation exists to
solve. It would also leave parked turns permanently unreachable through any read, which is the one
outcome worse than a stale answer.

**Rejected: a field reporting what was withheld.** A count of turns left out would make the loss
visible without returning the words. It is a new field on the response for a case that now cannot
arise, and it asks every adapter author to decide what to do about a number — where returning the
turns needs no decision from anyone.

**Rejected: deciding coverage from the offset range.** Simpler to compute, and wrong for exactly the
turns that matter most: the ones formation gave up on, whose words a summary never saw and never will.
The registration already answers the question correctly and is already indexed by the turn.

**Rejected: forming a turn on demand when a context is asked for.** It would close the window at the
cost of a model call on the read path, which is the one path paid for on every call, and it would put
extraction — the write path — behind a read. The operation's whole value is that it costs no model.

**What this does not cover.**
- Sizing the turns is still the expensive half of the read, and this does not touch it. Both readers
  compute each turn's size by joining `turn_message`, which nothing narrows to the subject, so the
  planner reads every message row in the project: 1,699 of the 1,982 buffers in the plan above.
  [#116](https://github.com/ensera-ai/taisce/issues/116) holds that, with the plan.
- A context is only as long as the budget, so a deployment whose model is unreachable fills that
  budget with stored turns and reports the cut. The newest are kept and the oldest are cut, which is
  a loss a caller can see rather than one it cannot — not a loss this removes.
- Formation is unchanged. A turn in a context is not yet answerable by a question, and the watermark
  is still the only thing that says so.
- Nothing here makes a parked turn form. Unparking is still the operator's action, and a parked turn
  stays outside every answer a question gives.

**Undo cost.** Low. One method on the store, one condition in the assembly rule, and one field on the
turn the store reports. No schema change — the registration it reads has existed since a segment was
first written — and nothing stored depends on it, so reverting changes what a read returns and leaves
every row as it is.

**Impact.** Correctness of the read path: the operation returns what its contract describes, and an
adapter that hands a model a context no longer hands it an empty one for a subject whose history is
newer than the worker backlog. Operations: a deployment whose model is unreachable keeps serving
contexts from stored text instead of serving empty ones.

## D17 — Every connection the chart builds states what it may open, and a release that over-declares is refused before it is applied

**Date.** 2026-11-08. **Issue.** [#101](https://github.com/ensera-ai/taisce/issues/101).

**Why it was open.** Every DSN the chart built omitted `pool_max_conns`. A pool told nothing sizes
itself at the number of CPUs the process can see, so each pod's connection ceiling was decided by
whichever node the scheduler put it on. Measured on a three-node cluster of 32, 12 and 4 CPUs: the
three API replicas behind one Service admitted **31, 11 and 3** concurrent requests, because
admission bounds work at one less than the memory pool. Which replica the Service picked decided
whether a request was served or shed. A load-shedding bound that is a property of scheduling is not a
bound anybody chose.

The second half is that nothing could notice. Each process compares its own pool against the slots
currently free and refuses to start when it is short, which catches a server that is already full.
Pools fill lazily, so at install every pod sees a nearly empty database and starts cleanly — seven
processes started here reporting 185 to 196 slots free while using 11. The sum of the declared
ceilings was compared against nothing, so over-declaration was invisible at install and would arrive
later as connection refusals on a deployment that came up green.

**Decided.**
- Each role carries its own ceiling as a value, on every connection string the chart builds:
  `api.memoryPool` 12, `api.registryPool` 2, `worker.memoryPool` 4, `manage.adminPool` 3, and 3 for
  the bootstrap Job. The API's is the top of the range this project's own sizing document gives,
  because the chart is the several-replica deployment and the API's pool is what admission follows:
  at 12 a replica serves eleven requests at once, five per project, two per credential.
- A ceiling per role rather than one shared number. The roles do different work against this
  database — a worker forms one turn at a time and resolves no credentials — and a single default
  wide enough for the busiest would be carried by every replica of the others.
- **The chart refuses to render when the declared demand, plus the 25% headroom the sizing document
  asks for, does not fit the `max_connections` it declares beside it.** The refusal names both
  numbers and what would satisfy it. Refused rather than warned, because a warning printed by
  `helm upgrade` is read after the fact and this is cheaper to correct before anything is applied.
- `deploy/helm/limits_test.go` finds every connection string in the rendering with a pattern rather
  than a list, so a role added later without a ceiling fails there; and it holds the refusal, the
  per-role values, and that raising one does not move the others.

**Rejected: deriving the pool from the container's CPU limit.** It keeps the ceiling a property of
configuration rather than of the node, and it needs no new values. It also keeps the number implicit,
so the admission bound is still something a reader computes from two other settings rather than one
the deployment states — and it ties request concurrency to a CPU limit that is set for a different
reason.

**Rejected: one pool value for every role.** Fewer knobs, and wrong in both directions at once: the
API's pool is an admission bound and the worker's is a queue depth for one turn at a time. Sized for
the API it multiplies the workers' demand against the database for nothing.

**Rejected: a warning instead of a refusal.** It would let an operator who means to over-declare
proceed. Nothing in this deployment benefits from over-declaring: the failure it buys is connection
refusals under load, on a release that installed cleanly, which is the hardest kind to attribute.

**Rejected: checking the sum at runtime instead.** A process can only see the slots that are free
now, which is why the existing check cannot catch this. Counting sibling processes would mean a
process that knows the topology, which is a coupling the deployment does not otherwise have.

**What this does not cover.**
- With `postgresql.mode=external` the DSNs come from a secret the chart never reads, so neither the
  ceilings nor the arithmetic apply there. They are the operator's to put in those strings, and the
  chart README says so.
- The numbers are a starting point from this project's sizing document, not a measurement. No
  latency or throughput figure stands behind 12 rather than 8 or 16, and the honest number comes
  from hardware that can be held still.
- The refusal arithmetic counts the processes this chart declares. An operator who runs something
  else against the same database — a pooler, a monitoring session, a migration from a laptop — is
  spending the headroom the 25% leaves, and nothing here can see that.

**Undo cost.** Low. Four values, one printf in a template, and a test. No schema change and nothing
stored; reverting changes what the next render produces and leaves every running deployment as it is.

**Impact.** Operations: the number of requests a replica serves at once is stated by the deployment
rather than by the node it landed on, and a topology the database cannot support is refused at render
rather than discovered under load. Performance: the admission bound stops moving between replicas, so
shedding becomes a property of the configuration.

## D18 — A dead peer is given up on by the database's keepalive, and formation's lock is not given a lease

**Date.** 2026-11-08. **Issue.** [#106](https://github.com/ensera-ai/taisce/issues/106).

**Why it was open.** Formation holds a session advisory lock for the whole drain of a scope. That is
the right shape — the drain spans a model call per turn, and a lock released at the end of the
transaction that picks a turn would be released before the call it exists to protect. A session lock
has one property a lease does not: it is released when the session ends and by nothing else. So "the
holder has gone" is a question only the database can answer, and it answers it by noticing the
connection is dead.

Nothing in the deployment bounded how long that took. The installed cluster had
`tcp_keepalives_idle`, `tcp_keepalives_interval` and `tcp_keepalives_count` all at `0`, which means
the operating system's defaults — on the Linux the database container runs, about two hours and
eleven minutes — and `idle_session_timeout` at `0`, so nothing else closed it either. For all of that
time every other worker asking for that scope is refused, which is correct behaviour producing the
wrong outcome, and a single-project instance is the common case and the worst one: one stuck lock is
the whole deployment's formation.

This is not the process-death case, which was measured on a cluster: a worker pod force-deleted with
no grace period had its lock released within three seconds, because the kernel on its node closes the
sockets of a process that no longer exists. It is the node-loss case — power, kernel panic, a severed
network — which is the failure several replicas exist to survive.

**Decided.**
- `tcp_keepalives_idle` is 60 seconds on the database the chart declares, as a value an operator can
  raise. The interval and count stay the operating system's: one idle bound moves detection from
  hours to a couple of minutes, and the other two have no number here that is better than the
  platform's.
- It is set on the database rather than in each client's connection string, so it closes the window
  for every session lock in the system — the rebuild lock and the bootstrap lock have the same
  exposure — rather than for the one that was noticed.
- `deploy/helm/limits_test.go` holds that the declared database sets it, that it is not zero, and
  that it is an operator's to raise.

**Rejected: giving formation a lease with a heartbeat.** It would bound takeover without depending on
the database noticing anything, and the embedding builds already carry a `lease_id`, so the pattern
exists here. It costs a heartbeat write per drain, and it introduces the failure a lease always
introduces: the holder is alive but slow, loses its lease, and two workers form the same turn. The
session lock has no such failure — it is held exactly as long as the holder's session exists. A
keepalive keeps that property and closes the window, so the lease buys a smaller window at the cost
of the guarantee the lock was chosen for.

**Rejected: `idle_session_timeout`.** It closes idle sessions, and a worker in the middle of a model
call is not idle in the sense the setting means — but a worker between drains is, so it would close
healthy pooled connections and make the pool churn. It bounds the wrong thing.

**Rejected: a liveness probe that releases the lock.** Nothing outside the holder's session can
release a session lock, by construction. An operator can terminate the backend, and that is the
documented manual remedy; a workload with permission to terminate backends is a workload that can
stop formation.

**What this does not cover.**
- With `postgresql.mode=external` the chart declares no database, so the keepalives are the
  operator's to set on their own server. The README says so.
- Sixty seconds is chosen against the platform defaults it replaces, not measured. Demonstrating the
  outcome needs a node removed or a partition imposed; what is measured is that nothing bounded it.
- Detection is not the whole window. After the keepalive gives up, the replacement worker has to
  reach its next tick, so the project's formation resumes some minutes after the node goes rather
  than immediately.
- Nothing here makes the stall visible while it lasts. The backlog grows and the freshness watermark
  falls behind, which looks the same as a slow model. That is [#105](https://github.com/ensera-ai/taisce/issues/105).

**Undo cost.** Very low: one parameter in the chart's values. It changes a setting on the next
reconciliation of the cluster and nothing stored depends on it.

**Impact.** Operations: a lost node stops a project's formation for a couple of minutes rather than
for over two hours. Security: none — this neither grants nor withholds anything, and the lock's
exclusivity is unchanged.

## D19 — The database parameters that are fractions of its memory limit are derived from it

**Date.** 2026-11-08. **Issue.** [#103](https://github.com/ensera-ai/taisce/issues/103).

**Why it was open.** The chart's default database asks for 2 CPUs and 8Gi with a 32Gi limit and a
250Gi volume, three times over, so the install command the README gave left the third instance
`Pending` on any cluster carrying other work. That alone is a sizing opinion. The defect was what
happened next: `shared_buffers` at 8GB, `effective_cache_size` at 22GB, `maintenance_work_mem` at
2GB, `max_wal_size` at 16GB and a 4Gi shared-memory volume were all written for the 32Gi limit beside
them. Lowering the limit on its own left a database configured to reserve more shared memory than its
container allowed, and it does not start — while the README's own instructions for shrinking the
deployment changed `instances` and the replica counts and nothing else. The smallest working change
was five values across two blocks, derived by hand from a limit the operator picked, none of it
documented and all of it needed before the first pod started.

**Decided.**
- The five are fractions of `postgresql.cnpg.resources.limits.memory`: a quarter for
  `shared_buffers`, eleven sixteenths for `effective_cache_size`, a sixteenth for
  `maintenance_work_mem`, a half for `max_wal_size`, an eighth for the shared-memory volume. The
  fractions reproduce the previous defaults exactly at 32Gi, so this changed no deployment that
  shrinking had not already broken.
- The shared-memory volume stays an eighth, which is always twice `maintenance_work_mem`: a parallel
  index build puts its work there, so a volume below that bound is one the build fails in. Keeping
  both as fractions of one number keeps that relation true without anyone maintaining it.
- Stating any of them in `postgresql.cnpg.parameters` still wins. These are the defaults underneath,
  not a ceiling over the top.
- **The defaults stay large.** A chart that is small by default is one somebody runs in production
  believing it was the large one, which is the worse failure.
- `deploy/helm/values-small.yaml` is the shape that fits an ordinary cluster, in the repository so it
  renders and is tested rather than being prose somebody retypes. What makes it short is the
  derivation: it states no parameter the limit now decides, and a test asserts that it does not.
- A memory limit written in a unit the chart cannot read is **refused**, naming why. Misreading a
  quantity by a factor of 1024 configures a database that will not start, for a reason nothing at the
  failure would point at.
- `work_mem` is not derived. It is memory per sort, so it follows how many queries run at once rather
  than how much memory the server has, and deriving it from the limit would tie it to the wrong
  number.

**Rejected: lowering the defaults.** It makes the chart install anywhere, and it makes the production
default the evaluation shape. The argument the chart already makes for its topology holds for its
sizing: the failure that matters is a deployment that is smaller than its operator believes.

**Rejected: documenting the five values instead.** A paragraph telling the reader to change five
settings in step with a sixth is the rule that gets broken under pressure, and it was already
half-written — the README's shrinking section existed and did not mention them.

**Rejected: named size presets.** A `postgresql.cnpg.size: small | medium | large` switch reads well
and hides the arithmetic. It answers only the shapes somebody thought of, and an operator with 6Gi to
spend is back to deriving by hand. Deriving from the limit answers every shape, including the ones
between.

**Rejected: deriving from the CPU request as well.** `max_connections` and `work_mem` do follow
concurrency, so there is a version of this that sizes them from CPU. Nothing here has measured that
relation, and a derivation nobody has measured is a guess with arithmetic in front of it.

**What this does not cover.**
- The fractions are conventional starting points, not measurements. No benchmark here says a quarter
  is better than a fifth, and the honest numbers come from hardware that can be held still.
- `max_connections` is not derived. It follows the pools the deployment declares, which D17 made a
  stated number and a render-time check.
- An operator who states one parameter and expects the others to follow it gets the derivation from
  the limit, not from their value. The limit is the one input.
- Nothing here makes the default topology schedulable. It is not meant to be: the small shape is the
  answer for a cluster without that headroom.

**Undo cost.** Low. Two template helpers, one merge in the cluster template, and a values file. The
rendered parameters at the default limit are byte-for-byte what they were, so reverting is visible
only to a deployment that has changed its limit.

**Impact.** Operations: lowering the database's memory limit is one value and the deployment starts,
where it previously needed five derived by hand and failed silently if any disagreed. Cost: trying
the chart on a cluster with ordinary headroom is one `-f` away.

## D20 — A worker that stands down is counted on the health row, not only in a log line

**Date.** 2026-11-08. **Issue.** [#105](https://github.com/ensera-ai/taisce/issues/105).

**Why it was open.** The chart runs two workers by default and invites more. What makes that safe is
that each scope is drained under an advisory lock, so a second worker finds the scope busy and stands
down instead of forming the same turn twice. The count of scopes that were busy existed for the
length of one pass and was then discarded, and the one log line carrying it sat behind a gate the
count was not in — so a pass whose every scope was held by another replica formed nothing, errored on
nothing, wrote no report and no segment, and logged nothing at all. The replica doing exactly what
the design intends was the silent one.

On a single-scope instance, which is what the default project makes and what most deployments will
be, every replica but one is busy on every pass. So on the most common topology, four healthy workers
and one healthy worker beside three that were wedged, crash-looping on a schedule that hid it, or
pointed at the wrong database produced identical evidence. Confirming the property the deployment is
sold on meant sampling `pg_locks` on the primary for the lock key and reading `pg_stat_activity` to
find which pod held it — not an answer an operator should reach for, and not available at all to
anyone who cannot open a psql session.

**Decided.**
- `formation_health` gains `stood_down_count` and `last_stood_down_at`, written by the driver after
  each pass that stood down. The ops centre shows both, because the instance page is where somebody
  asks whether their replicas are working and this is the answer to exactly that question.
- `Busy` joins the log gate as well. One line per worker per interval is the same volume the holder
  already produces, and it is the half that reaches whoever is reading logs.
- A pass that stood down for nothing writes nothing. The time means "a pass last stood down then";
  touching it on a pass that did not would make it mean "a pass last ran then", which the heartbeat
  already says.
- No scope name and no replica identity on the row. An operator asking whether their workers are
  serialising is asking about the instance, and a scope name would be the one piece of per-project
  state on an aggregate record.
- Not a judgement anywhere. Zero is normal on a single-replica deployment and expected on a
  multi-replica one until two workers want the same scope, so nothing is marked on this number.
- `TestAPassThatStoodDownOnEveryScopeStillReportsIt` holds both halves against a real deployment, and
  each fails on its own when its half is reverted.

**Rejected: the log line alone.** It is the cheaper half and it is in this change, but it answers
only whoever is reading logs. The reader the portal exists for is an operator looking at a page, and
a property the deployment is sold on should be visible to them.

**Rejected: a log line per stand-down.** The reasoning already in the code is right about volume —
in a deployment with several replicas this is the normal case — and wrong only in its conclusion.
Frequent is a reason not to log per occurrence; it is not a reason for the number never to be
reachable.

**Rejected: a per-scope or per-replica record.** It would answer which worker stood down on which
project, which sounds more useful and is a row per pass per scope on the formation path, written in
the common case, to answer a question nobody has asked. The aggregate answers "are my replicas
serialising", which is the question the issue was about.

**Rejected: deriving it from `pg_locks`.** The information is there and needs no write at all. It
needs a database session and a privilege the portal does not hold, and it reports the instant rather
than the history: a page refreshed between two passes sees nothing.

**What this does not cover.**
- The count says stand-downs happened, not that every replica is healthy. A worker that is wedged
  before it reaches the lock still contributes nothing, and the heartbeat is what speaks for that.
- It is instance-wide, so it does not say which project replicas are contending on.
- A single-replica deployment stays at zero forever, which is correct and indistinguishable from
  several replicas that have never wanted the same scope at the same moment.

**Undo cost.** Low. Two columns with defaults, one update statement, one gate condition and a tile.
The columns are additive, so a rollback of the code leaves them unread rather than wrong.

**Impact.** Operations: the mechanism the several-replica topology rests on is visible on the page an
operator already reads, instead of requiring a psql session against the primary.

## D21 — Whether a project's formation is failing is read from its backlog, not from a per-scope health row

**Date.** 2026-11-08. **Issue.** [#99](https://github.com/ensera-ai/taisce/issues/99).

**Why it was open.** `formation_health` is one row for the instance: one `last_progress_at`, one
`last_failure_at`. The ops centre's **Formation is failing** status and its **Failed attempts** tile
are read from it, so a turn forming in any project cleared the mark while another project failed
every attempt. An instance with one busy project and one broken one read as healthy on the page an
operator is told to watch. The evidence was present — the broken project's *Behind by* and *Parked*
counts rising — and drew no eye, and a rising backlog looks the same as a slow model.

**Decided.**
- A project is failing now when its backlog holds a turn that has been tried and did not form:
  unformed, not parked, and stamped with a failure. No second stamp and no window. A turn that formed
  has left the backlog and a turn the driver gave up on is parked, so what remains stamped with a
  failure is work still owed that has already gone wrong once — the backlog's own contents are the
  state.
- Counted from `observation` through `observation_backlog_idx`, a partial index holding exactly the
  unformed, unparked turns of a scope, which in a healthy project is empty. **No schema change and
  nothing on the formation path**: this adds no row, no counter and no stamp to the write path.
- The instance status takes the worse of the two readings, so a page that says nothing needs
  attention beside a failing project cannot happen.
- The projects table carries the count of such turns per project, not a boolean. One failing turn is
  a turn to park; every turn failing is an endpoint to look at, and a boolean loses the difference.
- `TestTheOpsCentreShowsAProjectFailingOnItsOwnWhileTheInstanceReadsAsRecovered` holds it, including
  that a parked turn is parked rather than failing and that a formed turn stops it with no stamp to
  reset.

**Rejected: a formation health row per scope.** The shape the issue reached for, and the one that
would make the existing stamp comparison work per project. It is a schema change, a migration and a
write on the formation path for every scope drained, to answer a question the rows already answer.
It would also need a rule for what the instance status says when one of forty projects is failing,
which this does not: the count is shown and the status is marked, and forty projects failing reads
differently from one because the number is there.

**Rejected: moving only the stamps per scope and leaving the counters instance-wide.** The middle
option, and it keeps the write on the formation path while splitting one record into two shapes that
have to be read together. The counters are lifetime totals nobody reads per project, which is the
part of the original issue's reasoning that holds; the conclusion that follows is to leave the row
alone entirely.

**Rejected: a time window on `formation_failed_at`.** "Failed in the last N minutes" needs a duration
nobody here has measured and is wrong in both directions on a quiet project — a project whose
attempts are further apart than the window reads as healthy in the gaps. The backlog needs no
duration.

**What this does not cover.**
- It says a project is failing, not why. The reason is on the turn, and the project page lists it.
- A project whose every turn has already parked reads as not failing, which is correct — it is
  reported as parked, a different thing to act on — but means a project that gave up entirely stops
  being marked as failing. Parked is the louder signal at that point.
- The instance-wide stamps are unchanged and still answer for the instance. They are not wrong; they
  were being asked a question they cannot answer.
- This is a count per project on a page, not an alert. Nothing notifies anybody.

**Undo cost.** Very low: one query, one view-model field and a table column. Nothing is stored, so
there is nothing to migrate back.

**Impact.** Operations: a project failing on its own is visible on the instance page and in its own
row, where before it was visible only as a backlog that looked like a slow model. Performance: one
aggregate over a partial index per page load, which in a healthy instance reads an empty index.

## D22 — A turn carries its own size, because sizing one subject's history was reading every message in the project

**Date.** 2026-11-08. **Issue.** [#116](https://github.com/ensera-ai/taisce/issues/116).

**Why it was open.** Both readers of a subject's history computed each turn's size in the query, by
joining `turn_message` and summing the length of every role and content. That table carries neither
scope nor subject — it is reached through the observation — so nothing narrowed the join, and the
planner read every message row in the project to size the turns of one person in it.

Measured on a project of 45,960 turns across 81 subjects. Asking for the 5,000-turn subject found its
own rows through an index in 92 buffers and then spent 1,699 sizing them: 85% of the 1,982 the whole
query touched, reading eighty other subjects' messages to answer a question about none of them. The
scan grows with the project while the answer does not, so two subjects of the same length cost
different amounts depending on who else the project holds, and every subject's context gets more
expensive when a new subject is added. `POST /v1/contexts` is on the read path, paid for on every
call.

**Decided.**
- `observation.content_characters` holds the characters of that turn's message roles and contents,
  written when the turn is written. Both readers take it off the row they are already reading.
- Counted in Go, from the turn in hand, rather than from the rows afterwards. The column is `NOT
  NULL` with no default, and a `NOT NULL` column cannot be deferred to the end of a transaction — so
  computing it after the messages were inserted would need either a nullable column or a default,
  and both of those let a write path that forgets it store a turn a context values at nothing. This
  way the substrate refuses such a path at the moment it tries.
- Code points, not bytes. That is what the budget is documented in and what the readers computed
  before this was stored; a Go `len()` would count bytes and quietly change what a budget buys for
  anything but ASCII. A test holds a turn of multi-byte text against both counts.
- Safe to store because a message's role and content are written once, in the transaction that
  creates the turn, and never updated afterwards — the one write to `turn_message` outside that path
  sets `occurred_at`.
- `TestATurnCarriesTheSizeSummingItsMessagesWouldHaveGiven` holds that the stored value equals what
  the join computed, for every turn in the schema rather than for the one just written, so a backfill
  that missed rows fails there.

**Measured, on the same project and the same question** (`EXPLAIN (ANALYZE, BUFFERS)`, read
2026-11-08, after a vacuum):

| Sizing | Buffers | What the cost follows |
|---|---:|---|
| Joining `turn_message`, as it stood | 1,985 | every message row in the project |
| A correlated scalar subquery per turn | 20,283 | four buffers per turn of the subject |
| On the observation row | **305** | the subject's own rows, and nothing else |

The message table is not read at all, so the term that grew with the project is gone rather than
reduced. These are plans and buffer counts; no latency figure is claimed, and none can be taken on a
development machine.

**Rejected: rewriting the query.** The issue's first candidate, and the one that needs no schema
change. A correlated scalar subquery bounds the work by the subject and cannot be planned as a scan
of the whole table — and costs four buffers a probe, which took the same answer to 20,283 buffers.
On a 512-turn subject it was 2,060 against the join's 1,714. So the join wins on a project whose
messages are few relative to the subject asked about, and the subquery wins on a large project with
small subjects: each shape wins on a different shape of data, which is the plan that flips the issue
warned about rather than a fix. Measured before being rejected, because the opposite was expected.

**Rejected: an index that narrows the join.** `turn_message` carries no scope and no subject, so
narrowing it needs a denormalised column on that table — which is the same stored value as this
decision, written once per message instead of once per turn, and still joined on every read.

**Rejected: a generated column.** A stored generated column would make the database compute it and
remove the possibility of a write path forgetting. PostgreSQL's cannot reference another table, and
the messages are in another table.

**What this does not cover.**
- The registration probe is now the larger half of the read, at 191 of the 305 buffers. It is bounded
  by the subject's own history ([D16](#d16--a-context-carries-every-turn-the-subject-said-and-a-summary-stands-in-only-for-the-turns-it-was-written-from)),
  so nothing here grows with the project, but it is where the next measurement should look.
- The backfill rewrites every observation row once, so an upgrade on a large deployment pays a full
  table rewrite and the table is twice its size until it is vacuumed. Measured here: the heap scan
  read 380 buffers before a vacuum and 305 after.
- Nothing is claimed about latency. The buffer counts are what a plan reports.

**Undo cost.** Low for the code, moderate for the column. Reverting the readers to the join is two
queries; the column would be dropped by a migration, and a deployment that had written turns with it
would lose nothing, because the value is derivable from the messages it was computed from.

**Impact.** Performance of the read path: sizing one subject's history stops reading the project, so
`POST /v1/contexts` costs what the subject holds rather than what the instance holds. Operations: the
cost of a context no longer rises when an unrelated subject is added.

## D23 — The adapter conformance suite runs on each adapter's main, against a released deployment it stands up itself

**Date.** 2026-11-08. **Issue.** [#109](https://github.com/ensera-ai/taisce/issues/109).

**Why it was open.** The suite holds ten rules that are easy to get subtly wrong once per language
and impossible to notice from inside one adapter. It is the only artefact in the project that holds
them for all three adapters at once, and it executed only when a person ran `taisce conformance` by
hand and looked at what it printed. Read on 2026-11-08, none of the three adapter repositories'
`ci.yml` ran it: each mentions it in a header comment and runs deployment-free tests. So each
adapter's own suite could stay green while its behaviour diverged from the other two, and the
divergence that matters most is the quiet one — an adapter that swallows a store failure loses
turns, and a lost turn is invisible until somebody asks what the system holds about them.

**Decided.**
- A `conformance` workflow in each adapter repository, on push to `main`, on demand, and callable
  from a release workflow.
- It stands up the quickstart's compose stack at a released version and runs that release's binary
  as the suite runner, so an adapter is proven against what an operator actually installs rather than
  against a build of the day.
- **No model key.** The suite stubs the model inside each driver and seeds facts through the API, so
  the stack is given a value that is not a key beside an allowlist naming no reachable host. That was
  checked rather than assumed: the stack comes up and all ten cases pass with nothing a provider
  could be reached through.
- **The worker is not started.** Without a model it can never become healthy, and formation is not
  what the suite tests. The workflow waits on the API, which is what an adapter talks to, and says in
  its own text that a green run therefore proves nothing about extraction.
- The binary is verified against the checksums published with the release. `compose.yaml` and
  `cases.json` are fetched at the **commit** the release was cut from, not the tag: a commit cannot
  be moved, which is the same reason every action in these workflows is pinned by SHA.
- Each adapter is run through its own repository's documented entry point — the module for the
  Python adapters, `scripts/conformance.sh` for the others — so what CI runs is what the README tells
  a person to run, rather than a path nobody else takes.
- The reference adapter runs first, so a failure afterwards is the adapter under test rather than the
  stack it was run against.

**Rejected: on every pull request.** A deployment per pull request costs runner minutes on every
change, including the ones that touch a README. The rules it holds change slowly, and a divergence
introduced in a branch is caught when that branch reaches `main`, before any release carries it.

**Rejected: on release only.** Cheapest, and it lets a broken adapter sit on `main` between releases
— so the failure is found by whoever cuts the release, at the moment it is most expensive to fix.

**Rejected: publishing `compose.yaml` and `cases.json` as release assets.** Attempted first, so that
one checksums file would cover everything a run trusts. They would exist only from the next release,
so the workflows could not run until one was cut — and a commit SHA is already immutable and already
the standard this project pins actions by. Built, measured against what it would buy, and removed.

**Rejected: a reusable workflow shared from this repository.** One file instead of three near-copies.
It makes every adapter's CI depend on a workflow in another repository resolving, and the three
differ in toolchain and in how many drivers they run, which is most of the file.

**What this does not cover.**
- Extraction. The deployment under test reaches no model, so nothing here exercises forming a turn
  into facts. `make test-inference` is what covers that, against a real provider.
- The release workflows do not yet refuse to publish a version whose conformance run did not pass.
  The workflow is `workflow_call`-able so that they can, which is a change in each adapter's
  `release.yml` and is not in this one.
- The version each adapter is tested against is pinned in its workflow and raised by hand. An adapter
  proven against an old release is not proven against the newest one, and nothing here notices.

**Undo cost.** Very low: one file per repository, deleting which returns each to deployment-free CI.
Nothing in this repository changes except the suite's own README.

**Impact.** Correctness across the adapters: the ten rules are exercised in three languages without
anybody remembering to run them, and the evidence an adapter release carries stops being pasted
terminal output nobody else can re-execute. Security: the run holds no credential of anybody's — the
deployment is created and destroyed by the job, and the key it is given reaches nothing.

## D24 — The clients erase and export, typed, and no model reaches either

**Date.** 2026-11-09. **Issue.** [#127](https://github.com/ensera-ai/taisce/issues/127).

**Why it was open.** The service declares 35 operations, and the Python, .NET and Java clients send 9
of them, which is what an agent loop needs: observe, freshness, recall, context, passage search, the
three artifact calls, and citation resolution. Proving what it deleted is one of the four things this
system claims to do that a vector store does not, and export is the other half of the governance it
claims. Neither was reachable from the library a developer already depends on. A person's erasure or access request reaches the
*application*, which at that moment holds the person's subject id and a project credential. Sending
that request on to the CLI, the portal or hand-written HTTP leaves the claim true of the deployment and
false at the seam where the developer works.

The one fact that could have changed the design: whether the credential an application holds is
allowed to erase. Read from `internal/api/authorization.go` on 2026-11-09, `read_write` permits every
operation, and `read_only` permits `export` but not `erase`. So a typed method needs no second
credential. It gives no authority the key did not already have; it only makes that authority easier to
reach.

**Decided.**
- The three clients (`taisce` for Python, `Taisce.Client`, `taisce-client`) each gain erase and export
  as typed methods. **By subject** and **by source observations** are separate methods, not one method
  with two optional arguments. The service refuses a request that names both or neither, and a
  separate method for each means that request cannot be written in the first place, rather than being
  a `400` found at runtime.
- **An erasure whose receipt is not clean is an error**, and the error carries the whole receipt. The
  residual count is the product: a receipt returned as a plain value lets an application that never
  reads it tell a person their data is gone while rows still match. Raising makes ignoring it
  impossible, and the receipt is still there for whoever handles the error.
- `reason` passes through to the receipt. A read-only key's `403` and an oversized export's `422
  export_too_large` each surface as their own typed error, not as a generic failure.
- **No model reaches either one.** The methods live on the client and nowhere on a framework adapter's
  model-facing surface (context providers, stores, tools), and the service's MCP tool list, which
  already omits both, stays that way. Text that reaches a model is written by somebody else, and
  erasure cannot be undone.
- Each client proves it against a live deployment: a clean receipt with residual counts, an erasure by
  sources, a read-only key refused on erase and allowed on export, and an export whose row counts match
  its sections. A conformance case holds the receipt rule for all three adapters at once.

**Rejected: the clients stay sized to the agent loop, and governance stays with the CLI and the
portal.** This keeps the client contract small, at the cost of the claim. The developer who reads
"erasure with a counted residual" and looks for it in the client finds nothing, and a second tool to
learn is where a deletion request gets handled late or by hand. Leaving the methods out does not
remove the authority either: the same key reaches the route over HTTP.

**Rejected: the full service surface in every client.** Records, entities, subjects, feedback and
notifications in three languages would be three times the contract to keep in step. Most of it, such
as entity purge, feedback promotion and notification endpoints, has no caller inside an application
that is serving an agent. The second use case will show which of these belongs in a client; guessing
now builds for a reader nobody has seen.

**Rejected: an erase tool the model can call.** It would let an agent act on "forget everything about
me" with no code. It would also let anyone whose text reaches the model ask for an erasure, of the
speaker or of somebody else, and a prompt-level instruction is not a bound. Whether a request to forget
is genuine is the application's to decide, in code, before it calls the method.

**Rejected: the client requires a separate erasure credential.** A rule the client enforces is a
convention, and the route still accepts any read-write key. If an agent's key should not be able to
erase, that boundary belongs in the service's access levels, and that question is #130.

**Rejected: return the receipt and let the caller check `clean`.** This is the more conventional
shape, and it is wrong in the one way that matters here: the failure it allows is a person told
something false about their own data.

**What this does not cover.**
- Every read-write key, including the one in an agent process, can erase every subject in its
  project. This decision makes that easier to reach from code; it does not widen it. Whether it
  should hold is #130.
- An export larger than one response is refused, not paged. The service has no paging for exports,
  and the clients do not invent one.
- The framework adapters gain nothing. There is no Go client (#128).

**Undo cost.** Low until a client release carries the methods. After that, removing them is a breaking
release in three registries. The rule that no model reaches erasure costs nothing to keep, and it is
the part to reopen last.

**Impact.** Security: no new authority. There is a new path from application code to an operation
that cannot be undone, deliberately kept off the path from model output. Governance: erasure and
export become reachable where the request arrives, and the receipt becomes impossible to ignore by
accident. Work: [#131](https://github.com/ensera-ai/taisce/issues/131) Python,
[#132](https://github.com/ensera-ai/taisce/issues/132) .NET,
[#133](https://github.com/ensera-ai/taisce/issues/133) Java,
[#134](https://github.com/ensera-ai/taisce/issues/134) conformance.

## D25 — A turn's key is a digest of length-prefixed fields, and one duplicate per in-flight retry is accepted at the change

**Date.** 2026-11-09. **Issue.** [#121](https://github.com/ensera-ai/taisce/issues/121).

**Why it was open.** Every adapter derives an observation's idempotency key from the turn, so that a
retry carries the same key and the deployment folds it into what it already holds. The derivation
joined the subject, the run and each message's role and content with newlines. Content can hold a
newline followed by a role name, so where one field ended could not be recovered from the bytes
hashed: two different turns in one run produced one key, and the deployment, which fingerprints the
turn behind a key, refused the second as a conflicting retry of the first. The refusal is loud, but
the key is deterministic, so the second turn was refused on every retry and could never be stored.
The suite asserted only that the same turn gets the same key, never that different turns get
different ones.

**Decided.**
- The key is SHA-256 over netstrings — length in UTF-8 bytes, `:`, the bytes, `,` — of
  `taisce-turn-key/2`, the subject, the run, and each message's role and content, shaped as a
  version-5 UUID. The specification is in `conformance/README.md`. `conformance/turn-keys.json` holds
  vectors written by an implementation outside the runner, which the runner's own test and every
  adapter's tests read.
- The derivation's name is the first field hashed, so no key from this derivation can equal one from
  any other, including the next.
- A conformance case, `different_turns_are_never_one_key`, runs two turns whose line-joined text is
  identical and expects both stored under different keys. The derivation it replaces is kept in the
  reference driver behind a knob, and the suite is watched refusing it.
- **Accepted: one duplicate per turn in flight across the upgrade.** A turn whose first attempt was
  sent by an adapter on the old derivation and whose retry is sent by one on the new derivation
  carries two keys and is stored twice. That needs a process upgraded between a failed attempt and
  its retry, and the cost is one duplicated turn, which recall and erasure both handle like any
  other.

**Rejected: deriving the key from the representation the deployment fingerprints.** One definition of
"the same turn" held in two places sounds stronger. But that representation is the deployment's
internal normalisation, which may change with a migration. Every adapter would have to track it, and
a change there would silently change every key in every language. The key is a contract between an
adapter and its own retries, and a contract is better stated where it can be versioned.

**Rejected: a canonical JSON encoding of the turn.** JSON canonicalisation differs across the
languages' libraries in escaping, number forms and Unicode normalisation. Byte-identical keys in four
languages would rest on four serialisers agreeing on details none of them promises.

**Rejected: escaping the separator.** It works, and it is a second encoding to get right in every
language, with its own edge cases (an escape character in content). A length prefix needs no rule
about content at all.

**Rejected: sending both keys during a migration window.** It would remove the duplicate at the
change, at the cost of a contract field the deployment carries forever for a window that closes on
the first retry after an upgrade.

**What this does not cover.** A key derived by an application that bypasses the adapters is the
application's own. The deployment's fingerprint still refuses a reused key with a different turn,
whatever produced it.

**Undo cost.** Moderate. Changing the derivation again is a new derivation name, new vectors and a
change in four adapters, and it costs the same one duplicate per turn in flight. Nothing stored
depends on how a key was derived.

**Impact.** Correctness: a turn whose text happens to, or is steered to, read like another turn in
the same run is stored, rather than refused for good. Security: model output is an input to the key,
so this closes a path by which content the model was steered to produce could make a later
legitimate turn unstorable.

## D26 — An agent key writes memory and cannot destroy it, and is what a key is issued as by default

**Date.** 2026-11-09. **Issue.** [#130](https://github.com/ensera-ai/taisce/issues/130).

**Why it was open.** A project credential was `read_only` or `read_write`, and `read_write` permitted
every operation. An agent process has to write turns, so every key in an agent process could erase
any person in its project, by subject or by up to ten thousand sources, and purge any entity. The
erasure leaves an audit row and a receipt, so it is not invisible, but it cannot be undone. And the
key in an agent process is the one most exposed: it sits beside a model that reads text other people
wrote, in a process that is often not the application's most guarded. D24 made erasure reachable
from the clients' typed methods without widening this; this entry narrows it.

**Decided.**
- **A third access level, `agent`**, between the two. It holds every read `read_only` holds, plus
  an explicit list of writes: observe, assert, correct and retract records, record and promote
  feedback, put and delete artifacts, register and update subjects. It is refused **erasure** and
  **entity purge**, the two operations that destroy data with no way back. It is also refused
  **registering or disabling a notification endpoint**, which destroys nothing but chooses where this
  deployment makes outbound requests: the operator's decision, and no agent's work.
- The list is an allowlist, like `read_only`'s. A new operation is refused to an agent key until
  somebody classifies it, and a test that walks every declared operation fails until they do.
- **A key issued without naming its access is `agent`**: from `taisce credential issue`, from the
  management surface with `access` omitted, and from the ops centre's form, whose first option it is.
  The registry column's default is `agent` too, and its constraint holds the set of three.
- **The bootstrap's first key stays `read_write`.** It is the project owner's key, printed once, and
  it is what the application's erasure path is set up with. The quickstart's erasure step uses it.
- **Existing keys keep the access they were issued with.** Nothing here narrows a key somebody is
  using.
- `--read-only` keeps working and means `--access read_only`. Given beside a different `--access`, it
  is refused rather than one of the two winning.

**Rejected: leave it, and record that an agent key is a project key.** It costs nothing to build.
But it leaves the irreversible operation in the least guarded place, the process whose inputs are
written by strangers, for no reason an agent ever needs it.

**Rejected: take erasure out of `read_write`, and add a new level that has it.** It is the same set of
levels with a cleaner name for the narrow one. It would silently remove erasure from every key
already issued, and an application whose erasure path suddenly answers `403` fails a person's request
for erasure. That is the failure this whole area exists to prevent.

**Rejected: per-operation grants on each credential.** It is the most general, and it means a grant
set to design, store, migrate and show in the ops centre for every key. Three ordered levels answer
the question that was actually asked: which key may destroy data. A fourth question would be the
second use case that shows whether grants are needed.

**Rejected: the bootstrap key as `agent` too.** It is the more uniform default, and it would leave a
fresh project with no key able to honour an erasure request, unless the operator first learns to issue
a second one. The first key is the owner's, and the owner is who answers for erasure.

**What this does not cover.**
- An `agent` key can still write any turn under any subject and any role, until it is revoked. It
  cannot destroy memory, but it can write memory that recall returns. Formation's speaker rule and
  the operator's review are what bound that, not this.
- An `agent` key can retract and correct records. A retraction keeps the history it withdraws from,
  so it is reversible by a correction. It is not an erasure.
- The level is only as good as where the keys go. An application that hands its `read_write` key to
  its agent process has the old exposure back, and nothing here can tell.

**Undo cost.** Low to add, awkward to remove. Taking the level away means deciding what every key
issued as `agent` becomes, and both answers are wrong for somebody. Changing the default back is one
line in three places.

**Impact.** Security: the key most likely to leak can no longer erase a person or purge an entity, and
can no longer choose an outbound destination. That is the worst path #130 named, closed in the
substrate (a column constraint and an allowlist), not by convention. Operations: an operator issuing a
key for an erasure path now has to say `--access read_write`. That is deliberate friction, written
into the CLI's help and the operator guide.
