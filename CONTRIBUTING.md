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

## Signing off

Every commit carries a sign-off:

```
git commit -s
```

which appends `Signed-off-by: Your Name <your@email>`. It is the
[Developer Certificate of Origin](https://developercertificate.org/): a statement that you wrote the
change, or otherwise have the right to submit it under this project's licence.

There is no contributor agreement to sign, deliberately. A CLA exists to let a project owner
relicense — including to a proprietary edition — and this project is Apache-2.0 with no commercial
line, so there is nothing for one to enable and no reason to put a signing step between your first
patch and its review. The reasoning is in [`docs/01-decisions.md`](docs/01-decisions.md), D45.

Ownership: Taisce is developed and maintained by Ensera. Commits before 2026-09 carry an earlier
address for the same author, which is why the history shows two.
