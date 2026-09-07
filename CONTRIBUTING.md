# How work is tracked

**Every piece of work has an issue, and the issue is closed with evidence when the work is done.**
No exceptions, including work that was already finished before anyone thought to open one — that
gets an issue retroactively, because an issue list that only records the work someone remembered to
file is not an account of the project.

## The rules

**An issue before the work.** If something turns out to need doing mid-task, it gets its own issue
rather than being absorbed into whatever was already open. Scope that creeps into an open issue is
scope nobody agreed to.

**Closed with evidence, not with a claim.** A closing comment names the commit and shows the thing
that proves it — the query plan, the test output, the measured number. *"Implemented"* is not a
closing comment. If the proof is a test, the test's name goes in the comment so it can be re-run.

**Push after commit.** An issue closed against an unpushed commit points at nothing.

**What was left out is stated.** If part of the scope moved elsewhere, the closing comment says so
and names the issue it moved to. Silent narrowing is how a milestone finishes with everything closed
and nothing working.

**Findings get issues too.** Something learned that changes a decision — a benchmark, a validation
against another system, a defect found by reading — is work, and it is often the most valuable work.

**Decisions that need a person are labelled `decision`** and say what has to be decided, with the
real cost of each option. They are not closed by implementing one of the options — they are closed
when the decision is written into [`docs/01-decisions.md`](docs/01-decisions.md) with its rejected
alternatives.

**A decision that building forces also goes in the register**, even when no issue asked for it.
If a later reader would otherwise have to re-derive the choice, it was a decision.

## Labels

`goal:G1`…`goal:G11` bind an issue to a goal in [`docs/00-goals.md`](docs/00-goals.md). An issue that
serves no goal is either a missing goal or work that should not be done.

`new` is a new design. `migrate` carries an **idea** from elsewhere. `decision` needs a person before
code. `docs` is documentation.

## What "migrate" means

**Clone the idea. Rewrite the implementation. Review and validate it here.** Never copy anything as
it stands.

A `migrate` issue is not less work than a `new` one — it is the same work with a head start on the
problem statement. What transfers is *what the thing is for*; what does not transfer is its code, its
schema, its constants, or the reasoning that justified it in a system with different constraints. If
an issue can be closed by copying a file, it was labelled wrong.

The check that catches this: **every part must have a reader here.** An extension, a column, a
config key or a dependency that arrived because it was in the original and not because something in
*this* codebase uses it is a cost with no mechanism behind it — the same defect class G13 names for
an entitlement nobody enforces. The substrate image is the worked example: two extensions arrived by
copying and were removed on review because nothing here read them.

**Nothing is brought in at all unless it is explicitly asked for** — not code, not decisions, not
measurements. Somewhere something was tried is not a source of answers here.

## What is public and what is not

This repository is the **service**, and it is private. The adapters and clients are open source and
live elsewhere (#35 settles where).

The line is not organisational, it is architectural: **the adapter translates, the service decides.**
Anything in an open adapter is readable and forkable, so retrieval, ranking, supersession,
communities, compaction and erasure stay server-side — which is the same rule as *"no logic in an
adapter"*, arrived at from the commercial direction instead of the consistency one.

Two things follow for anyone writing an issue here. A change to an operation's shape is a change to a
**public contract** the moment an SDK ships against it. And nothing internal — endpoints, peppers,
topology, a test DSN — belongs in text that will be mirrored into an open repository.

## Milestones

Ordered as **one end-to-end journey, deepened in passes** — never breadth-first layers. A milestone
is done when the journey runs against a real deployment, not when its code exists.

## The contributor agreement

Before a first contribution is merged, you sign a
[Contributor License Agreement](docs/cla/individual.md). If the work is done in the course of
employment, your employer signs the [corporate one](docs/cla/corporate.md) instead and names you on
it — because intellectual property created at work usually belongs to the employer, and an agreement
you were not entitled to sign protects nobody.

**You keep the copyright in everything you write.** The agreement is a licence, not a transfer.

**Why an agreement and not just a sign-off.** Apache-2.0 already licenses your contribution to this
project: section 5 puts it under the same terms automatically, patent grant included. And because
section 2 permits sublicensing, that licence would already allow a commercially licensed edition built
on contributed code — a permissive licence permits that by design, and an agreement is not what makes
it possible.

What the agreement adds is certainty rather than permission: a signed record of who owns what, which
is what a legal review asks for and what standing to act against an infringer requires. Ensera's
stated intention is that Taisce is and stays Apache-2.0, and the agreement does not bind Ensera to
that — so read section 2 before signing, and do not sign if a differently-licensed edition would not
be acceptable to you.

It also carries the representation a sign-off would otherwise make: that you wrote the contribution,
or otherwise have the right to submit it. That is the one gap Apache-2.0 section 5 genuinely leaves
open, because it licenses a contribution only as far as you were able to license it.

The reasoning, and what was rejected, is in [`docs/01-decisions.md`](docs/01-decisions.md), D45.

## What signing records about you

Signing records **your name, the email address and GitHub account you sign with, and the date**. If
your employer signs the corporate agreement, it records the same for the signatory plus the list of
people it covers.

**It is kept for as long as the project distributes your contribution**, because it is the evidence
that you granted the licences in the agreement. That has a consequence worth stating plainly rather
than leaving you to find: **it cannot be deleted on request while your contribution is still in the
project.** Removing the record would mean removing the code it accounts for.

It is used for nothing else. Not analytics, not a mailing list, not shared with anyone but the service
that operates the signing.

This project's own argument is that a system should be able to say what it holds about a person and
prove what it deleted. It would be a poor start to hold something about contributors and not mention
it.

## Ownership

Taisce is developed and maintained by **Baserat Al Mustaqbal**, an establishment registered in Jordan,
trading as **Ensera**.

Commits before 2026-09 carry an earlier address for the same author, which is why the history shows
two.

## Two repositories

**`althunibat/taisce`** is the working history: every commit, with the measurement that produced it,
the alternative it rejected, and the things that turned out to be wrong. Work happens here.

**`ensera-ai/taisce`** is the published repository: one commit per release. Somebody arriving to
evaluate this should read what the software is, not excavate how it came to be — those are different
documents for different readers, and collapsing them serves neither.

The working history is not hidden for being embarrassing. It is where a decision can be traced to the
run that produced it, which is the thing that makes the decision register trustworthy rather than
merely confident.

`scripts/publish.sh` moves one to the other. It runs every check first, and it refuses to write a
commit message for you: the published history is one line per release, and that line is the whole
account a reader gets.
