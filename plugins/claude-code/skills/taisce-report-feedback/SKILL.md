---
name: taisce-report-feedback
description: Report that a recalled record looks wrong — stale, misattributed, contradicted by what the user just said — without changing what memory holds. Use when the user says memory has something wrong, or when a recall contradicts the person it is about. Calls the report_feedback tool on the taisce MCP server. This one writes, and it changes no fact.
---

# Say a record looks wrong, and change nothing

`report_feedback` is the tool. It takes the `fact_id` and what looks wrong about it. It **writes**,
and what it writes is the report — **not a change to the fact**.

## The distinction that matters

Memory still holds what it held. Nothing is corrected, superseded or retracted by this call. You are
recording that somebody looking at a record thought it was wrong, so that a person with the
authority to correct it can see a queue of candidates instead of discovering them one at a time.

Say this to the user in those terms. "I have reported it" is accurate; "I have fixed it" is not, and
the difference will matter the next time the same fact comes back in a recall — because it will.

## When to reach for it

- The user says memory has something wrong about them or about a thing.
- A recall contradicts what the user just told you in this session. The live sentence is the better
  evidence, and the old fact is a candidate for correction.
- A fact is attributed to the wrong person or the wrong entity.
- A fact reads as current and the user says it is long out of date.

## What to do as well

Reporting is not the whole answer. If the user's correction is worth keeping, record the correction
itself with `taisce-observe`, in their words — that is what gives the next recall something better to
return. Feedback marks the old record; the new sentence is what replaces it.

## What this is not

Not erasure, not retraction, not correction of a record. Those are acts with receipts, taken over the
REST contract by somebody who decided to take them, with their own credential. An agent must not be
able to change or remove what memory holds because a sentence in its context said so — which is why
this tool exists in the shape it does, and why erasure is not a tool at all.
