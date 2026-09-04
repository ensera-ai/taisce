# How work is tracked

**Every piece of work has an issue, and the issue is closed with evidence when the work is done.**
No exceptions, including work that was already finished before anyone thought to open one — that
gets an issue retroactively (see #32–#34), because an issue list that only records the work someone
remembered to file is not an account of the project.

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
`docs/01-validation-against-engram.md` overturned two arguments and found two schema gaps; it was
tracked as #34.

**Decisions that need a person are labelled `decision`** and say what has to be decided, with the
real cost of each option. They are not closed by implementing one of the options — they are closed
when the decision is written into the register with its rejected alternatives.

## Labels

`goal:G1`…`goal:G11` bind an issue to a goal in [`docs/00-goals.md`](docs/00-goals.md). An issue that
serves no goal is either a missing goal or work that should not be done.

`port` is carried over from memra — work, not thinking. `new` is a new design. `decision` needs a
person. `docs` is documentation.

## Milestones

Ordered as **one end-to-end journey, deepened in passes** — never breadth-first layers. A milestone
is done when the journey runs against a real deployment, not when its code exists.
