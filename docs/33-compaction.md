<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Compaction

Tracking: [#22](https://github.com/althunibat/taisce/issues/22). Decision: D130. Code:
`internal/compaction` (the roll-up and the assembly, pure), `internal/formation/compaction.go` (the
pass), `internal/infra/pg/segmentstore.go` (the store), `internal/infra/inference/summariser.go` and
`prompts/compaction.yaml` (the model), `POST /v1/contexts` (the read).

An agent's history grows without limit and a context window does not. The answer here is a roll-up
on the time axis, written by the worker off the read path, and a read that assembles rather than
summarises.

## The roll-up

A subject's history is their formed, unparked turns in log order. The newest eight stay verbatim.
Behind them, every run of eight consecutive turns becomes one **level-1 segment**: a summary a model
wrote from those turns, stored with the range it covers. Every run of eight level-1 segments becomes
a level-2 segment written from their summaries, and so on to level six. Each turn is summarised once
per level it climbs, so the work is amortised constant per turn and the number of levels logarithmic.
Measured by `TestTenThousandTurnsCostLinearlyManySummaries`: ten thousand turns cost 1,426 summaries
against a linear bound of 1,428, and a context over them reads ten segments and eight turns.

A segment is a **projection**, registered to every observation it covers. Erasing a subject deletes
their segments through the registrations, and the pass then finds the gap and writes a new segment
from what survived; a run bounded by an existing segment closes early, so a gap is rewritten rather
than left loose. A segment is written once and never rewritten in place.

A tool call and its results are one group inside one turn (D64). A segment covers whole turns, so no
segment splits a group; the atomicity G9 asks for needs no rule of its own.

## The pass

`formation.Compaction.Run(scope, limit)` writes at most `limit` segments per scope per call, oldest
gaps first, a subject at a time. It runs on the formation driver's tick after the scope is drained,
bounded by the policy's `SegmentsPerPass` (default 4); the subject pass that writes community reports
runs beside it, bounded by `ReportsPerPass` (default 4). Before this neither pass ran outside a test
([#177](https://github.com/althunibat/taisce/issues/177)).

Material over 48,000 characters is cut from the oldest turn before it is sent, and the cut is counted
in the pass's result; the segment still covers the whole range, because its registrations must. The
summariser is a prompt file compiled in, temperature zero, the data boundary last. A failing model is
counted and does not stop the pass; a segment another replica wrote first is not an error.

## The read

`POST /v1/contexts` with `data_subject_id` and an optional `max_characters` returns:

- `turns`: the newest turns as they were said, oldest first, with every message and its role;
- `segments`: the highest-level segment covering each older turn, oldest first, with its level, its
  range and its summary;
- `watermark`: freshness, so a caller knows what the context cannot yet include;
- `characters` and `truncated`: what it cost and whether it was cut, from the oldest end.

It makes no model call. A history the pass has not reached yet is handed over verbatim and cut, never
summarised on demand. A subject the deployment knows nothing about is an empty context, not an error.
The budget is in characters (D82); a budget above the server's is refused.

## The seams

An adapter compacts by replacement, never by summarising (D131). When the framework's trigger
fires, the adapter asks `POST /v1/contexts` for the subject's history and replaces every message
before the person's current one, except system messages, with one untrusted user message carrying
the context: the watermark, the assembly's cost as the plan, the segments and the verbatim turns
unchanged. It writes no prose of its own. A context that cannot be fetched leaves the history as the
framework held it and reports the failure; the turn goes on. The message carries the memory prefix,
so it never reaches `observe`. The seam is the framework's own where it has one: the two Agent
Framework SDKs' compaction providers, LangGraph's state rewrite before the model call, the Spring AI
advisor and the LangChain4j decorator rewriting the request. The conformance suite holds every
adapter to it with `compaction_hands_the_model_the_deployments_context` and
`context_failure_is_not_fatal`.

## What is not built

A "stateless" mode where an application uploads a message list to be compacted, which G9 once
sketched: it is a model call on the read path for material the deployment cannot register, erase or
re-derive. The on-ramp is `observe`. The adapter seams are above.

Tests to re-run: `internal/compaction` (the four planner and assembly tests),
`TestASegmentIsRegisteredToEveryTurnItCoversAndErasureReachesIt` (`internal/infra/pg`),
`TestTheCompactionPassRollsUpAHistoryOneBoundedCallAtATime` and
`TestTheDriverRunsTheCompactionPassAfterDraining` (`internal/formation`),
`TestAContextIsTheNewestTurnsVerbatimAndSegmentsOverTheRestUnderABudget` (`internal/api`), and the
summariser tests in `internal/infra/inference`. The summariser's regression corpus (rule 14) runs on
the next GPU day.
