# Decision register

Every decision, closed. **Nothing in this document is an open question.** If another document appears
to leave a choice, that is a defect in that document — this register is the authority.

Each entry says what was decided, why, and **what was rejected**. The rejections are the point: a
decision recorded without its alternatives gets reopened by whoever has only heard the winning
argument.

Entries are added as decisions are made. An entry is written when a `decision`-labelled issue closes,
or when building something forces a choice that a later reader would otherwise have to re-derive.

---

## Shape

*These entries were taken on 2026-09-07 and they govern the ones below. Where an earlier entry
assumed a hosted, multi-tenant, part-closed product, it carries a note saying what replaced it.*

### D35 — Taisce is one open-source service, single-tenant, run by its operator

An instance holds one organisation's memory, on infrastructure that organisation controls: `docker
compose up` on a machine, a Helm chart in a cluster. There is no hosted edition and no part of the
system that is withheld.

The argument is adoption. A memory layer competes with a vector store somebody already runs and with
doing nothing, and both alternatives are free to start. Anything that makes evaluation require a
signup, a key, or a conversation loses to them before the design is ever considered. Self-hosting
also turns the governance claims from promises into observations: the data does not leave, and the
residual an erasure counts is computed on the operator's own substrate rather than asserted by a
vendor.

**What it costs.** The multi-tenant machinery already built has no reader in this shape, and the
security argument that a cross-customer read is *unreachable* becomes trivially true rather than
hard-won. Scaling evidence gets harder to gather: nobody is running a corpus of everyone's data, so
the numbers in G1 have to come from a benchmark rather than from production.

**Rejected:** the multi-tenant hosted service this was (it tells the governance story second-hand, and
every evaluation starts with a signup); running both (the out-of-scope section's own argument —
hosted and self-hosted cannot both be told well, and the one that suffers is whichever is not being
demonstrated).

**How hard to undo.** Moderate in code and severe in expectation. The schema-per-scope substrate would
carry a hosted edition with little change, but a licence cannot be narrowed and a community that
adopted an open product does not quietly accept it closing.

### D36 — Apache-2.0, for all of it

Service, portal, adapters, clients. One permissive licence, no split.

Adapters link into other people's applications, and a reciprocal licence there is a thing a legal
review refuses — which would cost exactly the adoption the adapters exist to win. Applying the same
licence to the service keeps the answer to *"what can I do with this"* one sentence long, and a
licence an operator has to reason about is friction at the moment they are deciding whether to try it
at all.

**What it costs, stated rather than discovered.** Anyone may host this as a competing service with no
obligation to contribute back. That is the price of the adoption, and it is paid permanently.

**Rejected:** AGPL for the service with Apache-2.0 adapters (the split most infrastructure projects
land on; it would preserve a hosted business and it was rejected because there is not going to be
one, and because AGPL anywhere in a stack is a question every adopter's legal team asks); BSL or
another source-available licence (protects a commercial position best, and it is not open source, so
it contradicts the thing being built).

**How hard to undo.** Permanent for anything published. Registries refuse to reuse a version number,
and code released under Apache-2.0 stays available under it.

### D37 — LiteLLM ships in the deployment; it is not a dependency

The service speaks the OpenAI-compatible HTTP interface and nothing more specific. The compose file
and the chart include LiteLLM configured as the default hop to a provider, because "bring your own
endpoint" as the only answer means the first five minutes involve choosing and configuring one.

Shipped is not required. An operator who already holds a key points the endpoint at it and runs
nothing extra. D1 is intact: PostgreSQL remains the only thing that has to be there.

**Rejected:** requiring LiteLLM (a second moving part on the write path, mandatory, in exchange for
convenience that an operator with a key does not need — and D1's argument applies to a proxy exactly
as it applies to a cache); shipping nothing and documenting a provider (correct and slower, and the
five minutes it costs are the five minutes that decide adoption); calling providers directly per
vendor (one interface is why inference can move without reaching the extractor).

**How hard to undo.** Easy. It is a service in a compose file and a subchart.

**Amended on 2026-09-07, when the compose file was built.** The reasoning above assumed LiteLLM
removed the configuration from the first five minutes. It does not: LiteLLM still needs a provider
and a key behind it, so shipping it by default adds a container without removing the signup. What
actually removes the signup is a model on the operator's own machine, so the quickstart points at a
local Ollama over the same OpenAI-compatible interface, and LiteLLM is an opt-in profile for the case
it is genuinely for — several providers, one key store, and cost visible in one place.

The conclusion is unchanged and better founded: the endpoint is configuration, nothing about it is
required, and the default is whichever answer costs an evaluator the least.

### D38 — The chart is highly available; the compose file is one machine and says so

The Helm chart defaults to several stateless replicas and a PostgreSQL topology that survives losing
a node. The compose file is a single host, documented as an evaluation and development deployment.

The application needs nothing new for this. Formation takes a per-scope advisory lock and the
watermark row is taken `FOR UPDATE`, so a second replica forming the same scope is refused rather
than racing — which is already tested. Availability is therefore what the chart arranges.

**Rejected:** arranging compose to look highly available (it survives a process dying and not the
machine, so the word would be doing work it cannot support); deferring availability until an RTO and
RPO exist (the topology is the part that is expensive to retrofit, and it can be built while the
number is still unstated — provided the claim is not made).

**What is deliberately not claimed.** No RTO or RPO has been agreed, so the chart is a topology with a
failover path rather than a guarantee. The first measurement of that number is the closing evidence
on the chart's own issue.

**How hard to undo.** Easy for compose, moderate for the chart.

### D39 — There is no commercial line, so none is designed for

No paid tier, no enterprise edition, no feature held back. Bundles, plan catalogues, entitlement
enforcers and subscription lifecycles are removed from scope rather than deferred.

An open-core line drawn later is a refactor through every feature that straddles it, and — worse — it
changes what gets built in the meantime, because anything that might end up on the paid side gets
designed with a seam it does not need. Deciding there is no line now is what stops that.

**Rejected:** open core with the portal or availability behind it (it makes operations the paid part
of a product whose argument is that operations are visible); a hosted edition later (D35); keeping the
bundle work dormant in case (a design nobody is building rots, and a milestone nobody will finish is
a roadmap that lies about what is coming).

**How hard to undo.** Easy in code, since removing unbuilt scope removes nothing that runs. Hard in
expectation, for the reason in D35.

**Amended by D45.** A contributor agreement exists to preserve the ability to release contributed code
under other terms, which is only worth having if a commercial edition is possible one day. So the
accurate statement is narrower than the heading: **nothing is designed for a commercial line, and the
option is deliberately kept open.**

Everything above still holds and is the part that governs day-to-day work — no paid tier, no feature
withheld, no seam built speculatively for a boundary that does not exist. What changed is only that
"there will never be one" is no longer claimed, because taking a CLA while claiming it would be the
register contradicting itself.

### D40 — The adapters are Python, Java and .NET, in that order

Held against the argument that adoption should send us to TypeScript first, and kept.

**Go where there is a named incumbent or no competitor, not where the noise is loudest.** .NET has a
first-party memory seam already occupied, which makes the claim testable: a head-to-head against a
specific thing a developer can name beats *"we also support this"*. Java has framework seams with no
first-party competitor at all. Python is where agents are built. Each of those is a reason a
developer would switch, rather than a place to be present.

**Rejected: TypeScript first.** It is the largest agent ecosystem and it has a client here with no
adapter, and that argument is real — recorded rather than dismissed, because it is the one that gets
made again. It loses on differentiation: every memory product ships a TypeScript SDK, so arriving
there means competing on presence in the most crowded seam, while the two seams where we would be
either the only option or the challenger to a named one go unserved. The counter-argument is that
crowding is evidence of where the users are, and if the first two adapters do not move adoption, that
is the evidence that reopens this.

**Also rejected:** one adapter taken all the way before a second exists (the second use case is what
reveals the right shape, and deferring it defers the lesson); MCP as the primary surface instead of
adapters (it reaches coding assistants, which are a different user from the agent applications the
adapters target — so it is additional, not a substitute, and it stays on the roadmap as its own
work).

**How hard to undo.** Easy. It is an ordering, not a structure, and the conformance suite is written
before any adapter moves so a fourth language is held to the same contract as the first three.

### D41 — The name is Taisce, the owner is Ensera, and the module path is `github.com/ensera-ai/taisce`

Stated by the owner on 2026-09-07 and recorded because two of these three are permanent.

**One project, one name.** The work that preceded this repository under a different name is the same
project and the same ownership; the name it carries now is Taisce. There is no second system to
migrate from, which is why the earlier caution about importing from it was a discipline about
re-deriving reasoning rather than a boundary between two owners.

**Ensera holds it.** That settles the question that blocked reuse: relicensing under Apache-2.0 is the
copyright holder's to do, and the holder is the party doing it. `LICENSE` and `NOTICE` name Ensera.

**`github.com/ensera-ai/taisce`.** Baked into every import in this repository and into every Go module
that will ever depend on it.

**Why it is taken before publishing rather than after.** A Go module path cannot be changed without
being a different module: consumers do not follow a rename, they break. Registries refuse to reuse a
version number. The cost of choosing now is one search and replace across 21 files; the cost of
choosing after the first tag is a permanent redirect and a second name in every dependency graph.

**Rejected:** a personal namespace (the repository's remote today, and it makes the project look like
one person's side project to exactly the audience being courted, while making a later move to an
organisation the breaking change above); a bare `taisce` vanity path over a redirect (a permanent
dependency on DNS and a redirect that must outlive interest in maintaining it, for a shorter string).

**How hard to undo.** The name and the module path are permanent for anything published. Ownership is
a matter of record rather than of code.

**Where it lives, and why there are two repositories.** `ensera-ai/taisce` is the home and the record.
The personal repository the work started in is kept rather than moved, at the owner's instruction, so
its history stays where anyone who already had the link will find it. GitHub refuses to transfer
issues between different owners, so the issue record was **recreated** in order — all 82, numbers
preserved, because commit messages and issue bodies cross-reference them by number and a renumbering
would break the argument trail this project is mostly made of. What did not survive is the original
timestamp on 46 comments; their text and authorship did.

**The consequence to watch:** two repositories now hold the same issue numbers, and only one of them
is the roadmap. An issue list that exists twice drifts, and the copy nobody is watching is the one
that will be wrong.

### D42 — One image, and its command decides what it does

Serving reads and forming the backlog run in one process by default. A `TAISCE_ROLE` of `api` or
`worker` runs one half, from the same image and the same binary.

**They are genuinely different workloads.** The API is fast and database-bound; formation is slow and
model-bound, and one of them wants pausing during a provider incident while the other keeps
answering. Those are real reasons to RUN them apart.

**They are not reasons to BUILD them apart.** Two binaries is two images, two build pipelines, two
version numbers that can disagree, and a schema migration that has to land in both — paid forever,
for a separation that is a runtime choice. With one image, running them apart is a line in a chart.

**The default runs both, and splitting should follow a number.** Nothing here has been measured
needing two deployments, and the development machine cannot produce that measurement. A split adopted
from a diagram is complexity with no argument behind it.

**What makes the split safe was already there.** The backlog lives in the database, not in a process,
so a paused worker means turns accumulate rather than disappear; and formation takes a per-scope
advisory lock, so several workers is a deployment decision rather than a code change.

**Rejected:** separate `api` and `worker` binaries (the permanent cost above, for a runtime choice);
one process always doing both (correct on a laptop and wrong the first time somebody needs to stop
extraction without stopping reads); a role that defaults when misspelled (a typo would silently give
every pod both halves, which is the state the split exists to avoid).

**A worker binds no port at all.** One that did would be a process an orchestrator could route traffic
to by accident, with a health check passing about reads it does not serve.

**How hard to undo.** Easy. It is an environment variable and a switch.

**Amended the same day, when the deployment shape was stated.** The compose file runs `api` and
`worker` as two services rather than one combined process, and the workers are what scale. The
reasoning above is unchanged — one image, role at runtime — and what changed is the DEFAULT: a
quickstart whose topology is nothing like a deployment is a topology that has to be unlearned, and
the cost of teaching the right one is a second container of an image that is already built.

### D43 — Two boundaries: the deployment is the tenant, the project is the isolation

**The tenant is the instance.** One deployment holds one organisation's memory (D35). There is no
tenant boundary to cross because there is no second tenant to cross into — not mitigated, unreachable.

**The project is the unit of isolation, and it is a real object rather than a string.** A project is a
namespace that data belongs to, that API tokens are attached to, that declares which kinds of memory
it keeps, and that carries its own retention policy. Today it is none of those: it is a table
partition, a watermark row, and a `scope` column on everything else. There is no project table at all,
which means there is nowhere for a setting to live and nothing for a token to be attached to.

**A token belongs to one project.** Not to a set of them. An agent that needs two projects holds two
tokens.

That is a deliberate narrowing of what was built, and it costs something specific: a recall can no
longer return one bundle spanning several projects, which G11 originally designed for. It is worth the
loss. A token that reaches one namespace makes "what can this credential see" a fact rather than a
list, makes every audit row unambiguous about which project an operation touched, and makes a leaked
token a bounded incident. And the direction is the safe one: widening a token to more projects later
breaks nothing, while narrowing one after clients depend on the width breaks all of them.

**Per-project settings, and what they are not.** A project declares which memory surfaces are active —
history, context provider, and whichever others exist — and its retention policy. This is the
operator configuring their own instance, and it must not become the entitlement machinery D39
removed: there is no plan, nothing is withheld, and a setting exists because an operator turns it off,
not because somebody has not paid for it. The distinction is worth guarding, because the two look
identical in a schema and only one of them has a reader.

**Rejected:** a project as a schema of its own (a multi-project query becomes a cross-schema union
that cannot be planned as one statement, and erasure would walk N namespaces instead of one); a token
holding a set of projects (what was built — it enables the cross-project bundle and makes the blast
radius of a leak a list rather than a name); settings on the credential rather than the project (two
tokens into one project could then disagree about what that project keeps, which is a project setting
by definition).

**How hard to undo.** The narrowing is easy to reverse and hard to re-apply, which is why it is taken
now. The project table is a migration; the settings on it are only as reversible as the behaviour they
switch.

### D44 — The service is one repository; each adapter has its own

`ensera-ai/taisce` holds the server, the workers and the migrations. Every framework adapter — Python,
Java, .NET — is a separate repository.

**The service is one deployable and one version.** Server and worker are the same binary in different
roles (D42), and a migration has to ship with the code that needs it: a schema change released ahead
of its reader, or behind it, is a deployment that half works. Splitting them across repositories would
create a version pair somebody has to reason about on every release.

**An adapter belongs to somebody else's ecosystem.** It is released on that ecosystem's cadence, into
that ecosystem's registry, under that ecosystem's versioning conventions — and it is consumed by
people who will never look at this repository. A Python package at `0.3.0` and a Go service at
`0.3.0` mean different things, and forcing them to move together means every adapter waits for the
slowest one.

**What that costs, and how it is paid.** The wire contract is now shared across repositories that
release independently, so it can drift — an adapter written against an operation the service has
changed. That is what the conformance suite (#26) is for, and it is why the suite is written before
any adapter moves rather than after the second one disagrees.

**Rejected:** a monorepo (one tag would mean five different things, and every ecosystem's tooling has
to agree about prefixed tags before any of them can release); a repository per component of the
service (server and worker are one binary, so it would be one codebase in two places).

**How hard to undo.** Hard once packages ship. A published module path is permanent, so moving an
adapter between repositories later means a new name and an abandoned one.

### D45 — Contributors sign a CLA, and the relicensing option is kept deliberately

A Contributor License Agreement, signed once before a first contribution is merged. It grants Ensera
rights beyond what Apache-2.0 gives — chiefly a licence broad enough to release the contribution under
other terms later.

**How this relates to the licence, because the two answer opposite questions.** Apache-2.0 is the
OUTBOUND licence: what this project grants to whoever receives it. A CLA is INBOUND: what a
contributor grants to this project when they hand over a patch.

Apache-2.0 already settles the inbound licence by itself. Section 5: *"any Contribution intentionally
submitted for inclusion in the Work by You to the Licensor shall be under the terms and conditions of
this License, without any additional terms or conditions."* So a pull request is already licensed
under Apache-2.0, patent grant included, with no agreement needed to obtain it.

**Which means the CLA is not here for the licence. It is here for certainty — and that is a narrower
claim than it first appears.**

An earlier version of this entry said a CLA was the only instrument preserving the ability to release
contributed code under other terms. That was wrong, and it is corrected here rather than quietly
edited, because it was the reasoning the decision was taken on.

Apache-2.0 section 2 grants recipients the right to *"reproduce, prepare Derivative Works of, publicly
display, publicly perform, **sublicense**, and distribute"* — and section 5 puts contributions under
those same terms. MIT grants sublicensing too. So a commercial edition built on contributed code is
already permitted by the licence, with attribution preserved, and no agreement is required to reach
it. That is what a permissive licence is.

**What an agreement buys is therefore certainty rather than capability:**

- **Chain of title.** An acquirer's or an investor's review asks who owns the code and whether that
  can be shown. Signatures answer in a document; sublicensing rights answer with an argument.
- **Standing to enforce.** Acting against an infringer generally requires ownership or an exclusive
  licence, and a sublicense does not confer it.
- **Relicensing the project wholesale**, including to less permissive terms, where relying on inbound
  sublicense rights is genuinely contested rather than merely inconvenient.
- **The absence of an argument at all**, which is worth more than it sounds at the moment somebody is
  performing diligence against a deadline.

**And what no agreement can buy: taking it back.** Code published under Apache-2.0 stays available
under Apache-2.0 permanently. A CLA changes what else may be done with a contribution; it does not
un-publish anything.

The timing argument is unchanged and is the reason this is settled now: an agreement has to exist
before the contribution does, because obtaining it afterwards means asking every contributor to sign
retroactively and removing the code of anyone who declines or cannot be reached.

**It also carries the provenance assertion** that a sign-off would otherwise provide — the contributor
representing that they wrote the contribution or otherwise have the right to submit it. That is the
gap Apache-2.0 section 5 leaves open: it licenses a contribution as far as the contributor was able to
license it, which for code copied from elsewhere, or written under a contract assigning it to an
employer, may be not at all.

**What it costs, stated rather than discovered.** A signing step between a stranger's first patch and
its review, which is where a proportion of drive-by contributions stop. A signing mechanism to
operate. A corporate variant, because a contributor employed by somebody else may need their employer
to sign rather than themselves. And the perception, which is real in open source, that the project
reserves rights its contributors do not have.

**Rejected:** a Developer Certificate of Origin (a sign-off asserting the right to submit; it closes
the provenance gap for the price of a commit flag, and — contrary to what this entry first said — the
commercial option survives it, because the licence already permits sublicensing. What it does not give
is chain of title on paper or standing to enforce); nothing at all (no record that a contributor had
the right to submit what they submitted, which is the one thing that matters if it turns out they did
not).

**MIT was considered and would not change this.** It grants sublicensing on the same terms, so the
choice between MIT and Apache-2.0 does not decide whether an agreement is needed. What Apache-2.0
gives that MIT does not is an express patent grant with defensive termination, an explicit trademark
carve-out, and section 5's automatic inbound licensing — see D36.

**How hard to undo.** Dropping a CLA later is easy. Adding one later is not, which is why it is
decided before the repository is readable.

**The counterparty is Baserat Al Mustaqbal, an establishment registered in Jordan, trading as Ensera.**
Named because a contract needs one, and because the form matters: where an establishment is a
registered trading name for a sole proprietor rather than a legal person distinct from its owner, the
agreement is in substance with the owner personally. That is not a problem in itself, and it decides
whether the chain of title this entry is built on survives a sale, a change of owner or a later
incorporation — which is the whole reason for preferring an agreement to a sign-off.

**What is recorded, and that it cannot be erased.** Signing records a name, an email address, an
account and a date, kept for as long as the project distributes the contribution, because it is the
evidence the licence was granted. It therefore cannot be deleted on request without removing the
contribution it accounts for. That is stated to contributors in `CONTRIBUTING.md` rather than left in
a privacy policy nobody opens — a project arguing that a system should say what it holds about a
person is a poor place to hold something quietly.

**This is not a lawyer's opinion and the text is not final.** `docs/cla/` holds the agreements and
`docs/cla/review-notes.md` holds the questions that are beyond an engineer to settle: whether the
establishment is a separate legal person, whether to name a governing law and what naming Jordan costs
in international contributions, and whether a source-file copyright notice should name Ensera or the
authors — since this is a licence and not an assignment, contributors keep their copyright and a
blanket notice stops being accurate the moment somebody else contributes.

### D46 — Nothing bakes in a public address

No URL of this instance is compiled in, signed into anything, or written into a stored record. Where
a public address is genuinely needed it is configuration, read at runtime.

**Because the address is the operator's, not ours.** This is self-hosted: there is no canonical
hostname, and a value that is right for one deployment is wrong for every other.

**And because an address inside a signed artefact is a migration.** A public site URL baked into
cryptographic checkpoint signatures has already been seen making a domain change into a data
migration rather than a DNS edit. #86's ledger is the place that hazard could recur, so the rule is
recorded before the ledger exists rather than after.

**Rejected:** a canonical domain in the code (wrong for every deployment but one); a compiled-in
default with an override (the default is what ships, so the override is what nobody sets).

**How hard to undo.** Easy while nothing is signed. Hard afterwards, which is the point.

### D47 — A caller never names a project; the credential decides it

A request carries no project. The credential resolves to exactly one (D43), and that is the project
the operation acts on.

**The check disappears with the field.** The surface currently accepts a `scope` in the body and
verifies it against the grant — a check that exists only because the field does. With the project
derived from the credential there is nothing to verify, nothing to get wrong, and no way for a caller
to name a project at all.

**It closes a disclosure surface.** A caller naming a project it does not hold has to be answered, and
every answer says something: `403` confirms the project exists, `404` conceals it at the cost of
lying. Neither is needed once the question cannot be asked.

**It makes the audit unambiguous.** #86 attributes every operation to a principal. If a request could
name its project, "which project did this touch" would be a second field that could disagree with the
first. Derived, the token names it.

**What it costs.** An agent working across two projects holds two credentials and switches between
them, rather than sending a different string. That is the same trade D43 already took, and it is the
same answer: the reach of a credential is a fact rather than an argument.

**And the question it dissolves.** "What happens when an agent writes to a project that does not
exist" was an open decision until this entry: it cannot, because the project comes from a credential
that had to resolve to a real one. Provisioning is an operator action (D43, #87), the default
partition stays as the safety net it was built to be, and the write path has one less way to be wrong.

**Rejected:** a project in the request verified against the grant (what was built — an extra field, an
extra check, an extra way to disagree); a project in the URL path (the same, and it puts the project
name in every access log).

**How hard to undo.** Easy while no adapter has shipped. The field would have to come back in a
contract version, which is why it is settled before #36 makes the contract public.

**What checks the project, since no constraint can.** The credential is in the registry schema and the
project is in the tenant schema, on opposite sides of the boundary that stops a memory query reading a
key — so there is no foreign key between them, and there cannot be.

Two checks stand in its place, and together they are stronger than the constraint would have been. The
command that mints a credential verifies the project exists, which catches a typo at the moment
somebody makes it. And **every authenticated request verifies the project is still there and not
suspended**, on the memory connection, which catches what a foreign key never could: a project
suspended or removed while credentials for it remain in circulation. A foreign key refuses a bad
reference at write time and says nothing about state that changes afterwards.

The refusal is `unauthenticated` rather than a missing project. From the holder's side the fact they
can act on is that their credential does not work; which projects exist, and whether an operator
suspended one, is not theirs to learn from an error message.

It costs one primary-key read per request. That is on the read path, where rule 9 says a cost is
justified against what every caller pays rather than what it prevents in the worst case — and what it
buys is that a credential for a project nobody is watching stops working rather than writing into it.

### D48 — Memory surfaces are chosen when a project is created, and only ever added

A project declares which kinds of memory it keeps at creation. Afterwards a surface may be turned on
and never off.

**Because turning one off is a destructive act pretending to be a setting.** Data already stored by a
surface has to go somewhere when it stops being served: left in place it is storage nobody can read
and an operator who believes "off" means "gone" is wrong about what their instance holds; erased, a
configuration toggle silently destroys memory and flipping it back returns an empty surface. Both are
bad, and the second is worse because it is quiet.

**Monotonic settings have no such state.** Turning a surface on is safe by construction: nothing has
been stored by it yet, so there is nothing to strand or destroy. There is no reversal to design, no
orphaned data to account for, and no configuration change that can lose a customer's memory.

**Stopping a surface is therefore an erasure, and looks like one.** An operator who wants a surface's
data gone asks for it explicitly and gets a receipt with a counted residual. That is this product's
whole argument about deletion, and routing it through a settings checkbox would be the one place it
did not apply.

**Rejected:** a reversible toggle that leaves data unreachable (storage held for something nobody can
read, and an instance holding more than its operator believes); a toggle that erases (a settings
change that destroys memory, discovered by whoever flips it back); refusing to turn off while data
exists (the correct answer to a question this decision removes).

**How hard to undo.** Easy to widen — permitting a turn-off later is additive. Hard to have avoided
if it had shipped the other way, since by then instances exist whose surfaces were switched off and
whose data is in whichever state was chosen.

### D49 — Two version lines: x.y.z on the working history, x.y.0 in public

Every commit on the working history has a version, `x.y.z`, where z counts commits since the last
release. The published repository contains only `x.y.0`.

**Repository roles.** Daily development, issues and pull requests belong in private
`althunibat/taisce`. The eventual Apache-2.0 public repository is `ensera-ai/taisce`. Each public
`x.y.0` is one reviewed source snapshot commit, descending only from the preceding public release.
The private development ancestry is never merged, mirrored or pushed into that public history.
The remote name `origin` does not establish which role a repository has.

**Release traceability.** The private release record maps the source SHA and exported tree digest to
the public commit, tag and artifact digests. Public builds carry the explicit public version and
public source provenance; a snapshot commit count cannot stand in for the private patch count.
Before publication, review the exported tree and all reachable public history for credentials, local
configuration, internal drafts and private tracking links. Existing organization-repository history
and issues also need review before the repository becomes public. Development issue #37 tracks the
automation and evidence; recording this contract does not mean the publisher is implemented.

**The two histories have different release markers.** Private `dev/vX.Y.0` names the source
commit; public `vX.Y.0` names the exported snapshot. Patch counting accepts only a marker that is an
ancestor of the current checkout. A fetched public tag cannot change private patch numbering.
After release, the private marker and the normal commit advancing `VERSION` are recorded explicitly;
the publisher never switches or pushes the private branch as a side effect.

**Preparation is separate from publication.** A release plan contains an isolated, reviewable snapshot
and the source/public mapping. The new public commit has exactly the previous public tip as its
parent. Publication checks that tip again and atomically pushes the branch and its release tag using
a compare-and-swap lease. A concurrent public update is refused. Tests against disposable Git remotes
prove that two releases retain two public commits, exclude private commit objects, and cannot publish
a tag alone when the branch push races. See `docs/05-releases.md`; supply-chain issue #37 remains open.

**The patch number is computed, never stored.** A number kept in a file is a number somebody forgets
to bump, and the failure is silent: two commits claiming the same version, discovered when a bug
report names one of them. Counting commits since a tag cannot disagree with the history it describes.
`VERSION` holds only the minor line — the part a person decides — and the patch is a fact about the
repository.

**A public version is always `.0`, and that is the whole point.** There is no such thing as an
unaccounted-for public release: each one is a commit somebody wrote, describing what changed, and the
patch position being permanently zero is what makes that structural rather than a convention.

**What it costs, which is not nothing.** An urgent fix ships as the next MINOR rather than as a patch,
so the version number itself cannot signal "small, urgent, upgrade today" — the release notes have to
say it, and some people upgrade on the number without reading them. That is a real loss, and it is the
price of every public version being a deliberate account rather than a build number.

**Rejected:** semantic patch releases in public (they would let a fix ship without an account of what
it fixed, which is exactly the accounting this separation exists to force); one repository with tags
distinguishing the two (a reader arriving at the published history would have to filter it, and the
first thing they see would be the excavation rather than the project); a stored patch number bumped by
a hook (it modifies the tree during a commit, conflicts with partial staging, and is one more thing
that silently stops working).

**The binary reports "unknown" when built without the stamp.** An honest answer rather than a
plausible wrong one: a binary reporting a version it does not have is worse than one admitting it does
not know, because the first is believed.

**How hard to undo.** Easy until a public tag exists. After that, changing the scheme means a version
line that jumps, which is the one thing a version number must not do.

### D50 — The ledger seals itself; there is no client-side proof protocol

The audit ledger is sealed in batches: each seal covers a contiguous range of entries, carries a
digest over them, and chains to the seal before it. An operator verifies it against their own instance
with `taisce audit verify`. The head digest is emitted to the process log on every seal.

**What was dropped, and why the reason changed rather than the engineering.** The original design was a
hash tree with a signed checkpoint and proofs a CLIENT computes, so that a customer could catch a
vendor rewriting a receipt. Self-hosted there is no vendor: the operator holds the disk, the database
and the backups. That threat did not get smaller, it stopped existing — and the machinery built for
it would be protecting a boundary that is not there.

**What remains is real and is rule 12's own question.** *Can an operator with database access do
something invisible?* Before this, yes: the append-only triggers refuse UPDATE and DELETE to every
identity the application holds, and somebody who can drop a trigger is not one of those. Now an
altered entry breaks its seal and every seal after it.

**Seals rather than a hash on every row, and the reason is the read path.** Chaining each entry to the
one before it means every audit write reads the previous digest — and every recall writes an audit
row, so every recall in the instance would queue behind every other. Rule 9: a cost on the read path is
judged against what every caller pays, not against the worst case it prevents. Batching moves the cost
to a background pass and the serialisation point with it.

**The two limits, stated where the mechanism is.** Entries written since the last seal are covered by
nothing — that window is the seal interval, and the verifier reports how many are in it every time
rather than only when it looks large. And nothing inside a database can stop somebody who can rebuild
the whole database: a full rewrite with a fresh chain is internally consistent. What defeats that is a
digest held where the database cannot reach, which is why the head is logged — whatever collects
process logs then holds a value a rebuilt chain cannot agree with. That is weaker than a signed
checkpoint and it is the honest version of what self-hosting can offer.

**Rejected:** the client-computed proof protocol and the client-held anchor (built for a vendor
boundary that no longer exists, and a client verifying an operator's own instance is a client checking
the operator against themselves); a per-entry hash chain (a serialisation point on the read path);
signing the head with a key the instance holds (a signature from a key the tamperer also holds proves
nothing, and pretending otherwise is worse than not signing).

**How hard to undo.** Easy to strengthen: publishing the head somewhere external is additive, and the
seal chain is what any stronger scheme would build on. The entries it covers are being recorded from
now, which is the part that could not have been added later.

### D51 — A path per operation on the wire; one binding lives in the client, not in the protocol

Five operations, five paths, five methods: `POST /v1/observations`, `GET /v1/freshness`,
`POST /v1/recalls`, `POST /v1/erasures`, `POST /v1/exports`. The alternative was one route that every
operation goes through, dispatched on a field in the body.

**What the one-route proposal was actually buying, and it is smaller than it looks.** The argument was
that an adapter binds once instead of five times. But a path is the cheapest part of a binding. An
adapter still constructs five request shapes and parses five response shapes in every language,
because the operations genuinely differ — an erasure receipt is not a recall bundle. What one route
removes is one string per operation, and what it costs is paid by everybody else forever.

**HTTP's vocabulary is the operator's, and it is not ours to spend.** `GET /v1/freshness` is legible as
a safe, retryable read to every proxy, load balancer, access log, metrics exporter and WAF between a
caller and this service — none of which we wrote, all of which the operator already runs. `POST /`
carrying `{"operation":"freshness"}` is opaque to all of them: every operation looks identical from
outside, and an operator asking which one is slow has to parse bodies to find out. Rule 8's own test
applies and fails: a boundary the operator can express in their own substrate — rate-limit recalls,
refuse exports at the edge, put erasures behind a network — becomes, under one route, a rule every
proxy config would have to read a body to apply. That is a convention, not a boundary.

**And freshness is the operation this costs most.** It is the highest-frequency call in the product —
an agent polling whether its write has formed (D19) — and it is the one whose safety and cacheability
a single POST throws away. Rule 9: work on the read path is judged by what every caller pays.

**The adapter gets its single binding anyway, one layer up.** A client exposes one internal method
taking an operation name and mapping it to a method and a path; the adapter above it adds operations
by name. That is one binding in the adapter and five on the wire, which is the whole benefit claimed
for dispatch-on-body, obtained without spending anything the operator owns. The mapping is a table in
one file per language rather than a translation layer, and #26's conformance suite is what holds the
languages to the same one.

**The MCP argument cuts the other way.** An MCP tool is a name and an argument object, so the mapping
to a path is a table — and that table lives once, in our own MCP server (#31), which is Go. It was
never going to be written five times.

**One name for an operation, everywhere it is named.** The wire's operation names *are* the ledger's
(`observe`, `recall`, `erase`, `freshness`, `export`), and the surface refuses to start if a route
carries a name the ledger cannot record or two routes share one. That was the real content of "one
route per operation": not one path, but one identifier running from the audit row through the wire to
the adapter method, so "which operation was that" has a single answer.

**Enumerable, which was the other thing one route was claimed to give.** The surface is declared as
data in `internal/api`, and `Operations()` hands it out. A test asserts every declared operation is
refused without a credential and leaves an attributed ledger row; #36 publishes the contract from the
same list. This is not a property of dispatch-on-body — it is a property of the set having one
declaration point, which a mux has as easily as a dispatch table. The defect it found immediately is
the argument for it: both tests named *"every operation"* enumerated four of five, and the missing one
was the export — the operation that returns everything held about a person.

**What it costs, and it is not nothing.** Adding an operation means an adapter change in every
language rather than a new name in an existing call. Five ecosystems ship a version to gain a
capability. That is a real recurring cost, accepted because the operator-side loss is permanent and
paid on every request while the adapter cost is paid once per capability.

**Rejected:** one route dispatched on a body field (opaque to every intermediary the operator runs,
vaguer status codes, and it buys an adapter four lines); a route per operation with a *different* name
in the contract than in the ledger (a mapping somebody maintains, and the first thing to disagree);
GET for recall, erase and export (a question and a data subject reference are personal data, and a URL
lands in access logs, proxy logs and browser history — this is D-level enough to say twice, and the
handlers say it too).

**How hard to undo.** Free today and never again, which is why it is settled now: no adapter has
shipped and no client depends on the shape. After the first SDK, changing it is a contract version and
an upgrade in five ecosystems. The enumerable declaration is easy to undo in either direction; the
wire shape is not.

### D52 — A bundle reports the shape it was built from, not a verdict on its relevance

A recall returns how many candidate names the question offered, how many were found in memory, and how
the facts distribute across them. It does not return a relevance score.

**The problem is real and it is not the one entity anchoring already solves.** Every mechanism that
orders candidates says which are *best* and none says whether any is *good*, so a question about
something a memory holds nothing on comes back full of whatever could be reached. Anchoring on named
entities makes the worst version impossible — a question naming nothing known reaches nothing. What it
does not prevent is a question that names something known and then asks about something else, which
comes back full, cited and plausible.

**Why not a verdict.** Strong, weak or none derived from how the top candidate compares to the rest
requires candidates to be scored, and nothing here scores them: D25 holds that a ranking stage
introduced before the plain path is measured makes it impossible to say afterwards which stage is
doing the work and which is doing harm. A verdict is a ranking judgement under a different name, and
it arrives with the same unjustifiable threshold.

**And an absolute floor is measured not to work.** On an embedder's own held-out set, near misses
reached higher similarity than true pairs began at. No cutoff separates them, so a floor tuned to
suppress one bad bundle removes real answers elsewhere.

**What is exact and needs no number.** Six candidate names offered, one matched, every fact drawn from
it: that is recognisably a question that named something and asked about something else, and a caller
sees it without anybody choosing a threshold. The distinction that matters most is between the two
empty answers — *we have never heard of what you asked about* and *we know that and have nothing to
say about it* — which are different, and indistinguishable in an empty bundle unless it says which.

**Rejected:** a similarity floor (measured not to separate); a computed verdict (a ranking stage
before ranking has been justified); returning fewer rows when relevance looks low (that is ranking
deciding what a caller sees, from a judgement nobody can defend, and the bundle already declares
truncation for the one cut it does make).

**How hard to undo.** Additive. When ranking arrives with the measurement that justifies it, a verdict
can sit beside these counts — which are facts about the retrieval and stay true whatever ranks it.

### D53 — A citation carries a bounded window of the message, not the message

A fact comes back with its quote, the span the quote occupies, and roughly 160 bytes of the message
either side of it, cut at word boundaries and labelled with whether that is the whole message.

**A span alone is verifiable and not legible.** It proves the words were in the message and says
nothing about the sentence they were in — whether it asserted the thing, hedged it, attributed it to
somebody else or denied it. A reader checking a fact has to go and read the message, and no path
returns one. The words either side are already stored, already scoped and already erased with the
message they belong to, so returning them searches nothing, ranks nothing and makes no message
reachable that the fact did not already reach.

**Why not the whole message.** It is the obvious answer and it fails twice. A bundle whose facts each
carry a paragraph is passages with facts attached, which inverts what the product is; and it would
introduce passage retrieval through the citation, where a caller cannot ask for it or decline it and
where it arrives with no measurement behind it. Bounding the window also bounds the cost: what a fact
costs is the same whether it came from a sentence or from a pasted document.

**Why not the sentence.** Sentence detection is a heuristic that is wrong on abbreviations, decimals
and lists, and being wrong means a citation that stops mid-clause while claiming to be a sentence. A
byte window makes no claim it cannot keep: it is a window, it says where it starts, and it says
whether anything was left out.

**Why a constant and not a setting.** Nothing has asked to vary it, and a setting is a default nobody
tunes plus a branch nobody exercises. What a caller does control is the character budget, which counts
the context — so a caller who cannot afford legible citations gets fewer facts rather than a knob to
find. The quote and the window are both charged even though the window contains the quote, because
both are on the wire and a budget that describes a smaller payload than the one that arrives is worse
than no budget.

**The window is cut in the database.** The span is bytes, so the window is byte arithmetic, and doing
it in the query is what stops a hundred-kilobyte message crossing the wire so that three hundred bytes
of it can be shown. The join that reads the message carries the scope predicate: it is the one join in
the read path that returns text nobody cited, and a bound that holds only while everything above it is
correct is not a bound.

**When the message is gone the fact still comes back**, with its quote, its span, and no context — and
the bundle says a citation lost its context. Dropping the fact instead would make an erasure elsewhere
in the turn look like memory loss.

**Rejected:** the whole message (inverts the bundle and smuggles in passage retrieval); sentence
segmentation (a heuristic making a claim it cannot keep); a per-request window size (a knob with no
reader, and the budget already expresses the constraint); charging only the quote (describes a payload
the caller does not receive); an inner join on the message (a fact vanishing because its context did).

**How hard to undo.** Easy in one direction and not the other. Widening the window or adding a
per-request size is additive. Removing the field is a change to a published contract, and a caller's
prompt template that renders context is broken by its absence — which is the ordinary asymmetry of
anything that goes on the wire.


### D54 — A read names two instants, and the half it does not name is the open interval

A recall can be taken at a moment in the world (`as_of`) and at a moment in this system's belief
(`as_known_at`). Both are optional and independent.

**The unnamed half is `upper_inf`, not a timestamp of now.** This was built the other way first and it
is wrong. `known` is stamped by the database's clock, and a "now" computed in the server process is a
different clock — measured 82ms apart between a server process and a database container on one
machine, and worse across hosts. A fact recorded a moment ago then falls outside `known @> ourNow`, so
a caller writes something and a read that named only a validity time cannot see it. The failure is
silent and it gets worse under exactly the conditions that make clocks drift. The open interval asks
what the caller meant — what we believe now — and involves no clock at all.

**The same argument runs the other way on the current read.** `upper_inf(valid)` rather than
`valid @> now()`: a turn whose `occurred_at` is a few minutes ahead of this server's clock is ordinary
when the client sends the time, and it produces a fact whose validity starts in the future.
Containment hides it until the clock catches up; a memory that cannot recall what was just written to
it is not a memory. What the current read is asking is "is this still the answer", and `upper_inf` is
that question exactly. It is also the predicate `fact_reconcile_idx` is partial on, so the hot read
stays on the index holding only current facts.

**Four statements rather than one.** A single query with `($6 IS NULL AND upper_inf(valid)) OR valid
@> $6` is unindexable in both arms, because the planner cannot know which branch it is planning for.
The temporal predicate is the only difference between the four, and they are generated from one
template so that no other predicate — the project bound, the source filter, the evidence join — can
be written once and forgotten in the others.

**Every fact carries the interval it held.** `valid_from` and `valid_until`, the second null for a
fact nothing has superseded. Without it a fact returned by a read of last March is indistinguishable
from one that holds now, which is the one way a historical answer can be worse than no answer.

**What this does not cover, and it matters.** Supersession closes `valid` on the row in place. So the
belief axis can hide facts recorded after an instant, and it cannot reconstruct a validity interval
that has since been closed: asking "what would you have said in March about July" returns nothing
rather than the answer we would have given, because the row that would have answered has had its
interval rewritten. A full belief history means never mutating a row — closing `known` and inserting a
corrected copy — which doubles rows on every supersession and changes what the exclusion constraint,
the erasure sweep and the evidence join are working over. That is a design, not a detail, and it is
held in its own issue rather than assumed here. The limit is stated where the mechanism is, because a
guarantee described as wider than it is will be relied on at its stated width.

**Rejected:** a timestamp of now for an unnamed instant (two clocks, silent, measured); one statement
with an OR (unindexable in both arms); a single `as_of` covering both axes (collapses the two
histories into one and makes "why did you tell me that" unanswerable); returning facts without their
interval (a historical answer that reads as a current one).

**How hard to undo.** The wire fields are additive and easy to widen. The in-place closure of `valid`
is the part that is expensive to change later: every superseded row ever written carries a rewritten
interval, and no migration can recover what those rows said before.

### D55 — Tense is asked separately from polarity, and only the present becomes a fact

A proposal carries a `tense` — present, past or future — and anything but present is refused and kept
with its words, exactly as a denial is.

**They are independent and both have to be right.** "I do not live in London" is a denial. "I used to
live in Amman" is an assertion, positive and unhedged, about a period that is over. It passes the
polarity gate correctly, and what lands is a fact saying the person lives in Amman now. One field
covering both would make a model choose between two true answers, and whichever it dropped would be
the one nobody was checking.

**This is the worse of the two presences.** A denial that slips through is a fact about something the
person went out of their way to call untrue, and it tends to read oddly to whoever sees it. A past
tense that slips through reads perfectly: it is the answer to "where does this person live", it is
cited, it is current, and the only thing wrong with it is a date nobody recorded.

**Refused rather than stored with an interval, because only half the interval is knowable.** The
sentence says the relation ended at or before the message. It says nothing about when it began, and
`valid` cannot be left unbounded below: every read is a containment test, so an unbounded start
answers a question about 1990 with yes, on evidence nobody has, and a bounded one is a date this
system invented and would then cite. The claim is kept in `rejected_claim` with its own reason, so
nothing is lost, the rate is countable, and the sentences are the corpus a later decision would be
made against.

**Asked of the model rather than detected in code.** A list of past-tense markers in Go is a rule a
sentence walks around — "worked at Acme until March" carries none of the obvious ones — and it would
also misfire on the perfect aspect, where "I have worked at Ensera since 2019" is a current fact. The
model is asked the question and the code refuses the answer it cannot use, which is the same division
the polarity gate already draws: the wording is in the prompt file, the refusal is in the code.

**The default is refusal.** An omitted tense is not "present": it is a model that did not answer, and
the failure of a widening default is that a provider ignoring a field silently writes facts nobody
vouched for.

**What this deliberately does not do:** using a past tense to close a fact already held. If memory
holds an open `lives_in(user, Amman)`, this sentence is testimony that it ended and closing the
interval is the honest response. That needs an evidence model for an ending — the receipt comes from a
different message than the one that opened the fact — and a read that survives a second evidence row,
which today's inner join does not. Held in its own issue rather than assumed here.

**Rejected:** one field carrying polarity and tense together (makes a model drop one true answer);
detecting tense in code (a rule a sentence walks around, and one that breaks the perfect aspect);
storing a past relation with a fabricated or unbounded start (a date this system invented, cited back
as evidence); treating an absent tense as present (a widening default on the path where a false fact
is indistinguishable from a true one).

**How hard to undo.** The refusal is a line, and the claims it refuses are kept — so admitting past
relations later is a decision made against a corpus rather than from scratch. What is not recoverable
is the intervals for sentences said while this was in force, because the messages behind them are
subject to retention like anything else.

### D56 — A second hop is asked for, bounded by a fanout cap, and returns the chain it followed

A recall reaches one relation out from what the question named unless the caller asks for two, and a
fact reached through a relation comes back with the entities and predicates that led to it.

**Depth is the caller's, because only they know the question.** At one hop every fact is about
something the question named. At two, some facts are about something else entirely — *who does Marta's
manager work for* is answered by exactly that, and *where does Marta live* is made worse by it. Nothing
in the system can tell which of those it was given, so a default that widened would make every bundle
bigger, every read slower and every answer harder to explain in order to help the minority of
questions that need a chain.

**The chain is what makes a hopped answer defensible.** *Marta → manages → Hamza → works_at → Ensera*
is a route a reader can check. The same fact with a distance score attached is something they have to
trust, and a bundle whose facts are about things the caller never mentioned needs to be checkable
rather than trusted. A one-hop fact carries no chain, because a path of one name and no relations is
ceremony on every row of the ordinary bundle.

**The first bottleneck is a hub entity, not corpus size.** An anchor with ten thousand facts reaches
ten thousand entities, and expanding each of those is what takes a read path down. So each entity is
expanded through at most sixty-four relations per direction, which bounds one hop at 128 rows and two
at about sixteen thousand — bounded on any corpus, by depth rather than by whatever the busiest entity
happens to be. What it costs is completeness at a hub, which is an arbitrary cut of the same kind the
character budget makes.

**One statement, not two.** The traversal replaces the direct read rather than sitting beside it: at
depth one it produces exactly the rows the previous query did, and every bound — the project
predicate, the source filter, the temporal predicate — is written once and applies at every level. Two
statements would be two places for a bound to be forgotten, and the level where it would be forgotten
is the deep one, which is also the one that reaches furthest from what the caller asked about.

**A fact is returned once, at the shortest route that reached it.** Several paths to one fact is one
fact; returning it per path would spend a caller's budget on repeats that read as corroboration —
which is the failure co-derivation reporting exists to prevent, arriving by a different road.

**Rejected:** two hops by default (costs every caller for the minority who need it, and changes what a
bundle is); a distance or relevance score instead of the chain (a ranking stage before ranking is
justified, and unverifiable by the reader); an `OR` across the two entity columns (defeats both partial
indexes and falls back to a sequential scan per level); unbounded fanout (a hub entity is a hang, and
the substrate carries no statement timeout — #101); returning one row per path
(repeats that read as corroboration).

**How hard to undo.** The depth parameter and the chain fields are additive on the wire. The fanout cap
is a constant and changing it changes what a deep read returns, so it moves with a measurement rather
than with an opinion. Reverting to the non-recursive read would be straightforward and would lose the
property that every bound is written once.

### D57 — Thematic reports are stored, are never shared, and are rebuilt from what survives

A report over a community of entities is written once and kept. It is registered to every observation
that contributed to it, an erasure deletes every report a departing subject contributed to, and what
replaces them is generated from the facts that remain.

**The collision this resolves.** A thematic answer needs something to search that no single message
states, which argues for a stored artefact. Erasure has to be provable by counting rows against the
predicate that selected the deletions, which argues against storing model-written prose about many
people at once. Both are load-bearing: without the first there is no thematic retrieval, and without
the second the product's central governance claim becomes a belief.

**They do not collide once the artefact is deleted rather than repaired.** The proof obligation is
that nothing derived from an erased source survives — not that the surviving text be inspected. A
report row registered to an erased observation is deleted and counted like any other projection, and
what is generated afterwards is a function of the facts that are still there. That is a stronger claim
than regeneration-and-belief, and it is the same claim every other projection already makes.

**The exemption that would break it, named so it is not inherited.** Erasure keeps a projection that
another subject also registered — `Ensera` is registered by everyone who mentioned it, and removing it
because one person left would take a node out of everybody else's graph. A report must not inherit
that rule. An entity is a shared *identity*: the row means the same thing to everybody who registered
it. A report is shared *text*: it contains what each contributor said, so keeping it because somebody
else also contributed is exactly the leak. The keep-if-shared predicate therefore has to become a
property of the projection kind rather than a rule applied to all of them, and a kind that aggregates
content declares that it does not survive a contributor's erasure.

**Not generated per question.** That would put a model call on the read path for every thematic
question, which is the cost this system refuses elsewhere for the same reason: anchoring resolves
entity names with a lookup rather than a model precisely so a caller waits on a query rather than on
inference. A thematic path that pays generation per question is slower than the passage search it is
meant to beat, and it makes read latency a function of how much has been stored.

**What a caller sees between an erasure and a rebuild.** Fewer reports, and a bundle that says so.
Regeneration is a pass on the driver's tick, not something a request triggers — a read that rebuilt
what an erasure removed would make one caller pay for another's governance operation, and would put
model calls on the read path by the back door.

**The cost, stated rather than discovered.** Erasing one subject can invalidate every report they
contributed to, which in a small scope is most of them, and rebuilding is a model call per community.
A subject who erases repeatedly forces repeated rebuilds — the amplification is bounded by communities
touched rather than by the number of requests, and the rebuild is a background budget the operator
controls rather than an unbounded reaction. It is a real cost and it is the price of the residual
count meaning what it says.

**Rejected:** persisting with the residual claim narrowed to name reports as regenerated-not-counted
(the narrowing is exactly the sentence a customer would be told, and a guarantee with one exception is
one everybody has to check for the exception); generating per question (a model call on the read path,
and read latency that grows with the corpus); inheriting the keep-if-shared rule (keeps text about a
person who asked to be forgotten because somebody else is also in it); regenerating inside the erasure
transaction (a governance operation that cannot complete while a model provider is down).

**How hard to undo.** The storage shape is a migration. What is expensive to reverse is the residual
claim: a deployment that once said reports are counted cannot later say they are approximate without
retracting what it told its users, so the decision is taken in the direction that keeps the stronger
claim available.

### D58 — Subjects are found by density, without randomness, and a level is a finer question

The entity graph is partitioned by moving each entity into the group it improves most, splitting every
group into its connected pieces, and collapsing groups into single nodes to start again. A group too
large to be one subject is split by asking a stricter question of it rather than by cutting it.

**Density against a resolution, not modularity.** Modularity compares a group's internal edges against
what a random graph with the same degrees would hold, and that comparison has a resolution limit:
below a size that depends on how big the WHOLE graph is, a genuine small group is always better merged
into a neighbour. The same four entities, densely joined, would be a subject in a small scope and
dissolved in a large one — and the dissolving would happen as somebody's memory grew. A retrieval
surface that degrades with use is the failure mode that cannot be found in testing, because testing is
done on small inputs. Density is local: a group's fate depends on the group, and nothing about it
changes when unrelated memory is added elsewhere.

**Connectedness is taken directly rather than emerging from a randomised phase.** A group in two
pieces that no edge joins is two subjects with one name, and whatever summarises it writes one summary
about two things. The published form of this algorithm gets that guarantee from a randomised search
for sub-communities; splitting each group into its connected pieces gets the same guarantee from an
operation with no parameter and no randomness. What that buys is not tidiness: it removes the
determinism problem rather than solving it. There is no generator to pin, no stream a toolchain
upgrade can change, and no way for a scope's themes to reorganise between two runs of the same input —
which a customer could not tell from a defect.

**What that costs, stated.** The randomised phase also escapes the arrangement the first pass settles
into, and that is given up. It is a quality improvement with no measurement here saying it is needed
(#102), and the day one says so, the refinement is what to add — with a generator whose stream is
fixed by its own definition, because determinism stops being free at that point.

**Aggregation is not an optimisation.** Moving relocates one entity at a time, so it can never merge
two groups however much merging would improve — measured here on two dense subjects tied by sixteen
relations, which stayed separate at every density until groups were collapsed into single nodes. What
makes a merge possible is that a group becomes one node, carrying the weight already inside it, so the
next round judges a merge by the same rule it judged a move.

**A level is a finer question, not a repeat of the same one.** Re-partitioning a group at the density
that chose it returns it whole, which is not a surprise. So a split raises the density, and takes the
coarsest one that actually divides the group — which makes the multiplier a property of the group
rather than another parameter. A group that only ever shatters into single entities has no structure
inside it and stays whole: cutting it to satisfy a size bound produces two summaries of half a subject
and no way to tell that is what happened.

**Every component, not the largest.** A document corpus has a giant component with noise around it. A
person's memory is work, family, a hobby and a trip, sharing no entity at all and of similar weight.
Keeping only the largest would silently drop most of somebody's life, and the answers that remained
would look fine.

**The numbers are provisional and say so in the code.** How dense a group must be, and how large before
it is split, have no derivation here — #102 holds what would settle them. A constant with a comment
saying it has not been derived is honest; the same constant without one reads as a decision somebody
made.

**Rejected:** modularity (a resolution limit that dissolves real subjects as a scope grows); a
randomised refinement (buys quality nobody has measured a need for, and costs determinism that is
currently free); local moving without aggregation (cannot merge two groups at all — measured); a fixed
multiplier for splitting (fragments a loosely joined group and leaves a tightly joined one whole); a
third-party clustering library (its randomness would be outside our control, which is the one property
the whole design turns on, and it is a supply-chain dependency for a core differentiator).

**How hard to undo.** The partition is derived, not stored, so recomputing under different rules is a
rebuild rather than a migration. What is expensive is anything downstream that has already been keyed
to a community identifier — which is why the identifiers are a function of the graph rather than of
the order the algorithm happened to visit it.

### D59 — Entity resolution stays exact, because proximity was measured not to separate

An entity is found by its normalised name and by the surface forms it has been referred to. A vector
surface — the names plus the verbatim quotes behind the entity's facts — was built and measured, and
it is not adopted as a way of finding candidates.

**What was measured.** Four pairs that mean the same thing and four that do not, against
`qwen3-embedding:4b-q8_0`, comparing a question to an entity's surface:

- names alone: paraphrases 0.6691–0.7567, near misses 0.4738–0.6885, **gap −0.0193**
- names with the stored quotes: paraphrases 0.6445–0.7546, near misses 0.5466–0.6760, **gap −0.0315**

The gap is the weakest true pair minus the strongest false one. It is negative both ways: the
distributions overlap, so no absolute threshold admits every paraphrase and rejects every near miss.

**The pair that breaks it is the one the product cannot afford to get wrong.** A question about the
Cork office scores 0.6885 against a surface for the Dublin office — above the weakest true pair. Two
offices of one company is the ordinary shape of a workplace memory, and merging them files one site's
facts under the other's name, cited and current and wrong.

**The quotes made it worse, which was the opposite of the expectation.** They lift near misses far
more than paraphrases: two projects of the same kind go from 0.4738 to 0.6442 once each surface carries
sentences about migrations. A surface built from what was said about a thing is partly a surface about
the KIND of thing it is, and same-kind confusion is exactly the failure mode being guarded against.

**So the asymmetry decides it, as D6 already said it would.** Under-merging costs recall on one
question and the entity is still there under its own name. Over-merging is irreversible in the way that
matters: nothing downstream can tell that two people became one node, and every answer afterwards is
confidently about the wrong person. A mechanism that cannot be shown to separate is not adopted on the
strength of the direction it usually gets right.

**This is a negative result and it is worth as much as a feature.** The alternative was building a
partitioned vector column, an index per project, an embedding pass, an erasure registration and a
threshold — all of it justified by an assumption that a measurement contradicts in an afternoon.

**Two follow-up measurements, taken to find out what would work, and neither did.**

*Describing instead of assembling.* D60 showed a community's written summary separates where an
entity's assembled quotes do not, so the same treatment was applied to a single entity — the same
material, the same writer, the same prompt, an entity as a community of one. Gap **−0.0360**, against
−0.0313 assembled. Slightly worse. Summarisation is not the mechanism.

*Adding the neighbourhood.* What a community carried and an entity did not was richer material: what
the subject is connected to. So the surface was rebuilt to carry the relations the entity stands in
rather than only the sentences that mention it. Gap **−0.0289**, against −0.0308 for the same pairs
without them. An improvement of 0.0019, which is nothing.

**The same pair breaks all three**, and that is the result. *"What is happening at the Cork office"*
against *the Dublin office* scores 0.6753, 0.5290 and 0.7098 across the three surfaces, and in each
case it is at or above the weakest true pair. The question and the entity are the same kind of thing in
the same company and differ by ONE token. So does *payments* against *database*, *Kelly* against
*Nowak*, *Holdings* against *Ensera*.

**The discriminating information is a token, and a dense vector averages it away.** That is why no
treatment of the surrounding text helps: there is no more context to add that changes what one word
means. It also explains why exact-match resolution has never made this error despite costing recall —
it is matching the token rather than embedding it.

**What it does not establish.** That no embedder separates them: one local model was available and
another may behave differently, which is the remaining untested variable. And eight pairs is a gap
rather than a distribution — nothing here is a rate.

**What it does establish well enough to act on.** Three treatments of an entity's own material, on this
embedder, all fail on the same pairs and for the same reason. Further variations of a surface are hill
climbing on eight fixtures, which is tuning to a set too small to generalise from. #105 holds what
would have to be brought instead: a set with a distribution, and a mechanism that treats a specifier as
a token rather than as a direction in a vector space.

**Rejected:** shipping a threshold anyway (a number with a decimal point and no evidence, failing
silently in the irreversible direction); embedding the quotes to widen the surface (measured to narrow
the gap); a synonym list maintained by hand (it is the same over-merge decided in advance, and it has
to be maintained per deployment, per language, forever).

**How hard to undo.** Nothing was built, which is the point. When a candidate mechanism has evidence,
what it needs — a vector column partitioned by scope, an index per partition, a projection
registration — is additive and the measurement that justifies it already exists in the repository.

### D60 — Themes are searched by proximity to a written summary, which was measured to separate

A thematic question is answered by comparing it to what was written about each subject. Measured, on
the same embedder that failed to separate entity surfaces:

| question | right subject | nearest wrong subject |
|---|---|---|
| "what is holding up the database work" | 0.5683 | 0.3735 |
| "what is going on with card payments" | 0.5887 | 0.3416 |
| "what do I do at weekends" | 0.4678 | 0.2618 |

Weakest right 0.4678, strongest wrong 0.3735, **gap +0.0943**. The right subject is nearest in every
case, and the distributions do not overlap.

**The contrast with D59 is the finding, not the number.** The same embedder, on entity surfaces built
by concatenating stored quotes, could not separate two migrations at all — adding the quotes made it
worse, because a surface built from what was said about a thing is partly a surface about the kind of
thing it is. Reports about the same two migrations separate by 0.23. What changed is that a summary
NAMES what makes a subject different — Postgres and a schema, against Stripe and a card vault — where
concatenated quotes pile up the vocabulary two subjects of one kind have in common.

**So summarising is doing work that could not be done by assembling.** That is worth stating plainly
because it is the opposite of the intuition that grounding a surface in raw evidence makes it sharper,
and because it is the argument for a report being a written artefact rather than a bag of its material.

**The hardest case is the one that matters most.** "What do I do at weekends" shares no word with any
material — no entity, no relation, no quote — and lands on the cycling club at 0.4678 against 0.2618
for the nearest wrong subject. That is a question entity-anchored recall cannot start on, answered.

**What this does not establish.** Three questions against three subjects is a gap, not a distribution,
and the threshold implied by it is not a number to ship. What it justifies is building the index; what
a threshold needs is the measurement on a corpus with a distribution.

**Rejected:** building the index on the strength of the argument alone (the entity surface had the same
argument and failed); re-ranking passage hits by community membership instead (that is not searching
the themes, and cannot surface a theme no single passage states); embedding a community's raw material
rather than its summary (measured to be the weaker representation, in D59's numbers).

**How hard to undo.** The measurement is cheap to repeat and lives in the repository, so a different
embedder is an afternoon rather than an argument. The index built on top is a migration and a rebuild
pass, which is reversible; what is not reversible is a threshold shipped before there is evidence for
it, which is why there is not one.

### D61 — Generation and embedding may be different providers, against one allowlist

The endpoint that writes may be somewhere other than the endpoint that embeds. Both fall back to one
value when unset, and both are checked against the same list of permitted hosts.

**They are different jobs.** Extraction is a generation, is the slow and expensive part, runs on a
background pass where nobody is waiting, and is where a better model visibly changes what is stored.
Embedding is a forward pass over short text, is cheap, and is the one an operator is most likely to
want on their own hardware. Forcing one endpoint makes an operator choose a provider that is wrong for
one of the two.

**There is no third role, because there is no model on the read path.** Recall resolves entity names
with a lookup, not with a model — a hop there is paid by every caller on the path they are waiting on,
and it makes read latency a property of a provider. Freshness, erasure and export call no model
either. So the roles are the ones that exist: generation on the write path, and embedding.

**One allowlist across both, and a second endpoint inherits nothing.** Generation sends a message;
embedding sends text written from several messages. Both are an egress of exactly the data this system
exists to hold. A list per role would be two places to forget a host and would let a deployment permit
a host for one role while believing it had permitted none — so there is one list, every endpoint is
checked against it, and an unlisted host fails at startup before anything has been sent.

**A key travels with its endpoint rather than falling back on its own.** A credential belongs to a
provider. Carrying the generation provider's key to a different host would send a secret somewhere it
does not belong, which is worse than the request failing — so an endpoint that differs uses its own
key or none.

**What this makes possible, and what it means.** A deployment can extract with a hosted model and
embed locally. That sends conversation text to a third party, which is a decision only the operator
can make: the mission is that data does not leave the operator's infrastructure, and the moment a
hosted endpoint is listed, it does. The allowlist is where that decision is made and it cannot be
arrived at by default — only by somebody writing the host down.

**Rejected:** one endpoint for everything (forces one provider to be right for two different jobs); an
allowlist per role (two places to forget a host, and a permission granted invisibly); a key that falls
back independently of its endpoint (sends a credential to a host it does not belong to); adding a
role for answering (there is no model on the read path, and inventing configuration for one would be a
knob with nothing behind it).

**How hard to undo.** Additive and per-deployment. The variables are optional, an existing deployment
sets none of them and behaves exactly as before, and removing the split would be removing two
environment variables nobody had to set.

### D62 — The complete JSON representation is validated before a request can act

A request contains exactly one object, uses valid Unicode, has unique keys at every nesting level,
and ends with whitespace or EOF. The whole body is limited to 1 MiB and 128 nesting levels.
Case-folded property aliases count as duplicates because the Go decoder maps them to the same field.
Every refusal returns `invalid_body` without echoing keys, content or parser errors.

The protected asset is the meaning of a write or governance selector. An authenticated caller can
submit bytes interpreted differently by a gateway and the server; no append or erasure may run until
those bytes have one unambiguous interpretation. The HTTP/PostgreSQL test
`TestAmbiguousBodiesCannotAppendOrErase` verifies that refused requests consume no offsets, erase no
observations and produce no successful operation entry. This does not authenticate message contents.

**Rejected:** decoding only once (ignores trailing data); checking EOF alone (still accepts duplicate
keys and repairs malformed Unicode); decoding into a map first (loses duplicate-key evidence).
The bounded token pass adds one linear scan before typed decoding, without another materialised tree.

**How hard to undo.** Transport-only, with no schema migration. Relaxing it would reintroduce multiple
interpretations of the same request. Tracked in development issue #87.

### D63 — Unresolved authentication is sampled without caller content

An unresolved credential records the fixed principal `unresolved`. Neither the bearer value, its
prefix, its digest, an address nor a request path is stored to identify an unknown caller. Successful
resolved operations retain their credential attribution independently of this sampling budget.

Each server admits at most 16 anonymous audit entries per fixed one-minute window. Further refusals
increment one saturating in-memory counter; the next admitted entry includes them in its magnitude.
The first suppressed attempt emits one content-free warning. A varying token cannot create new
counter keys, additional rows or one log message per attempt. Tests exercise concurrent admission
and an HTTP refusal burst against PostgreSQL, then seal and verify the resulting ledger.

This bounds anonymous audit storage rate per server, not lifetime storage, fleet-wide request volume
or credential-lookup work. Pending counts are best effort: they are recorded on the next sample and
can be lost at process exit or when the audit store is unavailable. Counts saturate at the PostgreSQL
integer limit. The ledger retains committed samples under its existing append-only policy. The change
prevents future input copying; it does not rewrite historical ledger entries.

**Rejected:** retaining token prefixes (arbitrary permanent content); unkeyed token hashes (an
unnecessary durable correlation identifier); an unbounded row per refusal (anonymous storage
amplification); a counter per token or IP (unbounded cardinality and additional personal identifiers).

**How hard to undo.** No schema change. Sampling policy can change independently of the wire refusal,
which remains identical for missing, malformed, unknown and revoked credentials. Authentication
admission and fleet-wide resource limits remain development issue #93; this resolves #88.

### D64 — Atomic message groups are explicit on the observation wire

`group_ordinal` is supplied for every message or omitted for all. Explicit groups begin at zero,
occupy contiguous runs in message order and advance by one. The domain enforces this for HTTP and
internal append callers, including callers that supply their messages out of ordinal order.

When groups are omitted, each ordinary message is a separate unit. A turn containing tool messages
must supply groups explicitly: roles alone cannot reconstruct which result answers which call.
The caller supplies the boundaries; this contract does not validate tool-call IDs or completeness of
the tool protocol. Export carries both observation identity and group ordinals, because group zero
in two different turns is two different units.

**Rejected:** silently collapsing all messages into group zero (unrelated messages become inseparable);
guessing tool groups from text or roles (no defensible call/result identity); allowing a group to
resume after another group (a contiguous history slice would split it).

**How hard to undo.** No storage migration: the group field already exists. This tightens admission of
ambiguous tool turns and adds the optional wire field. Existing stored group-zero turns retain their
original conservative boundaries. Development issue #86 covers the wire journey; compaction safety
and cross-adapter replay remain acceptance obligations under #22 and #26.

### D65 — Observation retries retain identity after their content is erased

An optional `idempotency_key` is a UUID generated once for a logical turn and reused on transport
retries. The authorized project scopes it. Matching normalized input returns the original observation
ID and offset with HTTP 201; a different payload or an erased source returns HTTP 409 and
`idempotency_conflict`. No key means an intentional new observation, even when the content repeats.

A key-specific transaction lock precedes the receipt lookup. Only a new write allocates the scope
offset, and its receipt, observation, messages and chunk registrations commit together. Retry
fingerprints use an explicit versioned representation, independent of domain struct layout. Omitted
timestamps are fingerprinted before the first accepted write supplies its clock value; explicit
zones and UUID casing normalize. The hash format has a compatibility regression test.

Erasure and retention clear both the payload fingerprint and observation reference in the same
transaction as deleting the observation. The remaining project/key digest is a permanent operation
tombstone: that key cannot be reused in that project. It contains no source ID, subject reference,
message text or content fingerprint. Clients must use a fresh random UUID per logical write, not an
identifier derived from a person. The tombstone is retained correlation metadata, not a claim of
anonymity. This costs one small row per keyed write after payload retention has expired; capacity
planning must include it.

**Rejected:** deduplicating by content (suppresses intentional repetition); an in-memory key cache
(lost on restart); recording a receipt after append (a crash duplicates the write); deleting or
expiring retry identity with the source (an old queued retry can restore erased content); keeping the
payload hash after erasure (retains content-derived correlation unnecessarily).

**How hard to undo.** Migration 21 adds retry metadata and an erasure trigger. Existing observations
remain unkeyed. Removing tombstones weakens the guarantee against stale retries and is an explicit
operator decision. The HTTP/PostgreSQL tests cover concurrent retries, conflicts, project separation,
one formation call, transaction rollback, erasure and retention. Development issue #85 remains open
for adapter use of stable keys and conformance under #26.

---

## Substrate


### D66 — Speaker identity binds to stored attribution inside a project

A speaker reference and a named entity are different identity kinds. Reserved first-person terms
live in `speaker_term`; an accepted reference resolves to `(scope, speaker_subject_id)` in the
speaker namespace. Named identities retain `(scope, normalized_name)` and a shared organization
remains shared. Type is still an attribute for named identities. D5/D6 apply to that named namespace.
The displayed speaker name is the generic `speaker`; the attribution key is separate metadata.

The model supplies words, never a subject identifier. Fact admission compares the claimed binding
with the stored observation's project and subject and the source message's user role. Missing or
mismatched attribution is refused. The production vocabulary admits supported first-person terms
for this binding; polarity, tense and exact quote validation still run before persistence. English
and Arabic explicit-reference fixtures run through formation and HTTP recall, including concurrent
speakers, corrections, quoted speech, missing identity and erasure. This is a bounded reference
vocabulary, not general pronoun resolution or proof that a model understood every sentence.

`data_subject_id` on recall is an optional attribution filter inside the credential's project. When
present it selects that speaker without requiring a name in the question, restricts named anchors
to that subject's registrations, filters facts before each hop's fanout and filters evidence by its
source observation. Empty retains project-wide named-anchor recall. The filter is not a new
permission: a project credential can supply any subject, just as it can already export or erase one.
Human authentication and subject-specific capabilities remain separate work.

**Upgrade behavior.** Migration 0022 refuses old name-only speaker entities before changing the
identity constraints. Such nodes may already have retired another person's fact; assigning an ID
cannot restore that history. The failed migration rolls back its DDL and version marker. Named-only
databases upgrade without reidentifying their entities. Databases with ambiguous history need a
projection rebuild from retained observations into the new representation. The generation/rebuild
workflow remains an explicit delivery dependency; this migration does not implement it or silently
claim existing data was repaired. Source export and erasure remain available on the prior version.

**Rejected:** hashing a subject into its displayed name (propagates protected attribution into every
statement and model context); using the message role as identity (all users share one role); merging
speakers by semantic similarity; retaining ambiguous first-person lookup when no subject is supplied;
filtering only the final bundle (other people's edges would already have guided traversal); guessing
old intervals during migration without re-forming the source that established them.

**Impact.** Separate unique indexes prevent cross-speaker supersession. Source binding adds an
indexed reference classification and a source lookup on speaker writes; it adds no inference call.
Subject-filtered reads use indexed speaker identity plus attribution predicates. No production
latency claim is made. Export includes stored attribution metadata for the selected subject, and
registered speaker entities participate in erasure. Sharing a named entity does not preserve another
subject's speaker. Undoing this requires a migration and another projection rebuild; widening the
read filter would also change a public contract callers rely on.


### D67 — Report material keeps entity references separate from display names

A report fact carries subject and object entity IDs alongside its displayed names. Community reads
supply those IDs; child ownership includes them in its identity key, so replacing one child's raw
material cannot remove another speaker's identical sentence. Model input renders opaque entity
references beside the names. Quotes and recorded statements are unchanged, and the reference labels
are counted by the same material-size calculation that drives substitution and truncation.

These are projection entity IDs already used for anchoring, not data-subject identifiers or hashes
of them. A shared organization carries the same reference across its contributors. Caller-supplied
material without IDs retains its named-only representation; the database path always provides IDs.
The input fence includes the rendered references along with the remaining material.

**Rejected:** name-plus-quote identity (two people can say the same words); putting protected subject
keys into model input; relying on the model to infer distinct speakers from identical displayed
names. Identity in the input prevents deterministic loss in context construction; it does not prove
that generated prose preserves every distinction. Free-text report quality remains measured against
its corpus, and old reports need regeneration to acquire the new input semantics.

**Impact.** Child substitution and report context budgets now account for opaque references. This
adds no model call and transmits no new data-subject key. Removing the fields is cheap in code but
would recreate the ambiguity; existing generated reports must be regenerated after changing their
input representation.

### D68 — Relationships agree on the project, and reports register exact source identities

Migration 0023 adds composite project/ID foreign keys for fact endpoints, evidence, chunks,
rejected claims, community membership and parentage, and both sides of projection dependencies.
Checks are deferred until transaction commit where deletion order requires it. Deleting an entity
before its facts within a complete transaction is valid; committing only the entity deletion is not.
This supersedes the original decision to tolerate dangling fact endpoints for erasure ordering.

The polymorphic dependency registry uses one generated UUID reference per declared built-in kind,
with exactly one reference present and a foreign key to its actual target in the same project.
A new kind now requires a relationship migration as well as its registry row (amending D26).
The eraser still discovers kinds through the registry. Runtime credentials can read policy catalogs
but cannot rewrite the kind mapping, vocabulary, speaker terms or migration state. Target deletion
cascades only registrations for targets actually removed. Source deletion remains last; commit-time
foreign keys also refuse surviving evidence or graph references. This strengthens D28's residual
accounting without claiming that a registration alone proves every derived row was registered.

Retention applies the same sharing policy as requested erasure. A shared identity or independently
supported fact survives; evidence from the departing observation does not. Aggregated report text
is removed when any contributing source leaves. Reports carry exact source observation IDs from
raw material and every substituted child. Quote equality is not provenance. Persistence checks and
locks the complete source set, refuses an erased/missing/foreign source, and ignores model-returned
source metadata. A concurrent report writer that loses the unique-key race creates no registration.

**Migration and recovery.** Existing relationships are validated atomically, and inconsistencies
refuse the upgrade with its schema/version changes rolled back. No project assignment is guessed.
Previously generated reports cannot be trusted to have complete source registrations, so migration
0023 invalidates those reports and their registrations; the worker regenerates missing reports from
retained facts. Observations, messages, facts and evidence are preserved. Review that regeneration
cost and database migration locking before rollout. This is not the general generation/rebuild
workflow: migration 0022's ambiguous speaker history still requires that separate workflow.

**RLS assessment.** No row-level security policy is added in this change. The deployment runtime
serves authorized cross-project reads and background work through a shared database identity.
Transaction-local project context could catch a missing application predicate, but only after every
read/write, pooled transaction, worker and administrative path has an explicit context contract.
An application allowed to choose that context is still trusted; RLS would not defeat a compromised
application or database owner. Runtime-role tests intentionally omit SELECT project predicates and
see both projects, while inconsistent cross-project relationships are refused. Authenticated project
filters therefore remain the read authorization boundary. The single-tenant runtime cleanup (#99)
and privilege design retain responsibility for changing that contract; these foreign keys are not
marketed as cross-project confidentiality.

**Rejected:** dangling endpoints as normal erasure behavior; immediate checks requiring fragile
per-table deletion order; quote-text joins for source ownership; runtime-mutable policy catalogs;
dynamic per-write triggers that look up arbitrary target tables instead of ordinary foreign keys;
adding a partial RLS policy without proving transaction/pool/worker behavior.

**Impact and undo cost.** Composite keys and dependency indexes add storage and write amplification;
workload measurements remain #102/#103, with no latency claim from correctness tests. Existing
invalid relationships must be reconciled from authoritative observations before upgrade. Reversing
the constraints needs a migration and weakens integrity; invalidated generated report prose is not
restored by reversing DDL. General rebuild/formation/erasure concurrency remains part of #100's
open acceptance until the generation lifecycle exists.

### D69 — One instance memory namespace, with separate control privileges

Runtime, bootstrap and operator commands resolve the same instance-wide namespace. Its default is
`memory`; the control namespace remains `control`. Projects are rows and storage partitions inside
that memory namespace. Project creation does not call schema provisioning, run memory migrations,
or consult a tenant registry. `ProvisionMemorySchema` names what provisioning actually does.

`TAISCE_SCHEMA` remains an optional deployment setting for installations already using another name
and for isolated database fixtures. It is fixed when the service starts; no credential, observation,
project or HTTP request can choose it. Changing it does not relocate existing data. Upgrades must
retain an existing explicit value. The identifier validator still rejects unsafe/truncated names;
its purpose is SQL correctness, not a claim of confidentiality through schema naming.

The memory login cannot read credential secrets or provision schemas. Bootstrap explicitly removes
superuser, database/role-creation, replication and RLS-bypass attributes from existing service logins,
not only from newly created ones. The administrative connection owns schema changes. These grants
do not isolate projects from a compromised application that already holds the deployment's memory
login; authenticated project predicates remain required. Arbitrary pre-existing role memberships or
ownership changes made by an administrator are outside the baseline bootstrap guarantee.

**Evidence.** A disposable database initialized with the shipped extension script supports default
bootstrap, repeated bootstrap with one credential, and a second project without a new namespace.
Version-22 data in an explicitly named namespace upgrades in place, preserves observation identity,
and supports repeat provisioning. Runtime-role tests prove credential/schema restrictions; a
disposable pre-existing privileged login loses its elevated flags when reconciled.

**Rejected:** schema creation per project; retaining tenant-routing language in runtime entry points;
requiring a namespace setting for an ordinary install; silently switching existing custom namespaces
to `memory`; renaming historical migration files merely to change their commentary.

**Impact and undo cost.** No data migration is introduced for terminology or defaults. Missing
`TAISCE_SCHEMA` now selects `memory` instead of stopping startup. Existing explicit namespaces keep
their behavior. The override remains because removing it would require an operator-controlled data
migration and would complicate isolated fixtures. Undoing the default is a configuration change;
restoring elevated login attributes weakens the documented privilege boundary.

### D70 — Runtime connections prove their effective database privilege boundary

Safe role flags do not undo a prior grant or object ownership. Bootstrap validates the effective
memory and registry roles after provisioning and before minting a credential. Runtime pools validate
every newly opened physical connection before admitting it; startup pings both pools before a
listener or worker starts. Login and active identities are both checked, along with immediately
inherited roles and roles reachable through `SET ROLE`, so `RESET ROLE` cannot expose a hidden
administrative session after a successful check.

The memory role may serve project memory and read its policy catalogs. It must not read control
objects, change those catalogs, own application tables/namespaces, create schemas, or reach elevated
role attributes. The registry runtime reads credentials; it neither writes them nor accesses project
memory. Bootstrap removes the previous registry write/CREATE grants. Credential issue/revoke and
schema changes use the operator's separate administrative connection. Memory namespace configuration
refuses `control`, `public`, `information_schema` and `pg_*` before any provisioning can mix stores.

Unsafe direct grants, column grants, inheritance, SET-role escapes, ownership and policy writes are
refused with a fixed diagnostic category. Tests seed each condition in isolated fixtures, force pool
reconnection, and prove explicit remediation restores service. Serving tests use actual non-owner
memory/registry logins, and administrative DSNs are refused on either runtime connection.

**Operator remediation.** Keep the admin DSN for bootstrap/operator commands; use separate non-owner
memory and read-only registry DSNs for serving. When validation reports a forbidden grant or owner,
inspect the named plane's role memberships and object privileges as the operator, remove the excess
privilege or restore the intended administrative owner, then restart. Bootstrap does not silently
reassign objects or revoke arbitrary memberships, since those may express an administrative policy
that must be understood before changing it. No credential material is included in the findings.

**Limits.** This is connection admission, not continuous monitoring of administrator actions. A
privileged administrator can change grants after admission, alter the checks or install a function
that exposes data. Arbitrary security-definer code and extension behavior require their own review.
The check does not make shared-role project reads confidential without authenticated scope filters.
It supplements the actual database grants; it is not a substitute for them.

**Rejected:** trusting different DSN strings to mean different privileges; checking only rolsuper;
checking only current_user after SET ROLE; checking only the first pool connection; automatically
reassigning ownership or stripping unrelated memberships; running catalog checks on every query.

**Impact and undo cost.** Privilege inspection adds catalog reads when a physical connection opens,
not to each memory operation. No production latency claim is made. Deployments that previously
served with an admin login now fail startup and must supply the intended non-owner identities.
Undoing the guard is a code change that restores that unsafe configuration; registry runtime writes
would also require a deliberate grant change and a new consumer contract.

### D71 — Project provisioning is atomic and physical names are validated

The provisioner and project table accept the same ASCII identifier contract: a lowercase letter,
then lowercase letters, digits or underscores, up to 63 bytes. Migration 0024 validates existing
project rows against that bound. Invalid historical rows refuse the migration atomically; project
names are never truncated or silently changed under credentials that already reference them.

Project storage and its registry row are one transaction, serialized per namespace/project for
repeat and concurrent calls. A failure while creating the partition, index or row rolls back newly
created storage. Catalog inspection checks the actual parent, partition bound and a valid full
cosine HNSW index on the embedding column. A relation with the expected name is not sufficient.
An incompatible existing relation is refused and preserved for operator investigation.

Short table names retain the existing `chunk_<project>` convention. Long table names and every new
index name use the complete SHA-256 digest encoded in base32. The generated table/index names have
different lengths, fit PostgreSQL's identifier bound, and avoid the previous table-versus-index
collision between projects such as `alpha` and `alpha_ann_idx`. Catalog validation also fails closed
on a pre-existing relation or a hypothetical digest collision. Existing correct attachments and
indexes are found by their actual properties and reused regardless of legacy names, including
previously truncated names. No retained data is moved or renamed.

The partitioned-parent index explanation is corrected: a parent index is virtual, with physical
indexes on its child partitions. Current provisioning creates a child HNSW index on demand. The
choice between that approach, parent-managed indexes or other partition strategies remains the
measurement work in #101; this correctness change does not settle their scaling tradeoffs.

**Evidence.** Real PostgreSQL tests cover rejected names without storage side effects, concurrent
long-name provisioning, shared prefixes, table/index name collisions, wrong bounds/parents/indexes,
legacy reuse, injected row failure and successful retry, migration refusal and project pruning.
The existing vector fixture verifies that the actual child HNSW index appears in the query plan.

**Rejected:** separate commits for DDL and the project row; trusting IF NOT EXISTS as validation;
letting PostgreSQL truncate derived identifiers; silently attaching or replacing a conflicting
relation; rebuilding every correctly attached legacy partition/index simply to rename it.

**Impact and undo cost.** Fresh project index creation is transactional and empty. Repairing a
missing index on populated existing storage is an operator maintenance operation with database
locking/build cost, not an online-build guarantee. Existing rows in the default partition that
prevent a new attachment require separate data maintenance; provisioning fails without losing them.
No partition-count, latency or approximate-recall quality claim follows from these tests. Reversing
the atomic write needs code changes; widening the project-name contract requires matching database
and API changes, while existing physical names can remain opaque implementation details.

### D1 — PostgreSQL is the only required dependency

Not a vector database, graph engine, message broker, cache or object store.

Each addition is a second place data lives, a second thing to operate, and — decisively — **a second
sweep an erasure has to prove it covered**. G6 rests on deleting through a registered dependency and
counting against the same predicate in the same transaction. That count means "nothing survives"
only while there is one place for something to survive in.

**Rejected:** a dedicated vector store (the vector is a column on the row it describes, and
separating them means two erasure paths and a consistency problem between them); a graph engine (the
fact table with resolved entity ends *is* the edge list, and a graph store is weakest at exactly the
temporal work D3 makes central); a broker (the log is already an ordered, durable, replayable
sequence).

### D2 — An extension ships only with a reader in this codebase

An extension runs inside the backend process, so a fault in one takes the cluster, and the cluster is
the product. The test is not *"might we want this"* but *"what reads it today"*.

Currently: `vector` (model-identified message generations and their dimension-specific HNSW, D104), `btree_gist` (the traversal
indexes and D4's constraint), `pg_stat_statements` (the only way to answer "which query got slower"
after a deploy without adding an instrumentation path to the server).

**Rejected:** a lexical/BM25 extension — no lexical index exists in this schema and none is designed,
since retrieval anchors on entities and reads passages as evidence (G1), and all three embedded
surfaces (G5b) are vector. It is also the most expensive thing the image could carry: a from-source
compile plus a `shared_preload_libraries` entry. If hybrid retrieval is wanted it returns **with the
query that reads it**. Also rejected: `pgcrypto`, because `gen_random_uuid()` has been core since
PostgreSQL 13 and that was its only use here.

### D3 — Time is a range, not a pair of columns with a sentinel

`valid` and `known` as `tstzrange`, GiST-indexed.

The alternative is scalar start/end columns where a sentinel means "open", which puts
`(end = 0 OR end > $1)` in every query and makes the end column a poor leading key in every index —
a representation problem surfacing as an indexing problem. A range is one column, one containment
operator, one index.

**Rejected:** sentinel-encoded bigints (above); `NULL` as open (correct, but a three-valued
comparison in every predicate is the same defect wearing a nicer hat).

### D4 — Supersession is a database constraint, not an application lock

A single-cardinality predicate cannot hold two overlapping valid intervals for one subject, enforced
by `EXCLUDE USING gist`.

A lock is correct only while every write path remembers to take it — including one written next year
by someone who has not read this. A constraint is correct because the database will not store the
violation.

**Rejected:** a fact-slot advisory lock (correct today, and one new write path from being wrong;
also the hottest contention point on the write path); optimistic retry on a unique index (a unique
index cannot express interval overlap, which is the actual invariant).

### D5 — Entity identity is `(scope, normalized_name)`. Type is an attribute

D66 limits this name-based key to named entities and gives speaker references a separate namespace.

Putting the type in the identity key makes one thing extracted once as `place` and once as `thing`
into **two nodes holding two halves of its facts** — a silent under-merge on every hop, invisible
because both rows look correct on their own.

**Rejected:** `(scope, entity_type, normalized_name)`. A disagreement about type should be a
disagreement about one node, not the creation of a second.

### D6 — Resolution merges by normalised name only; meaning is a separate surface

Lowercase, trim, collapse whitespace. Nothing cleverer at this layer.

Merging "the Dublin office" with "our Dublin site" needs either an embedding comparison with a
threshold or a model call per entity at write time. **Over-merging puts two people's facts on one
node, which is a governance failure; under-merging costs recall on a hop.** That asymmetry decides
it: the conservative rule is the default, and meaning is added as the entity's embedded surface (G2)
where it can be measured and tuned against a held-out set rather than guessed.

**Rejected:** similarity-threshold merging at write time (a threshold nobody can justify, applied
irreversibly); a model call per entity (a write-time artefact with a confidence score attached).

### D7 — The vector index is per project partition, never one index over all of them

`chunk` is LIST-partitioned on scope, with an HNSW built inside each partition.

A single graph behind a scope predicate is unreachable in both directions. The planner prefers an
exact sort and never enters it; and post-filtering an approximate result loses recall in proportion
to how little of the corpus belongs to the caller. Pruning first and scanning inside one project's
graph gives an exact-correct result with no `ef_search` to tune.

A partition is DDL per project at a controlled moment — project creation — not something issued from
a request or a worker. Because a tenant is a schema (D8), the partition count per table is bounded by
projects-per-tenant.

**Rejected:** one HNSW on the partitioned parent (unreachable, and maintained on every insert for
nothing); no ANN index at all with an exact scan per scope (correct, and linear in project size,
which is the thing G1 exists to remove); iterative/relaxed approximate scanning (tuning a recall
knob to compensate for an index built over the wrong set).

### D8 — A tenant is a schema; a project is a column

**Hard and soft, and they are not the same kind of boundary.** A cross-tenant read is impossible
because the other tenant's name is not in the statement — not because a predicate excluded it. There
is no tenant column to forget. Cross-project reads are *intended*, since a principal granted several
projects gets one bundle across them, so scope is a column and the boundary is a permission checked
on every read.

A schema name cannot be a bind parameter, so it is interpolated — which is why it comes from a
validated type that exists only after construction. Forgetting to validate is a compile error rather
than an injection.

**Rejected:** a `tenant_id` column with row-level security as the primary control (one missing
predicate from a cross-customer leak, and the guarantee lives in policy rather than in the absence of
a name); a schema per project (a multi-project recall becomes a cross-schema union that cannot be
planned as one query); a database per tenant (connection pooling and migration both scale badly).

## Erasure

**Amended by D35.** There is one tenant, so the hard boundary this describes is now trivially true
rather than load-bearing. The schema stays for the reasons that survive — erasure walks one namespace,
migration applies in one order, a backup has an obvious edge — and the project half of this entry is
unchanged and is the boundary that still does work.

### D9 — Erasure walks a registered dependency, not a list of tables

Every projection registers what it derived and from which observation. Erasure walks that, so
`COUNT(*)` afterwards means *"nothing derived from that observation survives"* rather than *"nothing
survives in the tables somebody remembered to check"*.

A projection kind that does not register is invisible to erasure, so the registration is asserted by
a test that fails when a kind is written without one.

**Rejected:** a hand-maintained delete list (correct until the next projection is added); a
containment index over source ids on each projection (works, and puts the burden on every future
table to remember the same pattern).

### D10 — The residual count is a column, not a log line

`erasure_request.residual` holds the per-projection-kind count after the delete, in the same
transaction, with a constraint that it is present exactly when the request is complete.

This is the artefact the product is sold on. Something a customer may be asked to rely on should be
queryable and constrained, not recoverable by grepping.

**Rejected:** emitting it to the audit log only (unqueryable, and a log is not covered by a
constraint that says an incomplete erasure cannot claim a residual).

### D11 — A confidence is reported; a salience is computed. They are separate columns

Salience is derived — recency, frequency, centrality. Confidence is what the producer of a claim
asserted about it. Collapsed into one number, a weakly-extracted fact that is mentioned often becomes
indistinguishable from a certain one, and extraction quality becomes unreviewable after the fact.

**Rejected:** one score (above); confidence as metadata on the evidence row rather than the fact
(the fact is what gets ranked, so the fact is what needs the number).

## Write path

**Amended: the salience column is gone, and the distinction is not.** It was zero for every fact ever
written, because nothing computed it and nothing read it. Salience is a ranking signal, recall
deliberately ranks nothing yet, and a ranking stage introduced before the plain path is measured makes
it impossible to say which stage is doing the work and which is doing harm — so it arrives with its
measurement or not at all, and that measurement needs hardware this project does not have.

The reasoning above is unchanged and is why `confidence` stays: a reported certainty and a computed
importance are different things, and collapsing them would make a weakly-extracted fact mentioned
often indistinguishable from a confidently-extracted one. What changed is only that one of the two had
no reader, and a column that exists because it was expected teaches every reader that this schema
contains things which are not true.

### D12 — Every observation carries its speaker, from the first migration

`source_role` is `NOT NULL`.

The role policy — an assistant's claim never becomes a user's fact — can only be enforced if every
write surface carries the speaker. A role that is optional at the bottom becomes optional everywhere
above it, and the surface that skips it is usually the one written last and used most.

**Rejected:** a nullable role with a default (the default is a lie about who spoke); role inferred
from the payload shape (guessing at the one thing the policy depends on).

### D13 — The log offset is assigned per scope, not by a global sequence

The freshness watermark is the highest offset with **no gap below it**. A globally-issued sequence
has holes in every scope by construction, so "contiguous" would be unanswerable and the watermark
would have to mean something weaker than it says.

**Rejected:** a global identity column (above); a timestamp as the watermark (clock skew and equal
timestamps make "everything up to here" unprovable).

## The graph

### D15 — The predicate vocabulary is closed, and a foreign key is what closes it

Thirty-nine relation types, seeded per tenant into `{schema}.predicate`, with `fact.predicate`
referencing it. Each carries a **cardinality** (`one` — at most one currently-valid fact per subject
and predicate; `many` — several coexist), an **object kind**, and a **semantic type**.

An extractor asked for a relation invents one per sentence: `works_at`, `employed_by`,
`is_employed_at`, `has_employer` — four edge types describing one relation. A traversal asking where
somebody works matches one and silently misses three quarters of what was extracted, and every row
involved looks correct. It also makes the stages after it unbuildable: supersession asks "is there a
current fact with this subject and this predicate", which only reconciles when two assertions chose
the same word, and a cardinality rule cannot be written for a vocabulary nobody can enumerate.

Cardinality is declared **when the predicate is defined**, because at supersession time all that is
visible is two rows and the question is no longer answerable. Its enforcer is the exclusion
constraint in #9; declaring it now is what lets that arrive as a constraint rather than as a redesign.

The size is bounded at 30–50 and asserted by a test. Too small and real relations have nowhere to go.
Too large and the near-duplicates are back, spread over a set nobody can hold in their head — which
is harder to see than an open vocabulary, not easier.

**Rejected:** an open vocabulary (above). A list in application code rather than a foreign key — that
is a convention every write path has to remember, and the path that forgets is the one written last;
as a constraint an unknown relation cannot be stored by any path, including a hand-written `INSERT`
during an incident. A generic `has_value` or `has_attribute` — a wildcard makes the closed set
vacuous, since anything can be phrased as a value of a compound object, and every claim the ontology
was meant to refuse arrives through it wearing an accepted name. Inverse predicates such as
`reports_to` alongside `manages` — edges are directed and traversal follows both ends, so an inverse
is a second spelling of one relation and reintroduces exactly the split this decision prevents. A
`mentioned` or `co_occurs_with` relation — an edge between two things that appeared near each other
has no direction and no meaning, so a multi-hop question gets no hops out of it. `ON DELETE CASCADE`
on the reference — retiring a predicate would delete the memory in order to permit a schema edit, so
it is `RESTRICT` and the change fails loudly.

### D16 — A relation outside the vocabulary is recorded as unmapped, never admitted as an edge

Not dropped, and not admitted.

Nothing is lost by refusing it, because the refusal is narrower than it looks: the message is already
stored as a row and retrievable as a passage, so what is refused is **the typed edge**, not the
memory. This is the same shape as the role policy — an assistant's sentence is still stored, it
simply does not become something the person is recorded as having said.

Admitting it is what cannot be allowed. One relation admitted outside the set means the set is
advisory, and an advisory vocabulary is an open one with extra steps.

The unmapped claim is kept with its evidence so the rate is a query rather than an estimate: a high
unmapped rate is evidence the vocabulary is wrong, and that argument needs a number. D18 is where
that record lives, and it holds the other silent refusal alongside this one.

**Rejected:** dropping unmapped claims silently (loses the evidence that the vocabulary is wrong,
and the failure is invisible precisely when it is worst); admitting them as edges with a flag (a flag
is a filter every future query has to remember, so the vocabulary is closed only in the queries
somebody remembered); mapping them onto the nearest admitted relation by similarity (records a
relation nobody asserted, and the wrongness is unrecoverable because the original wording is gone).

## Extraction

### D17 — A quote is a search term, never evidence. Whitespace is tolerated and nothing else is

The extractor takes the model's quote, locates it in the message, and stores **the text it found
there**. Every claim satisfies `content[ByteStart:ByteEnd] == Quote` by construction rather than by
the model's cooperation, and a claim whose quote cannot be located is discarded — not a weaker claim,
an uncitable one, in a product whose argument is citation.

Matching is exact first, then whitespace-tolerant, with the span mapped back to the original bytes. A
model copying text routinely turns a line break inside a sentence into a space; the words are the
same words, so refusing that discards a correct citation over a formatting artefact. A span into the
normalised copy would be the same failure from the other side, so it is never returned.

Case is not tolerated. A model that changed the case retyped rather than copied, and a tolerance wide
enough to match `moved to dublin` against `moved to Dublin` is wide enough to match a sentence the
model composed.

A quote appearing twice takes the first occurrence: the same words in both places resolve to the same
text, only the instance differs, and nothing reads the instance.

**Rejected:** trusting the span the model returns (a span that is off by one character resolves to
the wrong words while looking correct, and there is no way to notice); exact matching only (discards
correct citations over a newline); case-insensitive or fuzzy matching (no line left to draw once
retyping is admitted); embedding similarity between quote and message (a threshold nobody can
justify, applied to the one thing that must be verbatim); storing the model's wording alongside a
located span (a quote its own span does not reproduce — self-inconsistent, and it looks fine).

### D18 — A claim outside the vocabulary or without a locatable quote is written down, not counted

`{schema}.rejected_claim` keeps both refusals with the model's own wording, and its `predicate`
column deliberately has no foreign key.

Both refusals are silent and both are claims about quality nobody can check without the text. A rate
of eleven percent cannot distinguish one missing relation asserted five hundred times — the
vocabulary is wrong and the fix is one row — from five hundred asserted once, which is the tail and
is noise. Those need opposite responses.

It registers for erasure like every other projection: a refused claim still carries somebody's words,
and a table of "things we did not store" that turns out to store them is the worst version of this
feature.

**Rejected:** a counter per reason (cannot distinguish the two cases above, which is the only reason
to measure); an error log (both refusals are ordinary, so they would be noise inside the record of
things that are not); normalising the recorded predicate (the near-duplicates that justify adding a
relation are visible only in the exact words proposed).

## Formation

### D19 — Forming happens behind the append, and freshness is two numbers

A turn is stored immediately and formed afterwards. Forming means a model call per message; held
inside the append, an agent's turn would block on its own memory write and the product would be
slower than having no memory at all.

What that costs is a window where a turn is stored and not yet recallable, so the window has to be
readable. `watermark.log_offset` advances on append and says the turn was stored — which the caller
already knows, because the append returned. `watermark.formed_offset` is the highest offset whose
turn has been through extraction **with nothing unformed below it**, and it is the number a recall
depends on.

It is derived from the backlog, not incremented per completion. An increment is correct only if
completions happen in order, and two turns forming concurrently finish in whichever order their model
calls return — so the number would run past a gap and a caller told "current to 40" would be missing
turn 12. That is the one failure a freshness number must not have, because it is acted on rather than
read.

`formed_offset` is NULL until the scope's first turn forms, not -1. Offsets start at zero, so a
sentinel is a value in the same domain as the data and every comparison carries a clause to exclude
it that one query eventually forgets.

One worker per scope, held by an advisory lock across the whole drain. Two workers taking the same
turn would assert every fact twice, and the duplicates would be indistinguishable afterwards from a
person saying the same thing in two conversations — a lock inside the transaction that picks the turn
is released before the model call it was meant to protect.

**Rejected:** synchronous formation (correct recall immediately, at the price of an append that waits
on a model); redefining the single watermark to mean formed (loses the answer to "did you receive
it", which is what an append is asked to confirm); incrementing the formed offset per completion
(above); a tenant-wide formation lock (one busy project would starve every quiet one beside it).

### D20 — The role applied to a claim is the message's, never the turn's

Formation passes each message's own speaker to the fact table.

A turn opened by the user does not make an assistant message three positions later speak for the
principal — and turns opened by the user are most turns, so passing the turn's role would quietly
promote nearly every model sentence in the system.

Every message is extracted, including ones whose claims will then be refused. Skipping them would
save a model call and produce identical rows, but only because formation would be applying the role
policy itself, in a second place, with reasoning that has to stay in step with the first. The policy
lives on the one road to the fact table so that a path which forgets it does not exist.

Refusals by role are counted and **not** written to `rejected_claim`. Unlike the other two refusals
this is not a quality signal — it is the policy working as designed, and its rate is already knowable
from the roles of the messages in the turn.

**Rejected:** the turn's lead role (above); a role on the observation only, with no per-message column
(the policy would be unenforceable for every message after the first); skipping extraction for
messages that cannot speak for the principal (duplicates the policy to save a call).

### D22 — Message content leaves only to a host somebody wrote down

`TAISCE_INFERENCE_ALLOWLIST` names the permitted hosts, separately from the endpoint, and a
disagreement between them is fatal at startup.

Extraction sends a customer's message text to a model. That is an egress of exactly the data this
product exists to hold, and the destination is a string in an environment variable — one typo, one
copied deployment file, and the content goes somewhere nobody chose. Unlike most misconfigurations it
cannot be undone once noticed.

Matched on host **and port**, never by prefix: a prefix match accepts
`https://inference.example.attacker.test` for a list containing `inference.example`, which is the
failure an allowlist exists for. Plaintext is refused anywhere but the local machine — listing a host
says where content may go, not that it may go there in the clear.

An empty allowlist permits nothing. Listing a host is a deliberate act, and that is the whole
mechanism: it cannot be arrived at by default, only by somebody writing the host down.

**Rejected:** deriving the permitted host from the endpoint (there is then nothing to disagree with,
so a typo configures its own approval); a warning rather than a refusal (read after the egress it was
meant to prevent); open when unset (one forgotten variable away from not being a guard); a single
combined variable (the same string cannot both propose a destination and authorise it).

### D21 — Evidence names the message it indexes, not only the turn

`fact_evidence.source_ordinal`, in the primary key.

A span indexes one message and an observation is a whole turn. Without the ordinal, resolving a
citation means joining to the turn's messages with nothing to choose between them — and a byte offset
taken against the wrong message does not fail. It returns a plausible fragment of a different
sentence and attributes it to the fact as its evidence, and the two disagree only if somebody reads
them side by side.

In the key rather than beside it, because one fact can be evidenced from two messages and two spans
starting at the same offset in different messages are different evidence. Outside the key the second
silently replaces the first.

**Rejected:** the ordinal as a plain column (a duplicate key would drop a citation); one chunk per
turn so the span indexes the turn (a span into a concatenation that exists nowhere); resolving by
searching the turn for the quote (the search is what produced the span in the first place, and it
would find the wrong occurrence exactly when two messages restate each other).

## Recall

### D23 — Anchors are resolved by windowing the question, not by a model call

Every window of up to four words in the question is normalised the way entity names are and looked up
against canonical names and aliases.

A model call per question would put a second inference hop in front of every read, on the path a
caller is waiting on, to answer something a lookup already answers: an entity is in memory or it is
not, and its name is a string we already hold. Windowing is exact, has no vocabulary of its own, and
costs one query.

Multi-word windows are what make an entity reachable by the name it is stored under. Single words
alone leave every two-word entity unreachable. Aliases are matched alongside canonical names because
a person asks about something the way they mentioned it, and the alias list is exactly the record of
how they mentioned it.

**Rejected:** named-entity recognition by a model (a second inference hop on the read path, with its
own vocabulary that can disagree with what is stored); single words only (two-word entities become
unreachable by name); full-text search over entity names (ranks, and a ranking that nothing measured
is a stage doing unknown work); trigram or fuzzy name matching (it is D6's over-merge problem moved
to read time, where it is invisible instead of inspectable).

### D24 — The authorised project set is an argument on every read, and empty returns nothing

A project is a permission inside a tenant, so `scope = ANY($1)` is on every statement and the set
comes from the caller.

Empty returns nothing rather than everything. The failure being prevented is a read that WIDENS when
its permission list is missing — the one mistake a permission list exists to stop, and the one that
is invisible in a test suite where every caller happens to pass a scope.

This is the soft boundary and it is deliberately not the hard one. Cross-project reads are intended:
a principal granted several projects recalls across them in one bundle. The hard boundary is the
schema name in the statement, which no predicate can be wrong about.

**Rejected:** a scope held on the reader and defaulted (a default is a decision taken once for every
future caller); empty meaning all (fails open, and fails open exactly when something upstream
forgot); a project as a schema (would make the intended cross-project read a cross-schema query).

**Amended by D43.** A token belongs to one project, so the authorised set has one member and the
cross-project bundle this entry contemplated is not available to a single credential. Everything else
here is unchanged and matters more: the set is still an argument on every read rather than a default,
and an empty one still returns nothing — that property is what makes a revoked or unattached token
harmless rather than dangerous.

### D25 — The first recall ranks nothing, and a bundle that was cut says so

Facts come back in a deterministic order that carries no judgment — predicate, then id. Not recency,
not confidence, not salience.

Each of those is a stage that can only be justified by a measurement it improves. Introduced before
the plain anchor-and-return path is measured, they make it impossible to say which stage is doing the
work or which is doing harm — and one of them usually is.

The limit is a cap, not a selection, so `Truncated` is on the bundle. Without it a caller reads the
cut as a verdict: "these are the important ones" is the one thing this bundle is not claiming.

A fact with no evidence is not returned. That is a product statement rather than an oversight: what
is sold is a memory that can show its receipt.

**Rejected:** a recency lens, reciprocal rank fusion, a cross-encoder reranker (all deferred until a
measurement justifies them — see G7); ordering by confidence (confidence is what an extractor
asserted, not what a reader needs, and using it as a rank quietly makes it one); silent truncation
(the caller cannot tell an exhaustive answer from a cut one, and will assume the wrong one).

### D26 — A projection kind declares where it lives, and an undeclared one cannot be registered

`{schema}.projection_kind` holds every kind of derived row and the table it lives in.
`projection_dependency.projection_kind` references it.

D9 made erasure walk a registered dependency rather than a list of tables. That leaves one thing
still held somewhere: a kind and an id do not say which table to delete from. As a switch inside the
eraser that mapping is a list somebody maintains, and every milestone after this one adds a
projection — embeddings, community reports, summaries. Producer and eraser drift in one direction
only: the producer ships and the eraser is updated later, or not at all. What survives is a residual
count that says zero because it counted the kinds somebody remembered.

As a foreign key, a projection kind that is not declared **cannot be registered at all**. The failure
moves from "erasure quietly misses it" to "the write fails the first time it runs" — a failure landing
on the person adding the projection, on the day they add it. And because the eraser iterates the
declarations rather than a list of its own, a kind declared later is covered without the eraser
changing.

The table and column names are constrained to `[a-z][a-z0-9_]*` at the source, because they are
interpolated into a `DELETE`: PostgreSQL parameters are values and never identifiers, so the
constraint is what makes the interpolation safe where the name is written rather than where it is
used.

**Rejected:** a switch in the eraser (above); a test asserting the eraser covers every kind seen in
`projection_dependency` (only fails if the test's write path happens to produce the new kind, so the
projection most likely to be missed is the one written by a path nobody added to the test); erasure
by scanning every table for a `data_subject_id` column (an entity has none, and a projection that
does not carry one would be invisible while looking covered).

### D27 — A projection shared with another subject is kept, and keeping it is not a residual

The deletion predicate removes a projection only when every registration of it belongs to the subject
being erased. The residual is counted with that same predicate.

An entity is the case that decides it. `Ensera` is registered by every subject who mentioned it, and
deleting it because one of them asked to be forgotten would take a node out of everybody else's graph
— an erasure that damages the people who did not request one. Counting it as a residual instead would
be worse: every erasure would report a non-zero residual for correct behaviour, and the number would
stop being read.

**Rejected:** deleting every registered projection (damages other subjects' graphs); keeping shared
projections but counting them as residual (makes a clean erasure indistinguishable from a failed one,
so the number is ignored); a reference count on the projection row (a second place the truth lives,
and it drifts the moment a delete happens outside the path that maintains it).

### D28 — The residual is counted before the observations are deleted, in the same transaction

Projections are deleted through their registrations, counted through the **same** registrations, and
only then are the observations removed.

Deleting the observations first cascades the registrations away, and the count is then made against a
predicate that selects nothing — a residual of zero meaning "I could not find anything to check"
rather than "nothing survived". That is the failure mode of a residual count: it is far easier to
produce a zero that is vacuous than one that is earned.

Counting after the commit would be a separate read of a moving target: a concurrent formation writing
a new projection for the same subject appears as a residual the erasure did not leave, and two
concurrent erasures of one subject each report the other's work as their own clean sweep.

**Rejected:** counting after the transaction commits (above); deleting the observation first and
relying on cascade for everything (the cascade is what makes the count vacuous, and an entity has no
foreign key to cascade through anyway); a residual in a log line rather than a column (D10 — it is
the artefact the product is sold on, and a log line is not something a customer can be shown).

## The control plane

### D29 — One database, two schemas, two database identities

The registry — tenants, projects, principals, credentials, plans — lives in its own schema, owned by
its own login role. The memory service connects as a different role with no privilege there at all.

**Why the boundary is a grant and not a rule.** The memory service serves one tenant per statement,
and its entire boundary is that another tenant's name is not in the statement it is running. A
component that can list every customer, read every credential and read every subscription is one bad
predicate away from crossing that. Expressed in code, "the memory service does not read the registry"
is something every future query has to remember, and the query that forgets is the one written by
hand during an incident. Expressed as a grant, no statement can break it.

**Why one database rather than two.** Creating a tenant is a registry row, a `CREATE SCHEMA` and a
full migration run. DDL is transactional here, so in one database that is one transaction and it
either all happened or none of it did. Across two databases it is not atomic and needs a reconciler:
a registry row with no schema is a customer who can authenticate and has nowhere to write, and a
schema with no row is storage nobody bills and nobody reclaims. Two databases buy a stronger
isolation than the grant already provides, and they buy it by making the most safety-critical
operation in the system non-atomic.

**What this boundary does not buy, stated so nobody claims more.** It does not separate tenants from
each other. One process serves every tenant, so it necessarily holds privilege on every tenant
schema, and the separation between them remains D8's — the schema name in the statement. It also
cannot hide the *existence* of other tenants: every role can read the system catalogue, and that is
not revocable without breaking ordinary tooling.

Neither is a shortfall. A component that already holds every customer's memory learns nothing from
the list of their names. What the grant protects is a different category — credentials,
subscriptions, and the people behind the accounts — and none of that is memory.

**Rejected:** one identity with the rule in application code (a convention every query must remember;
the one that forgets is written under pressure); two databases (above); a single schema with the
registry beside the tenants (there is then no boundary to grant across, only a naming convention);
building the registry first and fencing it afterwards (it is readable by the memory service for
however long that takes, and every query written meanwhile assumed that was allowed).

**Deferred rather than rejected:** running the two planes as separate processes. The grant is what
makes the guarantee, and process separation adds isolation on top of it without changing the data
model — so it is a deployment choice that can be made later, and making it now would buy nothing that
is not already bought.

**Amended by D35.** The registry it separates has no reader in a single-tenant instance, so the
control plane as a *commercial* boundary is retired. The least-privilege half is kept and matters more
than before: the portal is a new reader of personal data, and a role that cannot change the schema is
the difference between an operator surface and a database client.

### D30 — A credential is exchanged for a short-lived signed grant, verified without a round trip

The control plane resolves a credential to a tenant schema, an authorised project set and an
entitlement set, and issues them as a short-lived signed grant. The memory service verifies the
signature with a public key and reads nothing.

**Why not ask the control plane per request.** Recall is on the critical path of every agent turn.
A lookup there puts a network hop on every read and makes the whole product's availability equal to
the registry's — so a control plane that is down stops every agent that is already working, not just
the ones trying to sign up. Verifying a signature offline keeps memory serving through an outage,
and what stops is issuing new grants: the right thing to lose.

**Why not let the memory service read the registry.** D29.

**Why entitlements travel with the grant.** A bundle constrains in both directions, and an enforcer
that fetches the promise before enforcing it fails open the moment the fetch fails. Carried in the
grant, enforcement is local and a failed lookup is a failed request rather than an unlimited one.

**The cost, and the thing that bounds it.** Revocation is not instant: a grant remains valid until it
expires, so the lifetime is the revocation window and it is a number somebody has to choose rather
than a property to hope about. Where instant matters — a compromised credential, or a subject whose
erasure must take effect now — the control plane publishes a list of revoked grant identifiers, which
is small, contains nothing about anybody, and can be replicated to the memory service without giving
it a single privilege in the registry.

**Rejected:** a lookup per request (above); a long-lived grant (makes the revocation window a
liability rather than a bound); a shared secret rather than a signature (every party that verifies
can also mint, so the memory service could issue itself any authority it liked); putting the
credential itself in the grant (the memory service would then hold something it could replay against
the control plane, which is the authority it was deliberately not given).

**Superseded by D35.** A credential resolving to a tenant and its entitlements describes a product
that no longer exists. What replaces it is narrower and still required: a credential resolves to an
authorised project set, and an empty set returns nothing. The issue that owns the replacement says so.

### D31 — The read audit is append-only, and it holds nothing that could ever need erasing

Who accessed a project, with which credential, when, and how much came back. Not whose memory it was,
and not what was asked.

**The two properties only fit together because of the second one.** An audit trail an erasure can
delete is not an audit trail — the record of who read a person's memory would vanish on the request of
whoever wanted it gone, and that request is the last thing that should be able to do it. An audit
trail an erasure cannot touch is personal data surviving an erasure, which the product exists to make
impossible. The way out is to record nothing that an erasure could ever be asked about, which makes
append-only unconditional rather than a conflict to manage.

So no data subject identifier, and **no query text** — a question is content, sometimes the most
sensitive content anyone sends, and a hash of a short question is guessable rather than opaque.

**What this can answer:** an unusual volume or pattern against a credential, which is how a
compromised key is found; and which of a customer's own principals reached which project, when.

**What it cannot answer, stated as a loss rather than omitted:** a data subject asking who read *their*
memory specifically. Answering that would mean recording, permanently and un-erasably, that a
particular person's data was read — which is a worse thing to hold than the question is worth
answering.

**Recorded behind the response, not before it.** Recall is on the critical path of every agent turn,
and a synchronous insert makes every read wait on a write that no caller is waiting for. The cost is
that a process dying between the response and the record loses that entry, which is acceptable for a
trail whose purpose is pattern rather than proof of any single read.

**Rejected:** the subject identifier in the record (above); a pseudonymous or hashed subject (still an
identifier, and it would be the one un-erasable fact about a person in the system); deleting audit
records with the subject they name (makes the trail unreliable exactly when it matters); the query
text or an embedding of it (content, in a log that can never be cleaned).

### D32 — Inside a subscription the customer's clock governs retention. Outside it, ours

`retention_until` is set from a policy the customer configures. We enforce what they set and impose
nothing. A customer who configures no policy keeps everything.

Retention is their judgement about their own obligations, and those differ by industry, jurisdiction
and contract in ways no default of ours could be right about. A plan-derived expiry would also change
what happens to data already stored when somebody changes plan, which means a commercial decision
silently rewriting a compliance one.

The one clock that is ours is the one that runs when there is no subscription at all — see D33. That
line is worth stating because it is the whole of it: while they are a customer, nothing of theirs
expires except on their own terms.

**Rejected:** a retention default derived from the plan (a commercial decision rewriting a compliance
one, retroactively, on downgrade); a platform maximum (an opinion about somebody else's obligations);
no retention mechanism at all (three columns carrying an expiry that nothing reads is a cost with no
mechanism behind it).

**Superseded by D39.** There is no subscription, so there is no second clock. Retention runs under
the operator's policy, unconditionally — which is what this entry argued was correct whenever it was
possible.

### D33 — A lapsed subscription gets a grace period, then the tenant's schema is dropped

Not suspended indefinitely, and not deleted on the day a payment fails.

Storing a customer's memory forever after they stop paying makes the product free to anyone willing to
let a card expire, and unbounded storage from the cheapest action anybody can take. Deleting at the
moment of failure destroys a year of somebody's memory because a card expired and a notice went to
spam. A bounded grace period is the only shape that is neither.

**Export works throughout the grace period, and so does erasure.** Those are rights that do not depend
on a commercial relationship, and a product that withholds them to collect a debt is holding data
hostage. The customer is told, more than once, what will be deleted and when — a deletion nobody was
warned about is the same failure as a deletion nobody asked for.

**The mechanism is dropping the schema, which is the strongest erasure available here.** A tenant is a
schema, so there is no sweep, no walk over registered projections, and no residual to count: the
container is gone, so residual is zero by construction rather than by the correctness of a routine.

**Two consequences that follow and should not be discovered later.** Anything held in that schema goes
with it, including the tenant's own verification log — so a receipt issued to a departing customer
becomes uncheckable once the tenant is gone, and the export they were urged to take is the only thing
that survives. And an abandoned account that never paid at all is the same path: signing up is the
cheapest action anyone can take, and a tenant created and forgotten is a schema, a partition and an
index sitting in the substrate forever.

**Rejected:** indefinite suspension (unbounded storage from a free action); deletion on first payment
failure (a card expiring is not a request to be forgotten); withholding export from a non-paying
customer (holding data hostage); marking rows deleted rather than dropping the schema (leaves the
residual question open when the answer could have been structural).

**Superseded by D39.** Nothing lapses. Dropping the schema remains the strongest erasure available
and is now an operator action rather than a billing consequence.

### D34 — One verification log per tenant

The log's origin names the tenant, its checkpoints are the tenant's, and the anchor a client retains
is about their own history and nothing else.

A tenant is a schema and the boundary is that another tenant's name is not in the statement. A single
log across all customers would be a structure whose size discloses our total governance volume, and
which any one customer verifying it would learn the rate at which everybody else is erasing. Per
tenant, a customer's verification tells them everything about themselves and nothing about anyone
else — which is the same shape as every other boundary here.

The cost is more checkpoints to produce and, if it is ever offered, more for an auditor to hold. That
is the cost of the boundary and it is the same cost every other table pays.

**Rejected:** one log for the deployment (discloses volume and rate across customers, to any customer
who checks); a log per project (a project is a permission and not a boundary, so this would multiply
the structure without separating anything that is not already separated by scope).

## Method

**Amended by D35.** One tenant means one log by construction. The reasoning that a shared log
discloses other customers' governance volume no longer applies, and what remains is the simpler
version: the log covers the instance.

### D14 — Nothing is brought in from another codebase unless it is asked for

Not code, not decisions, not measurements. Somewhere something was tried is not a source of answers
here: those choices were made under constraints that are not these constraints, and a decision
carried across without being re-derived is one nobody can defend when it is challenged.

Where something **is** asked for, what transfers is the **idea**: the implementation is rewritten,
reviewed and validated here. The check that catches a copy is D2's — every part must have a reader in
this codebase. Something present because it was in the original, rather than because something here
uses it, is a cost with no mechanism behind it.

**Rejected:** importing a decision register wholesale (imports the frame that produced a different
architecture); "carry the decisions that still hold" (presumes they hold, and the presumption is the
error).

**The exception was invoked on 2026-09-07**, for the predecessor system this work continues — read on
request, to establish scope and to learn what it got wrong. The boundary held while reading it is the
one rule 3 already sets: **capabilities and findings cross, files do not.** What came back is issues
with their own arguments — #80, #81, and confirming evidence on #62 and #74 — each re-derived against
this codebase rather than justified by where it was seen.

Copying files is blocked separately and for a different reason: that repository carries no licence and
almost all of its history is authored under a company address, so publishing derived code under
Apache-2.0 is a question of who owns it rather than a question of engineering. #79 holds it.


## D72 — Exact anchoring uses separate indexable lookups and refuses excessive expansion

**Date:** 2026-09-08. **Tracking:** [#102](https://github.com/althunibat/taisce/issues/102),
[#93](https://github.com/althunibat/taisce/issues/93).

**Decision.** Canonical names use the existing project/name B-tree identity index. Alias lookup
uses array containment in a separate query arm, which the existing GIN operator class supports.
An alias identical to the canonical name does not duplicate that match. Distinct matching terms
remain visible; this does not merge entities, add fuzzy matching, or invent aliases. Subject
attribution and authorized project predicates apply in both arms, and the selected speaker retains
its own identity path.

A question may contain at most 8192 UTF-8 bytes and expand into at most 512 distinct candidate names.
Direct store calls also admit at most 32768 aggregate term bytes. The SQL returns at most 257
entity/name pairs, including the selected speaker, before its final ordering. More than 256 matches
refuses the whole recall before graph traversal. HTTP returns `400 invalid_question`; it does not
return an arbitrary partial bundle or suggest that nothing was known. Ordinary fact-budget cuts
continue to return a successful bundle marked `truncated`. These are conservative admission
ceilings, not measured optimal settings or relevance thresholds. #93 owns workload-based tuning.

**Security and cost.** A compromised project credential can submit long questions or common aliases.
Checking bytes before term expansion bounds that allocation; checking term count/bytes before SQL
bounds the database arguments; checking match overflow before facts bounds traversal seeds.
Per-term GIN work can still depend on posting lists in other projects, pending index entries and
planner decisions. These controls do not establish a corpus-independent read cost or replace
instance/project concurrency and backlog admission.

**Evidence and alternatives.** `TestAnchorQueryPlans` explains the actual production query on
20,000 entities across two projects with 16 aliases each, using normal planner settings. It checks
canonical hits, aliases, misses and a 128-term missing question, including index use and project
results. The previous OR expression scanned the entity table in that fixture. A single batched
array-overlap lookup also selected a full scan for the large question; it was not selected. A
normalized, project-keyed alias relation remains under evaluation in #102 because its write and
erasure ownership contract needs measurement and design. Local plan diagnostics are documented in
[28-anchor-query-plans.md](28-anchor-query-plans.md); no local latency result is a production claim.

Rejecting overflow was chosen over silent anchor truncation, which could discard the selected
speaker or a legitimate ambiguity and make the surviving facts appear complete. Adding a model
call to recognize names was rejected: it changes the exact-match contract and adds inference
latency and availability to each read.

**Undo cost.** No persisted representation changes. Query structure can be replaced without a
migration. Limits are documented client-visible refusals; raising them requires resource evidence,
and lowering them requires a compatibility review. Alias ownership, concurrent registration,
normalized-relation comparison and controlled-host measurements remain open under #102.


## D73 — HTTP work is admitted before database use and observations have explicit size ceilings

**Date:** 2026-09-08. **Tracking:** [#93](https://github.com/althunibat/taisce/issues/93).

**Decision.** The serving command derives a process-local admission gate from the actual connection
pools. Memory work admits at most `min(64, memory_pool_max - 1)` simultaneous requests, at most half
that capacity per project (rounded down, minimum one), and at most half the project capacity per
credential (minimum one). API/all roles require at least three memory connections; worker-only
mode has no HTTP capacity requirement. Authentication admits at most two concurrent registry
lookups (one when the registry pool has one connection), with a 256-request burst and refill of
128 requests per second. These rate and absolute concurrency ceilings are conservative protection,
not measured optimum settings; workload tuning remains part of #93.

Excess work is refused immediately with `429 rate_limited` and `Retry-After: 1`. It is never queued
in an application slice or goroutine waiting for capacity. Authentication completes before a
project/credential slot is acquired, and project existence checks happen inside that memory slot.
Each slot is released on return, cancellation or panic. Counter maps retain only active trusted
project/credential identifiers, with a count bounded by admitted work; raw tokens and client
addresses are not keys. Aggregate counters are readable without per-principal metric labels and
the serving command logs totals at shutdown. They are not a substitute for the live observability
work in #94.

Anonymous audit writes also consume the shared memory-work budget, with at most one simultaneous
write. A busy audit path is suppressed and counted separately. The existing 16-samples-per-minute
audit budget remains. Audit writes have a one-second context deadline; credential lookup has two
seconds; protected requests carry a 30-second context deadline through database calls. Health
checks stay outside the gate and perform no database work.

An observation admits at most 64 messages, 65536 UTF-8 content bytes per message and 262144 content
bytes in total. Domain validation applies before copying/sorting/fingerprinting a new turn or
starting its transaction. HTTP checks message count before constructing a second message slice.
Over-limit turns return `400 invalid_turn` with no storage writes; stored history is not rewritten.
The limits also apply when a caller submits a retry payload. Clients must retain the same
idempotency key when retrying a capacity refusal.

**Security and availability.** The assets are memory connections, registry connections, request
memory and downstream extraction work. The attacker may hold a project credential, rotate among
credentials for that project, or send anonymous headers. The gate prevents one admitted project
from occupying every memory connection in the serving process, and bounds concurrent/aggregate
authentication work. A real HTTP test holds one project's append on a database lock while the
second project observes and recalls through a four-connection memory pool. A separate test holds
a registry table lock and verifies only two queries wait while excess anonymous requests receive
429; cancellation and lookup timeout release capacity. Credential rotation, global exhaustion,
audit capacity, key cleanup and retry identity have dedicated tests.

**Limits.** This is per process. Several API replicas have separate gates. Authentication is a
shared step before identity is known; the mechanism does not guarantee admission fairness during
an adversarial authentication flood. Upstream traffic protection and deployment-wide workloads
remain necessary. Size-bounded writes still accumulate indefinitely during a provider outage until
durable backlog admission is implemented. This batch therefore does not close #93. The 30-second
context propagates cancellation; it does not prove every extension or future query promptly
honors cancellation.

**Rejected alternatives.** Queuing requests hides saturation while retaining memory and sockets.
A credential-only bound is defeated by credential rotation. A global-only bound lets one project
occupy every admitted memory operation. A rate limit without concurrency limits does not stop
slow requests from accumulating. A shared distributed limiter was deferred until its availability
and deployment-wide cost are measured; no cluster-wide guarantee is claimed from local counters.

**Undo cost.** There is no persisted format change. Removing the gate or increasing ceilings
changes overload behavior and requires new resource evidence. Lowering observation limits affects
client compatibility, including retries of previously accepted larger payloads. Durable backlog
accounting and live metrics need their own storage/operational contracts.


## D74 — Unfinished turns hold durable ingestion reservations

**Date:** 2026-09-08. **Tracking:** [#93](https://github.com/althunibat/taisce/issues/93).

**Decision.** An unformed conversation turn reserves one backlog slot in PostgreSQL. Defaults are
4096 slots for the instance memory namespace and 512 for any one project. Parked turns still count: a
provider outage must not create room for unlimited new ingestion merely by exhausting retries.
Formation releases the reservation, and erasure/retention release it through the source observation's
foreign-key cascade. Admission and release commit atomically with the operation that caused them.
Idempotent replays return an existing receipt without requesting another reservation.

Migration 0025 backfills reservations and exact counters from existing unformed turns, including
parked ones. Previously accepted history is preserved even above the new limits. Further ingestion
is refused until work drains or the operator deliberately raises the policy. Runtime identities
can read policy/accounting but cannot change it or forge reservations. HTTP returns `429 rate_limited`
with `Retry-After: 1` when the database refuses capacity; rejected observation rows, messages, chunks,
retry receipts and watermark increments roll back together.

**Transaction and performance tradeoff.** A deferred source trigger synchronizes the reservation
against the final observation state. It updates a singleton counter and the project's counter at
transaction end, after ordinary observation/watermark locks are taken. Releasing a reservation also
uses a deferred trigger. The global row update serializes final accounting and causes stale
repeatable-read transactions to fail with a serialization error instead of oversubscribing from an
old snapshot. Each admission uses a fixed number of counter/registration statements, avoiding a
backlog scan on every write. The singleton is an intentional write bottleneck; its throughput and
vacuum cost require controlled-host measurement under #103. No latency claim is made here.

**Security boundary.** The threat is a noisy API credential or repeated ingestion during a provider
outage. Table privileges prevent runtime queries from deleting reservations or inflating budgets.
The narrowly scoped trigger functions run as the migration owner, use qualified relation names and
a fixed `pg_catalog, pg_temp` search path, refuse unexpected trigger tables, and revoke PUBLIC
execution. Runtime writes can fire installed triggers without gaining permission to attach them
to other tables. Tests also grant EXECUTE deliberately and prove a temporary-table spoof cannot
decrement counters. This does not defend against the database owner or an arbitrary SQL principal
already permitted to falsely mark observations formed; the application worker owns that transition.

**Scope and limits.** Slots count unfinished turns, not bytes or all retained history. New API turns
also have D73's message/content bounds. Physical disk is not reserved, completed memory can continue
to grow, and operators must select limits with storage headroom and retention in mind. Reservations
are shared by API replicas using the same memory namespace; HTTP concurrency gates remain local
to each process. A quota is not an inference scheduling fairness guarantee.

**Alternatives.** Process-local backlog counters disappear on restart and disagree across replicas.
A preflight count without an atomic reservation races at capacity. Counting the complete backlog
under a global lock imposes a scan on every admitted write. Treating parking as completion permits
unbounded growth during an outage. Dropping old accepted turns to fit a new limit was rejected
because an operational configuration is not authorization to delete memory.

**Evidence.** Real PostgreSQL tests cover project/global boundaries, 24 concurrent writers, stale
repeatable-read snapshots, rollback after forced trigger execution, idempotent retries at capacity,
parking/unparking, formation, erasure, retention, upgrade above a limit and runtime privilege refusal.
A worker test uses an HTTP provider returning 503, verifies parking does not release capacity, then
recovers the provider and proves successful formation restores admission. This fault-injection test
is separate from live-model quality/profile evaluation.

**Undo cost.** Removing the policy requires dropping its triggers and operational tables in a
reviewed migration; observations and their projections need not be rewritten. Changing the counted
unit to messages/bytes requires backfilling and reconciling accounting before a cutover. Configure
budgets in their own operator transaction rather than mixing policy updates with application writes.

## D75 — Operator recovery is bounded, project-scoped and atomic with its audit

**Decision.** `taisce formation parked` reads one live keyset page of metadata (default 100,
maximum 200). The partial parked index now orders by project and log offset, so the query can stop
at the requested page instead of sorting the full queue. A page fetches one extra row to determine
whether `next_after` is needed. New work behind a cursor appears when the operator starts a fresh
scan; this is not a durable snapshot export.

`taisce formation unpark` accepts one project and observation UUID, locks that project's turn,
resets its failure state and corrects its freshness watermark in one transaction. It retains the
unfinished-turn reservation. Concurrent retries serialize on the observation: only one changes the
parked state; subsequent retries are audited no-ops. A missing or wrong-project turn is a refused
operation. No command bypasses the worker's normal extraction/retry policy.

**Security and operations.** These commands use the existing operator database connection and a
fixed ten-second deadline. The output contains no subject identifiers, message text or stored
provider error: error strings may contain submitted memory. Recovery writes `formation.unpark` to
the existing ledger with project, outcome and magnitude; audit failure rolls back the recovery.
The principal is a generated invocation UUID returned to the operator, not a claim of authenticated
human identity. Human attribution comes from external access/session controls around the operator
connection. No observation UUID is retained in the audit row. This deliberate operator action can
fail closed on audit, while ordinary request audit retains its separately documented availability
tradeoff. Metadata listing does not read memory content and adds no new audit operation.

A timeout or broken output may happen after commit. Repeating the same project/observation is safe
and reports the current parked state; if the worker has parked it again, it may be retried again.
Operators should repair the provider before retrying. The command does not automatically drain,
raise capacity, delete data or resume suspended projects.

**Alternatives rejected.** Direct SQL flag resets cannot guarantee the freshness watermark is
correct. An unscoped UUID-only operator command makes a mistaken project selection invisible.
Returning raw provider errors can disclose memory during routine diagnostics. An unbounded listing
can consume increasing memory during the outage it is intended to diagnose. Bulk unpark and a
remote operator API add amplification and authorization policy that this single-turn workflow does
not require. Requiring a new human identity system for a local operator command would delay recovery
without making a shared database credential attributable.

**Evidence.** Real-PostgreSQL tests cover pagination and project filtering, twelve concurrent
retries with one state transition, wrong-project refusal, retained quota, downward watermark
correction, rollback when audit is refused and cancellation while a row is locked. CLI tests cover
argument refusal, metadata-only output and safe retry when output fails after commit.

**Reversal cost.** Low for the CLI and index, one migration for the operation constraint. Existing
audit rows must retain their operation vocabulary; removing command support must not invalidate or
rewrite the ledger. No memory representation or inference prompt changes. Tracked in #113.

## D76 — Readiness checks serving dependencies; formation progress is a separate signal

**Decision.** Keep liveness independent of dependencies. Add content-free, unauthenticated readiness
with one in-flight dependency check, a one-second total context budget and one-second result cache.
Concurrent uncached probes refuse immediately. Probe cancellation cannot abort a shared check.
Memory and registry access use their actual runtime identities. An explicit
`TAISCE_REQUIRE_FORMATION=true` adds a recent fleet heartbeat; false permits API-only deployments.

Formation reports at the start of a driver pass and after each completed attempt. A driver records
one aggregate instance row, with no process/project/subject cardinality, using the already-held
connection during a drain. A local atomic timestamp updates only when that report succeeds. The
330-second responsiveness window accommodates a normal five-minute attempt plus overhead. There
is no independent timer that can conceal a blocked processing path. A live heartbeat does not prove
provider quality, completion of all projects or the health of every worker in the fleet.

**Deployment and access.** Workers expose health routes only on a validated loopback address,
defaulting to 127.0.0.1:8081. Their readiness requires their own progress and memory access; another
worker cannot satisfy it. They expose no memory routes. The binary supplies local readiness/liveness
probe commands with a two-second deadline, no proxy and no redirects, so the distroless image needs
no shell or curl. Compose consumes readiness. Helm packaging and failover evidence remain #76;
this change does not claim a chart exists or a failover target has been met.

Detailed operational aggregates are exposed by an operator command requiring explicit admin DSN,
not by unauthenticated or project-scoped HTTP. One snapshot reports backlog/parked count, oldest
pending timestamp, recent processing times, attempt counters and database connection pressure.
There are no content fields or unbounded labels. A partial unfinished-turn timestamp index supports
the age query. The command deadline bounds diagnostic work; it is not polled by the public probe.

**Tradeoffs and limits.** The aggregate health row adds one write per pass and completed attempt.
It is best-effort telemetry: recording failure logs a content-free warning and does not undo formed
memory. It is not an audit ledger, an inference-quality measure or a precise historical attempt
count. Counters start at migration 0027, exclude direct workers without a driver and survive erasure
because they contain no data identifiers. Idle workers continue reporting from their pass loop.
One responsive worker can satisfy fleet readiness while another project is stalled. Per-worker
loopback checks and backlog/freshness diagnostics provide the additional signals. Production cost
and thresholds still need #103 workload evidence.

**Alternatives rejected.** Calling a provider from readiness adds cost and can amplify an outage.
Failing the entire API for a parked input gives one malformed turn control over other projects'
availability. Public aggregate metrics disclose instance activity; project labels create both
cardinality and privacy problems. An independent heartbeat goroutine proves only that the timer
runs. Unbounded waiting probes consume the same scarce connections they are diagnosing. Worker
health through another worker's fleet timestamp conceals a locally stalled process.

**Evidence.** Real PostgreSQL tests lock a required relation and show readiness refusal within its
budget, bounded concurrent probes, unaffected liveness and recovery. Tests distinguish absent/stale
required formation from optional formation, track provider failure/recovery through the driver,
check runtime-role wiring and validate metadata-only diagnostics. D76 concerns correctness and the
probe contract, not a production availability guarantee. See docs/07-operational-health.md.

**Reversal cost.** Low for configuration and probes; one migration removes the aggregate table and
age index. Removing the public readiness route requires updating deployment probes. No prompt,
memory representation or credential scope changes. Tracked in #94.

## D77 — Resolve saved citations from retained, verified sources

**Decision.** An authenticated `POST /v1/citations/resolve` accepts a fact UUID and an optional
keyset cursor. The credential determines the project. Unknown, foreign-project and erased records
share the same not-found response. The request does not rerun retrieval or inference. Each request
reads metadata and evidence in one read-only repeatable-read transaction.

The response includes retained validity/knowledge intervals, current/validity-closed/knowledge-closed
status, entity identifiers and extraction provenance. Every evidence row is joined to its exact
message ordinal; its quote must equal the UTF-8 byte span in that message. Missing or inconsistent
sources fail the request without returning partial content. Multiple supports and multiple spans
in one message survive pagination, ordered by observation UUID, ordinal and byte start using the
existing evidence primary key. Message role and occurrence time come from the message itself.

**Bounds.** Default eight sources, maximum 32, with one lookahead row. A page contains at most
256 KiB of decoded statement/quote/context/version text; JSON escaping adds bounded wire overhead.
Statements and source messages are limited to 64 KiB, versions to 256 bytes. Oversized retained
legacy records return `citation_limit` and direct the caller to subject export. SQL bounds text
before transferring it to Go. The existing authenticated admission/deadline policy applies.

**History and erasure.** Saved fact IDs remain resolvable after the present supersession writer
closes validity. This is inspection of the retained row, not reconstruction of earlier knowledge.
The in-place supersession defect is independently reproduced in #116. Successor/retraction links,
versioned historical views and stable rebuild mappings remain unfinished under #90, #89 and #58.
No links or history are inferred from coincident entity/predicate values. Pages are separate live
snapshots: erasure or additional supports between pages can change later pages. Completed erasure
removes access on subsequent requests; an already returned response cannot be revoked.

**Audit.** `citation.resolve` records project, credential, outcome and returned source count;
no fact identifier, source identifier, subject, quote or statement enters the audit entry. Database
errors produce a generic response and a content-free project-scoped log message.

**Alternatives rejected.** Repeating recall can omit a retained citation because of temporal or
retrieval budgets. Selecting one arbitrary support loses provenance. Whole-message responses or
unbounded evidence arrays permit avoidable resource exhaustion. Offset pagination becomes more
expensive on later pages. Reconstructing unrecorded history would present guesses as evidence.

**Evidence and reversal.** Real PostgreSQL and HTTP tests exercise exact Arabic bytes, message
identity, independent temporal metadata, supersession, erasure, project privacy, count/text bounds,
multiple spans, corrupt evidence and content-free audit. This establishes behavior, not production
throughput (#103). Removing the route/store is low cost; migration 0028 adds only an audit operation.
Persisted audit entries must remain valid even if the route is later retired.

## D78 — Preserve validity as it was known before supersession

**Problem.** Closing validity in place changed answers to earlier knowledge-time queries. A Dublin
claim known at K1 with open validity disappeared from an August/K1 query after a later Amman claim
closed it in June at K2. An April/K1 result also disclosed a June end that was not known at K1.

**Decision.** Keep the fact UUID and immutable claim content on the retained fact. On the supported
single-cardinality supersession path, save its previous validity and knowledge interval in
`fact_history`, close that knowledge interval at K2, and advance the retained row's knowledge start
to K2 while closing its validity. The new assertion begins knowledge at the same K2. The archive,
causal source, provenance registrations, changed validity and new fact commit in one transaction.
K2 comes from the database clock after entity row locks, not a transaction-start timestamp that
could precede a competing writer's commit. The existing cardinality exclusion still rejects
conflicting equal-start/out-of-order writes; this does not introduce automatic retroactive repair.

Current and valid-time-only reads retain their existing query shape. Reads with an explicit
knowledge time select current or archived validity before filtering/fanout at each graph hop. A
GiST exclusion/index prevents overlapping archived knowledge intervals for one fact and supports
per-fact historical lookup. Saved citation IDs remain unchanged; current citation inspection adds
the successor and causal observation, with project-consistent foreign keys. Historical recall and
subject export expose preserved versions; a general version-list/correction API remains #89.

**Fresh installation.** Taisce has no deployed installations. Migration 0029 provisions the archive
and causal references; preservation starts with the first assertion. Knowledge before that assertion
returns no facts. No compatibility cutoff, availability catalog or special API refusal is needed.
The greenfield development policy is recorded in D80.

**Governance and causal invalidation.** History registers both the fact's retained source
observations and the observation causing the transition. Its projection kind does not survive
removal of any registered contributor. Current closed validity also names its causal observation
through a project-consistent foreign key. Erasure or expiry of that cause invalidates the affected
fact and its history, even when the original source survives. This deliberately loses a derived
record rather than retain an end date derived from erased information or guess that reopening it
would be consistent. The surviving observation remains available for a future rebuild (#58).
Removing a successor fact clears that link; retained causal observation identity still explains the
transition. Cascaded projection deletes are governed by the same transaction, although per-kind
receipt counts count direct deletions and can omit cascaded rows. Residual and actual-table tests
verify removal. No general cascade count is presented as a complete row total.

**Scope and limits.** This preserves temporal state for the existing immutable-claim supersession
writer. Later curated changes to statement, entities, roles or evidence must version those fields
as well; they must not reuse this interval-only archive as if it were a complete mutable-record
snapshot. Historical recall still uses current entity names/aliases and retained evidence, and
therefore is not a byte-identical replay of an earlier API response. Knowledge timestamps identify
logical assertion order; they are not PostgreSQL commit timestamps. Direct owner edits to factual
rows can defeat this application contract; this is not a tamper-proof historical ledger.

**Alternatives.** Cloning every fact and all evidence complicates stable IDs, graph references and
erasure while duplicating unchanged content. Reconstructing old validity from later facts guesses
at missing history. Replacing every query with a historical union adds work to the common current
path. Stored-procedure conversion is not needed: parameterized SQL and the existing Go transaction
boundary implement this change.

**Evidence and reversal cost.** Tests distinguish earlier/current knowledge, valid time and exact
transition boundaries, concurrent conflicts, late rollback, project boundaries, source erasure,
retention, export and knowledge before the first assertion. A 2,000-version fixture checks indexed historical
lookup. Production mixed-workload cost and rebuild integration remain #103/#58 and keep #116 open.
Removing the archive would lose acquired historical knowledge; history preservation remains a
functional requirement even while unreleased schemas and contracts can be revised.

## D79 — Explicit read-only project credentials

**Decision.** Registry credentials carry `read_only` or `read_write` access. Fresh bootstrap installs
control migration 0004 with the access constraint; ordinary issuance defaults to read/write.
`taisce credential issue <name> --project <project> --read-only` issues a restricted key. Unexpected
positional arguments are rejected so a misplaced flag cannot silently produce a writable key.

The shared API gate runs after authentication, active-project validation and admission, but before
body parsing or memory operations. Readers can recall, inspect citations, read freshness and export
within their project. Observation ingestion and erasure return `403 forbidden` with an attributed,
content-free refused-operation audit row. New operations default to denied for readers until added
explicitly. Unknown access modes fail closed. This is memory authorization, not a commercial plan
or a per-subject end-user identity. Readers can export project-authorized subject data.

**Token and authority.** Both modes use one opaque `tsk_` token format. The stored digest identifies
the credential; the protected registry determines its access. Altering token text changes the digest
and cannot acquire another grant. The API cannot update registry metadata, and the serving registry
connection is read-only. Operator commands expose issuance and revocation; changing authority through
those commands requires revocation and reissuance. Taisce is greenfield, so no earlier-binary token
format or mixed-version rollout behavior is promised.

**Alternatives and limits.** Encoding authority in a token prefix duplicates registry state and adds
an unnecessary format distinction. Fine-grained capabilities, human roles and per-subject grants
remain separate work. This closes the permission prerequisite for curated memory under #89, not its
record lifecycle or batch semantics. The gate prevents caller-requested memory mutation; reads still
create audit/operational records.

**Evidence.** Tests enumerate every served operation for a restricted key, verify no denied mutation
changes memory, test audit privacy, malformed writes, token tampering, revocation, fresh registry
constraints and CLI issuance. Database-role tests verify that serving connections cannot change
credential authority. Coverage and contract gates apply to the simplified implementation.

## D80 — Greenfield development until the first supported release

**Context and decision.** The project owner confirmed that Taisce is greenfield and nowhere deployed.
Local containers, test fixtures and development version numbers are not evidence of deployed clients
or a supported upgrade path. Unreleased database schemas, APIs and internal representations may be
revised to establish the best functional, performance and security design.

Do not add legacy-data repair, compatibility shims, mixed-binary safeguards or zero-downtime upgrade
work solely for hypothetical earlier deployments. D78 and D79 reflect this policy. Existing migration
files remain a reproducible way to provision and test the schema; retaining that mechanism does not
promise compatibility between unreleased commits. Before the first supported release, review the
schema baseline and define version support, upgrades and rollback against an actual release policy.

**Still required.** Atomic writes, project boundaries, least privilege, provenance, erasure, temporal
correctness, recovery/rebuild and measured workload bounds remain product requirements. Rebuild is
needed for derived-state recovery and model changes, independently of legacy-data migration. Tests
use isolated fixtures; this policy does not authorize deleting unrelated local development data.

**Distribution.** Daily development remains private in `althunibat/taisce` with `x.y.z` versions. The
planned Apache-2.0 public repository on `ensera-ai` receives separate `x.y.0` release snapshots.
Greenfield status does not change that release topology or publish anything automatically.

## D81 — Bounded record inventory and temporal inspection

**Status:** Accepted. **Date:** 2026-09-08. Tracked under curated memory #89.

**Context and decision.** Applications need to find a retained claim before resolving its evidence
or asking about its history. `POST /v1/records/list` lists retained facts within the authenticated
project, optionally restricted by their registered data-subject provenance. The same fact appears
once when several observations support it. Inventory includes closed validity/knowledge records;
`POST /v1/citations/resolve` remains the reader for complete, byte-verified evidence.

`POST /v1/records/history` returns the current validity/knowledge intervals and a page of earlier
intervals with the observation that ended each earlier knowledge version. It does not pretend that
entity labels, evidence or mutable claim content were versioned: D78 preserves intervals around
immutable claims. The original assertion's sources remain available through citation resolution.

**Bounds and ordering.** Default pages contain 20 records or archived versions, with an explicit
limit from 1 through 100. A record preview contains at most 512 Unicode characters (at most 2,048
UTF-8 bytes); the full statement's byte count and truncation flag tell the client when to resolve
the citation for more. Serialization may expand escaped text. SQL selects the page before fetching
previews. Subject input is bounded to 1 MiB, consistent with the outer request-body ceiling; this
is not a new subject normalization/identity policy (#95).

Record order is ascending immutable UUID. History order is descending knowledge end and history UUID.
Cursors describe positions, not capabilities, and never relax project or subject predicates. The
first and subsequent record queries have separate indexed comparison shapes. The project path uses
its existing composite fact key; migration 0030 adds subject/provenance and history-page indexes.

Every page uses a consistent database snapshot; history's current/archive reads share a repeatable-read
transaction. A multi-page sequence is live. Deleting its cursor row does not break continuation or
shift already returned records. Concurrent insertion behind a cursor requires restarting to see that
record. Erasure can remove records between pages. Clients retain filters when continuing a browse.
There is no claim of an exhaustive point-in-time export across pages.

**Security and cost.** Project authority comes from the credential, with ordinary admission controls
and read-only grants admitted to both operations. Unknown, foreign and erased history IDs receive
identical not-found responses. Request values travel in POST bodies. Audit records contain operation,
credential, project, outcome and row count; no record IDs, subjects, statements or source content enter
the ledger or database-failure diagnostics. Reading a preview does not verify its evidence or make it
true. A registry-authorized reader has project-wide access; subject filtering is an inspection choice.

**Alternatives.** OFFSET requires rescanning and shifts under deletion. Returning full citation
objects for every list entry multiplies source reads and response size. A server-held pagination
snapshot pins connections and retained row versions across client think time. The chosen bounded,
live pages avoid those costs while making their concurrency limits explicit. At greater scale,
provenance duplication for one subject/fact is the first cost to measure: DISTINCT still visits
matching support rows, even though it avoids scanning unrelated subjects.

**Evidence and reversal cost.** Real PostgreSQL/HTTP tests cover shared attribution, erasure,
concurrent writes, deleted cursor rows, Unicode previews, temporal cause links, read-only access,
audit privacy and storage failures. Controlled fixtures inspect deep-page plans with 10,000 facts
and 2,000 historical intervals. These establish access paths, not production latency targets.
The endpoints add no new source-of-truth rows. Removing their indexes only changes performance;
removing endpoints after the first supported release would require a versioned contract change.
Corrections, retractions, batches, entity purge and rebuild preservation remain #89/#58.

## D82 — Recall controls are explicit and context allowances are hard limits

**Status:** Accepted. **Date:** 2026-09-08. Tracked by #92.

**Decision.** Recall accepts optional `max_characters` and `source_roles` alongside its existing
hop control. The configured character budget is the default and ceiling; a request can choose any
positive value up to it. The row ceiling remains server-controlled. Effective character/row limits,
roles and clamped hops are returned in `controls`, including when no facts match. A copied recaller
configuration holds each request's choice so simultaneous clients cannot alter one another's budget.

An omitted/null role list means `user`. An explicit list must be nonempty, unique and drawn from
`user`, `assistant`, `system`, `tool`. This selects assertion provenance, not project authority or
permission to promote non-user messages into personal facts. Existing attribution/write rules and
project, subject, temporal and erasure predicates remain in force at every hop. A fetched document
has its stored message role; there is no invented `document` role or new ingestion mechanism. Document
ingestion remains #91. Invalid controls return `400 invalid_recall_controls` before store reads.
Hops retain their established clamping to 1–2 and the response makes the chosen value inspectable.

**Accounting.** `characters` counts Unicode code points in returned fact subjects, predicates,
objects, statements, source-role labels, quotes, contexts, path names and path predicates. Both quote
and context count because both are returned. It is a fact-content allowance: identifiers, anchors,
dates, numeric metadata and JSON syntax are outside it. It is neither grapheme counting, a tokenizer
estimate nor the total serialized response size.

The cut applies before appending every fact, including the first. If nothing fits, the response has
no facts, zero characters and `truncated: true`. A normal empty result has `truncated: false`. The
implementation previously counted UTF-8 bytes and allowed an oversized first fact; both behaviors
are incompatible with a client-selected character ceiling. Greenfield contracts can be corrected
without preserving those behaviors. Budgeting still cuts an unranked sequence; it does not claim to
return the most useful subset or reduce database work below the independent row/hop/fanout bounds.

**Alternatives.** Allowing the first fact to overflow defeats the context limit. Silently raising a
small request hides the effective allowance. Returning an error for an ordinary cut would make
partial recall harder to consume despite existing truncation metadata. Byte counts penalize the
same number of Arabic letters relative to ASCII; Unicode code points give an explicit, reproducible
unit. Token counts depend on a client model/tokenizer this service does not control. Exposing all
roles by default removes the trust choice the caller must make explicitly.

**Evidence and reversal cost.** Real HTTP/PostgreSQL conformance covers independent client budgets,
exact content accounting, labeled non-user facts, invalid controls, clamped hops and project/subject/
temporal/erasure boundaries. A portable JSON fixture records these cases for SDK runners. A concurrent
Go test checks budget isolation and a Unicode/path test catches byte counting or uncharged graph
text. Changing the accounting unit after a supported release would change client context sizing and
require a versioned contract decision. No stored rows, SQL writer or model prompt changes are needed.


## D83 — Reports are valid only for the source fact revisions they read

**Context.** A retained observation can remain available while its derived facts change. Checking
only source existence at report persistence permits a model call started before supersession to
publish an outdated claim afterwards. Already persisted reports also need invalidation when their
source projections change. This is a prerequisite for curated retractions and rebuild, tracked in
[#117](https://github.com/althunibat/taisce/issues/117).

**Decision.** Each observation carries a positive, monotonic fact revision. Fact updates/deletes and
evidence inserts/updates/deletes advance the affected source revisions and remove reports registered
to those sources in the same transaction. A report inherited through child substitution carries the
same source revisions, so parent summaries are invalidated too. A new fact becomes report material
through its evidence; the evidence insert advances the revision.

Material reads capture each revision in the same database statement as the facts. Revision metadata
stays outside model input. Report persistence merges the revisions from direct facts and child
reports, refuses missing or contradictory metadata, then locks sources in identifier order and
compares the retained revisions before inserting anything. Those locks conflict with revision
advancement: a racing source change either makes persistence refuse or removes the report it just
stored. A deferred composite foreign key also prevents committing a report registration whose
revision differs from its source. The model response cannot supply persistence authority.

**Alternatives rejected.** Source existence is insufficient when projections change but the log is
retained. Timestamps can collide and confuse observation time with projection changes. A project-wide
revision would invalidate summaries of unrelated observations whenever any fact changed. Checking
without a conflicting lock leaves a window between validation and insertion. Holding a transaction
open across model inference would consume database resources for the entire provider latency.

**Costs and limits.** Writes add a source-row update and invalidate every summary supported by that
observation, including summaries whose particular subset of facts was unaffected. Regeneration can
cost model calls. This deliberately chooses a conservative source-level boundary over a new
fact-level report dependency graph. Ordinary recall gains no revision lookup. Fact/evidence changes
are covered; entity-label edits and model/pipeline version transitions remain separate generation
work. This mechanism does not implement record retractions or rebuild itself. Controlled-host write
amplification and contention measurements remain under #103; no production latency claim is made.

**Evidence.** Real PostgreSQL tests cover supersession with retained citation sources, direct/parent
invalidation, stale and missing metadata, rollback, removed evidence, substrate-level revision
constraints and concurrent report publication versus fact changes. The prompt-material regression
proves that source IDs and revision metadata do not change the provider input. Existing project,
erasure, source-provenance and concurrent-retry tests remain part of the full suite.

**Reversal.** The counter, dependency column, constraints and triggers are derived-state machinery.
A replacement can rebuild reports with a different dependency model from retained observations and
facts. Source identifiers, fact identifiers and citation bytes do not change. Define the supported
schema transition before a released deployment relies on it; this implementation remains greenfield.


## D84 — Retractions are versioned source instructions and replay keeps claim identity

**Status:** Accepted. **Date:** 2026-09-08. Tracked by #89 and #118.

**Decision.** A write credential can retract 1–20 retained records atomically using opaque UUID
versions obtained through inspection. Every fact update advances its version, including temporal
supersession. The transaction locks source observations by advisory key and target facts by sorted
identifier, checks versions and complete bounded support, closes knowledge, persists instructions,
invalidates reports through D83 and writes an attributable content-free success audit. Stale or
already withdrawn records conflict. Validity and evidence are preserved; earlier knowledge remains
queryable. The batch admits at most 128 evidence rows and reads at most 129 before refusing.

An instruction belongs to a source message and matches a SHA-256 signature over a versioned encoding
of predicate and normalized endpoint identity. Speaker endpoints use stored subject attribution;
named endpoints use normalized names. The target UUID is historical metadata with no fact foreign
key, so loss of a derived row does not discard human input. Source/message foreign keys cascade on
deletion; export includes instructions and subject erasure counts their deletion and residual.
The signature is an equality key, not anonymization or authority. No free-form reason or claim text
is duplicated. A new independent source may assert the same relation after withdrawal.

Formation uses the same per-source advisory lock and checks withdrawal before writing. A retained
structural source claim has a deterministic UUIDv8 derived using SHA-256 from project, canonical source
UUID, message ordinal and signature. Retries return an existing identity before entity updates or
supersession, leaving its version, ranges and evidence intact. Recreating a lost derived claim returns
that identity. Statement paraphrases, extracted types and valid-from dates are not identity: retrying
model output must not turn wording variation into duplicate current memory. Explicit reinterpretation
under changed rules remains generation work; replay does not silently replace retained semantics.

**Alternatives rejected.** Physical deletion alone lets formation resurrect a claim from its source.
Editing the source transcript corrupts citation evidence. Closing validity asserts an unsupported
fact about when the claim was true. A project-wide transaction lock serializes unrelated sources.
Checking versions outside the mutation transaction allows competing editors to succeed. A random
UUID per assertion duplicates retry work and changes saved citation IDs after derived-row loss. A
separate source-to-fact identity table duplicates a derivable key and adds another lifecycle to erase.
Including paraphrases or dates in the key permits ordinary model variation to bypass withdrawals.

**Security and cost.** The asset is project memory and its history. The credential registry supplies
the project and actor; body-supplied ownership cannot widen access. Unknown/foreign/erased records
share one not-found response; read-only credentials fail at the shared gate before mutation parsing.
A stolen write credential can withdraw project knowledge, with an atomic actor/count ledger entry.
Finer human authorization remains #89. Source locks serialize concurrent formation of one observation;
SHA-256 work is bounded by existing claim-input limits. Normal current recall has no new query. Closed
record detail adds a metadata lookup. The first scaling costs to measure are source lock contention,
support fanout and D83 report regeneration, under #103; no production speed claim is made.

**Evidence and limits.** Real PostgreSQL and HTTP tests cover temporal reads, source-byte retention,
atomic rollback including audit failure, stale versions, project refusals, read-only access, support
bounds, concurrent replay/retraction, export/erasure/retention and published/in-flight report
invalidation. Concurrent source replay retains one fact and one evidence row; independent sources
remain distinct. These tests do not prove complete rebuild, changed extraction mappings, entity-purge
policy or arbitrary formation/erasure concurrency: #58/#89/#90/#100/#116 remain open for those scopes.

**Reversal.** Endpoint removal after a supported release requires a contract change. Version tokens
are disposable coordination metadata; source instructions are human input and must be preserved by
any replacement. Changing signature or identity normalization after release requires a versioned
mapping for retained citations and instructions. Greenfield schemas may still be consolidated before
the first supported release; the source-preservation requirement remains.


## D85 — Corrections create independently attributable source claims

**Status:** Accepted. **Date:** 2026-09-08. Tracked by #89; generation integration remains #58/#116.

**Decision.** A correction of a current record preserves its subject, predicate and stored attribution
while replacing the object and statement. Its expected version is mandatory. A batch of 1–20 records
shares D84's 128-evidence-row support ceiling; objects are at most 1024 UTF-8 bytes and statements at
most 16384. Text must be nonblank, valid UTF-8 and free of NUL. Optional valid-from is explicit;
omission uses server time. Closed validity or knowledge conflicts rather than rewriting history.

One database transaction retracts each original, appends a separate user-role observation containing
the submitted statement, asserts the replacement through the shared fact boundary and stores its
structured authored claim. The original knowledge end and replacement knowledge start are identical.
The source is marked formed and the existing contiguous freshness calculation runs in that same
transaction; no unformed source escapes and no provider call occupies a transaction. Deferred admission examines
final source state, so an already-formed correction consumes no pending reservation and remains
available at backlog capacity without advancing freshness past an older pending turn. The transaction
records both the withdrawal and correction actor/count operations. Audit failure rolls everything back.

`curated_claim` is source-owned human input, not a projection. It retains the structural instruction,
source UUID, editor UUID and stable expected fact UUID. Its source is marked `curated`. Formation
checks that marker and replays the stored claim without extraction. Missing/corrupt instructions or
an identity mismatch fail closed. Recovery reuses the source's server-assigned editing time as knowledge
start; client-supplied validity cannot backdate knowledge. The source text and exact quote remain inspectable with
`extractor_version: curated/v1` and `authored_by`. Confidence stays zero: this is an attributable human
assertion, not a model confidence estimate or evidence of truth.

The original's withdrawal links to the replacement source and record while that source survives.
Deleting the replacement source clears both identifiers through the database foreign key and trigger,
without deleting the original withdrawal. Subject export includes authored instructions; erasure
counts their deletion and residual, and source expiry cascades them. Removing the old source does not
remove independently authored replacement content. Later withdrawal still suppresses the authored claim.

**Alternatives rejected.** Updating the original statement would make a saved citation point at words
it never supported. Re-extracting human corrections introduces provider cost and a second interpretation
that can disagree with explicit input. Committing withdrawal before replacement permits partial edits.
A background correction would need pending/failed semantics despite the absence of inference work.
Allowing subject or predicate changes here can silently alter a different relationship or owner; that
requires a separate assertion and explicit withdrawal, under the remaining #89 lifecycle scope.

**Security and cost.** A project write credential can correct project memory. The actor and original
attribution come from the registry and retained record; neither is a body field. Read-only credentials
fail at the shared gate. The new statement and structured instruction are sensitive input, owned by
the new source; their matching IDs/hashes are not anonymization. JSON instruction storage duplicates
some bounded source text to keep replay self-contained; it is erased with that source. Formation adds
one source-kind lookup per turn. Citation detail adds a bounded indexed author join; ordinary recall
adds no lookup. Source/fact locks and D83 invalidation dominate contention. General cross-operation
lock ordering and measured workloads remain #100/#103; this is an atomicity claim, not a production
throughput claim or a promise that arbitrary concurrent database work cannot abort and require retry.

**Evidence and limits.** Real PostgreSQL and HTTP tests check current/past reads at the transition,
immutable original evidence, stored attribution, concurrent edits, atomic audit failure, malformed and
oversized input, project and reader refusals, no-model replay, identity drift refusal, source loss,
export, erasure and replacement expiry. Independent assertions, feedback, entity purge and arbitrary
historical editing remain #89. Full reconstruction of superseded temporal generations remains #58/#116;
recovering one lost derived authored claim does not close those issues.

**Reversal.** Transaction helpers are internal. Authored sources and their structured instructions
must survive any replacement design. Changing their encoding or interpreting an existing correction
under new rules requires an explicit source-format/generation decision; they cannot be discarded as
rebuildable projections. Public endpoint removal requires contract versioning after first release.


## D86 — Automatic supersession uses ordered logical knowledge timestamps

**Status:** Accepted. **Date:** 2026-09-08. Tracked by #119.

**Decision.** For an automatically timestamped single-cardinality assertion, choose the greater of
current database wall time and one microsecond after the latest knowledge start among the current
records it can supersede. The writer already holds the resolved subject's row lock before this read,
so supported concurrent assertions for that subject serialize. The same timestamp closes archived
knowledge and begins both the changed prior state and successor. Explicit stored correction/recovery
timestamps retain their original semantics; this policy does not reinterpret historical source input.

A wall clock can move backwards, and an unbounded fresh clock reading can precede a committed
knowledge start. Passing that reading to range construction rejects a valid assertion with a reversed
range. Equal readings can also erase an interval's duration. The one-microsecond advance matches the
storage timestamp resolution and ensures a nonempty interval without inventing a larger time window.

**Alternatives rejected.** Failing until wall time catches up makes clock correction an ingestion
outage. Retrying the same earlier clock reading has the same failure. Changing the old timestamp
rewrites historical knowledge. A global timestamp sequencer would serialize unrelated projects and
relations despite the invariant being local to the records involved in this transition.

**Cost and limits.** The existing clock read becomes a scoped read of eligible current facts for
single-cardinality writes, under existing entity locks. Ordinary recall adds no work. Logical time
may temporarily be ahead of physical wall time after a rollback; it expresses ordering, not an exact
measurement of elapsed time. This is not a distributed global clock or a repair for out-of-order
validity changes. Direct database writes do not automatically participate in application lock order;
constraints remain the final refusal boundary. Workload costs remain #103.

**Evidence and reversal.** TestSupersessionAfterClockRollbackPreservesOrderedKnowledge places a retained
knowledge start one minute ahead of wall time, then checks successful supersession, exact alignment
of archive/prior/successor boundaries and both historical answers. It fails with SQLSTATE 22000 before
the fix. Concurrent replay, report invalidation and authored recovery remain regression checks. The
policy changes timestamp selection only; removing it needs no schema migration but reintroduces the
clock-ordering failure. Existing historical timestamps must remain retained.


## D87 — Rejected proposal replay preserves exact diagnostic identity

**Status:** Accepted. **Date:** 2026-09-08. Tracked by #120.

**Decision.** Derive a UUIDv8 using SHA-256 over a versioned JSON string tuple containing project,
canonical source UUID, message ordinal, predicate, statement, quote, reason and extractor version.
Identical retries insert with conflict-do-nothing and retain their original recorded timestamp. The
projection registration commits in the same transaction. Recreating a lost rejection uses the same
identity; an independent source, differing diagnostic text or extractor version remains distinct.

**Alternatives rejected.** A new random UUID on every rejection inflates diagnostic counts when a
worker retries or source recovery resumes. A unique index over every text field risks index-entry
size limits and duplicates long text in an index. Semantic or normalized-text merging hides the exact
model proposals this diagnostic exists to show. An additional persistent identity map has no reader
beyond a key that can already be derived.

**Security and cost.** The digest is identity, not anonymization or authority. The source and project
foreign keys still constrain persistence. The rejection and its derived identity are source-owned,
exported and erased through the existing projection registration; neither is added to permanent
audit. Hashing visits the already bounded model proposal once. Replays contend on one primary-key
entry and add no rows; ordinary recall is unchanged. This deduplicates identical proposed diagnostics,
not model invocations or the count of rejected proposals observed in a formation attempt.

**Evidence and reversal.** Real PostgreSQL tests cover sixteen simultaneous replays, canonical UUID
spelling, one retained timestamp and registration, derived-row recovery, distinct sources and text,
export/erasure, foreign-source refusal and registration-failure rollback. Changing the identity format
would create new diagnostic identities on replay and require a deliberate data-generation decision
after release; no independent mapping table needs migration. This is a recovery prerequisite and
does not itself implement the rebuild workflow under #58.

## D88 — Recorded formation outcomes are source-owned recovery input

**Decision.** Admitted facts retain a `fact_receipt` in the same transaction as their fact,
evidence and registration. The receipt contains the exact admitted record, original evidence,
entity identities and pipeline stamp. Temporal updates and archived intervals retain their state
transactionally. These receipts, authored claims and withdrawal instructions extend the authoritative
source set beyond the raw message log: a model's exact interpretation and the time it became knowledge
cannot be reconstructed from its input words alone. Losing this source set requires backup recovery.

`taisce recover facts` reads a project-scoped page of 1–100 receipts, optionally restricted to one
observation. It restores missing facts, their recorded entity identities, original evidence and
archived knowledge intervals without inference. Existing facts win. It verifies the quote against
surviving source bytes before writing; inconsistent inputs or constraints abort the whole page.
Source locks serialize competing recovery, assertion and curation for those records. Foreign keys
and source-row locks prevent a committed recovery from retaining an erased source. Database deadlock
or cancellation aborts a page; retry repeats an idempotent unit.

Recall remains available from retained records during recovery. A page commits atomically, and other
pages do not disappear while it runs. A successor outside the page is temporarily an unresolved link;
its receipt keeps the identifier, and recovery attaches the live link when the successor arrives.
Intervals already have their recorded bounds, so a missing successor never reopens an older claim.
Reattaching a link may advance an optimistic record version; saved claim and history IDs remain.
Pages are live UUID scans, not corpus snapshots. Restart a complete pass to include concurrent
additions below a previous cursor. The operator receives counts and a cursor, never memory content;
each committed page records a content-free audit operation with a per-invocation operator UUID.

**Ownership and limits.** Receipts belong to their original observation and any later supersession
cause. History also belongs to its transition source. Export covers those dependencies, explicit
erasure counts them before removing observations, and retention cascades them. Entity identity
copies omit accumulated aliases from other observations. This reader covers admitted records written
with receipts. It does not recover missing receipts, evidence-only loss on an otherwise retained
fact, manually attached additional evidence, entity-only loss, or manually inserted facts without
receipts. It does not regenerate chunks, embeddings or summaries, and it does not reinterpret text
under a newer model. Those generation workflows remain #58; their evidence must name the rules that
actually ran. There is no fabricated historical backfill for this unreleased schema.

**Security and cost.** The asset is recorded knowledge and its provenance; project credentials must
not recover foreign sources, and an operator must not inadvertently resurrect erased or withdrawn
knowledge. SQL predicates, source constraints, withdrawal state, byte checks and atomic audit hold
that boundary. A database administrator can rewrite both sources and receipts; this is not tamper
proof storage. Receipts duplicate admitted statement/evidence and identity metadata, and each fact
transition updates its receipt. That adds storage and write amplification, including WAL; it adds no
receipt lookup to ordinary recall. The page limit bounds transaction work, not a production latency
promise. Controlled-host storage and workload measurements remain #101/#103.

**Alternatives rejected.** Re-extraction alone changes outputs and knowledge times. Inferring times
from `formed_at` cannot reconstruct individual claims or transitions. A full event-sourced rewrite
would introduce a second temporal engine; retaining the existing recorded states reuses the tested
constraints and read semantics. Deleting all visible memory before replay would expose an empty
project. A generation switch is appropriate for changed interpretations, but is unnecessary for
restoring missing records with already recorded meaning.

**Reversal.** Moderate. Stop writing receipts, retire the recovery reader and drop two source-owned
tables after choosing another supported recovery contract. Their historical contents cannot be
recreated from messages after deletion; backups and the release support policy must reflect that.

## D89 — Extraction changes use required and forbidden relation sets

**Decision.** The extraction regression corpus records relations that must appear, relations that
must never appear, and the observed extras. Each fixture runs several attempts against a live model.
A required relation missing from every attempt fails; a forbidden relation appearing even once fails.
Pending cases identify an unresolved issue and report their observations without pretending to prove
that issue fixed. `scripts/compare-corpus.sh` presents case-level outcomes and differences between two
recorded runs, explicitly identifying cases that only one run reached.

The corpus stays behind the `inference` build tag and runs with the configured local, OpenRouter or
GPU profile. Prompt changes require this regression run. Ordinary deterministic tests validate parsing,
source binding and refusals without making their success depend on an external provider. Provider
failures are incomplete verification, not passes. The local run recorded for this review passed all
19 extraction fixtures; nonlocal profile verification remains incomplete as recorded in the roadmap.

**Alternatives rejected.** Exact output matching rejects harmless provider wording changes. An
aggregate success-rate floor can conceal a false assertion. One attempt cannot distinguish a stable
omission from sampling variance. Requiring every positive relation in every attempt would obscure
systematic regressions with intermittent misses; requiring no forbidden output is deliberately stricter.
A live provider in every ordinary test run would make substrate correctness depend on availability and
billing. Automatically clearing pending cases after one success would mistake variance for a fix.

**Cost and limits.** Repeated live calls cost time and inference capacity. Required relations appearing
once may still have poor recall across attempts; the corpus is a change detector, not a corpus-wide
quality score or proof that a model cannot produce a false fact. Unmapped rates and retrieval quality
remain #43/#15, and controlled workload measurements remain separate.

**Reversal.** Easy in code, consequential for interpretation. Changing required/forbidden sets or the
attempt policy changes what a pass means; version the fixtures and retain both run outputs when
comparing policies. This decision records the existing test policy; it changes no prompt or threshold.

## D90 — Different content access requires different projects

**Decision.** A credential grants access to the content of one project. Read-only credentials limit
mutations; they do not conceal a subset of that project's content. Applications with different
sensitivity domains provision separate projects and give each caller only the credentials it needs.
There are no per-record sensitivity labels or credential clearances. Migration 0035 removes the
unused `classification` columns from observations and projection registrations, so exports no longer
present an inert field that could be mistaken for an enforced access rule.

**Boundary.** The protected assets include statements, source context, entity identities, derived
reports, citations and export material. A lower-trust application must not obtain another domain's
content by naming its entity, subject, record ID, project or invented clearance. The HTTP server
selects the project from the authenticated registry grant; queries and project-consistent references
apply that scope throughout the memory lifecycle. Unknown label/project request fields are refused.
Project names express no authority themselves. A caller given both credentials can access both
projects; a data-plane SQL identity or database administrator is outside this HTTP confidentiality
boundary. D68/D70 document that distinction.

`TestSeparateProjectsEnforceContentAccessAcrossTheMemoryLifecycle` provisions two projects with
identical entity names and subject identifiers. It verifies content separation across HTTP recall,
inventory, citations, history, export and mutations; foreign and absent record lookups are identical,
read-only mutations fail, and erasing one project leaves the other's content intact. The test also
checks that the unused columns are gone. Existing report provenance and graph tests cover the shared
project predicates and cross-project relationships below those routes.

**Alternatives rejected.** Per-record labels and clearance levels would require a defined order,
authority for relabeling, label propagation through entity sharing, temporal changes, summaries and
every read/export path. An omitted propagation check could reveal protected material through derived
content. No concrete requirement here justifies that additional security model. Filtering only the
final response would leave graph paths, summaries and metadata as disclosure paths. Keeping unused
labels would imply a control that does not exist. A separate PostgreSQL instance per sensitivity domain
is available when operators need that stronger infrastructure boundary, but is not a required
application deployment.

**Cost and limits.** Project separation can duplicate common context and prevents automatic graph
traversal across domains. An application authorized for several domains must query each explicitly
and govern its combined result. Erasure/export are per project, so the application must account for
all projects in which it stored a person's data. This choice adds no label joins or clearance checks
to ordinary recall, and makes no production performance claim.

**Reversal.** Moderate before release, substantial afterward. Adding in-project sensitivity would
require a complete authority/propagation contract, data classification, new tests and migration of
existing records. It must never reinterpret missing labels as broader access. The project-only
contract remains the supported behavior unless that work is explicitly adopted.

## D91 — Independent assertions are attributable source input with stable retries

**Decision.** `POST /v1/records/assert` accepts an atomic batch of 1–20 application-authored claims.
Each carries subject, admitted predicate, object, statement, an optional data subject/validity start,
and a required UUID retry key. The vocabulary supplies cardinality and object type. The statement
becomes a new source message and exact evidence; `curated/v1` and the authenticated credential's UUID
identify its origin. Confidence is zero, meaning no model estimate. The operation calls no model.
These records use the same inspection, correction, withdrawal, source export/erasure and receipt
recovery paths as other admitted records.

The project and author come from the credential. A supplied data subject is the application's
attribution, not independently verified human identity. Relative speaker references require that
attribution and use the stored speaker binding. Unresolvable subject terms are refused unless they
are registered speaker references. The statement is authored evidence; byte agreement does not prove
its truth or that it logically entails the supplied structured relation. Read-only keys are denied
before parsing. Client fields cannot override role, cardinality, confidence, project or author.

**Retries.** The existing observation retry table stores an operation-namespaced key digest and an
explicit versioned request fingerprint. Text is compared exactly and validity instants are normalized
to UTC; omitted validity remains omitted in the fingerprint. Reusing a key with different input or an
erased source conflicts. Deletion clears source/payload metadata and retains the digest tombstone.
An identical retry returns the source's record with `replayed=true`, restores missing derived state
from its receipt when needed, and preserves withdrawal. A new key intentionally authors a new source.
Batch results preserve request order and carry final record versions, including when a later entry
superseded an earlier one. Success audit counts newly authored records; a pure retry counts zero.

**Time and atomicity.** Independent assertions use the shared ordered knowledge clock. Their authored
source timestamps agree with the recorded knowledge event, including logical advancement after clock
rollback. Explicit validity remains distinct. Corrections keep their existing exact withdrawal boundary.
The whole batch, retry receipts, authored inputs, facts, temporal changes and success audit commit or
roll back together. Assertions complete synchronously and do not consume unfinished inference capacity
or advance freshness past earlier pending observations. Database interruption or deadlock aborts the
batch; retry with the same keys. Out-of-order overlapping single-cardinality validity is refused with a
record conflict rather than inventing a historical correction.

**Bounds and cost.** Subject/object/data-subject values are at most 1,024 UTF-8 bytes; a predicate is at
most 128 and a statement at most 16,384. Required fields must contain text, and NUL/invalid UTF-8 are
refused. The shared request-body/admission limits also apply. Each new assertion writes a source,
chunk, authored claim, fact/evidence, registration and recovery receipt; each key retains a minimal
post-erasure tombstone. This buys reliable client retries and exact provenance at a storage/write cost.
There is no additional model call or ordinary recall stage, and no production throughput claim.

**Alternatives rejected.** Editing an existing transcript misattributes the new claim. Re-extracting
the authored statement makes the submitted relation depend on model behavior. Accepting cardinality
or confidence from a client bypasses the vocabulary or misrepresents provenance. Best-effort batches
leave partial success for clients to discover. Content deduplication merges independently authored
sources; retry identity instead expresses whether a request is the same operation. A new retry table
would duplicate the existing tombstone and source-erasure mechanism without a new requirement.

**Reversal.** Moderate. The endpoint and shared writer can be retired, but existing authored sources
must remain inspectable and erasable, and retry tombstones must not be forgotten while queued clients
can replay them. Changing fingerprint semantics requires an explicit version decision. Independent
assertion does not complete feedback, entity purge, human-specific authority or full generations in #89/#58.

## D92 — Agent artifacts are bounded opaque storage with declared ownership

**Decision.** A project stores agent `state` and `file` objects through one artifact contract:
put, get, bounded metadata inventory with kind/literal-name-prefix filters and version-checked deletion.
Metadata search does not inspect opaque contents. Each object has a UUID identity,
an immutable declared data subject and kind, an optional display name, opaque bytes and an opaque
version. Names are metadata and never filesystem paths. A credential supplies the project and writer;
read-only credentials can inspect but cannot write or delete. This is project-scoped storage, with
no implied session lifecycle. Session/history/checkpoint protocols remain #23 and framework adapters
remain #26.

An artifact has its own completed `artifact` observation, assistant role and a stamped expiry. It
creates no message, chunk, fact, embedding or inferred claim and spends no inference reservation.
Its source cannot change owner/role/kind or be made unformed. The bytes live in `agent_artifact`,
registered as a non-sharing projection kind for governance. Deferred relationships require the row,
source, project, owner and erasure registration to agree. Export, erasure and retention discover it
through the existing registry, including the same residual-count predicate. The registration means
that erasure can reach the bytes; it does not make opaque application state reproducible by a model.
These rows are authoritative storage and must be backed up. A projection-generation reset must
preserve them; there is no model-based recovery of a deleted artifact.

**Ownership limit.** The application declares ownership. Opaque bytes are not inspected to discover
other people mentioned inside them. Erasing the declared subject deletes the whole artifact. It
cannot identify an undeclared person in another subject's file. Applications must keep one declared
owner per artifact and avoid putting unrelated people's personal data inside it when their erasure
must also reach it. This is a real limitation, not an anonymization claim. Project credentials grant
project-wide access under D90, not subject-restricted access.

**Mutation and retries.** Creation uses a caller-generated UUID and an operation-namespaced source
retry receipt. An identical create retry returns current metadata without overwriting later bytes;
a changed create or reuse after deletion/erasure/expiry conflicts. Tombstones retain only the key
digest after source deletion. Updates require the current version. The last accepted prior-version
and exact payload digest recognize a repeated overwrite after a lost acknowledgement. Older versions
conflict. Source identity, ownership and original expiry remain fixed. There is no archive of prior
opaque payloads. Deletion also requires the current version and removes the source; repeating a
completed delete returns not found. Each mutation and its content-free success audit commit together.
Concurrent storage errors/deadlocks abort the transaction; callers retry the same request and inspect
on a version conflict. Failed writes must not reserve bytes or leave unregistered sources.

**Bounds.** Requests use canonical base64 within the shared 1 MiB body/admission budget. The safety
ceiling is 512 KiB decoded per object. Names are at most 256 UTF-8 bytes; declared owners at most 1,024.
Default project allowances are 256 KiB/object, 16 MiB of retained payload, 128 objects and 720 hours
maximum lifetime. `taisce artifact limits` reads usage or atomically replaces all four allowances
through an administrator connection. Configurable ceilings are 512 KiB/object, 1 GiB/project,
100,000 objects and 8,760 hours. This is operator-controlled entitlement, with no billing dependency.
PostgreSQL counters serialize quota reservations across processes and release capacity transactionally
on overwrite shrink, deletion, erasure and expiry. Runtime credentials cannot edit either limits or
usage. Reducing allowances retains accepted data and still permits shrinking/deletion. Expiry is
stamped at creation using the shorter artifact/project policy and never extended on overwrite.
Expired objects are unavailable immediately; the existing retention sweep reclaims their storage and
allowance. A delayed sweep may therefore temporarily refuse new writes despite hidden expired rows.

**Cost.** Each create writes a source, opaque row, registration, retry receipt and audit entry.
Payload allowance measures current bytes, not indexes, metadata, WAL, replicas or backup storage;
object-count limits bound metadata separately. PostgreSQL payloads increase write/backup volume and
contend on one quota row per project. There is no model cost or extra recall stage. Minimal retry
tombstones outlive payload erasure; total ledger/tombstone growth remains part of #93/#103 capacity
work. The defaults are safety budgets, not production throughput or latency measurements.

**Alternatives rejected.** A second object store adds a separate dependency and erasure sweep.
Extracting scratchpads promotes agent working notes into personal memory. Unbounded project storage
has no defensible lifetime or resource ceiling. Session-only storage makes the byte journey depend
on a framework lifecycle that is not yet defined. Blind overwrite loses concurrent edits; retaining
all previous byte versions would enlarge both retention and erasure without a requested reader.

**Reversal.** Moderate before release, substantial after clients retain identities. Changing to
session ownership or adding multiple owners needs an explicit migration and erasure policy. Moving
bytes to another substrate requires preserving registration, expiry, versions and counted erasure.
Retiring the endpoint cannot forget outstanding retry tombstones while clients may replay them.

## D93 — Retention scheduling follows stored deadlines

**Decision.** The worker discovers a project when an observation has a due stamped deadline,
independently of the project's current retention setting. A partial index on project, expiry and
log offset supports an existence probe and bounded expiry-ordered deletion. Sources with future
or absent deadlines are retained. Artifact deadlines therefore run under the default indefinite
project policy, and changing a project's policy to indefinite does not cancel earlier deadlines.
The existing registry walk still deletes projections and source bytes in one transaction; artifact
quota is released by the same database accounting mechanism as explicit erasure.

**Evidence.** `TestRetentionDriverFindsArtifactDeadlinesWithoutAProjectRetentionPolicy` reproduced
retained expired bytes before this change and now verifies driver-level removal and quota release,
while another project's future artifact survives. `TestOnlyProjectsWithDueStoredDeadlinesAreSwept`
checks stored deadlines after a policy becomes indefinite. The existing retention tests exercise
bounded pages, source sharing and registered projection removal.

**Cost and alternatives.** Discovery performs a project inventory with indexed existence checks;
expiry-bearing sources add an index entry and its write/storage cost. This changes worker scheduling,
not ordinary recall, and makes no production throughput claim. Looking only at current project policy
misses artifacts and previously stamped deadlines. Repeatedly enumerating every due source merely
to deduplicate projects can rescan a large backlog on every bounded sweep pass. Unconditionally sweeping
every project performs deletion transactions even where no deadline is due.

**Reversal.** Small before release: the discovery predicate, ordering and supporting index can change
together. Any replacement must preserve already-stamped deadlines and schedule artifact expiry without
requiring a project-wide retention policy. This fixes #122; broader capacity measurements remain #103.

## D94 — Optional subject identities keep external mappings at the governance boundary

**Decision.** A project can register a subject with a server-generated random UUID and optional
external reference/label. Get resolves exactly one ID or reference; inventory is bounded immutable-ID
pagination; versioned updates replace current metadata without rewriting attribution. References use
exact case-sensitive bytes and are unique within a project. There is no automatic normalization,
alias history or merge. Existing application-managed opaque IDs remain supported and are not
implicitly copied into the registry. New integrations use the random ID for source attribution and
person-filtered recall, export and erasure.

**Boundary.** A credential supplies project and writer. Read-only credentials can inspect/export,
not register/update. Unknown, foreign, erased and inactive unused identities share a 404. Metadata
never enters inference, displayed claims, permanent audit or metric labels. Source bodies remain
application input; the registry does not redact a reference the application put in a message/file.
A random UUID is pseudonymous, not anonymous, and neither it nor a reference verifies a human or
narrows project-wide authority. A stolen project credential retains project-wide access under D90.
Mapping rotation must not be used to assign another person's account to existing memory.

**Retries and mutation.** Registration requires a UUID retry key. Project-scoped key digests serialize
retries, retain an original request fingerprint and return current metadata after later updates.
Changed input or reuse after erasure/expiry conflicts. Only the immediate prior version and exact
latest update fingerprint support overwrite retries; other stale writes conflict. Unique references,
immutable identity/lifetime, retry ownership and payload clearing are database constraints/triggers.
Mutations and content-free success audit commit atomically. Original registration fingerprints remain
until the mapping is removed; hashing a predictable external reference is not represented as erasure
or anonymization. Export includes the registry and linked retry metadata. Erasure counts the registry
and residual even with no source observations, clears metadata and leaves only retry-key tombstones.

**Lifetime and concurrency.** Registration stamps an inactive deadline from project retention.
Null retains until erasure. Finite deadlines apply once no attributed observation survives; artifact
sources count too. Metadata updates and current policy changes do not extend that deadline. The
worker discovers registry-only expiry and deletes bounded pages. Source acceptance takes an optional
registry row lock; cleanup locks candidates and rechecks absence of sources at a fresh statement
snapshot. In-flight accepted sources therefore preserve their mapping. Explicit erasure serializes
with registered metadata changes. A newly accepted source after completed erasure is new collection,
not permanently prohibited; applications must stop sending deleted IDs and resolve/register for new
collection. Existing per-operation retry tombstones still prevent old source operations from replaying.

**Bounds and cost.** References are at most 1,024 UTF-8 bytes and labels 256; the shared request limits
apply. Inventory defaults to 20, at most 100, with live cursor semantics. A registration writes one
registry row, retry receipt and audit entry. Source writes add an indexed optional lookup/lock;
ordinary recall gains no registry join or model call. Registry reads/expiry use the existing source
attribution index. Long-lived sources can keep many expired mappings alive, so examined rows are not
bounded by returned/deleted page size. Total metadata/tombstone capacity remains #93/#103. There is
no production performance claim.

**Alternatives rejected.** Repeating account/email references through every projection enlarges the
identity handling boundary. Deterministic public hashes remain guessable and linkable. Automatic
case folding or merging guesses at external account semantics and can combine different people.
Treating identity metadata as extracted memory adds model work and implies it can be regenerated.
Expiring a mapping before retained source bytes removes the lookup needed to govern those bytes.
Retaining every finite-policy mapping indefinitely ignores the stated policy for unused accounts.
Making the registry mandatory would rewrite existing attribution without a defined source history.

**Reversal and proof.** Moderate before release; substantial once clients retain IDs. Registry rows
are authoritative and must survive projection rebuilds and backups. Missing-fact recovery preserves
source IDs and current rotated mappings; model reinterpretation must not invent external references.
There is no implicit rekey/merge of existing IDs. HTTP/PostgreSQL tests cover two speakers, rotation,
missing-fact recovery, project/read-only boundaries, exact references, races, counted erasure,
source-aware expiry, quiet-project scheduling and rollback. This completes the subject contract in
#95; full generations remain #58 and do not require inferring registry contents.

## D95 — Operator recovery repairs missing support without replacing retained records

**Decision.** `recover facts` handles both missing fact rows and missing receipt-backed support for
retained facts. `restored` counts newly recovered fact rows; `repaired` counts existing records whose
original evidence, temporal history or erasure registration was restored. These counts are disjoint
and sum to the success-audit magnitude. A failed page reports no committed progress. Ordinary
formation and authored replay retain the existing missing-fact helper and do not run this extra work.

Retained records are locked with their receipt and surviving source before support repair. Exact
source bytes and attribution must agree. Existing conflicting evidence, history, ownership or
pipeline metadata is refused, never overwritten. Missing support is inserted with conflict checks;
both original and causal observations own restored temporal history. Statements, current intervals,
versions and human withdrawals remain unchanged. Evidence restoration still advances the source's
report revision and invalidates derived reports atomically. A repeated complete page performs no
projection mutation or revision bump. Erased source/receipt state cannot be reconstructed.

**Cost and alternatives.** This adds source validation and indexed support/registration checks for
retained records on an operator recovery page; ordinary recall and ingestion gain no additional
queries or model calls. Pages remain limited to 100 receipts with the operator's 30-second deadline;
actual supporting history rows are determined by retained source receipts, not a throughput promise.
Skipping retained records leaves missing citation/history data unrepaired. Deleting and recreating
those records needlessly restamps state and risks losing human edits. Blind upsert would overwrite
conflicting retained evidence instead of exposing an integrity failure. Re-extraction cannot reproduce
the exact original knowledge state and remains an explicit separate generation operation.

**Reversal and proof.** Small before release: the additional operator repair branch can be disabled
without rewriting stored state. The `repaired` progress field is then zero. The regression test first
failed with retained facts and missing evidence, and now restores exact evidence/history plus both
erasure registrations. Other tests cover preserved withdrawals/versions, no-op source revisions,
conflicting support, atomic storage/audit failure, and races with human withdrawal and erasure.
This advances #58; unrecorded additional support, full projection generations and changed-model
reinterpretation remain open. Source receipts and authoritative artifact/subject storage still need
backups; model-based rebuild must preserve those governance roots.


## D96 — Extraction attempts pin a content-free configuration identity

**Decision.** The shipped HTTP extraction path stamps evidence and rejected proposals with
`extract/v2:<sha256>`. The digest covers configured model name and endpoint origin; embedded request,
parser, prompt-composition and admission source; embedded extraction YAML; the ordered relation
vocabulary; and the sorted unresolvable-term set. Credentials, URL userinfo/path/query/fragment,
message content, random prompt fences and embedding configuration are excluded. Citations and export
already expose evidence stamps; missing-fact and support recovery preserve those original stamps.
Unidentified in-process proposers retain the `extract/v1` code label, which does not identify a model
and must not be treated as proof of model equivalence. The runtime HTTP proposer always identifies
its configuration. Curated sources remain `curated/v1` and bypass inference.

A source-owned `source_extraction` row pins the first attempted configuration before any inference,
including failed attempts and empty results. It is not a formation-completion marker. New fact and
rejection writes check the pin in their transaction under the existing source lock. A conflicting
retry refuses before inference in the normal worker, or before writes in a direct versioned call.
A missing pin can be restored only if surviving evidence, rejections and fact receipts agree.
Pins cannot be updated, are project-consistent by foreign key, are exported with their source, and
are deleted by erasure or source expiry. An erased source cannot regain a pin. A newly collected
independent source may use another configuration.

**Trust and limits.** This protects accepted source outcomes from an accidentally changed worker
configuration; the operator still controls the deployment and model service. A digest is a matching
identifier, not attestation or anonymization. An unchanged model alias can point at different weights,
and routing may change behind the same origin. URL paths are deliberately excluded because they may
carry credentials. Therefore this is a configuration identity, never a promise of identical output.
Database entity-resolution/speaker policy, graph/community algorithms, embedding/report generations
and the dependency registry's `v1` implementation stamp are separate concerns. They are not claimed
to be covered by this extraction fingerprint.

**Cost and alternatives.** One small control row per attempted source, one transaction before a
model attempt, and an indexed pin check per admitted/rejected write are added off the recall path.
First pin creation also checks source-indexed surviving outputs. Digests are computed from bounded
compiled code and vocabulary, not the growing corpus. There is no production latency claim.
A fixed version hides model and prompt changes. Including the whole binary or Git commit makes
unrelated components invalidate every attempt. Hashing only a model name misses local rules and
vocabulary. Hashing endpoint paths or credentials retains secret-derived material without proving
weights. The chosen source hashing is conservative: even a comment edit in a covered source changes
the identity. Reusing the original binary/configuration is the supported retry after such a change.
Silently appending new interpretations to partial old output is rejected.

**Reversal and proof.** Moderate before release: remove the pin/checks in one migration and keep
historical evidence stamps. Removing stamps would destroy provenance. This is a dependency of #58,
not a completed changed-model rebuild. Until explicit generation cutover is implemented, an affected
unfinished source must finish with its original configuration; deleting its pin is not a recovery
procedure. PostgreSQL/HTTP tests cover real configuration propagation, key rotation invariance,
changed configuration refusal before inference, failed/empty attempts, concurrent first pins,
project/missing-source refusal, immutable pins, storage failure rollback, exact citation recovery,
export, counted erasure and expiry. Existing correction/replay tests retain human-authored provenance.

## D97 — Rebuild publishes one bounded source generation atomically

**Decision.** `taisce rebuild facts --project <project> --source <observation-uuid> --key
<operation-uuid>` prepares one retained conversation source under the current extraction identity
and publishes it in one PostgreSQL transaction. The source is read with a repeatable-read snapshot,
bounded to 256 messages and 1 MiB, and the model runs before the cutover lock is taken. The cutover
rechecks the source digest, retires only prior model-derived facts for that source, writes new facts
with generation-scoped IDs, records admitted/rejected/retracted/refused outcomes, and replaces the
source extraction pin. Missing current model-derived facts are restored from source-owned receipts
inside publication before retirement, so later recovery cannot revive the previous interpretation
(#123). Damaged receipts abort publication. A generation row and per-fact disposition rows are the
durable receipt.

The old facts stay readable while preparation runs and remain as historical, knowledge-closed rows
after publication. Citations expose the generation that admitted a fact and the generation that
retired it, so a changed interpretation is explainable rather than an apparent edit. A human
correction wins over a generated claim with the same source-message subject/relation slot; changing
the proposed object cannot bypass it. A source-backed withdrawal continues to block its matching
structural claim. The same operation key and extraction identity
replay the committed result and finish the formed watermark without another model call. A changed
source, identity or concurrent correction aborts the cutover and leaves the prior generation intact.

**Cost and alternatives.** This adds two bounded lineage tables and a source extraction pointer; it
does not add a cache or a second datastore. Preparation holds no database transaction, so model
latency does not block a project's ingestion queue. Publication locks one source, scans at most 100
claims and retires at most 100 prior generated facts. Deleting and re-deriving would make recall
temporarily empty and lose the historical explanation. Appending new facts without retirement would
make current recall contain competing interpretations. A scope-wide rebuild is still a sequence of
these source generations and requires its own progress and cancellation contract.

**Reversal and proof.** Moderate before release: generation tables and the source pointer can be
removed only with a migration that preserves old facts and their evidence. PostgreSQL tests prove
atomic rollback, changed-source refusal, idempotent replay, exact quote validation, role and
retraction policy, human-correction protection, temporal boundaries, citation/export lineage and
subject erasure. The command has a content-free JSON progress result. Full-scope orchestration,
other projection rebuilds and workload measurements remain #58/#103. Model-response caching is
tracked separately in [ensera-ai/taisce#108](https://github.com/ensera-ai/taisce/issues/108).

## D98 — Project fact rebuilds checkpoint a fixed log range

**Decision.** `taisce rebuild project` runs a bounded page under one durable operation key and
extraction identity. PostgreSQL captures the initial log upper bound, cursor and pending offset.
Every source has a stable operation key derived from the job and its log offset, so a committed
generation survives a lost acknowledgement without another inference call. Each source uses D97's
atomic publication; the whole project does not switch in one transaction. Human-authored sources
are excluded, and later appends belong to a later job. #124 tracks the implementation.

One session advisory lock limits runners per project, and a persisted lease fences a replaced
runner inside generation publication. One active job per project is a database constraint. No
transaction remains open during inference. Each invocation admits at most 100 sources, with the
existing five-minute per-source deadline; at least two pool connections are required and validated.

**Cancellation and governance.** Reserving the pending offset is the admission boundary. A live
runner may finish that source after cancellation is requested, but cannot reserve another. When
the runner is gone, cancellation takes the free project lock, reconciles a committed pending
generation if present, finishes its freshness bookkeeping, and settles the operation in one
transaction without inference or its original configuration.
Start and cancellation require an atomic audit write. The authority is the operator database
connection; no public HTTP endpoint or human login authority is added.

Job rows retain only log offsets, configuration hashes and aggregate progress. They contain no
source IDs, subject attribution or memory text, so source erasure can remove a pending observation
without a foreign-key coupling or a retained personal link. Reserved erased/cancelled sources count
as skipped; sources erased before visiting are absent from the counters. Operational checkpoints
remain after erasure, like watermarks. Provider errors and model responses are not stored in jobs.

**Alternatives and reversal.** A single transaction over model work would hold source locks for
provider latency and make restart cost the whole job. A shell loop has no durable boundary, identity
pin or lost-acknowledgement protection. A broker adds an independent store without improving these
guarantees. This adds one control table and an optional publication fence. Removing orchestration
before release is moderate: preserve source-generation receipts and let active jobs settle before
removing their control table. Entity/chunk/embedding/report rebuilds and response caching remain
separate work under #58.

**Proof.** PostgreSQL tests cover finite pages, later appends, empty projects, authoritative source
exclusion, cancellation with live and disconnected runners, erasure before/during inference, lost
progress after publication, stale-runner refusal, concurrent admission, configuration mismatch and
audit/storage rollback. CLI tests prove status/cancellation remain available without a provider.

## D99 — Recovery verifies the identity behind a retained endpoint UUID

**Decision.** A saved entity snapshot must match the fact endpoint and the retained entity's project,
canonical/normalized name, identity kind and speaker subject. Recovery holds a shared entity row
lock through commit so another writer cannot change those fields after validation. Missing entities
retain their original IDs; incompatible retained identities refuse the whole recovery transaction.
The same check applies when repairing support on a retained fact.

**Alternatives.** Reusing any row with the same UUID silently changes the identity attached to
recovered memory. Overwriting that row would change every other fact using it. Neither is acceptable.
Entity type and resolution metadata are not identity keys and are not overwritten on retained rows.
Aliases are excluded: the current snapshot deliberately omits them and cannot establish their source
ownership. Faithful alias reconstruction remains #102/#58; no alias is inferred from a canonical name.

**Impact and reversal.** This protects recovery integrity, including generation replay and curated
recovery. It adds at most two entity checks per recovered record and holds shared locks until the
bounded page commits. This is not controlled-host performance evidence. Reversal requires only code,
but would restore the demonstrated identity-confusion defect (#125).

**Proof.** Real PostgreSQL regressions first reproduced silent conflicting-identity reuse for both
missing and retained facts. They now refuse without partial facts, evidence or registrations.
Additional checks cover damaged endpoint snapshots, faithful reconstruction, compatible shared
entities and preservation of existing alias arrays; generation/recovery race tests cover shared callers.

## D100 — Name variants retain source ownership outside the entity cache

**Decision.** Each admitted named endpoint retains its exact spelling and normalized identity in
`entity_name_receipt`, keyed by project, observation, entity and a SHA-256 text key. A database trigger
computes the key and refuses a source/project or named-identity mismatch. The original text remains
stored and checked. Records have a source FK and survive derived entity loss, like fact receipts.
Export includes the subject's own records; erasure and expiry delete them and rebuild affected caches
in the same transaction. Recovery restores alias caches from surviving records and counts repaired
retained facts in its existing progress output.

**Identity boundary.** Canonical name, normalized identity and speaker attribution remain the stable
shared identity validated by D99. Variants are source-contributed spellings of that same normalized
name, not semantic equivalence declarations or changes to the canonical identity. The canonical
identity may survive a contributing source when another source still supports the entity. Independent
support for the same variant survives one contributor's erasure. Aliases from an old untracked array
have no provable owner: migration 0043 clears those caches rather than assigning invented sources.
New source-owned records are the recovery input; this unreleased migration does not claim to recover
unrecorded historical spellings.

**Bounds and cost.** Each name and normalized name is at most 4096 UTF-8 bytes. An entity has at most
64 displayed variants beyond its canonical spelling; exceeding the bound refuses the fact transaction.
Ordinary writes update this bounded cache without scanning earlier name records. Recovery and deletion
query source support through the project/entity index; their work can grow with an entity's supporting
sources. Controlled-host plan and workload comparisons remain #102/#103. Arrays contain distinct
display spellings and distinct normalized search terms, not parallel position-paired lists.

**Concurrency and alternatives.** Source rows are locked before named entity work; erasure locks its
source rows before refreshing shared entities. Recovery uses update locks for its endpoints, extending
D99's shared locks to avoid competing lock upgrades during cache repair. Reconstructing from shared
canonical snapshots would invent provenance; retaining unowned arrays would leave erased contributions;
re-querying all support on every assertion would amplify the write cost with corpus growth. All are
rejected. Reversal requires a migration and loss or export of the source-owned variant records; changing
the cache layout alone does not require re-extraction.

**Proof.** PostgreSQL tests cover shared variants, source-specific export, erasure/expiry, entity loss,
cache repair counts, concurrent insertion, size/cardinality limits, project mismatch and rollback.
HTTP assertions/corrections expose bounded-input refusals with no partial sources. General entity
resolution generations and independent projection rebuilding remain #58; this completes #127's
source-owned spelling lifecycle.

## D101 — A message owns its chunk identity and recovery uses bounded source pages

**Decision.** The authoritative `turn_message` retains its chunk UUID. Ingestion returns that UUID
from the message insert and uses it for the derived chunk and erasure registration. A deferred
composite foreign key binds each message chunk to that exact source and ordinal. Migration 0044
retains existing IDs and refuses ambiguous multiple-chunk mappings. A message whose chunk was
already missing receives a new ID: an identity lost before this boundary cannot be reconstructed.
The retained message column is part of the source backup and subject export, and disappears with
its source on erasure or expiry.

**Recovery.** `taisce recover chunks --project NAME` repairs one page, optionally restricted by
`--source UUID`. A `(log_offset, ordinal)` cursor orders messages within the project. Each transaction
selects at most 100 messages plus one lookahead, locks those sources/messages, checks a 16 MiB source
text budget, and reconstructs missing chunks and registrations. Existing incompatible source, text,
time, role, owner or registration pipeline causes the entire page to refuse. Existing embeddings
are preserved; lost embeddings remain absent until an explicit embedding workflow supplies them.
Audit and recovery commit together. A lost acknowledgement is safe to retry with the same cursor.

**Alternatives and limits.** Generating IDs during recovery would break stable references on each
repair. Inferring a missing UUID from an erasure registration would fail if that derived registration
was also lost. Keeping the UUID with the message gives one source of identity with no additional
inference or database round trip on ingestion. The FK costs a composite source index. Documents,
changed segmentation, semantic generations and embedding model changes have separate contracts;
this operation covers the actual one-chunk-per-message representation. Bounds limit page contents
and locks, not a proven constant database query cost. Controlled-host plan and workload qualification
remain #103. Reversal requires removing the FK and source column and loses stable repair identities.

**Proof.** PostgreSQL tests cover exact IDs/content across pages, registration-only repair, wrong
project refusal, conflicting data/pipeline refusal, failed-audit rollback, size bounds, migration
ambiguity and restart, embedding preservation, concurrent recovery and erasure. CLI tests cover
validation, metadata-only progress, cancellation and failed output. This completes #128's repair
scope; other projection rebuilds remain #58.

## D102 — Entity inspection is bounded project-authorized identity access

**Decision.** `/v1/entities/list` exposes retained entity IDs, identity kind, canonical-name preview,
name byte count, truncation flag, alias count and first-seen time. `/v1/entities/get` resolves one ID
to its canonical name, bounded aliases and optional speaker subject binding. Both operations use the
credential's project and permit read-only credentials. A foreign ID and an absent ID produce the same
404 response; supplied cursors grant no authority. The database and Go audit vocabularies include the
two operations. Audit rows contain the operation, principal, project, result and magnitude, not names,
subject IDs or caller-supplied entity IDs.

**Bounds and access.** Inventory pages contain 1–100 entities with at most 512 Unicode characters
per name preview. UUID ordering uses the existing `(scope, entity_id)` index. Deleting the cursor row
does not shift the next page; this is a live inventory and inserts behind the cursor require restarting.
Exact lookup admits canonical names/aliases of at most 4096 UTF-8 bytes, at most 64 aliases and a
speaker subject ID of at most 1 MiB, matching the source contracts. SQL bounds transferred fields
before Go allocation and rejects unsupported stored identities rather than presenting a clipped value
as the identity. Generated descriptions, embeddings and unbounded fact/support lists are excluded.

**Alternatives.** Expanding entities through recall makes a simple identity lookup depend on retrieval
selection and a question's wording. Returning complete identity/support content for every inventory
row would amplify page size with source history. Separate bounded inventory and exact lookup give a
client stable navigation from record endpoint IDs without inference. Project credentials authorize
project content (D90); this operation does not introduce per-subject confidentiality or perform merging,
correction or purging. Those mutations require their own provenance and version policy under #89.
Reversal is removal of these read operations and their ledger names; retained identity data is unchanged.

**Proof.** HTTP/database tests cover endpoint resolution, speaker attribution, read-only authorization,
strict input validation, indistinguishable cross-project refusal, content-free auditing and erasure.
Database tests cover Unicode preview bounds, deletion between pages, oversized stored identities,
shared alias removal and an actual project/UUID index scan without an intermediate sort. Race checks
pass. These plans establish the access shape, not controlled-host latency or capacity (#103).

## D103 — Embedding work is bounded and cosine requires finite directions

**Decision.** The embedding adapter accepts at most 64 nonblank UTF-8 inputs, 64 KiB per input and
1 MiB of input text per call. It refuses before JSON serialization or HTTP work when those bounds
fail. A reply is at most 64 MiB, checked with one extra byte so a valid JSON prefix at the ceiling
cannot hide an oversized response. Each vector has 1–16,000 finite components and a nonzero norm;
index alignment and equal dimensions within the batch remain required. Failure returns no partial
vector batch and never quotes source text or the provider response.

**Numeric boundary.** The shared cosine function refuses non-finite components, zero directions,
different dimensions, empty vectors and dimensions above the same ceiling. Float64 accumulation
handles finite float32 extremes; the result is clamped to [-1,1] for floating-point endpoint error.
Zero is a valid similarity score and must not be used as a substitute for an invalid comparison.

**Alternatives and scope.** An HTTP response byte ceiling alone leaves request serialization
unbounded. Relying on downstream ranking to reject zero vectors delays a provider defect until after
a caller may have persisted it. Returning NaN makes ordering undefined. These are rejected at the
adapter/numeric boundary. The ceilings are initial defensive memory/work limits, not provider token
limits, throughput targets or a measured operating envelope. They preserve the current small batched
semantic callers; larger jobs must page their inputs. They neither fix a persisted vector dimension
nor claim that differently versioned models share a space. Persistent model/dimension identity,
index selection and generation cutover remain #101/#19. Reversal is a code change; it restores the
unbounded-input and invalid-direction failure modes without changing stored data.

**Proof.** Regressions reproduced accepted zero embeddings and successful NaN comparisons before
the fix. Adapter tests now cover refusal before network work, exact count/byte/dimension boundaries,
valid order, a response whose oversized prefix is valid JSON and finite extreme cosine inputs.
The full adapter suite passes under the race detector. This closes #130's boundary-hardening scope.

## D104 — Message vectors belong to explicit model generations and their exact sources

**Decision.** Persist full-precision message vectors under immutable project/model/revision/endpoint-
digest/dimension/metric/input-version identity. An operator starts a generation with an idempotent
UUID key and a captured maximum observation offset. At most two non-discarded generations are retained
per project. Build progress is separate from identity and activation; runtime grants cannot modify
those policy tables. Explicit activation independently checks surviving source coverage and hashes.
The active pointer changes atomically, never as a side effect of starting or finishing a build.

**Sources and failure.** Stable retained message chunk IDs and SHA-256 of exact UTF-8 content bind each
vector to its source. Bidirectional deferred constraints require the matching source registration;
export, erasure and retention cover every generation. Pages contain at most 64 messages, 64 KiB each
and 1 MiB total. The model supplies coordinates only. A session lock spans the provider call without
holding a transaction; a persisted lease fences disconnected runners. Commit locks sources/chunks,
checks compatibility and publishes vectors, registrations, cursor and audit together. Source deletion
wins; source edits refuse. Repair replays the captured range with the same model and preserves
compatible vectors. Cancellation clears the lease; bounded pruning excludes active/building generations,
detaches and drops only an empty catalog-validated partition, and retains an operation-key tombstone.

**Retrieval and indexing.** Storage admits 1–4000 finite components with nonzero direction. A generation
partition has a dimension-specific cosine HNSW index. Through 2000 dimensions it indexes vector values;
above that it indexes normalized half-precision expressions while retaining original coordinates for
reranking. Exact retrieval materializes eligible rows before distance ordering so it cannot silently
use ANN. Optional bounded ANN candidates are filtered against current authoritative source hashes and
reranked in full precision. Both paths are project scoped and return message evidence with bounded
previews. Approximate filtered recall and production capacity remain unqualified (#101/#103).

**Alternatives and reversibility.** An unstamped fixed-dimension column cannot distinguish model spaces
and cannot store the configured 2560-dimensional provider output. In-place re-embedding mixes spaces
while work is incomplete. Both are rejected; the unused fixed 1024-dimensional chunk column and its
provisioning-time index are removed in the unreleased schema. A second database would add an erasure
and operational boundary without demonstrated need. Partition-per-generation bounds the dimension
contract and isolates an index graph; its catalog/DDL cost needs controlled-host qualification before
large project counts. Reversal needs a storage migration and regeneration from retained messages.

**Limits.** Model revision is operator-declared, not a provider attestation. Builds cover finite source
snapshots; continuous embedding of new messages is not delivered here. Active repair can temporarily
have incomplete coverage. No entity/report embedding, semantic quality, latency, throughput or capacity
claim follows from this message-vector journey. Tests prove source ownership, cancellation and rollback,
plus exact/approximate compatibility with the local provider's actual 2560 dimensions. Index fixtures
prove eligibility with controlled planner costs, not that ANN is always the cheapest small-corpus plan.

## D105 — Entity semantic representations bound identity as well as evidence

**Decision.** `semantic.Surface` returns a validated representation or an explicit refusal. Its
2,000-byte UTF-8 budget covers the trimmed canonical name, optional aliases, evidence and separators.
The canonical identity must fit in full. Optional aliases and quotes are deduplicated, sorted and
included whole when they fit; an oversized item is skipped so it cannot exclude a later fitting one.
At most 64 aliases, 1024 quotes and 1 MiB of combined input bound validation, deduplication and sorting.
Invalid UTF-8, NUL and an empty canonical identity refuse with no partial output. Measurements must
handle refusal before asking a provider to embed anything.

**Alternatives.** Budgeting only evidence lets names bypass the declared work bound: a regression
produced 12,004 output bytes against a 2,000-byte budget. Clipping a canonical name or a quote silently
changes the identity or source represented, so both are rejected. Stopping at the first non-fitting
optional item gives its sorted position unintended power over all subsequent evidence. Deterministic
whole-item selection preserves the same ordinary inputs while making omissions explicit in the contract.
Byte accounting matches provider request bounds; calling bytes characters would imply an unequal and
incorrect Unicode allowance. These defensive ceilings are not measured semantic-quality optima.

**Scope and reversal.** This package supplies experimental candidate representations. It does not
persist entity vectors, choose a merge threshold or enable automatic semantic merging. Existing
measurements still distinguish paraphrase gains from near-miss risks; #10/#11/#19 retain those gates.
A caller already loading unbounded evidence must separately bound its database query; this pure helper
cannot undo that allocation. Reversal is a code/API change and requires no data migration. Tests cover
exact Unicode/collection limits, invalid input, whole-item selection, duplicates and deterministic order.

The affected description, neighborhood, resolution and thematic checks pass against the configured
local provider. Resolution distributions still overlap; the small thematic fixture retains a positive
gap but does not establish a shipping threshold. The semantic package has 100% statement coverage
under the race detector; the complete cross-package suite passes at 91.9%, above the unchanged floor.

## D106 — Authenticated passage evidence uses an explicitly bound active message generation

**Decision.** `POST /v1/passages/search` accepts a bounded question and retrieves source messages
under the credential's project. It is a read operation, available to read-only credentials. Similarity
is evidence selection, not an assertion of a fact: results carry source/chunk identity, ordinal,
role, occurrence time, similarity and a UTF-8 preview. The preview is at most 512 Unicode characters;
`message_bytes`, `preview_byte_end` (exclusive, starting at zero) and `preview_complete` distinguish
an excerpt from the entire message. This route does not expose a general source-content lookup.

The API opts into read-path inference with `TAISCE_INFERENCE_EMBEDDING_REVISION`. Empty disables the
capability; setting it requires valid embedding configuration at startup. Embedding-only configuration
validates the effective provider's allowlisted endpoint and paired credential without requiring an
extractor model. Worker-only processes do not configure this HTTP capability. The configured model
name, explicit revision, endpoint digest and active dimensions must match the active generation
before inference; storage checks identity again after the provider returns. Activation during inference
therefore cannot combine different vector spaces. No active/matching generation or provider failure
returns a content-free 503. Operator configuration and generation idempotency keys are not in results.

**Boundary and cost.** The credential authorizes a project, not a data subject; callers can filter
by subject and role, and must supply the intended subject for personal questions. Shared HTTP
admission, the 30-second request context and inference payload/vector bounds also apply here. Questions
are at most 8192 bytes, results at most 32, and optional ANN candidates at most 1000. Each request
performs an active-generation lookup, one provider call and a PostgreSQL read. No latency claim is
made for this additional read-path dependency. Exact retrieval is the default. ANN candidates are
selected before subject/role filtering and full-precision reranking; selective queries can return
fewer results, and ANN recall is unqualified. The response includes generation identity, finite
`through_offset` and build state; it does not promise continuous indexing of new observations.

Source hashes and source joins are checked at the final read. An erasure completed during inference
removes the source from that result. As with other memory reads, a response already read or delivered
cannot be recalled after a concurrent erasure. Credential audit uses the existing best-effort,
content-free transport policy; a failed audit write is logged, not a claim of atomic read/audit
coverage. Operator vector search retains its transactionally recorded operator audit.

**Alternatives and reversal.** Rejected silently selecting any configured embedding model, since equal
vector lengths do not establish a common space; rejected merging passages into asserted-fact recall,
since similarity does not establish truth; rejected requiring an extractor for API-only inference,
since it is not used. Mandatory ANN would sacrifice the exact comparison baseline without measured
justification. This adds a transport/application capability over D104 storage and requires no migration.
Disabling the opt-in or removing the route reverses it without rewriting stored generations.

**Evidence.** Real PostgreSQL and HTTP tests cover read-only access, credential audit, project/subject/
role isolation, bounded Unicode previews, source erasure, provider failure, activation during inference
and storage refusal. The configured-provider test exercises the same authenticated route under the
`inference` build tag. Controlled-host capacity and semantic ANN quality remain #101/#103.

The local configured-provider HTTP journey passes at its actual 2560 dimensions. The full uncached
PostgreSQL suite passes at 91.9% cross-package coverage after correcting the independent clock-sensitive
inspection fixture (#135); the unchanged floor is 91.6%. Targeted race and contract checks pass.

## D107 — Active message embeddings advance through bounded, explicit catch-up

**Decision.** An operator may follow an explicitly selected active message generation with the
unprivileged memory identity. Each catch-up sequence captures the transactionally serialized append
watermark and processes bounded pages after the preceding completed range. New observations arriving
while that range is processed wait for the next sequence. Model identity and the original generation
boundary remain immutable operator policy; mutable worker progress holds the current target separately.

`through_offset` is the finite target, `covered_through_offset` is the last fully processed boundary,
and `initial_through_offset` in operator status records the original build boundary. Completed coverage
is conservative: a page can publish useful evidence above it before the whole range completes. Erased
and non-message observations consume offsets but require no embedding. A ready range with inconsistent
coverage refuses catch-up rather than skipping it. Repair resets coverage and scans the current target
range with the same model; it does not extend the target itself.

**Concurrency and authority.** Catch-up reuses the bounded source reader, session advisory lock,
persisted lease and atomic source-owned publication. A disconnected worker cannot publish an old
lease. Publication serializes with activation using the existing project policy advisory lock and
refuses if the selected generation is no longer active. The runtime worker never receives UPDATE
permission on model identity or activation policy. Source erasure or expiry during inference wins;
provider failure does not advance the source cursor or completed coverage. A suspended project refuses
new catch-up work. Administrative source corruption requires repair; a past coverage marker cannot
prove that an operator has not subsequently changed storage directly.

**Operator process and cost.** `taisce embeddings follow` uses `TAISCE_MEMORY_DSN`, explicit project,
generation and revision, and an allowlisted embedding provider. One invocation performs at most one
page. `--watch` repeats pages with a configured 100ms–1m wait (default 2s), per-page two-minute deadline,
exponential failure backoff capped at one minute, cancellation and content-free status. Unchanged idle
state stays quiet. Provider payloads never enter watch status; offsets of -1 with `retrying` mean no
coverage was verified for that attempt. Invalid model/state/policy stops the worker; transient storage
or provider failures retry visibly. No model switch is selected automatically.

This is incremental work in new source messages, with bounded per-page source text and vectors. The
first bottleneck is provider throughput; each worker also holds one database connection during a model
call for its session lock. Operators control the number of worker processes and their intervals.
There is no laptop-derived latency/capacity claim. Automatic orchestration, deployment health for the
standalone follower, and entity/report embedding targets remain separate work.

**Alternatives and reversal.** Rebuilding a full replacement generation on every append repeats old
inference and requires index allocation/cutover for unchanged weights. Mutating the identity table
would grant a worker authority to redefine a vector space. Capturing an ever-moving maximum during
one build can prevent completion under sustained ingestion. The chosen finite targets preserve an
observable completed frontier while allowing the active generation to stay useful. A dedicated
watch command makes background inference an explicit operator action; ordinary API startup does not
silently consume inference capacity. Stopping the follower restores finite snapshot operation without
rewriting vectors. Removing the added progress columns is a migration; immutable model policy is intact.

**Evidence.** PostgreSQL tests cover finite pages, late arrivals, model/activation refusal, retries,
concurrent workers, erasure/expiry, empty and non-message ranges, and repair. HTTP tests distinguish
partial from completed coverage and return newly embedded messages under the same generation. CLI
tests use the runtime role, verify policy writes are refused, and exercise recovery, cancellation and
content-free output. The actual configured-provider journey includes post-activation catch-up.

The local provider passes authenticated incremental retrieval with 2560-dimensional vectors. The full
uncached PostgreSQL suite passes at 91.9% cross-package coverage against the unchanged 91.6% floor.
PostgreSQL, HTTP and CLI race checks pass, including allocation rollback after updating the SQL fault
injector to recognize the added target column. These are correctness results, not capacity measurements.

## D108 — Saved message references resolve through digest-bound UTF-8 windows

**Decision.** `POST /v1/messages/get` reads the authoritative message named by a chunk UUID under the
credential's project. It is a read-only operation with the existing credential audit policy and shared
HTTP admission/deadline. It uses the provenance store, with no provider call, fact requirement or active
embedding dependency. A unique index on the authoritative message UUID makes the reference unambiguous
and gives its new reader a direct access path. Losing a derived chunk does not change the source id.

The request supplies `chunk_id`, optional zero-based `byte_start`, `byte_limit` (4–4096, default 4096),
and `expected_digest`. The response names the observation, message ordinal, role and occurrence time,
returns at most the requested UTF-8 bytes, and includes total message bytes, exclusive window end,
SHA-256 content digest and optional next byte offset. `complete` means the window contains the entire
message from byte zero; a missing next offset means it has reached the end. The window ends on a
code-point boundary; a requested start within a code point is refused. These are byte offsets, not
grapheme, character or token counts.

Any nonzero start requires the digest from a previous window or passage preview. A changed source
returns 409 rather than silently assembling pages from different versions. Search results include the
same digest for direct preview continuation. Digest verification and the byte window come from one
PostgreSQL statement snapshot. Source messages remain limited to the 64-KiB ingestion contract even
if privileged direct storage writes bypass ingestion validation; oversized storage refuses the read
before returning content. SQL bounds the returned slice, so the API never loads an unbounded message.

**Boundary and limits.** Authorization is project-wide, not a data-subject grant. Foreign, erased and
missing source references all return the same 404. Retention and erasure remove the authoritative
source; subsequent reads cannot resolve it. A response already read or delivered cannot be recalled
by a later deletion. Audit records the credential, operation, project, outcome and count, never the
source UUID, digest, text or subject. Audit remains best-effort as defined for other application reads.
A digest binds content consistency; it is neither a signature nor an authorization credential.

**Alternatives and reversal.** Repeating semantic search to resolve a saved reference makes identity
lookup depend on model availability and ranking. Returning an unbounded full message would lose the
read-path bound. An unversioned byte offset could silently combine different content after privileged
source repair. UTF-8 code-point boundaries preserve exact reconstructability; token offsets would
couple source identity to a caller's tokenizer. The index adds write/storage cost for a real reader;
its fixture establishes index eligibility, not a latency or corpus-scaling claim. Removing the route
and its dedicated index reverses the addition without rewriting source text; existing source UUIDs
and projection recovery remain unchanged. General document segmentation remains #91.

**Verification.** The full uncached PostgreSQL regression suite passes at 91.9% cross-package
coverage against the unchanged 91.6% floor. Targeted HTTP/PostgreSQL race checks pass, including
source windows, source-reference identity, authorization and audit. The configured local-provider
passage regression passes at 2560 dimensions. Vet, contract and license checks pass. These establish
correctness and model compatibility, not production latency or scaling.

## D109 — GPU qualification owns provisioning, models and teardown

**Decision.** A GPU qualification run is a disposable deployment lifecycle: create an explicitly
selected eight-A100 80-GB node, deploy the application and its vLLM generation/embedding services
with Docker Compose, run both deployed journeys and repository tests, retain sanitized evidence,
remove the run's data and resources, terminate the instance, and verify termination. A successful
test without verified teardown is an incomplete run. A failed test still requires teardown.

The operator-local controller retains the create operation and instance identity, uses a unique
run name to reconcile ambiguous responses, caps the offered hourly price and requests a provider-side
runtime limit. The provider key and SSH private key stay local; the remote stack receives fresh
application/database credentials. Only the run's registered SSH key and owned instance are removed.
A remote wipe watchdog is scheduled before the provider deadline. The provider runtime limit covers
controller loss; it does not replace normal cleanup.

The Compose override pins the vLLM version and both model revisions, captures the resolved image
digest, and keeps model endpoints and PostgreSQL on the internal network. Native vLLM TLS uses
a temporary CA trusted explicitly by the clients; plaintext admission is not widened. The CA signing
key is removed after issuance, and TLS secrets are excluded from build contexts and Git. Integration
fixtures use a separate PostgreSQL cluster because they alter cluster-wide roles. Generation uses four
A100s with tensor parallelism and embeddings use one. This avoids the small KV-cache margin of
placing the full BF16 generation weights on one 80-GB card. The remaining cards are unused in this
functional run; eight rented cards do not imply eight-card utilization or a scaling result.

**Alternatives and limits.** Models hosted on a developer laptop would leave the deployment path
untested. Copying a developer's complete environment would disclose unrelated credentials. Leaving
cleanup to a later manual step could continue billing after a failed test. Removing containers,
volumes and files is a logical wipe; it cannot establish forensic overwrite of provider storage.
This profile establishes functional compatibility, not agreed latency, throughput, availability or
recovery targets. Removing the qualification override and scripts reverses this test infrastructure
without changing the production memory schema or deployment defaults. Tracking: #138.

**Verification.** The disposable eight-A100 run passed the deployed API/worker observation,
formation, recall and erasure journey, all seven runner gates, and 92.0% cross-package coverage
against the unchanged 91.6% floor. Initial qualification configuration failures were corrected and
retained as evidence. See `docs/23-gpu-qualification.md` for model identities and lifecycle evidence.
The remote logical wipe returned zero and provider termination was verified before closing the run.
The ephemeral SSH key was removed; the account-level GPU credential remains only in the operator environment.

## D110 — Entity similarity proposes identities and never resolves them

**Decision.** Entity embeddings are a separate model space over bounded canonical names, retained
aliases and verbatim fact evidence. They have their own input version and immutable
model/revision/endpoint/dimension identity. Equal model settings do not make message and entity
vectors interchangeable. Candidate search returns stable entity IDs, bounded name previews and
similarity scores; it cannot merge, rename or create an entity. Resolution policy remains a measured
caller decision because a false merge has a different governance cost from a missed candidate.

Each generation materializes a finite set of named entity IDs under the project row lock. A named
entity created after that point makes every retained generation stale, including an active one, and
search refuses before sending the question to the provider. Repair can reconstruct lost vectors only
when the target snapshot remains complete. A stale generation requires a new generation, which can be
activated atomically before the old generation is cancelled and pruned. At most two nondiscarded
generations and one million targets are retained per project.

Every aggregate vector registers every contributing observation: entity creation/name receipts and
the observations behind its verbatim fact evidence, up to 4096 distinct sources. Removing any source
deletes the aggregate and makes a ready build incomplete. A bounded repair reconstructs the surface
from retained data. Model calls run outside database transactions; publication rechecks input and
source digests under source-row and per-entity locks, so a result calculated from changed or erased
evidence cannot commit. Search is project-scoped, exact by default, optionally uses a bounded HNSW
candidate set, and writes only content-free credential audit.

**Alternatives and limits.** Reusing message vectors would conflate two input contracts. Embedding an
LLM-written entity summary would introduce claims that are harder to attribute and erase. Incrementally
adding targets to a completed generation would make its coverage and model input frontier ambiguous;
explicit replacement keeps both observable. These candidates do not establish a safe automatic merge
threshold, semantic quality, filtered ANN recall or production capacity. Community-report embeddings
remain a distinct third surface. Tracking: #139, #10, #11 and #19.

**Verification.** PostgreSQL tests cover exact and optional ANN ordering, distinct message/entity
identity, shared-source ownership, erasure, retention, export, repair, replacement after a new entity,
changed input during inference, invalid provider output, activation, cancellation, pruning and retained
generation capacity. HTTP tests cover project authorization, read-only credentials, bounded input,
provider failure and content-free audit. CLI tests cover the generation lifecycle and search contract.
The full PostgreSQL-backed repository suite passes at 91.8% cross-package coverage against the
unchanged 91.6% floor; focused PostgreSQL, HTTP and CLI race checks pass. These establish functional
and concurrency correctness. The configured local provider persists and retrieves entity candidates
at its actual 2560 dimensions. These results do not establish semantic quality, latency, throughput
or production capacity.

## D111 — Terminal graphics are a view over stable command results

**Decision.** Interactive help, operator health, project rebuild status and command failures use a
compact Taisce terminal vocabulary: amber identity, blue navigation, health-only green/red, a memory
path motif and bordered operational panels. The layout collapses by width and has a Unicode-aware
ASCII fallback. Long project rebuild and fact recovery operations use a ten-Hz indeterminate spinner
with elapsed time on stderr because their total work may not yet be known. Final measured progress is
derived only from the durable result.

Redirected stdout and `TAISCE_OUTPUT=json` preserve the existing JSON contracts exactly. ANSI color is
limited to an interactive terminal and respects `NO_COLOR`; `TERM=dumb` selects the non-decorative
path. Dynamic values have control characters removed and are clipped by terminal display width.
Decorative status contains only already-authorized aggregate or operation metadata. It never renders
credentials, source memory, provider replies or raw provider/database errors.

**Alternatives and limits.** A full-screen TUI would add state, resize and terminal-restoration risk to
one-shot operator commands. Replacing JSON with a human table would break scripts. The chosen renderer
is dependency-free and keeps presentation at the process edge. Aggregate health cannot show project
names, and configured inference is not a provider-health assertion. Details and controls are in
`docs/24-cli-presentation.md`. Tracking: #126.

**Verification.** Focused output tests cover wide/color, narrow/ASCII, healthy/degraded, active,
completed, cancelled and failure rendering; control-sequence removal; wide and combining rune widths;
and explicit fancy/JSON selection. The existing command suite proves buffered output remains JSON.
The full repository suite passes at 91.7% cross-package coverage against the unchanged 91.6% floor;
the CLI package passes under the race detector. Representative 48- and 110-column renders were
reviewed without color, including the ASCII fallback.

## D112 — Community reports occupy the third semantic model space

**Decision.** A community report is embedded from its bounded title, summary, importance reason and
whole findings. Report vectors use their own immutable generation identity, even when their provider,
revision and dimensions equal a message or entity generation. Search returns a stable report and
community ID with bounded title and summary previews. It does not substitute a report for evidence;
the report's existing source registrations remain the route to the underlying observations.

Start captures a finite set of current report IDs under the project lock. Any report insert, update or
delete makes retained report generations stale, and a stale active generation is refused before
provider egress. Provider calls run without a database transaction. Publication locks every source,
then re-reads the report input and source revisions under a per-report lock before accepting a vector.
Every vector is registered to every contributing observation, with a 4096-source cap. Source erasure,
retention, report invalidation and community replacement therefore remove the vector by cascade.
Repair is bounded when its target report still exists; a changed report set requires a replacement
generation. At most two nondiscarded generations and one million captured targets remain per project.

Exact cosine ordering is the default. A caller may opt into a bounded HNSW candidate set; the result
states that it is approximate. The authenticated HTTP route and operator CLI use the configured
embedding provider, enforce the active generation's model identity and record content-free audit.

**Alternatives and limits.** Message vectors cannot represent a theme no single passage states.
Entity vectors represent identities and their verbatim context, not aggregate subjects. Folding all
three into one generation would let an equal model name hide incompatible input contracts. The
stored report is model-written aggregate prose, so its source registrations and revision checks are
part of the vector's ownership rather than optional metadata. The implementation establishes
functional and lifecycle correctness; semantic quality, filtered ANN recall and capacity still need
named-corpus measurement under #19/#101/#103.

**Verification.** PostgreSQL tests cover deterministic bounded input, exact and optional ANN results,
multi-source ownership, export, erasure, retention, repair, finite-snapshot invalidation, model
cutover and stale-model refusal. HTTP tests cover authentication, read-only access, input bounds and
the generated contract. CLI tests cover generation management without printing provider input. The
configured-provider inference profile checks its actual vector width separately from quality claims.

## D113 — Each semantic input wins the question it was designed to answer

**Decision.** Passage, entity and community-report generations remain separate because their
production input shapes retrieve different intents under the configured embedder. The named
`surface-contract/v1` fixture compares every question against all three inputs from one domain. A
source-note question must select the verbatim passage, an alias question must select the bounded
entity surface, and an aggregate question must select the written report. Each assertion explicitly
requires the intended input to outrank both substitutes.

**Alternatives and limits.** Distinct generation hashes prove that inputs cannot be mixed in storage,
but do not prove that the representations behave differently. A synthetic embedder would prove only
test plumbing. The live fixture uses the same message text, `semantic.Surface` and
`report.EmbeddingSurface` builders sent to the configured provider. Three questions are a contract
fixture rather than a quality distribution, and they do not establish an absolute relevance cutoff,
filtered ANN recall, latency or capacity. Those require the named corpus and controlled host tracked
in #19/#101/#103.

**Verification.** With `qwen3-embedding:4b-q8_0` at 2560 dimensions, the source-note question scores
passage 0.8180, entity 0.6017 and report 0.6662; the alias question scores 0.7119, 0.7941 and 0.6680;
the aggregate question scores 0.5210, 0.5228 and 0.6154. All three intended surfaces win, and either
substitution fails its fixture.

## D114 — Report quality and ANN fidelity are measured against exact truth

**Decision.** `report-themes-v1` is the named qualification corpus for the first community-report
retrieval measurement. It contains 1,152 reports: 24 themes with 48 independently identified
variants per theme. Each theme has one natural-language question. Exact topic recall at rank 1
measures whether the intended report theme wins. Filtered HNSW recall at rank 10 measures overlap
with the exact cosine top ten at explicit candidate bounds 32, 64 and 128. Query vectors, generation
identity, project filtering and stored vectors use the production provider and PostgreSQL paths.

The run fixes the corpus, model revision, vector width, cache state, page size, concurrency and
warm-up count before provisioning. It records observed build time and p50/p95/p99 latency without
inventing a target after seeing the result. Semantic topic recall and ANN fidelity remain separate
metrics: a faithful approximate index cannot rescue a semantically poor representation.

**Alternatives and limits.** Synthetic vectors test index plumbing but cannot establish semantic
retrieval. Comparing HNSW to theme labels alone can hide divergence from exact ordering, so every
ANN result is compared to the exact top ten. This first corpus is balanced English aggregate prose,
uses one project and one sequential client, and fits in memory. It does not establish multilingual
quality, concurrent throughput, saturation, large-corpus scaling or a production service objective.
Those remain controlled workload work under #101/#103.

**Verification.** On a fresh eight-A100 GPU.ai VM, pinned
`Qwen/Qwen3-Embedding-4B` revision `5cf2132abc99cad020ac570b19d031efec650f2b` produced 2,560
dimensional vectors. Exact topic recall@1 was 1.0 (24/24), and ANN recall@10 versus exact was 1.0 at
all three candidate bounds. Building and activating 1,152 vectors took 13.325 seconds. Exact warm
sequential latency was 45.492/47.234/52.153 ms p50/p95/p99; ANN latency ranged from 95.857 to
102.018 ms across the measured bounds. All eight regression/provider/race gates passed at the
unchanged 91.6% coverage floor. The remote logical wipe returned zero, provider termination was
verified, and a fresh paginated inventory contained zero active instances. Full evidence and method
are in `docs/26-report-quality-qualification.md`.

## D115 — Immutable embedding generations are the physical vector boundary

**Decision.** Keep message, entity and report vectors list-partitioned by immutable embedding
generation. Project remains the logical authorization and query boundary. Every generation child owns
one directly-created, dimension-specific HNSW index; the partitioned parent owns no HNSW index.
Exact cosine remains the default search. Approximate search is an explicit request and its candidate
bound must be qualified against exact truth for the intended corpus.

The generation boundary binds model name, immutable revision, endpoint identity, input contract and
dimension to a finite corpus. A model or dimension change creates, builds and validates a replacement
generation, activates it atomically, then cancels and prunes the old generation. A mismatched retry
or provider reply fails before partial publication. Detaching and dropping the old child provides a
bounded retirement path without filtered deletion and vacuum.

**Measurement.** `vector-layout-v1` compared ordinary shared storage, the current generation-list
layout and 32-way project hash storage across 128 projects and 13,824 2,560-dimensional vectors. The
ordinary filtered query selected no HNSW index and its representative execution took 402.074 ms; the
generation layout pruned to and selected its child HNSW in 6.063 ms. At candidate bounds 32/64,
generation recall@10 versus exact was 0.8458; 128 restored 1.0 with a 343.477 ms p95 tail. The hash
alternative reached only 0.7958 at the lower bounds and did not improve the model/dimension lifecycle.

Generation retirement took 0.126 seconds, wrote 8 KB of WAL and required no vacuum. Ordinary and
hash deletion required 113.977 and 31.536 seconds of vacuum respectively. Generation-list storage
was 296.3 MB versus 287.0 MB ordinary. At 1,000 generations, catalog storage was 32.8 MB, cumulative
provisioning took 43.362 seconds and p95 planning was 4.089 ms with one planned leaf. The complete
method and limits are in `docs/27-vector-layout-qualification.md`. Tracking: #101 and #98.

**Alternatives and limits.** A parent index is virtual and does not create one global physical graph;
D71 corrected that earlier explanation. The measured ordinary/shared index still failed to serve the
filtered query shape. Project hash partitions added a second physical boundary without improving
recall or lifecycle. Tiny projects showed no reason to request ANN, and no project-specific vector
partition is created. Their generation child retains the common HNSW lifecycle: adding a size-triggered
index state would add publication races and a second search contract without a measured operational
benefit at the qualified catalog size. Revisit only if #103 identifies that cost as material.

This supersedes D7's historical vector-layout conclusion. It does not change D69's single-tenant
namespace or the project-partitioned, non-vector `chunk` table. The measurement is one controlled
distribution on one host; it does not establish a production corpus ceiling, throughput objective or
universal ANN candidate bound.

## D116 — GPU qualification scales through independent generation replicas

**Decision.** The eight-A100 qualification profile assigns GPUs 0–6 to seven independent
`Qwen/Qwen3.8-27B` BF16 vLLM replicas and GPU 7 to `Qwen/Qwen3-Embedding-4B`. A pinned nginx service
verifies backend TLS and distributes requests by least active connections. Seven Taisce workers match
the seven generation replicas. Model traffic stays inside Compose and the existing credential,
revision, image-digest, watchdog, wipe and verified-termination controls remain mandatory.

This replaces D109's original four-generation-GPU allocation. Seven-way model parallelism is not a
valid fit for the selected models: the earlier Qwen3.6 pipeline layout failed schema-constrained
requests, and Qwen3.8-Flash-Next's n-gram embedding layer does not support the needed pipeline layout.
Independent 27B replicas use each generation card through supported tensor-parallel size one and
increase concurrent request capacity without coupling all requests to one seven-stage pipeline.

**Verification and limits.** A fresh-node run at `5c9edec` returned 140/140 valid structured
responses at concurrency 28 in 5.342 seconds: 26.205 requests/s, 655.137 completion tokens/s and
1.025/1.212/1.222 seconds p50/p95/p99. Every generation GPU reached 100% observed utilization and
held about 75.7 GiB; per-replica logs proved traffic reached all seven. The deployed memory journey,
all nine runner gates, logical wipe and provider termination passed. This is a controlled benchmark,
not a production service objective. Full evidence is in `docs/27-vector-layout-qualification.md`.
Tracking: #140.

## D117 — The qualification topology is sized by the cards the node exposes

**Decision.** `compose.gpu.yaml` remains the eight-card reference D116 records. The runner counts the
A100s the node actually exposes and generates a third compose file and load-balancer configuration
from that count: N−1 generation replicas on cards 0 to N−2, the embedding model on card N−1, and only
those services started. Eight cards reproduce the reference exactly; fewer narrow it; fewer than two
is refused. The generated files are ignored like the TLS material and never committed. Coverage mode
runs several workers per replica and deals its corpus across that many projects, because formation
drains one project per worker while a replica batches many sequences; qualification mode keeps one
worker per replica, the arrangement D116 measured.

**Why.** On 2026-09-10 the provider's eight-A100 pool delivered nothing usable in eight attempts:
two nodes errored during boot, three stalled in starting past twelve minutes, two booted with seven
cards visible, one was aborted; its four-card VM of the same class was available throughout. A
topology asserted at eight cards made the run impossible on hardware that could have run it.

**Rejected.** Editing the reference overlay per node, which would leave no committed record of the
measured topology. Templating the overlay itself, which would make D116's record a rendering rather
than a file. Accepting a seven-card node by dropping the embedding model, which would silently change
what the run measures.

**How hard to undo.** No storage change. Deleting the generator returns the runner to asserting eight
cards. Tracking: #151.

## D118 — The formation turn budget is an operator setting, and the model client is bounded by it

**Decision.** `TAISCE_FORMATION_TURN_BUDGET` bounds one formation attempt, defaulting to the five
minutes the default policy already chose, and refuses any value that is not a positive duration. The extraction
client's HTTP timeout follows the budget instead of carrying its own two-minute constant, so the
deadline that fires is the one the operator configured and the one the failure reports. The explicit
single-source rebuild uses the same setting.

**Why.** Measured on the AP news corpus with twelve formations sharing one tensor-parallel server:
eleven of 1,397 articles were cut off by the client bound at least once, two were parked after six
attempts, and the affected articles averaged 10.7 KB against 4.5 KB for the rest. A conversational
turn never approaches the bound; a document-sized turn under constrained decoding does, and until
documents are segmented on the way in (#91) this is the only knob.

**Rejected.** Raising the constants, which fixes one corpus and hides the next. Retrying past the
attempt limit, which would hold a project's lock for a working day on a poisonous turn. Splitting the
document in the harness, which would make two observations of one source and corrupt citation offsets.

**How hard to undo.** No storage change; removing the setting restores the constants. What cannot be
undone by configuration: raising the budget changes the extractor implementation digest's file only
if the extractor changes, and a change to that file invalidates D96's source pins for any source with
an earlier attempt, which the coverage run demonstrated and recovered from with explicit rebuilds.
Tracking: #152.

## D119 — A coverage corpus is observed without a data subject, and the reason is a finding

**Decision.** The vocabulary-coverage harness observes each document as one tool-role message with a
deterministic retry key and no data subject, deals the corpus across one project per worker, and reads
admitted facts against every stored refusal reason from the database, with unmapped refusals broken
down by the relation the model tried to use. The unmapped count is complete whatever else happens,
because the vocabulary check is the first admission check in the extractor.

**Why no subject.** The fact store refuses every claim from a non-user message whenever the observation
names a data subject, not only claims about that subject. Observed under a synthetic subject, 535
articles formed zero facts and every extractor-admitted claim died at that gate uncounted, so the
denominator was short. Without a subject, tool-role facts are admitted and the denominator is facts
plus stored refusals. That the rule is wider than its own comment, and that role refusals are counted
but never stored, are findings filed as #153; whether documents belong to a subject at all is that
issue's decision, not this one's.

**Rejected.** Observing documents as user messages, which would make a news agency's words the
subject's own statements. Counting role refusals from worker logs, which are per-pass aggregates.
Deriving the denominator from a sample rebuild, which is one article's ratio presented as a corpus's.

**How hard to undo.** The harness flag is one environment variable. The measurement itself is
re-runnable from the committed harness against any corpus the operator licenses. Tracking: #43.

## D120 — The vocabulary does not grow to admit a news corpus's unmapped tail

**Decision.** No relation is added to the vocabulary from the AP news measurement (#43). Measured
over 537 formed articles at `5b3775b` with the qualified extractor: 7,482 proposals, 430 unmapped, a
5.75% unmapped rate; 228 distinct unmapped predicates of which 166 appear once. That is the shape #43
itself named as a vocabulary that is right with a tail that is noise. The head of the tail is
attribution — `reported`, `denied`, `said`, `announced`, `told`, `says`, together about a fifth of the
unmapped claims — and attribution is not a relation between two entities: the quote already carries
who said it, and a fact whose object is a sentence has no cardinality, no supersession and no edge to
follow. Event verbs in the tail (`died`, `killed`, `filed`, `sued`, `lost`) are #154's decision about
event relations, not a vocabulary gap to fill by name.

**Why the measurement is trusted for this.** The vocabulary check is the first admission check in
the extractor, so the unmapped count is complete over every proposal regardless of what later gates
refuse; and the second pass observed without a data subject, so the denominator is facts plus every
stored refusal (D119). Thirty-seven of the thirty-nine relations were used; `has_timezone` and
`learning` were not, which says what news does not talk about rather than anything about the set.

**Rejected.** Adding `said`/`reported` as relations, which turns every quoted sentence into a fact
about its speaker with the sentence as object and makes the graph a passage store with extra steps.
Adding per-corpus predicates, which reopens D15's closed set for the one property the closure buys:
that a relation is queryable, cardinality-decidable and supersession-capable because a foreign key
admits it. Treating 5.75% as a target to drive to zero, which would be measured against reporting
verbs the graph should not hold.

**How hard to undo.** A migration adds a predicate; nothing on the read path changes. The number
that would reopen this is a corpus where the unmapped head is a genuine entity relation asserted
often, and the harness re-runs on any corpus the operator licenses.

## D121 — A second current value in one message is a refusal, not a failed turn

**Decision.** When a message gives a single-cardinality relation two current values, the exclusion
constraint refuses the second insert and the store names that refusal (`ErrConflictingValue`), keeping
the database error in the chain. Formation records the second claim as a `rejected_claim` with the
closed reason `conflicting_value`; the first value in message order wins; the turn completes and its
watermark moves. The rebuild path asserts each claim under a savepoint so one refused insert cannot
abort a generation. Migration 0052 widens the reason set to seven. The authored-assertion route
already maps the same violation to a 409 and keeps doing so, because a batch is atomic by D91.

**Why.** Supersession requires a strictly later validity start (D86); two claims from one message
share one, so neither can close the other. Before this, the raw constraint error was treated as a
model failure: six retries of the same content, a parked turn, nothing stored, and an operator left
with an error naming a constraint rather than a claim. Measured on the AP news corpus, about five
articles in a hundred parked this way (#155). A claim the message genuinely made and cannot store
beside its rival is the same class of outcome as a duplicate or a paraphrase.

**Rejected.** Letting the second value supersede the first at the same instant, which would make
message order a temporal fact and invent an interval of zero length. Storing both and letting recall
choose, which is what the constraint exists to forbid (G4). Dropping the second silently, which loses
the count that says how often a corpus does this.

**How hard to undo.** A migration narrows the reason set again; rows already written with the reason
would have to be deleted first. The store mapping is one clause.

## D122 — The role gate is on the speaker reference, not on the role alone

**Decision.** A non-user message observed under a data subject may assert facts about named third
parties, and they are stored under that message's own role and registered under the observation's
subject. What such a message may never write is a claim that speaks for the principal: a claim whose
subject or object is a speaker term is refused as `not_spoken_by_principal` unless the message's
stored role is `user`, and that refusal is now a `rejected_claim` row beside the others as well as a
count. Migration 0053 widens the closed reason set to eight.

**What is protected, and from whom.** The asset is the principal's own statements: facts recall
returns by default and erasure walks by subject. The actor is anything that is not the principal
speaking, which includes the model's own turn and any fetched text. The boundary is the stored
message role, read from the observation, never the caller's argument. The worst path was and remains
a model or a document putting words in the person's mouth; that path is unchanged, and the test that
an assistant's inference cannot become the user's fact still holds.

**What changes, and what covers it.** A fetched document's claims about others become tool-role
facts inside a subject's observation. They are excluded from recall unless the caller selects the
role (D82), they are labelled at every read with the role that said them, and the subject's erasure
reaches them because registration carries the subject. A planted instruction in a document can
therefore become a labelled tool-role fact about a third party, which is exactly what it could
already become in an observation with no subject, and which the extractor's own refusal of
instructing text is measured against in the inference corpus.

**Why.** Measured on a news corpus observed under a subject, the wide rule refused every claim from
every tool-role message, 535 articles to zero facts, and the refusals were counted but never stored.
The recall controls already describe fetched text as served under its stored role, which the wide
rule made impossible to reach with a subject attached. D119's reason for observing a coverage corpus
without a subject no longer applies; the harness keeps that default because a benchmark corpus is
nobody's turn, not because the store requires it.

**Rejected.** Keeping the wide rule and giving documents a subject-independent provenance and
erasure path, which duplicates the walk erasure already makes and leaves documents outside a
person's erasure. Admitting first-person claims from tool text when the document is "about" the
subject, which is the forged-attribution path D66 exists to close. Counting role refusals without
storing them, which hides from an operator the largest refusal class a document corpus produces.

**How hard to undo.** The gate is one condition in the speaker binding; restoring the wide rule is
one line. Rows written with the new reason would be deleted before a migration narrowed the set.
Tracking: #153.

## D123 — An event is reported in the past tense and stays true

**Decision.** The vocabulary marks five relations as events: `created`, `contributed_to`,
`participated_in`, `occurred_on` and `born_in`. The extractor admits a past-tense claim on an event
relation and still refuses past tense on every state relation, and still refuses future tense on
both. The flag is a column on the predicate table (migration 0054), read by the loader, carried on
the ontology, and not rendered into the prompt, so the composed prompt and its golden are unchanged
and the corpus rerun rule 14 requires is not triggered. An admitted event's validity starts when it
was reported; when the message gives the event's own time, `occurred_on` carries it.

**Why.** The tense rule's argument is right for a state: "I used to live in Amman" asserts a
relation that ended, and a fact written from it answers "where do they live" with a place they left.
An event is not a state. "Voters adopted an amendment" is complete and remains true, there is no
interval to bound and nothing that stopped holding, and the past tense is the only tense an event is
ever reported in. Measured on a news corpus, `not_current` refusals outnumbered every other reason
six to one, and the samples were completed events (#154).

**Rejected.** Changing the prompt's definition of `past`, which would make the model decide per
sentence whether an occurrence is over or ended, the judgement the closed vocabulary exists to take
away from it; and any prompt change costs a corpus rerun before it can be trusted. A separate
`event` tense value, which asks the model to classify the relation rather than the sentence, and
which an ordinary past-tense reply would then miss. Admitting past tense for everything, which is the
false-current-fact the rule exists to refuse.

**How hard to undo.** One column with a default; clearing the flag restores the refusal. Facts
admitted as events would remain, correctly stamped with their tense-independent validity.

## D124 — Recall composes exact anchors, semantic anchors, the report hierarchy and passages under one budget

**Decision.** `POST /v1/recalls` consults four surfaces in a fixed order, each charged to the same
character and row budget D82 defines. Exact anchoring on canonical names and aliases runs first, as
before. When it finds nothing, the entity-candidate retriever is asked once for the whole question
and up to four candidates become anchors, marked `semantic:<similarity>` so a caller sees a meaning
match was used; a candidate never merges identities (D110). Facts are expanded over the union as
before. When nothing anchored, or the caller sets `themes`, the report-candidate retriever returns up
to three community reports, and the best match's children are read from the stored hierarchy, depth
one, up to five, with no model call; each is returned in `reports` with its title, summary and the
observations it was registered against. When the budget is not spent and the passage capability is
on, up to eight source messages nearest the question are returned in `passages` with their role,
identity, offsets and a bounded quote. A passage is evidence and is never a fact. A new `surfaces`
control selects a subset of `facts`, `reports`, `passages`; nil is all three; an empty list, a repeat
or an unknown name is refused. `controls` echoes what applied.

**What does not change.** The exact-anchor path's queries, plans and rows when the embedding
revision is unset: a deployment without semantic surfaces answers exactly as before and says nothing
about surfaces unless the caller selected them by name, in which case each surface that did not run
is named in `degraded`. A surface that refuses at call time is named the same way and the rest of the
bundle answers. The source-role default, the project boundary every retriever already enforces
through the credential's scope, and the content-free audit row are unchanged. The only model work on
the read path is the question embedding D106 already opted into; there is no generation call.

**Why.** G5 promises that a question with no anchor is answered from themes the graph already holds
and G2 that an entity is reachable by meaning. Both surfaces existed as separate routes, so both
promises held only for a caller who assembled the answer, which G8 forbids putting in an adapter. The
LazyGraphRAG reading (#148) found every surface a single query-time search reaches present here as a
stored projection and missing only the one read that composes them.

**Rejected.** A model-driven query-time search loop, which puts a budgeted number of model calls
into every agent turn and produces claims that cannot be counted after a delete (#148). Composition
in adapters, which forks per language (G8). One fused index over the three inputs, which loses that
each input shape wins its own question (D113). Ranking across surfaces, which nothing here has
measured; the order is fixed and stated, and the cut is reported as a cut.

**How hard to undo.** No storage change. Removing the semantic wiring returns the exact path; the
two response arrays would then be empty and `surfaces` refused. Tracking: #149.

## D125 — A document is ingested as segments, each an observation under the public write contract

**Date:** 2026-09-10. **Tracking:** [#91](https://github.com/althunibat/taisce/issues/91).

**Decision.** `taisce ingest` sends a document through `POST /v1/observations` as one observation per
segment, one message each, `group_ordinal` 0, under the role the operator names (`tool` by default,
`user` for the person's own writing), the data subject if one is named, and the time the document says
it happened. Segments are at most 8 KiB by default (bounds 256 bytes to the message ceiling), cut at a
paragraph, else a sentence, else whitespace, else a code point, and concatenate to the text exactly.
A segment's idempotency key is a version-5 UUID over the document digest, ordinal, role and subject.
The command reads `.txt`, `.md` and one JSON shape (`title`, `text`, `occurred_at`); it refuses a file
whole, before sending, when it is over the size ceiling, not UTF-8, carries NUL, is empty, is another
format, or is malformed JSON. A manifest per document records digests, offsets and receipts, never
text. The token is read from the environment only. Details in `docs/32-document-ingestion.md`.

**Why.** Formation extracts one message per model call and parks a turn whole, so a message is the
unit of cost, retry and citation; a document cut into observations is formed, retried and cited piece
by piece, and a segment's offset maps a citation back to the file exactly. Going through the public
contract means every rule about roles, subjects, sizes, admission and audit applies unchanged and no
citation exists that a client could not have made. Keys derived from content make a rerun a replay
without a resume file to lose or trust. The 8 KiB ceiling is the measured margin under the articles
that overran a two-minute budget on the docs/31 corpus.

**Rejected.** One long message per document: one model call carrying the whole cost, parked whole,
with the turn budget as the only bound. A document column on the observation: nothing reads it; the
manifest holds the identity on the operator's side and the key derives from the content. A private
ingestion path into the store: no credential, no audit, no admission. Converters for PDF, Office or
HTML: dependencies whose failures become this system's. A token flag: visible in the process list.
Token-based segmentation: the count depends on the model's tokenizer, which the command does not have.

**Impact.** Security: the boundary is the existing one; the command holds a credential like any client
and the manifest carries no text. Cost: model calls per document equal segment count and are printed.
Operations: a project-wide document is erasable by nothing but retention, recorded as a limit and as
[#170](https://github.com/althunibat/taisce/issues/170).

**How hard to undo.** No storage change; the observations are ordinary turns. Removing the command
leaves what it stored as conversation turns under the tool role.

## D126 — Contract version 1 is frozen, and what may still change is enumerated

**Date:** 2026-09-10. **Tracking:** [#36](https://github.com/althunibat/taisce/issues/36).

**Decision.** The surface served under `/v1` is frozen at the snapshot
`internal/api/testdata/contract-v1.json`, written by `make freeze-contract` from the same operation
table and reflection the contract document is rendered from. Every operation in it, with its method,
path and success status, every request and response field with its wire type, and every refusal
code, is served by every later build of version 1. Four changes remain permitted: a response field
may be added; a request field may be added when a request without it is still accepted; a refusal
code may be added; an operation may be added. Anything else, a removal, a rename, a retyping, a
changed route or status, is version 2, served beside version 1 under its own path. Version 1 stops
being served only by a decision recorded here, never by a patch. `TestTheFrozenV1ContractIsStillServed`
fails a build that breaks the freeze; the document test still surfaces every addition for review.
Public copy is governed by `TestPublicCopyCarriesNoSecretAndNoUnmeasuredClaim`: no connection string
with a password, no key-shaped credential, no key variable with a key-shaped value in any README or
document; no unmeasured promise in the READMEs or the goals.

**Why.** An open SDK makes the contract public in practice: any client can enumerate every operation,
field and code, and a change to one is somebody else's outage. The document test made every change
visible; it did not say which changes are forbidden, so a reviewer had to know, and a reviewer who
did not would regenerate the document and ship the break. A freeze held by a test is a boundary the
build enforces; a freeze held by review is a convention. The additions list is the one clients written
against the freeze tolerate: a strict client that refuses unknown response fields is a client that has
chosen its own outage, and the document has said since its first line that a field is only ever added.

**What it does not cover.** Whether a newly added request field is optional in practice: the shape
records its type, and "optional" in it means nullable on the wire, not accepted-when-absent. That
promise is kept by the handler tests that send the earlier request body. Behaviour described in prose
around the operations, which is held by the tests named in the goals and the register, not by the
freeze. The claim guard names phrases; a claim in other words is found by a reader and added to the
list, which only grows.

**Rejected.** Pinning the operation count: a silently widened or narrowed shape passes it. A
hand-written version-1 document: a second statement of the same thing, and the two diverge on the
first field somebody adds without knowing it exists. A deprecation window measured in releases:
nothing here yet has a release cadence a window could be measured in, so the rule is the decision
record rather than a number nobody can defend. Governing public copy by review: the line that is
false six months later was fine when it was reviewed.

**Impact.** Security: no surface change; the public-copy guard is a control over what the next snapshot
carries. Operations: a breaking change costs a version, which is the point.

**How hard to undo.** Deleting the snapshot and its test removes the freeze and nothing else; the
document and its test remain. Reversing a freeze is a decision here.

## D127 — One conformance suite holds every adapter, and the memory message has one shape

**Date:** 2026-09-10. **Tracking:** [#26](https://github.com/althunibat/taisce/issues/26).

**Decision.** The adapter rules G8 states are held by one suite, `conformance/cases.json`, run by every
adapter against a live deployment through `taisce conformance`. An adapter is reached as a driver: a
loop around the adapter that takes one JSON instruction per turn on stdin and answers one JSON report
on stdout, so a driver in any language is a few dozen lines and holds no rule of its own. The runner
seeds facts through the authored-assertion operation under a data subject per case, reads the stored
offset from freshness before and after, and stands a recording proxy between the driver and the
deployment that forwards, records, and for the cases that need it answers `503` to one operation.
The model is stubbed inside the driver and nothing else is. Six cases hold the six rules: one
untrusted `user` memory message carrying the watermark and the plan; recall failure not fatal; write
failure never silent; store only on success; external-only filters by default; no synthetic or
injected message observed. The memory message every adapter injects has one shape: the line
`taisce-memory/v1 untrusted`, then a JSON document with `watermark` (freshness), `plan` (the recall's
effective controls, degraded surfaces and reach) and the recall's `facts`, `reports` and `passages`
unchanged; nothing is injected when the recall returns nothing, and the message is never stored. A
reference adapter ships with the suite and is served as a driver by `--serve-reference`; nine broken
variants of it are shown failing exactly the case that holds their rule.

**Why.** Six adapters in five languages, each with its own green tests, is six chances for the
behaviour to fork quietly; a suite written before any adapter moves holds each to the same rules from
its first commit rather than auditing it afterwards. A fixed memory message is what makes the first
rule checkable: a suite that can count messages but not read them cannot say whether the watermark,
the one number that tells an agent how far behind its memory is, is present in every language. The
proxy is the honest way to make a real store unavailable; a mock of the deployment proves that the
mock matches the assertion. A stubbed model is unavoidable and stated: the suite is about what the
adapter hands the model and stores afterwards.

**Rejected.** A suite per adapter, restated in each language: the fork it exists to prevent. Mocks
of the deployment. A free-form memory message: uncheckable and different per language. Injecting an
empty memory message when the recall returns nothing: tokens spent on nothing, and a message the
model has to learn to ignore. Injecting as `system`: a system message is trusted by construction and
retrieved content is data. A compaction case now: the operation does not exist (#22, #25); the case
joins when it does, and the suite says so.

**What it does not cover.** Compaction, until #22 lands. The adapter's own framework seam and its
filter configuration beyond the default: an adapter that lets an operator opt tool traffic in is
conformant when the default is out. The wording a model sees beyond the prefix line: the document is
JSON and adapters render the same fields.

**Impact.** Security: the memory message is marked untrusted on the wire and by the adapter, and it
is never stored, so retrieved content cannot re-enter memory as a person's words. Operations: an
adapter's release runs the suite against a real deployment; the runner is the released binary.

**How hard to undo.** The suite is a file and a package; the memory message shape is a contract
adapters render, and changing it after the first adapter ships is `taisce-memory/v2` beside v1.
## D128 — The MCP surface is one route on the same server, with four tools and no erasure

**Date:** 2026-09-10. **Tracking:** [#31](https://github.com/althunibat/taisce/issues/31).

**Decision.** `POST /mcp` on the API server speaks the Model Context Protocol over streamable HTTP
without sessions, answering as `application/json`, through the official Go SDK. It takes the
deployment's own bearer credential, checked at the door with the same refusal every route gives a
stranger. Four tools: `recall`, `observe`, `freshness` and `resolve_citation`. A tool call becomes an
ordinary request to the v1 route of the same name, carrying the caller's credential, looped back
through the server's own handler chain, so credential resolution, admission, the ledger row, the
request shape and the refusal codes are the ones every client gets; the tool's result is the v1
response, structured and as text, and a refusal is a tool error carrying the operation's code. The
door checks the credential but takes no admission work slot, because the looped-back request takes
it and a door holding one too would refuse its own tool calls as busy under the per-credential
limit; credential resolution is therefore split from the work slot for every route. Erasure and
export are not tools. The Claude Code plugin under `plugins/claude-code` is the coding-agent surface
over this route: the MCP configuration, three commands, a skill, a session-start hook that reports
the watermark, and a capture hook that is off by default and records only the user's message under
a key derived from the session and the message.

**Why.** A model reads a tool list as a menu, so what is on it is a policy: an agent must not be
able to erase or export a person because a sentence in its context told it to, and forgetting stays
an administrative act with a receipt. Looping back through the routes keeps two doors one
implementation; a second dispatch table would be the fork G8 forbids in adapters, inside the server.
The credential rather than OAuth: a credential here is already bound to one project, revocable and
attributed on every act, and a second identity system would end in a bearer token reaching exactly
what the credential reaches. The plugin lives in this repository because it has no build, no
ecosystem registry and no version of its own: it is configuration over this server's route and
versions with the tool names.

**Rejected.** A separate MCP process: a second deployable with its own credential handling and
audit, for a mapping that is a table. Sessions and event streams: state carried for nothing and a
buffering setting every proxy has to know. Tools that reach the store directly: no admission, no
ledger, no contract. A session-start digest of "recent decisions": recall is anchored on names, and a
digest without a question would be a paraphrase presented as memory; the hook reports the watermark
and points at the tool instead. Capture on by default: a setting that sends every turn to the
deployment is one the person turns on.

**Impact.** Security: the door refuses strangers before the protocol is spoken; every tool call is
audited as its operation under the caller's credential; nothing on the menu deletes. Operations: no
new process, no new port; the route is on the API listener. Cost: one credential resolution at the
door and one in the loop per tool call.

**How hard to undo.** Removing the route and the plugin leaves the server as it was; the credential
split in `authenticated` stays as an ordinary refactor.

## D129 — Every adapter is the same shape on its framework's own seam, and the memory bytes agree

**Date:** 2026-09-10. **Tracking:** [#27](https://github.com/althunibat/taisce/issues/27),
[#28](https://github.com/althunibat/taisce/issues/28), [#29](https://github.com/althunibat/taisce/issues/29).

**Decision.** Each adapter maps the six rules D127 holds onto the one seam its framework offers for
"before the model call, after the answer", and adds nothing of its own. Microsoft Agent Framework in
.NET and Python: the context provider, built through the framework's constructor hooks and its default
filters, with the adapter's response filter keeping only the assistant's final text. LangGraph: agent
middleware around the model call and after the run. Spring AI: an advisor. LangChain4j: a decorator
over the application's `ChatModel`, because LangChain4j reads its chat memory before the new message
is added to it, so a memory cannot inject for the turn in hand and the model is the one seam that
sees the whole request and the whole answer. Every adapter renders the memory message D127 defines and
derives the turn's idempotency key as a version-5 UUID over the subject, the run and the messages, so
a turn stored by two adapters is one observation and a memory message from any parses the same. The
untrusted mark lives where the framework has a slot for it: message metadata or additional properties
where they exist, the user message's attributes and name in LangChain4j. Each adapter ships a
conformance driver, and the run of the suite against a live deployment is the evidence it carries.
Each language is one repository (D44): `taisce-dotnet`, `taisce-python` (three distributions),
`taisce-java` (three modules).

**Why.** An adapter that reimplements its framework's wrapper or filters is the fork G8 forbids;
building on the framework's own hooks keeps the defaults in one place every adapter inherits. One
key derivation across languages is what makes "resumable and duplicate-safe" a property of the
system rather than of each client. The seam choices are the frameworks' facts, found by driving each
with a scripted model: the LangChain4j one was found when a chat-memory adapter passed five cases and
never handed the model a memory message.

**Rejected.** Emulating a missing seam locally (#28): logic in an adapter. A shared client across
languages: each language's HTTP and JSON idioms are its own, and the contract is small. Injecting as a
system message anywhere: both Java frameworks pin a system message first and never evict it.

**Impact.** Security: the memory message is marked untrusted on every framework and never re-enters
memory as a person's words. Operations: an adapter's release runs the suite against a real deployment.

**How hard to undo.** Each adapter is its own repository; replacing one changes nothing here.
## D130 — Compaction is a roll-up of segments the worker writes; a context is assembled, never summarised on demand

**Date:** 2026-09-10. **Tracking:** [#22](https://github.com/althunibat/taisce/issues/22).

**Decision.** A subject's history rolls up on the time axis in segments: the newest eight turns stay
verbatim; behind them every run of eight consecutive formed turns becomes one level-1 segment, a
summary a model wrote from those turns; every run of eight level-1 segments becomes a level-2 segment
written from their summaries, and so on to level six. A segment is a projection, not an observation:
it lives in its own table, is registered to every observation it covers, and is deleted by an erasure
of any of them through the projection registry, after which the pass writes a new one from what
survived; a run bounded by an existing segment closes early, so a gap an erasure left is written
again and not left loose. The compaction pass runs on the formation driver's tick per scope after
draining, bounded by `SegmentsPerPass` model calls, and the subject pass that writes community reports
is scheduled the same way, bounded by `ReportsPerPass`; before this neither ran outside a test (#177).
`POST /v1/contexts` assembles one subject's history under a character budget with no model call: the
newest turns verbatim and the highest-level segment covering everything older, oldest first, cut from
the oldest end with the cut reported, plus the freshness watermark. A tool call and its results are
one group inside one turn (D64), and a segment covers whole turns, so no segment splits a group.
Material over 48,000 characters is cut from the oldest turn before summarisation and the cut is
counted. The summariser is a prompt file compiled in, temperature zero, with the data boundary last,
loaded by the report prompt's loader because the two files answer the same questions.

**Why.** Summarising each turn once per level it climbs is amortised O(1) per turn, and a context
reads O(log n) segments: ten thousand turns cost 1,426 summaries against a linear bound of 1,428 and
a context over them reads ten segments and eight turns, measured by `TestTenThousandTurnsCostLinearlyManySummaries`.
A projection rather than an observation keeps the log to what people said (G6) and gives erasure its
registrations for free. Writing off the read path is the rule the read path already keeps for
anchoring and reports: a model call on the read path is paid by every caller.

**Rejected.** The stateless mode G9 sketched, where an application uploads a message list and gets it
compacted: that is a model call on the read path for material the deployment does not hold, so it
cannot register, cannot erase and cannot re-derive; the on-ramp it was for is `observe`, which makes
the same list this deployment's history and gives it everything above. G9 is amended to say so. A
summary stored as an observation: prose a model wrote entering the log beside what a person said, the
artefact class G6 exists to keep out. Rewriting a segment in place when its range changes: a segment
is written once, and what invalidates it is erasure through the registry, which is the mechanism every
projection already has. Token-based budgets: the tokenizer is the model's, not the server's;
characters are what the server counts everywhere else (D82).

**Impact.** Cost: at most `SegmentsPerPass` plus `ReportsPerPass` model calls per scope per tick, each
over bounded material. Security: a segment holds the subject's words and is registered as such;
nothing on `/v1/contexts` writes. Operations: a deployment that never ran the subject pass starts
writing reports on its next tick.

**How hard to undo.** Dropping the table and the pass returns the deployment to verbatim history;
the operation would then return turns only. The scheduling of the subject pass stays either way.

## D131 — An adapter compacts by replacement with the deployment's context, never by summarising

**Date:** 2026-09-10. **Tracking:** [#25](https://github.com/althunibat/taisce/issues/25), [#179](https://github.com/althunibat/taisce/issues/179).

**Decision.** When a framework's compaction trigger fires, the adapter makes one call, `POST
/v1/contexts` for the data subject under its configured budget, and replaces every message before
the person's current message, except system messages, with one user message marked untrusted that
carries the answer unchanged: the watermark, the assembly's `characters` and `truncated` as the
plan, the segments and the verbatim turns. The adapter writes no summary of its own and decides
nothing about what the context holds. A context that cannot be fetched leaves the history as the
framework held it and reports through the adapter's error callback; the turn is not fatal. The
message carries the memory prefix, so the store path never observes it. The trigger is the
framework's own vocabulary where it has one: a `CompactionTrigger` in the .NET Agent Framework's
`CompactionStrategy`, registered through a `CompactionProvider` on the chat client builder; a
message count in the Python Agent Framework's context provider, which replaces the history other
providers loaded for the run and leaves the stored history to the application (D129); LangGraph's
state rewrite before the model call with the framework's own remove-all message, as its summarisation
middleware does; and a message count in the Spring AI advisor and the LangChain4j decorator, which
rewrite the request the model sees and leave the application's chat memory alone. The MCP surface
gains a `context` tool over the same operation.

The conformance suite holds every adapter to this with two cases. The proxy answers a context the
case states, because a segment is written by the worker's pass from a model's summary and a
conformance run has no model; the adapter's obligation is fidelity to the answer, which a stated
answer is the one way to check, and the deployment's side is proved by the service's own tests. Four
broken variants of the reference adapter, one per rule, each fail the case that holds their rule.

**Why.** The history the model needs after a long conversation is the history the deployment already
holds, rolled up under D130 off the read path. An adapter that summarises locally produces prose in
each language's own way, prose the deployment cannot register, erase or re-derive, and prose that
enters the framework's own history as though somebody said it. Replacement with the deployment's
answer keeps one behaviour across five frameworks and keeps every word the model reads traceable to a
turn or a segment the deployment can erase.

**Rejected.** Summarising in the adapter, for the reasons above. Merging the deployment's segments
with the framework's own recent messages by count: the framework counts messages and the deployment
counts turns, and the two never line up, so a turn falls into the gap between the last segment and
the framework's preserved window or is read twice; the deployment's verbatim turns are the authority
on what was said recently. A live compaction case in the suite: it needs a worker and a summarising
model on the deployment under test, and would prove the deployment rather than the adapter. A role
other than `user` for the context message: the memory rule of D129 applies unchanged, because a
segment is somebody's words condensed and an instruction-bearing role would make them instructions.

**Impact.** Security: what reaches the model after compaction is the deployment's answer at role
`user`, marked untrusted, and nothing an adapter wrote; the observe path recognises it by prefix, so
it is never stored. Operations: where the framework's seam is request-only (Spring AI, LangChain4j),
the context is fetched on every model call while the application's chat memory stays over the
trigger, one read per call until the memory window shrinks; the two Agent Framework SDKs and LangGraph
persist the replacement, so the fetch happens once per trigger. Cost is one read of `/v1/contexts`
per compaction, no model call.

**How hard to undo.** Adapter-side only: one class per adapter and two cases in the suite. Removing
a seam leaves the operation and the rest of the adapter untouched.

## D132 — A Go client is deferred; a Go application integrates over the raw contract

**Date:** 2026-09-10. **Tracking:** [#97](https://github.com/althunibat/taisce/issues/97).

**Decision.** No Go client and no Go agent adapter are built. D40's ordering stands: Python, Java and
.NET are the adapters, and TypeScript is the next candidate when adoption evidence reopens it (#30).
A Go application integrates over the wire contract in `docs/02-contract.md` directly: the `net/http`
calls are the contract, the credential goes in the `Authorization` header from the environment, an
observation is retried with the same `idempotency_key` (D-frozen under D126), a recall's arrays are
handed to the model as one untrusted user message under the memory prefix the conformance suite
defines (D127), and a read failure is swallowed while a write failure is surfaced. The one Go
implementation of that pattern that exists is the reference adapter in `internal/conformance`, which
is a test fixture and is not published as a library; a Go application that wants a typed client may
copy the shape, not the package.

**Revisit criterion.** A Go agent framework with a memory or context-provider seam that a developer
can name, or an adopter asking for Go with an application behind the ask. Either reopens this as a
`decision` issue; the conformance suite then holds a Go adapter to the same eight cases before it is
called one.

**Why.** An adapter is the product where a framework has a seam (D40). Go has agent libraries but no
first-party memory seam with an incumbent to displace, so a Go client would be a typed HTTP wrapper
with no framework mapping to carry, and every wrapper is a second place the contract lives. The
service itself is Go, which makes a client look cheap; it is not cheap to keep, because it is released
on its own cadence (D44) and held by the conformance suite like the others.

**Rejected.** Publishing `internal/conformance`'s reference as the Go client: it exists to break the
rules on purpose and its knobs would ship with it. A Go module inside the service repository: D44
puts every adapter in its own repository, and a client that moves with the server's version pair is
the drift the suite exists to catch. Building it now because the language is at hand: the second use
case is what reveals an adapter's shape, and there is no first.

**Impact.** Operations: none; nothing is shipped. Cost: a Go application carries its own HTTP calls,
which is the documented pattern rather than an omission.

**How hard to undo.** Easy: a repository and a conformance driver, under the ordering D40 already
allows.

## D133 — Two credential kinds, and a management surface served by a role of its own

**Date:** 2026-09-10. **Tracking:** [#181](https://github.com/althunibat/taisce/issues/181).

**Decision.** A credential is one of two kinds, held as a column in the registry and never as a
claim the token makes. A *project* credential opens one project's memory routes and nothing an
operator does. An *operator* credential opens the management surface, `/manage/v1`, and no
project's memory; it names no project, and the registry's constraint makes "names no project" and
"is an operator credential" the same statement, so a row cannot be both or neither. Each door
resolves only its own kind and refuses the other with the one answer a stranger gets. The
management surface is served by the `manage` role of the same binary, on its own listener, with the
administrative connection, and is not served at all unless that role is chosen. It performs what
the CLI did over a database connection: projects (create, list, suspend, resume), credentials
(issue, list, revoke), refusal counts by reason and predicate, erasure receipts, formation status
and parked turns with unparking, and the ledger's seal and verification. Every operation is on the
ledger with the operator credential as its principal. The CLI speaks the surface when
`TAISCE_MANAGE_API` and `TAISCE_OPERATOR_TOKEN` are set and takes the direct path otherwise; the
direct path is what bootstrap uses before any credential exists, and bootstrap mints the first
operator credential when none is live. The management contract is frozen beside the memory
contract under D126's rule.

**Why.** An operator, and the portal (G14), must not need a database connection: the connection
that can issue a credential can read every one, and a browser holding it is the memory dump a port
away that rule 8 exists to prevent. Creating a project is DDL, which the serving identities
deliberately cannot perform, so the surface that creates projects holds the administrative
connection and is therefore a process of its own rather than a route on the memory server: the
memory server keeps the privileges it has today. Two kinds rather than one credential with two
powers, because a token that opens both doors is one theft away from opening both.

**What is not shown.** Refusal content. A refused claim is somebody's words; the management surface
returns counts, and the words are read under a project credential through the memory routes, where
the read is on that project's ledger. This is the boundary recall already enforces, applied to the
operator.

**Rejected.** A management route on the memory server (it would hand that process the
administrative connection, or leave project creation on the CLI, which is the split this removes).
A single credential kind with an `admin` access level (one token opening both doors). Role
hierarchies beyond operator and project (nothing reads them yet). A separate management binary (one
binary, one version, D42). A metrics exporter beside the database (#77 reads tables).

**Impact.** Security: the memory server's privileges are unchanged; the manage role holds the
administrative connection and serves nothing else, and is off unless chosen. Operations: a
deployment that wants the surface runs one more process; the compose file and the chart arrange it.
Cost: none on the read path.

**How hard to undo.** Moderate: the `kind` column and its constraint are one control migration; the
surface and the CLI's HTTP path are additive, and removing them returns the CLI to the direct path
it still has.

## D135 — The portal is server-rendered pages over the management surface, off by default

**Date:** 2026-09-11. **Tracking:** [#77](https://github.com/althunibat/taisce/issues/77).

**Decision.** The portal is a set of HTML pages rendered by the `manage` role from the management
surface's own stores (D133), served on the same loopback listener only when `TAISCE_PORTAL` is
`on`. It signs an operator in by their operator credential, keeps a random session id in the
process mapped to that token, resolves the token again on every request so a revocation signs the
browser out at the registry, and writes nothing about a session anywhere. Off, its routes do not
exist. Its panels are the surface's operations: projects with the watermark and the gap between
stored and formed, refusal counts by reason and by the relation the model proposed, erasure receipts
with their residuals, parked turns, credentials by name and prefix, and the ledger's verification.
Each panel is on the ledger with the operator as principal. It shows no message content and no
refused claim's words: those are somebody's, and they are read under a credential for the project
they came from, through the memory routes. What the model costs is not recorded anywhere, and the
panel says so rather than reading a number out of a gateway the deployment may not run.

**Why.** G14 wants the operator to see what the instance is doing without reading the database, and
rule 8 wants that surface authenticated and unexposed by default. Rendering on the server from the
same stores keeps one reader of the tables, one ledger of who read them, and no second source of
truth. Being off by default, and existing only inside the manage role, is what makes "not exposed"
a property of the process rather than a firewall rule somebody remembers.

**Rejected.** A single-page application with its own API calls (a second client of the surface to
keep in step, and a browser holding the operator token in script-reachable storage). A metrics
exporter or a dashboard fed by one (every panel would have a number that can disagree with the
table). Showing refusal quotes to an operator session (the operator is not the data subject, and the
boundary recall already enforces is the boundary here). A login that stores the token in the cookie
(a stolen cookie would then be a credential rather than a session).

**Impact.** Security: the portal opens nothing a bearer of the same credential could not read over
the surface, and it reads nothing a project credential can. Operations: one environment variable
turns it on; sessions live in one process and end with it. Cost: none on the memory path.

**How hard to undo.** Easy: templates and one handler file behind a switch; nothing else reads them.

## D134 — Storage scope and search scope map onto the observation and the data subject; there is no third axis

**Date:** 2026-09-10. **Tracking:** [#24](https://github.com/althunibat/taisce/issues/24).

**Decision.** An application writes into one session and recalls across a person's history, and it
does so with what the contract already has. The storage scope is the observation: a turn is stored
under a `data_subject_id` with the run folded into its idempotency key by the adapter, so a session
is a run and a turn belongs to exactly one. The search scope is the data subject: a recall names
`data_subject_id` to read one person's memory across every session they had, or omits it to read
the project. A context (`POST /v1/contexts`) is likewise one subject's history across sessions.
Nothing is added for "this session only": the session's own messages are the framework's history,
which the framework already holds and the compaction seam replaces with the deployment's context
(D131). An application, an agent or a session identifier is not a memory axis here.

**Why.** The two scopes an application needs are "what was said now" and "what this person said
ever", and those are the observation and the subject. A third axis would be a filter on rows at
query time, which is a convention every query has to remember; the subject is a partition of
attribution that erasure, export and recall already enforce in the substrate.

**Rejected.** A per-session recall filter (a stale answer from an earlier session is exactly what
supersession exists to mark, and hiding it by session hides the correction too). An application or
agent axis (two agents of one application reading one person's memory is the point of a memory
service; an agent that must not read what another wrote is a project).

**Impact.** None on the wire: nothing changes. Documentation: the adapters' `RunId` is named as the
session.

**How hard to undo.** Easy: an axis can be added as a field; nothing depends on its absence.
