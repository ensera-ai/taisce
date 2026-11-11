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

## D27 — There is a Go adapter, on the Agent Framework's Go context provider, held to the same suite

**Date.** 2026-11-09. **Issue.** [#128](https://github.com/ensera-ai/taisce/issues/128).

**Why it was open.** The adapters existed for Python, Java and .NET. A Go application reached Taisce
only through hand-written HTTP or the CLI, because every package in the service's module is under
`internal/`. The Agent Framework has a Go implementation, `microsoft/agent-framework-go`, whose
`ContextProvider` has the two hooks the other adapters use: `Invoking`, which returns the messages a
run will send, and `Invoked`, which carries the run's error. But it was at `v0.3.0`, before 1.0, and
the recommendation in #128 was to wait for 1.0. The owner chose to build it now.

**Decided.**
- A fourth adapter repository, `ensera-ai/taisce-go`, one module (`github.com/ensera-ai/taisce-go`)
  with two packages. The root package `taisce` is the client: observe, freshness, recall, context,
  citations, and the typed erasure and export of D24. `agentframework` is the adapter: a
  `ContextProvider` that recalls before a run and observes after it, and a `Compaction` provider that
  replaces a long loaded history with the deployment's context.
- It keeps every rule the others keep, and it is proved the same way: its driver passes every
  conformance case against a live deployment, its `conformance` workflow stands one up, and its live
  tests run erasure and export against it. Its key is D25's derivation, checked against the suite's
  vectors.
- The client holds the same transport rule as the other clients. It refuses plaintext to a host
  that is not loopback, and it follows no redirect, since Go's standard client would otherwise
  resend the credential to the same host.
- The framework is pinned at the version in `go.mod`, and CI tests that version and nothing newer.

**Rejected: no Go adapter until the framework reaches 1.0.** It is the cheaper position, and it was
the recommendation. It costs a Go developer on the Agent Framework an adapter that exists in three
other languages, for a risk that can be contained: the upstream seam is two methods, and a break
there is one file to change, found by the suite on the first run against a new version.

**Rejected: a Go client with no adapter.** Typed access without a framework commitment. But a
client is plumbing sized to serve an adapter, and one with no adapter serves nothing anybody has
named.

**Rejected: the client and the adapter as two modules.** A consumer of the client alone would not
download the framework's dependencies, but it would mean two versions to tag and keep in step, for a
client whose only reader is the adapter beside it. Go's module graph pruning already keeps the
framework's code out of a build that does not import `agentframework`.

**What this does not cover.**
- An upstream release that changes the `ContextProvider` seam. Nothing here notices it until somebody
  raises the pinned version.
- Frameworks other than Microsoft's. The client is usable from any Go code; an adapter for another
  framework would be its own decision.

**Undo cost.** High once a version is tagged. The Go module proxy and checksum database keep every
published version for good, and a `retract` directive can only warn about one. Before the first tag,
it is one repository to archive.

**Impact.** Operations: a fourth adapter repository to keep in step with every rule the suite holds,
and a fourth CI to watch. Security: the same boundary as the other three: an untrusted user message
in, the person's words and the final reply out, and no erasure or export reachable from a model.

## D28 — Where CI runs: GitHub-hosted by default, own hardware only for trusted triggers, and only once a measurement says it pays

**Date.** 2026-11-09. **Issue.** [#136](https://github.com/ensera-ai/taisce/issues/136).

**Why it was open.** Runners on the project's own hardware, already serving a self-hosted GitLab, were
proposed as faster or cheaper CI. Two shapes were on the table: GitHub Actions jobs on runners hosted
on that hardware, or GitLab becoming the source of truth and push-mirroring to GitHub so CI could move
to GitLab's runners. Nothing had been measured: not the wall-clock time `make gate` takes on that
hardware, not Actions minutes, not variance.

**Decided.**
- **GitHub stays the source of truth.** The mirror stays one-way, from GitHub outwards.
- **CI stays on GitHub-hosted runners until a measurement says otherwise.** That measurement is
  `make gate` on one candidate host and on `ubuntu-latest`, three runs each, recording wall-clock
  time and variance in a dated document. If the hardware is not clearly better, nothing changes.
- **If own runners are adopted, they run trusted triggers only.** That means pushes to `main`, manual
  runs, and benchmark jobs, chosen by a `runs-on` expression that depends on the trigger, in a runner
  group limited to this organisation's repositories. A fork's pull request never runs on them: that
  would be a stranger's code inside the project's network.
- **The release never runs on them.** D6's Build Level 2 claim rests on a hosted build platform and
  on keyless signing bound to GitHub's identity. Moving the release would reopen D6.

**Rejected: GitLab as the source of truth, mirroring to GitHub.**
- It needs a stored GitHub token able to write `main` and workflow files, and branch protection
  would have to let that token past it. That ends "a pull request and a green build" on the public
  repository.
- A public GitHub repository cannot turn pull requests off. Contributors' pull requests would still
  arrive there and would have to be re-applied by hand, or the mirror push would fail on divergence.
- Every workflow in four repositories would be rewritten, and publishing would need stored registry
  keys in place of the identity exchanged at the moment of use. Rule 8 prefers the second.

**Rejected: adopting own runners now.** Rule 10: a second thing to operate has to beat the simpler
thing with a number, and there is no number yet.

**What this does not cover.** Performance evidence. If a held-still host is adopted for benchmarks,
what it may claim is decided where those benchmarks are, not here.

**Undo cost.** Low. Runner labels are a line in a workflow, and the default is the state that exists
today.

**Impact.** Security: no stored push credential anywhere, branch protection unchanged, and no
untrusted code on the project's hardware. Cost: none until a measurement justifies one.

## D29 — An operator extends the closed vocabulary over the management surface, and the vocabulary stays closed

**Date.** 2026-11-09. **Issue.** [#141](https://github.com/ensera-ai/taisce/issues/141).

**Why it was open.** The closed predicate vocabulary is what makes a relation queryable, a cardinality
decidable and supersession possible, and a foreign key rather than a prompt enforces it. None of that
was in question. What was in question is who may extend it: the rows were seeded by migration
`0003_a_closed_predicate_vocabulary.sql`, and no route, command or portal path added one. So the single
surface the design has for fitting a deployment to its own domain was reachable only by connecting to
the database as an identity the running service does not hold. A deployment whose conversations are
about clinical visits or shipments hits relations the vocabulary cannot name, every one is refused, and
the operator can neither widen it nor easily discover it is happening.

**The fact that decided it.** `internal/extract/identity.go` already folds `e.vocabulary.All()` into
`PipelineVersion()`, noting that vocabulary order is significant because the model sees it in that
order. So a vocabulary change *already* changes the extractor identity every fact carries, and a
rebuild can *already* find what the change invalidated. The question this raised was whether that chain
would have to be built; it exists. And the table bounds what an extension can be: `predicate` must
match `^[a-z][a-z_]*[a-z]$`, `cardinality` is `one` or `many`, `object_kind` is one of six values and
`semantic_type` one of eleven, each closed by a `CHECK`. An operator extending the vocabulary chooses a
name inside three closed axes; they do not invent an ontology.

**Decided.**
- Add and list are management operations on `/manage/v1`, recorded on the ledger with the operator as
  principal, with a CLI verb over the same operation.
- **All five declarations are required, with no defaults.** `cardinality` especially: supersession rests
  on it, it cannot be inferred later because by then all that is visible is two rows, and a guessed
  value closes a fact that should have coexisted or keeps two that should have superseded. A default
  here is a wrong answer months later.
- **The refusals are the database's.** The handler does not restate the name pattern or the closed axes
  in Go; it lets the `CHECK` constraints refuse and reports which one refused. A rule enforced in two
  places is a rule that will disagree with itself.
- **Nothing is removed and nothing is renamed.** The foreign key is `RESTRICT` by design, so retiring a
  predicate facts still use fails loudly rather than deleting memory, and a rename is a second spelling
  of one relation. A `description` correction is permitted by this entry but **is not exposed**: no
  caller needs it yet, and an operation with no reader is not built.
- **The authority did not widen, and not by a grant.** There is no `manage` database role. The
  management surface serves over the administrative connection, which owns the namespace, so granting
  it insert on one table would be a no-op and "nothing further" is already untrue of an owner. What
  bounds this instead is the identity an agent's traffic actually reaches the database through: the
  memory login holds `SELECT` on the vocabulary and nothing else, which
  `TestTheMemoryIdentityReadsTheVocabularyAndCannotChangeIt` proves by watching `INSERT`, `UPDATE`,
  `DELETE` and `TRUNCATE` all refused while `SELECT` works. The vocabulary is not in `memoryWrites` and
  this entry is why it stays out.
- **The project named on the operation is attribution, not a partition.** The vocabulary is one table
  per tenant schema and every project in that instance shares it, which
  `TestTheVocabularyIsSharedByEveryProjectInAnInstanceAndByNoOther` holds. The project is still
  required and must be active, because the operation is recorded against it, but "a vocabulary per
  project" is a wrong mental model and is written down here so nobody builds on it.
- Adding a predicate changes the extractor identity, so facts formed afterwards carry a different one.
  That is the record of what changed, not a side effect to hide.

**What this does not cover.** A formation worker loads the vocabulary at startup, so a relation added
while it runs is admitted by the foreign key and still refused as `unmapped_relation` until the worker
restarts — [#164](https://github.com/ensera-ai/taisce/issues/164). And the cardinality constraint
cascades on update, so a hand-written rename would rewrite every fact carrying it, which the delete
side's reasoning already argues against — [#165](https://github.com/ensera-ai/taisce/issues/165).
Neither is closed by this entry, and the limit belongs beside the mechanism.

**Rejected: leave it to migrations.** The status quo, and it has the real virtue of putting no new write
path near extraction. Rejected because it prices a vocabulary that fits the deployment at one release
per deployment, and because "reachable only as an identity the service does not hold" is not a
boundary, it is the absence of a mechanism.

**Rejected: let the operator add a semantic type or an object kind.** Those are closed sets a `CHECK`
enforces, the read path organises a bundle along `semantic_type`, and an unresolvable end is judged
against `object_kind`. Opening either makes the axis mean nothing, which costs more than the relation it
would admit.

**Rejected: let a model propose predicates from the deployment's own corpus.** The closed set exists to
keep that decision out of a model's judgement per message. It would also put sampled customer content
into an artefact nobody reviewed, on the path where a planted instruction becomes a stored fact.

**Rejected: deleting a predicate.** `RESTRICT` is the design. An operator who wants a relation gone
wants the facts gone, and that is erasure or an entity purge, each of which already counts what it
removed.

**Undo cost.** Low for the operation: additive, the table already existed, and withdrawing the route
leaves the data untouched. Not undoable for the data — a predicate that has been used is referenced by
rows, and `RESTRICT` means it stays. The operation is cheap to reverse and its consequences are not,
which is why the five declarations are mandatory.

**Impact.** Operations: a deployment fits its domain without waiting for a release. Security: no new
database authority — the operation runs on the connection that already owned the namespace, and the
memory identity's reach is unchanged and now tested. Governance: no erasure or receipt guarantee moves;
a predicate is not memory.
## D30 — A retrieval result is published with its conditions, or no number is published at all

**Date.** 2026-11-09. **Issue.** [#142](https://github.com/ensera-ai/taisce/issues/142).

**Why it was open.** Every comparable product publishes a retrieval score and this one publishes none,
so a developer evaluating it finds a blank where they expect a number. The question was whether to fill
it.

**What was read on 2026-11-09 decided it.** LoCoMo's answer key is wrong in 99 of 1,540 questions,
putting the ceiling at 93.57%, and its judge accepted 62.81% of answers written to be vague and wrong.
One system's LoCoMo score is reported as 65.99%, 75.14% and 58.44% by three parties who disagree about
adversarial questions, timestamps and API version. Changing only the embedding model, with a pipeline
otherwise identical, moved LongMemEval-S accuracy by 6.2 points. A study over planted facts with
validity intervals found rankings invert with the time horizon, and full history tying or beating the
best memory system at three weeks. And no product read publishes a p50 or p95 retrieval latency with
its conditions stated. The sources are named in `docs/03-market.md` with the dates they were read.

**Decided.**
- **No bare score is published, ever.** A number without its conditions joins a pile whose conditions
  nobody states, and it would be quoted back at a width it cannot carry.
- If a retrieval number is published it is on **LongMemEval-S**, and it carries, beside the figure: the
  embedding model, the answer model, the judge, the corpus and its size, the hardware, the number of
  runs, and the write cost paid to produce the memory being read. The harness lives in the repository
  so the figure can be re-run by whoever doubts it.
- The figure is described as what it is — this system **and that answer model**, scored by **that
  judge** — because LongMemEval-S scores answers rather than retrieval, and a reader told otherwise
  would take it for a property of the storage.
- Until such a run exists, the documentation says plainly that no comparable number exists and why,
  rather than leaving the blank to be read as an oversight.
- **Stating the conditions is the position**, not a disclaimer attached to one. It is the thing none of
  the published figures above do.

**Rejected: LoCoMo as the headline.** It is the most quoted benchmark in this market and that is its
only advantage here. A benchmark whose answer key is 6.4% wrong and whose judge rewards vagueness
cannot carry a claim, and quoting it would mean competing on a number this project would have to
disown when asked how it was produced.

**Rejected: publish nothing and say nothing.** Cheapest, and it leaves the one question every
evaluation asks unanswered while the market answers it badly. Silence about a weakness reads as not
having looked.

**Rejected: a figure from a development machine.** A laptop runs the model, the database and the
service at once, so the number measures that arrangement. This is the existing rule on measured claims,
restated here because a benchmark is exactly where it would be bent.

**Undo cost.** High in one direction only. Not publishing costs nothing and can be reversed any day.
A published number cannot be unpublished, and a published number without conditions cannot be defended.

**Impact.** How the project is evaluated, and nothing in the code. The work of actually running it
needs hardware that can be held still, which is why the issue stays open after this entry.

## D31 — There is no TypeScript client, and the absence is the decision

**Date.** 2026-11-09. **Issue.** [#144](https://github.com/ensera-ai/taisce/issues/144).

**Why it was open.** A TypeScript or JavaScript client is on this market's floor: of sixteen
self-hostable projects read on 2026-11-09, five ship one, against eleven with a Python client. The
adapters here are Python, Java and .NET. An absence on the floor is noticed, and this one was once
worse than noticed — a page advertised a TypeScript client that did not exist.

**Decided.**
- **There is no TypeScript client, and the documentation says so.** Not as a roadmap item and not as
  "coming soon", but as a position with its reason: the three adapters sit on the seams of the
  frameworks where Java and .NET developers already are, and that is the part of this market nobody
  else occupies.
- If one is ever built it owes exactly what the other three owe — a conformance driver passing every
  case against a live deployment, the session store, the passage seam, the compaction seam, and typed
  erase and export surfacing the residual count. A client that is not held to the suite is a claim that
  the bytes agree with nothing checking it.
- Nothing anywhere in this project advertises a client that does not exist. That is already guarded for
  the published copy, and this entry is why the guard matters.

**Rejected: build one now.** It is the floor capability, and reach is a real argument. It was rejected
on cost against position: a fourth language is a quarter more surface on every contract change,
forever, and it is spent in the ecosystem where every competitor already is rather than the two where
none of them is. The frameworks where TypeScript is first-class also mostly ship their own session
seam, so what would be built is a client against a seam shaped differently from the three that exist.

**Rejected: a thin client with no conformance driver.** Cheap, and it would make "one contract for every
adapter" false the day it shipped.

**Undo cost.** None. Recording an absence is free to reverse, and the day the argument from reach wins,
the only thing this entry cost was writing it down.

**Impact.** Reach, against the cost of keeping four contracts in step. No security or governance
impact: nothing is built, and no authority moves.

## D32 — The instance's numbers are scraped from the management surface, as instance-wide aggregates

**Date.** 2026-11-09. **Issue.** [#143](https://github.com/ensera-ai/taisce/issues/143).

**Why it was open.** Nothing outside a deployment could see how it was running. `/health` and `/ready`
served, `/metrics` returned 404, and the tree carried no telemetry dependency. The operator is the
second reader this product has, and an instance that can only be understood by opening its own web
page cannot be alerted on, cannot sit on a dashboard beside everything else they run, and cannot be
left alone. Four things had to be settled before anything was emitted.

**Decided.**
- **Which numbers: only what answers "is this instance behind, and is it refusing things".** The
  formation backlog and its limit, the parked count, failing projects as a count, whether a worker has
  reported in and when, the oldest pending turn, database and cluster connections against the limit and
  the headroom, and operations allowed and refused. Every one is read from where it is already computed
  for the health report and the ops centre, so a metric cannot disagree with the page the operator was
  reading a moment ago, and **this endpoint is not a way to add instrumentation to a path that does not
  have it**. No durations were added to the read path for it.
- **It serves on the management surface, not the memory port.** These are the operator's numbers. A
  project credential reaching them would tell every agent holding one how far behind the instance is
  and how much capacity is left — the shape of the deployment, told to a caller that only ever needed
  to write a turn.
- **Instance-wide aggregates, and the writer has no label parameter at all.** Not an unused one: none.
  A scrape does not stay where it is taken — it goes to a monitoring system whose access and retention
  are a different trust domain from the operator credential that opened the door. A per-project label
  set would make every scrape an enumeration of who the instance's customers are, and a label whose
  values came from anybody's words would make a monitoring system a second place memory content lives.
  Per-project detail stays on the management API and the ops centre, behind the same credential, where a
  person asks for it one project at a time.
- **Metrics, and no tracing.** The OpenTelemetry GenAI semantic conventions are at Development status,
  with no release and no memory span, so a span named now is a span renamed later. Revisit when they
  settle, not before.
- **The exposition format is written out by hand.** A client library would be a dependency in the write
  path's process for a format that is a name, a space and an integer.
- **The two moments are emitted as `NaN` when absent, never as zero.** Zero is 1970, so a rule of the
  form `time() - oldest_pending` would read an empty backlog as decades behind and fire the alert the
  series exists to enable. Every comparison against `NaN` is false, which is the correct behaviour for
  "there is nothing pending".
- **The counts are gauges over a stated window, not cumulative counters**, and the window is published
  as its own series. A cumulative count means a full pass over a table that only grows, behind an
  unattended timer, forever. They are deliberately not named `_total`, so nothing invites a `rate()`
  over them. A cumulative counter needs an index or a counter row first, and nobody has asked for one.
- **An allowed scrape writes no ledger row.** This is a deliberate exception to the rule that every
  operation is recorded, and it is written down here rather than left as a gap somebody finds: a timer
  scraping an append-only ledger would make the operation count a measure of its own monitoring, and
  the rows would outnumber the work. The limit on the exception is that **a refused scrape is still
  recorded** by the door every management route shares, so an attempt to reach this without an
  operator credential is as visible as any other refusal.

**Rejected: a per-project label, which is the one most likely to be asked for again.** It is the more
useful endpoint for a single-tenant operator, and the argument above is the whole reason it is not
here. Per-project numbers are a question asked through a credentialed API, not a dimension shipped to
wherever the scrape lands.

**Rejected: serving it on the memory port, unauthenticated, as most services do.** It is the
conventional shape and it would make the scrape configuration trivial. It also publishes the
deployment's capacity and backlog to anybody who can reach the port the agents use.

**Rejected: adopting the GenAI conventions now so the names match later.** They have no release and no
memory span; matching an unstable convention is how a name becomes wrong without anybody changing it.

**Rejected: a client library.** It would be the only dependency earned by a format the test walks line
by line.

**Undo cost.** Low in one direction and not in the other. The endpoint can be withdrawn; a series
somebody has written an alerting rule against cannot be renamed without breaking their rule, so the
names are the part to be slow about. The absence of labels is the cheapest thing here to reverse and
the one that should be reversed last.

**Impact.** Operations: an instance can be alerted on and left alone, which is what the self-hosted
reader needed. Security: no new authority — the same operator door, one more read behind it — and the
narrowness of what a scrape may carry is the security content of this entry. Performance: the scrape
reads the snapshot the health report already builds, so it adds one query per scrape and nothing to the
read path.

## D33 — The coverage floor is a percentage and the toolchain that measured it

**Date.** 2026-11-09. **Issue.** [#162](https://github.com/ensera-ai/taisce/issues/162).

**Why it was open.** `make test` failed the 92.3 floor at 92.0% on a commit the hosted build had
reported green at 92.4% (#168). Neither run was wrong. The same tree, one machine, one database and
one command measure 92.4% under Go 1.27.1 and 92.0% under 1.27.3, and the floor sat between the two
numbers. A floor recorded as a bare percentage is a measurement without its instrument, and the gate
had been comparing readings from two meters with nothing saying so.

**What changed, measured.** Nothing became untested. On `73e61f4` both profiles instrument the same
8,702 blocks with the same spans; no block exists in one and not the other, and none went from covered
to uncovered. The whole difference is 639 statements that 1.27.3 stops counting inside 336 blocks
across 61 files, every one of them covered in both runs, where a single statement spans many source
lines. `api.Contract()` holds four such blocks, counted as 17 statements each under 1.27.1 and as 1,
2, 2 and 3 under 1.27.3; the source of each is one `b.WriteString` call whose literal is concatenated
across a dozen or more lines. Uncovered statements were flat — 992 against 990 — so the fall is equal
amounts coming off numerator and denominator together. The older count tracks the lines an expression
occupies; the newer one tracks the statements in it, which is what the gate claims to measure. Full
figures: `research/2026-11-09-toolchain-advisories.md`.

**Decided.** `coverage.floor` records the percentage; the `toolchain` line in `go.mod` records what
produced it, and the two move in the same commit or neither moves. The floor is re-measured whenever
the pin moves, from the lowest of at least three full runs under the new toolchain, and the register
entry for a pin move carries those runs. The floor is then placed below what the **hosted runner**
reads, not below what a development machine reads: the platform moves the number as well as the
toolchain does, and the runner is the one that fails pull requests. Local runs are evidence about the
size of a change in the meter; they are not the basis for the number. D7 stands unchanged in everything it decided: the floor is
absolute, it is a ratchet, and a change that only deletes covered code still fails it. What D7 did not
anticipate is a change of instrument, which is not a change in what the suite covers and cannot be
answered by covering more.

**Rejected: writing tests to lift the number back over 92.3.** There is nothing for such a test to
test. No statement became untested, so a test written to move the figure would hold no property anyone
could name — the thing #162 and D7's own reasoning both forbid. This was not hypothetical: a branch
had already added 340 lines of tests so the suite would clear 92.3 under 1.27.3. Those tests cover
refusal branches nothing exercised, which rule 6 wants for its own sake, and they are welcome on that
footing — but they are not the answer to a change of meter, and keeping the two apart is the whole of
this entry.

**Rejected: keeping the floor at 92.3 by pinning at 1.27.1.** This code reaches twelve advisories in
the 1.27.1 standard library, through the operator portal's template renderer and through every HTTP
path the service serves. Holding a compiler with reachable advisories to protect a coverage figure
trades a security property for a number.

**Rejected: a floor relative to the base branch, reconsidered.** D7 rejected it and its reasons hold —
erosion in steps smaller than noise, a second suite run or a stored artefact per pull request, the
wrong question answered. It would also not have helped here: the base branch's number is a reading
from whichever toolchain measured it.

**Rejected: deriving the floor from the toolchain automatically so it never needs restating.** A number
the build computes for itself is a target that follows the suite, which is the failure D7 names. The
pair is restated by a person, in a diff, with the runs attached.

**Measured.** Three full runs of `make test` on `055b893` — `a97f3b3` plus this change — one machine,
one substrate container of its own, no test failures in any run:

| Run | Statements | Coverage |
|---|---|---|
| 1 | 11,642 / 12,641 | 92.0971% |
| 2 | 11,645 / 12,641 | 92.1209% |
| 3 | 11,646 / 12,641 | 92.1288% |

Those are darwin/arm64 figures, and they are not what the gate reads. CI run 42637282343, on
linux/amd64 under the same pinned toolchain, reports **92.0%** for the same commits — lower than every
local run. `go tool cover -func` prints one decimal and `scripts/coverage-gate.sh` compares that
string, so the runner's true value is somewhere in [91.95, 92.05) and the gate can only ever place a
floor to a tenth of a point.

So the meter is the toolchain **and the platform**, and the basis for the floor is the runner's figure,
because the runner is what enforces it. A floor set from the local reading is a floor the hosted build
sits on: 92.0 against a reading of 92.0 passes as "at the floor" with no headroom, and the next
ordinary run that lands a tenth lower fails a gate nobody changed — the failure
`scripts/coverage-gate.sh` says costs more than the tenth it protects.

The floor moves from 92.3 to **91.9**: one step below what the enforcing runner reads, 0.2 below the
lowest local run. The same code under 1.27.1 measured 92.5% in CI run 42633642119, which is the gap
this entry exists to account for.

One observation recorded rather than acted on: six runs across two trees spread by less than 0.04 of a
point, while `scripts/coverage-gate.sh` says the range across consecutive runs of an unchanged tree is
about half a point. That comment is not corrected here, because six runs on one machine against one
substrate are not evidence about the runner where the claim may have come from. Whether the gate's
stated noise is real is worth its own measurement before anything leans on it.

**Undo cost.** Low for the floor — one number and this entry. Not low for the pin it is now tied to:
moving the toolchain back means accepting the advisories in #162, so the pair is cheap to restate and
the reason for its value is not.

**Impact.** Process and security. A pull request that moves the toolchain now owes three suite runs and
a register entry, which is the cost of knowing what a coverage number means. Security: it ties the
floor to a pin whose value was chosen for what the compiler carries, so lowering the meter can no
longer be done without touching the thing that decides reachable advisories. It moves no boundary in
the service and changes no data.

## D34 — Operator authority is instance-wide, so a restricted project is restricted from applications and not from operators

**Date.** 2026-11-09. **Issue.** [#181](https://github.com/ensera-ai/taisce/issues/181).

**Why it was open.** Projects are the unit of access and they hold content apart well: a credential
reaches one project, the project comes from the credential and never from the request body, the same
subject id in two projects is two unrelated things, and an erasure in one leaves the others untouched.
What had no expression was who may administer a project. An operator credential is scoped to nothing —
`projects/create`, `suspend`, `resume`, `retention`, `credentials/issue`, `credentials/list`,
`credentials/revoke`, `vocabulary/add`, `audit/seal` and `audit/verify` are open to every operator
credential — so any operator can issue themselves a read/write credential for a project created to be
restricted. The arrangement that exposes it is the ordinary one for this audience: one instance, a
shared workspace every agent reaches, a finance workspace a few may reach, and a workspace per
engagement.

**Decided.** Operator authority stays instance-wide. One privileged credential administers the whole
instance, and that is a property of the design rather than an omission in it. What changes is that the
consequence is written where somebody laying out workspaces will read it, in
`docs/16-content-access.md` beside the content boundary it qualifies: a restricted project is
restricted from applications, not from operators, and separation that must hold against an
administrator is a separate instance.

**Rejected: an operator credential names the projects it may administer.** It is the obvious answer and
it is more mechanism than this system can currently justify. It needs a scope column on the operator
registry, a check on every management route, a rule for the acts that are not about one project —
`audit/seal`, `vocabulary/add`, creating a project at all — and an answer for who may grant scope to
whom, which is the beginning of a role model. Each of those is a place the check can be forgotten, and
a boundary with ten enforcement points is weaker than one with a single stated limit. Reconsider it
when an instance has two operators whose rights actually differ and somebody is being asked to share a
token because of it.

**Rejected: a project owner, as a principal between application and instance.** Same cost, and it
implies members, joining and leaving, which this system has no notion of: a credential identifies an
application and not an end user, so "members only" would mean "whoever holds the token" with a
friendlier name on it. A membership model that cannot name a member is worse than none, because it
reads as though access ends when a person leaves.

**Rejected: saying nothing and leaving the content boundary to imply the administrative one.** The
content document already states the honest version of its half — the boundary is application-level, and
anyone with direct SQL on the memory schema sees across projects. The administrative half was the one a
reader would assume and nothing said it. An unstated limit is relied on at its imagined width, which is
rule 8's point and the reason this entry exists at all rather than only the rejection above.

**Undo cost.** Moderate, and asymmetric. Adding scope to operator credentials later is a migration on
the registry schema plus a check on every management route, and it can be done without touching stored
memory. What cannot be undone is an instance that was laid out on the assumption that a restricted
project resisted its operators: the separation it needed was a second instance, and discovering that
after the fact means moving data rather than changing a flag.

**Impact.** Security. It names the width of a boundary rather than widening or narrowing it: an operator
token is an instance-wide key, and an instance is the only separation that holds against one. It moves
nothing in the service, changes no data, and costs no performance.

## D35 — A formation worker reads the relation vocabulary at the top of every turn, not once at startup

**Date.** 2026-11-09. **Issue.** [#164](https://github.com/ensera-ai/taisce/issues/164).

**Why it was open.** The vocabulary is the one surface that fits an instance to its own domain, and
an operator can now extend it over the management surface while the deployment is running. The
formation worker read it once, when it started, so a relation an operator added was admitted by the
foreign key immediately and was not in the list the model was given: the turn naming it kept being
refused as `unmapped_relation`, with the same refusal the operator was trying to remove and nothing
saying the process was behind. The operation worked and appeared not to. The workaround was to add
the relations before anything was loaded and restart the worker, which is a deployment action
standing between an operator and the one knob they have.

**Decided.** The vocabulary is read at the top of every turn, through a reader the worker holds
rather than a set it loaded. The startup read stays, as the check that it can be read at all: a
deployment whose vocabulary is unreadable should say so when it starts and not on its first turn. A
turn whose read fails is refused and retried rather than formed under the set from last time,
because a fact stamped with an identity naming a vocabulary nobody can produce is not recoverable
and a waiting turn is.

Per turn, and not per message, because the turn is the unit the extraction identity is pinned to:
the whole of a turn is extracted under one vocabulary and the identity recorded for it names that
vocabulary. Reading per message would let the set move inside a turn whose identity says it did not.
The extractor is handed an immutable vocabulary for the turn — a second extractor value, never a
mutation — so the set cannot move under a turn whose identity has already been taken.

What this costs an operator is stated beside the command: a turn that had already failed an attempt
before the relation was added cannot complete afterwards. Its identity is pinned, the vocabulary is
part of that identity, and a turn is never half one interpretation and half another — so it is
refused as a changed extraction and parks, and forming it needs a rebuild. That is not new
behaviour; it is what a worker restarted after a vocabulary change already did. What is new is that
it no longer takes a restart, which makes it a consequence of adding a relation rather than of a
deployment action, which is why it is written down and held by a test.

**Rejected: keep the startup load and document the restart.** It was the cheaper answer and it was
on the table. The argument for it was comparability — a worker told the same list for every turn
means two extractions were asked the same question. That argument does not hold. The list's *order*
is what comparability needs and the query guarantees it (`ORDER BY predicate`), while the *set* lives
in a table no process can hold still: two replicas that started either side of a change are already
extracting under two vocabularies, and a rolling restart has the same window. What actually carries
comparability is the identity pinned to each turn, which names the vocabulary that turn was
extracted under and is readable afterwards. Holding the set in a process makes comparability depend
on process lifetime, which nobody can see.

**Rejected: reload on a tick, or cache with a time to live.** It adds a staleness window, a second
thing to tune, and a question — how stale is acceptable — that nothing has measured a reason to ask.
The read it would avoid is forty rows on a path that already makes a model call per message. A cache
is justified by a number that says the read matters, and there is no such number; when the latency
budget work produces one, this is the first place to look.

**Rejected: `LISTEN`/`NOTIFY` on a vocabulary change.** It makes the vocabulary push instead of
pull, which removes the per-turn read, and it costs a dedicated connection per worker, a trigger on
the table, and a recovery path for a notification missed while the listener was reconnecting — at
which point the worker is stale again and nothing says so. More moving parts than the read they
save.

**Undo cost.** Low. It is one call at the top of a turn and one reader passed to the former at
startup; removing it restores the previous behaviour exactly and nothing stored depends on it. What
the data carries is the extractor identity per turn, which was already there and is unchanged in
shape.

**Impact.** Operations, and the write path. An operator extending the vocabulary gets an operation
that takes effect, which is what D29 decided and what this makes true. On the write path it adds one
read of forty rows per turn, against a model call per message in the same turn; no latency number is
claimed for it, because this project has no hardware that can produce one
(`AGENTS.md`, "What is not settled"). It changes no data, no schema and no grant: the memory
identity already had SELECT on these tables and reading them more often is still only reading them.

## D36 — The vocabulary key a fact references cannot move while the fact carries it

**Date.** 2026-11-09. **Issue.** [#165](https://github.com/ensera-ai/taisce/issues/165).

**Why it was open.** `fact_cardinality_matches_vocabulary` referenced `predicate(predicate,
cardinality)` `ON UPDATE CASCADE`. The deletion side of the same reference refuses, and the migration
that created it says why: retiring a relation while facts still use it must fail loudly, because a
cascade would delete the memory instead of refusing the change. That reasoning was applied to delete
and not to update, and what the update side then permitted was measured rather than assumed.

One statement — `UPDATE predicate SET cardinality='one' WHERE predicate='works_at'` — rewrote the
cardinality on every fact carrying the relation, in the same statement:

```
  when  | vocabulary | on_the_fact
--------+------------+-------------
 before | many       | many
        |            |
 after one statement | one | one
```

No fact deleted, no count changed, no memory write on the ledger, receipts unaffected, and the rows
look correct afterwards. The copy on each fact is what supersession rests on, so this is not a label:
it changes whether a later fact closed an earlier one, for facts formed before the change, invisibly.

**What was not open, which is where this started.** The issue reported that a *rename* cascaded. It
does not: a rename of a relation facts carry was already refused by `fact_predicate_fk`, whose
`ON UPDATE NO ACTION` check fires before the cascade reaches the rows, because that constraint was
created first. The premise was checked before anything was built, which is what the issue asked for,
and it did not hold.

**Decided.** The reference becomes `ON UPDATE RESTRICT`, matching the delete side. That closes the
cardinality door, and it also makes the rename's refusal a stated rule rather than an accident of the
order two internal referential triggers happen to fire in — an order nothing states, nothing tests,
and a later migration that dropped and recreated `fact_predicate_fk` would reverse without anybody
noticing.

In the substrate rather than in the application, because the design already rejects both operations
(D29: a rename is a second spelling of one relation) and the vocabulary operation offers neither.
"We do not offer it" is a convention, and a convention is broken by the statement written under
pressure by whoever holds the administrative connection — which is the only identity that can reach
this table at all.

**What the cascade was serving: nothing.** No path in this system updates either referenced column.
The vocabulary operation inserts and does not update, no migration changes a name or a cardinality,
and the memory identity has no write on the table.

**Rejected: leaving it and recording that renames are unsupported.** The issue offered this as the
honest outcome if a rename turned out to be unreachable. Half of it was unreachable and the other
half was not, so a register entry alone would have left the cardinality door open and written down a
guarantee about the rename that rested on trigger creation order.

**Rejected: refusing it in the vocabulary operation instead.** The operation already does not offer
either change, so there is nothing to add there. The statement that does the damage does not go
through the operation.

**Rejected: offering a supported rename, by rewriting facts and recording the change.** It is a
larger feature than the door it closes, and it would need a receipt saying which facts were rewritten
and under whose authority, which is a rebuild with extra steps. The path that already exists — add
the new relation, rebuild the sources that used the old one, erase what is left — keeps every receipt
honest. A rename would leave the history claiming the facts were always spelled that way.

**Undo cost.** Low. One migration restores the cascade, and nothing stored depends on the setting.
What is not undoable is a cardinality that was cascaded before this: the previous value is nowhere,
because the cascade overwrote it on every fact and recorded nothing.

**Impact.** Security, in the narrow sense rule 8 means: a boundary the design asserted is now
expressed where a statement cannot get around it, rather than in a convention. It is not a privilege
boundary — the identity that could do this is the one that can do anything an owner may do — and it
moves nothing on the read or write path, changes no data, and costs no performance. What it does not
cover is a relation no fact carries: nothing references it, so there is nothing to refuse, and
renaming one loses no memory.

## D37 — The words around a citation are cut once, when the evidence is written

**Date.** 2026-11-09. **Issue.** [#158](https://github.com/ensera-ai/taisce/issues/158).

**Why it was open.** The evidence join on the recall read did
`substring(convert_to(m.content,'UTF8') from … for …)` per returned row, to keep three hundred and
twenty bytes of context around each quote. That converts the whole message to bytes once per fact per
read, so the cost of showing a few hundred bytes depended on how long the message was — and it is on
the read path, which every caller pays on every call. Quote, span and message content are immutable
once stored, so every read was recomputing bytes that could not have changed.

**Decided.** Three columns on `fact_evidence` — the window, where it starts, and the message's length
— filled by a trigger when the row is written, and read back as columns. The read converts no message
to bytes. `RawWindow.Legible` still repairs the cut edges in Go, bounded by the window's own size, so
the rune and word repair stays in one place and keeps its own tests.

The joins to the observation and the message **stay**, with no column of the message's text selected
through them. The first attempt removed them along with the cut, and two tests that predate this
change caught it: a window is only honest while the message it came from is still in this project.
`src.scope = r.scope` is the cross-project guard — a row repointed at another project's observation
keeps its own scope, so the evidence row's scope cannot stand in for it — and the message's presence
is what says the words should still be shown, because a fact whose message is gone has a quote it
cites and three hundred bytes of surrounding words nobody cited. Either join missing makes the window
null, which is the answer the read gave before. Selecting nothing from the message means its row is
found by index and its content is never detoasted, so the check costs a lookup rather than a
message.

A trigger rather than the three statements that insert evidence, because a fourth will be written by
somebody who has never read the migration, and forgetting the window is silent: the fact comes back
with its quote and no words around it, which is indistinguishable from an erased message.

**Governance, which is the part that would normally sink it.** The columns hold a copy of message
text, so an erasure has to reach them. `fact_evidence` is not a new place text lives: its rows are
registered through `projection_dependency`, cascade away with the fact, are deleted by the eraser for
any erased observation, and are deleted by the retention sweep before a turn's messages are. And it
is not an ordering anybody has to remember — `evidence_message_fk` references `turn_message` with no
delete action, so the substrate refuses to remove a message while an evidence row cites it. A
statement that tried to orphan a stored window fails.

That was the stated condition for shipping this at all: before the change, a fact whose message was
gone came back with its quote and an empty context, and a stored copy could have outlived the message
instead. A quote outliving the message it came from is not worth a faster read.
`TestAStoredCitationWindowIsErasedWithTheEvidenceItBelongsTo` holds it, through the substrate's
refusal and through both sweeps — and `TestAFactWhoseMessageIsGoneKeepsItsQuoteWithNoContext` holds
the case where that constraint has been dropped, which is the damaged data the read already had a
branch for.

**Rejected: leaving the cut in the read.** It is the status quo and it is free in governance terms,
which is a real argument. What decided against it is that the work is provably redundant — the same
bytes, recomputed per fact per read, for immutable inputs — and that it is on the path rule 9 calls
the critical one.

**Rejected: a table of its own for the stored window.** It would have been a new kind with a new
residual key for an erasure to prove, for no benefit: the window belongs to exactly one evidence row
and has the same lifetime.

**Rejected: storing the legible text instead of the raw window.** It would move the rune and word
repair to the write path and store its result, saving a bounded repair over at most three hundred and
twenty bytes per returned row. That is not the cost the issue is about, and it would put the repair's
logic in two places — a trigger and `RawWindow.Legible` — with no test able to say they agree.

**Rejected: changing citation resolution the same way.** That path re-derives the quote and the window
from the message on purpose: its job is to prove the stored span still resolves against the stored
bytes, and a stored copy answering for itself would prove nothing. Its cost is paid once per
inspection by a caller who asked for exactly that.

**Undo cost.** Low to moderate. Reverting the read is a few lines; the columns and the trigger would
be dropped by a migration, and nothing else reads them. What a revert cannot recover is nothing:
every value in them is derivable from the message, which is why the migration could backfill them.

**Impact.** Performance, on the read path, and no number is claimed for it: how much it saves depends
on message sizes, and this project has no hardware that can produce a latency figure
(`AGENTS.md`, "What is not settled"). What can be said without measuring is that the read no longer
converts message content to bytes, so the cost of a fact's context stopped being a function of the
message's length. Security is unchanged rather than improved: the guards that stood on those joins
still stand on them, and this change's first attempt is the reason that sentence is here. Erasure's
guarantees are exactly as they were, which is the condition it had to meet.

## D38 — A recall explains itself when asked, and the explanation carries references rather than text

**Date.** 2026-11-09. **Issue.** [#160](https://github.com/ensera-ai/taisce/issues/160).

**Why it was open.** `controls`, `reach` and `degraded` tell a caller what applied, what was reached,
what did not run and whether the bundle was cut. They stop short of what debugging a bad answer takes:
which arm proposed each anchor, how many candidates each surface offered before its cut, and what the
cut took. The last of those was unanswerable from outside — the bundle said a cut happened and not
what it dropped — so a caller seeing four facts become two could not tell a thin memory from a tight
allowance. Built after the ranking and anchoring work rather than beside it, this is a debugging tool;
built first, it is how that work gets developed and measured.

**Decided.** An `explain` recall control, off by default, that returns a per-stage view beside the
ordinary bundle: every anchor with the arm that proposed it, its score where the arm scores, whether
it survived and how many facts it brought; and per selected surface, what it offered, what was kept
and what each cut dropped. It reports and never changes what is returned — a test compares the bundle
with and without it, field by field, because an explanation that changed the answer would be the
wrong instrument for finding out why the answer was what it was.

`degraded`'s discipline, not a sparser one: once asked for, every selected surface is present, so a
stage that ran and found nothing reads as zero rather than as absent. A surface the caller deselected
is not in it at all, because it was never a stage of that recall.

**The dropped candidates carry no text, and that is the correctness argument.** A fact the caller's own
budget cut is a fact they were not given. Naming its words in the explanation would hand it back
through the other door and make the budget a suggestion — a bound on what crosses the wire that a flag
could walk around. So a drop is a reference, the characters it would have cost, and whatever the stage
scored it. The reference resolves through citation resolution or record inspection, which are
authorised in their own right.

`domain.Anchor` gains a `Similarity`, which the semantic surface already computed and was formatting
into the published `matched` string (`semantic:0.77`). The string form stays, because version 1 froze
it; the number is for the explanation, since an anchor proposed by meaning is unexplainable without
it.

**Rejected: on by default.** It is a larger response on every call for something most callers never
read, which is rule 9's point about the read path: anything added there is paid for by every caller.

**Rejected: an operator-only surface.** The caller asking why memory answered this way is usually the
developer who will change the question. An explanation they cannot reach is one they ask an operator
for, which makes a lookup into a conversation.

**Rejected: returning the dropped candidates in full.** It is the obvious shape and it is the one that
breaks the budget, as above.

**Rejected: a trace of the statements the read ran.** It would be a different artefact — the shape of
the SQL rather than the shape of the retrieval — it would publish the schema as a contract, and it
answers a question nobody debugging a bad answer has.

**Undo cost.** Low. The control defaults to off, the field is absent unless asked for, and version 1's
freeze permits adding a request field a request may omit and a response field. Removing it later is a
new version rather than a patch, which is the ordinary cost of publishing anything.

**Impact.** Operations and development, not the read path's cost: nothing is recorded unless the
control is on, and the collector is a nil value otherwise, so a recall that did not ask allocates
nothing. It touches security by stating a boundary rather than moving one — the budget bounds what
crosses the wire, and the explanation is held to it.

## D39 — A lookup is charged to strangers' allowance only when the token is not one the instance issued

**Date.** 2026-11-11. **Issue.** [#214](https://github.com/ensera-ai/taisce/issues/214).

**Why it was open.** Every protected request took a token from one bucket — 256, refilled at 128 a
second, per process — on the first line of the door, before its `Authorization` header was read. Two
things followed, and neither depended on hardware. A process served at most 128 requests a second
whatever it ran on, freshness polls included. And anyone who could reach the port could empty the
bucket without a credential: a request with no header spent a token and was then refused, so about
130 of those a second left nothing for callers who held credentials, who were answered `429`.
Measured against the door on a real registry: a flood with no credential was itself refused at
request 259, and one credential asking for freshness in a loop was refused at request 341, 0.7
seconds in.

**Decided.**
- **What cannot be a token is refused before anything is spent.** No header, or a value without the
  token prefix, has nothing to look up. It costs the registry nothing, so there is nothing for a rate
  limit to protect, and it is answered `401` without one.
- **A token the instance issued spends nothing from the strangers' allowance.** The store holds the
  digests of the credentials the registry had unrevoked when it was last read, at most five seconds
  ago. A token whose SHA-256 is in that set is bounded by the lookup slots and then by the work
  slots, which are sized from the memory pool and shared between projects. Any other well-formed
  token is bounded by the bucket, as before.
- **The set decides who pays, never who is let in.** Every admitted request is still resolved against
  the registry. A revocation applies to the next request, exactly as the security model promises, and
  a credential minted a second ago works before the set has heard of it. Staleness therefore costs
  one thing in each direction: a new credential pays from the strangers' allowance for up to five
  seconds, and a revoked one has its refusals go unbilled for up to five seconds.
- **No one caller holds every lookup slot**, where every stranger together is one caller. Lookups are
  not queued, so a slot held is a request refused. A flood of unknown tokens can take all the slots
  but one, and so can one credential sent as fast as its holder can send it.
- **Each door knows only its own kind.** An operator token is not a known caller at the memory door,
  or a holder of one could spend the allowance meant for the other.

**Rejected: remembering a resolved grant for a second or two.** It is the usual answer and it would
also take two round trips off most requests. It costs the property that a revocation applies to the
very next request, which the security page states as a guarantee. The set gives the fairness without
that trade, because it never stands in for the lookup.

**Rejected: classifying by the stored token prefix.** Cheaper — no hash before the decision — and the
prefix is already held to tell credentials apart. It is also shown in every credential list, so anyone
who had seen one could craft tokens that ride its holder's allowance. A digest in the set means
holding a token the instance issued, and nothing less does.

**Rejected: charging the bucket only when a lookup fails.** It removes the ceiling and leaves the
outage: a flood of well-formed unknown tokens still empties the bucket, and the next request is
refused at the door before anybody knows whether it is a customer's.

**Rejected: a queue for lookups.** It would end the case where two holders arriving together refuse a
third. It also turns a refusal into a wait on the path every request passes, bounded by a queue length
somebody has to choose without a latency target to choose it against (#155). Left as a stated limit.

**Undo cost.** Low. The set, the classification and the per-caller bound are in two files and no
schema; reverting them restores the single bucket. Nothing stored depends on it.

**Impact.** Security: a stranger's volume no longer becomes a credential holder's refusal, and what a
stranger may do is unchanged — the same allowance, and one fewer lookup slot. What it does not cover
is beside the mechanism: with one registry connection there is one slot and nothing to hold back;
several issued credentials acting together can hold every slot; and a slot is refused rather than
waited for. Performance: the door no longer caps a process at 128 requests a second. No figure is
claimed for what it now serves — that is whatever the work slots and the database allow, and nothing
here measured it. The cost added to every request is one SHA-256 and a map lookup, and one registry
read every five seconds per process.

## D40 — A project says whether its people share one world, and a fact is filed under an account

**Date.** 2026-11-11. **Issue.** [#210](https://github.com/ensera-ai/taisce/issues/210).

**Why it was open.** A named entity is one node per project, and a single-valued relation holds one
current value, so a newer statement closes an older one. Whether that is right depends on something
no row could say. In a team's workspace, "Atlas is in Dublin" followed by a colleague's "Atlas moved
to Oslo" is news about one world, and the second must close the first. In an application with end
users, Alice's "my sister Sarah lives in Paris" and Bob's "my sister Sarah lives in Tokyo" are two
worlds that share a name, and the second must not. The store assumed the first everywhere. Reproduced
against a real database for the second: Alice's fact was given an end at the moment Bob's began —
nothing refused, nothing erased, a genuine citation on the fact that did it, and a recall scoped to
Alice answering as though she had never said it.

A first attempt keyed supersession on the speaker everywhere. Two existing tests failed, and they
were right to: they state the Atlas case and require the colleague's statement to win. So neither
rule is the defect. The defect was that a project could not say which it is.

**Decided.**
- **`project.statements` is `shared` or `separate`.** Shared: the project is one account, and
  anybody's newer statement closes the older one. Separate: each data subject has an account, and a
  statement closes only what the same person said.
- **Every fact carries the account it is filed under, and the constraint compares it.** The empty
  string in a shared project, the data subject in a separate one. In both kinds of project
  supersession stays a constraint, which is the property "a constraint, not a lock" exists for; a
  per-project rule enforced only in the write path would have given that up for half of them.
- **A trigger stamps the account, from the project.** Four paths write facts — formation, an authored
  assertion, a rebuild and recovery from receipts — and a column each had to compute the same way is
  one that would eventually be computed differently. The statements that look for a slot ask the same
  function the trigger uses.
- **A project nobody decided about is `separate`.** A shared world left on `separate` shows two
  current values where there should be one: both dated, both cited, visible in the first read.
  Separate worlds left on `shared` lose a true fact silently. The default is the mistake that shows.
- **A project that already existed stays `shared`.** Its facts were formed under that rule and are
  filed under the shared account; changing the rule underneath them would leave rows filed under one
  account and governed by another.
- **The setting is fixed once the project holds a fact**, by a trigger, so an UPDATE written by hand
  is refused as surely as the command. A fact's account cannot be updated either. The memory role
  cannot write the setting at all: it is the operator's, on the administrative connection.

**Rejected: keyed on the speaker everywhere.** Right for end-user applications and wrong for a team's
workspace, where it leaves a stale value current beside its replacement. It also reverses behaviour
two tests state on purpose. This was the first attempt.

**Rejected: a named entity belongs to the person who named it.** Identity would become project, data
subject and name. It ends the collision at its root and gives up what the entity model is for: an
organisation several people mention is one node, kept when one of them is erased, reachable by
anybody who asks about it. It would also change what every existing entity id means.

**Rejected: leaving it, and telling operators to give each end user a project.** No code, and one
project, one credential and one formation lock per person. It also leaves the loss in place for
whoever did not read the page, and the loss is silent.

**Rejected: `shared` as the default, because it is what every project was.** Compatibility for
projects that exist is kept by the migration, which is where it belongs. A default is for the project
that does not exist yet, and there the question is only which mistake is survivable.

**Rejected: computing the account in each statement that inserts a fact.** Simpler to read in one
place and wrong in the fifth place somebody writes later. Also rejected: enforcing the per-project
rule in the supersession statement alone, with the constraint left at its loosest — the constraint
would then refuse nothing in a shared project that the write path forgot to close.

**Rejected: letting the setting change after facts exist, with a rebuild.** It is what an operator
who chose wrong will want. A rebuild re-derives facts source by source while others are still filed
under the old rule, and the project would hold both until it finished. Until there is a rebuild that
re-files a whole project in one cutover, the honest answer is a new project.

**Undo cost.** High, and it is the kind that grows. The column, the triggers and the constraint are
one migration in either direction. But the account is on every fact written from here on, and in a
separate project pairs of facts are current that the narrower rule would not allow, so going back
means choosing whose statement to end — the choice this entry exists to stop the store making.

**Impact.** Security, in the sense that matters for a memory: in a project that says its people do not
share a world, what one person is recorded as having said no longer depends on what somebody else
said. What it does not cover — the node is still shared, so a read across the project shows every
speaker's statements about a name; a data subject is a string the application supplies and nothing
verifies it; a project already wrongly `shared` keeps the facts it closed, which can be counted
(`docs/09-temporal-history.md`) and are restored only by forming the content again in a separate
project; and erasing the person whose statement closed another's still leaves that fact unavailable
in a shared project. Performance: every fact insert takes a shared lock on its project's row and
calls one function, the constraint's index gains a text column, and replacing the constraint locks
the fact table for one index build; no figure is claimed for any of them. Operations: a new project
is `separate` unless told otherwise, which changes behaviour for a team workspace created after this
without anybody setting it — the listing shows the setting, and the command to change it works until
the first fact. The setting is reachable by the CLI over the administrative connection and not yet
by the management API or the portal.
