# Working rules

These are not preferences. Each one exists because ignoring it produced a defect.

---

## 1. Everything is tracked on GitHub

**Every piece of work has an issue, opened before the work starts, and closed when it is done.**
No exceptions. Work that was already finished before anyone thought to file one gets an issue
retroactively — an issue list that records only the work somebody remembered to file is not an
account of the project.

**Close with evidence, not with a claim.** The closing comment names the commit and shows the thing
that proves it: the query plan, the test output, the measured number, the name of the test so it can
be re-run. *"Implemented"* is not a closing comment.

**Push before closing**, or the issue points at nothing.

**Say what was left out.** If part of the scope moved elsewhere, name the issue it moved to. Silent
narrowing is how a milestone finishes with everything closed and nothing working.

**Findings are work.** Something learned that changes a decision — a benchmark, a defect found by
reading, a validation against another system — gets its own issue. It is often the most valuable
work.

**Scope that appears mid-task gets its own issue** rather than being absorbed into whatever was
already open. Scope that creeps into an open issue is scope nobody agreed to.

## 2. Nothing is brought in from anywhere else

**Not code, not decisions, not measurements, not benchmarks — unless it is explicitly asked for.**

Another system is a place something was tried. Its decisions were made under constraints that are
not these constraints, and a decision carried across without being re-derived is one nobody can
defend when it is challenged.

This includes rejections. An option ruled out in a conversation about a different codebase was never
in scope here, so it cannot be "out of scope" here either.

## 3. "Migrate" means clone the idea, not the code

When something **is** asked for: **clone the idea, rewrite the implementation, review and validate it
here.** Never copy anything as it stands.

Migrating is not less work than building — it is the same work with a head start on the problem
statement. What transfers is *what the thing is for*. What does not transfer is its code, its schema,
its constants, or the reasoning that justified it elsewhere.

**The check that catches a copy: every part must have a reader here.** An extension, a column, a
config key or a dependency that arrived because it was in the original — rather than because
something in *this* codebase uses it — is a cost with no mechanism behind it.

## 4. Write as though nothing came before

Documents, issues, commit messages and code comments describe **what this system does and why**,
argued from first principles. No citations to another codebase, no "unlike X", no "this replaces Y".

A reader who wants to know why an index exists should find the argument, not a pointer to somewhere
else. If the reasoning cannot be stated without naming another system, the reasoning is not finished.

## 5. Decisions go in the register

`docs/01-decisions.md` is the authority. If another document appears to leave a choice open, that is
a defect in that document.

**Every entry carries what was rejected.** A decision recorded without its alternatives gets reopened
by whoever has only heard the winning argument — and they are usually right to, because nothing told
them the alternative had been considered.

An entry is written when a `decision`-labelled issue closes — that is what closes it, not
implementing one of its options — **or** when building forces a choice a later reader would otherwise
have to re-derive. If the reasoning would only ever exist in a code comment, it was a decision.

## 6. Proof, not assertion

A claim in a document, an issue or a commit message needs something that runs behind it.

Tests run **against a real deployment**. A mock proves that the mock matches the assertion. A test
that skips when its dependency is absent is a green run that asserted nothing — so the test target
has no skip-when-absent arm.

## 7. Work is ordered as one journey

Milestones are **one end-to-end journey, deepened in passes** — never breadth-first layers. A
milestone is done when the journey runs against a real deployment, not when its code exists.

---

# What this is

**Taisce is a multi-tenant SaaS.** One service, EU-hosted, with the tenant boundary as a schema and
the project boundary as a permission.

**The adapters and clients are open source; the service is not.** So the line is: **the adapter
translates, the service decides.** Retrieval, ranking, supersession, communities, compaction and
erasure are the service. Message filtering, envelope construction and one framework's seam are the
adapter. Anything in an open adapter is readable and forkable, which makes "no logic in an adapter"
the boundary between what is given away and what is sold.

**Adapter focus: Python, Java and .NET.** The adapter is the product; the client underneath is
plumbing sized to serve it.

Start with [`docs/00-goals.md`](docs/00-goals.md) for what must be true and what would prove it, and
[`docs/01-decisions.md`](docs/01-decisions.md) for what is settled and what was rejected. The roadmap
is the issue list.
