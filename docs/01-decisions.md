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

---

## Substrate

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

Currently: `vector` (`chunk.embedding` and the per-partition HNSW), `btree_gist` (the traversal
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
