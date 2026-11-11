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

## Where a decision is

Each decision is a file of its own in [`decisions/`](decisions/), named for its number and nothing
else: D12 is [`decisions/D12.md`](decisions/D12.md). This page holds no entry and never will.

```sh
make decisions        # every decision, with its number and title
```

**To record one**, take the next number, write `decisions/D<n>.md` with the heading
`# D<n> — <what was decided>`, and give it the parts every entry has: **Date.** and the issue,
**Why it was open.**, **Decided.**, one **Rejected:** for each alternative, **Undo cost.** and
**Impact.** The build refuses a file whose number disagrees with its heading, or one that leaves a
part out.

**Why a file each, and why the name is only the number.** When every decision was appended here,
two pull requests that each recorded one changed the same lines and could not both merge without
somebody merging them by hand and waiting for a build. Separate files do not conflict. Two pull
requests that take the same number do — they add the same file — and that is the one collision worth
having, seen before either merges. A title in the file name would read better in a listing and would
let two D40s land side by side ([D43](decisions/D43.md)).
