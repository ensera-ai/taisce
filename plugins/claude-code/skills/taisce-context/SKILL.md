---
name: taisce-context
description: Assemble one person's conversation history under a character budget, for a handover, a summary of what somebody has said, or when a long history has to be compacted into what a model can read. Calls the context tool on the taisce MCP server. Reads only, and needs a data subject.
---

# One person's history, under a budget

`context` is the tool. It takes a `data_subject_id` and a character budget, and answers with the
newest turns **as they were said**, older ones as the segments written from them, and anything no
segment covers verbatim too.

## When this is the right call

- A handover: what has this person said, in their words, up to a size a model can read.
- Compacting a long history into something that fits, without inventing a summary of your own.
- Answering "what has X told us" where the answer should be the record rather than an inference
  drawn from it.

## When recall is the right call instead

When the question is about *what is true*. `recall` answers from facts with evidence and citations;
`context` hands back history. Asking `context` what was decided means reading a transcript and
deciding for yourself, which is the work `recall` already did with provenance attached.

## Read the two fields that say what you did not get

- **`truncated`** — the budget cut the history. What you have is the newest part, not the whole.
- **`watermark`** — how much of this history is not yet answerable by a question, which is the same
  backlog `taisce-freshness` reports.

A short answer is one of those two. It is never silence: if the history is shorter than you expected
and both fields say nothing was cut or pending, the history is genuinely that short.

## It is not gated on formation

A turn the deployment has not formed yet still comes back verbatim, because the words were the
person's when they were stored. So this is the operation to reach for when something was just said
and the backlog is long — and it is the reason a compaction built on it does not quietly replace real
history with an empty answer.

## Do not write a summary of your own into memory

If you compact a context for a model, that is a thing you did for this turn. It is not a memory. A
summary recorded as though somebody said it is prose the deployment cannot register, erase or
re-derive.
