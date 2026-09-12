<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Compaction

An agent's history keeps growing, but a model's context window does not. Compaction gives you a
short version of a person's history that fits: the newest turns word for word, and summaries of
everything older.

Taisce writes those summaries in the background, on the worker. When you ask for a context, it
assembles one from what is already written. It never calls a model while you wait.

## Get a context

```http
POST /v1/contexts
Authorization: Bearer <project-credential>
Content-Type: application/json

{"data_subject_id":"subject-1","max_characters":20000}
```

`max_characters` is optional. Leave it out and the server's recall budget applies. A budget above the
server's is refused.

An abridged response:

```json
{
  "segments": [
    {"segment_id":"…","level":2,"from_offset":0,"to_offset":511,"covered":64,"summary":"…"},
    {"segment_id":"…","level":1,"from_offset":512,"to_offset":575,"covered":8,"summary":"…"}
  ],
  "turns": [
    {"log_offset":590,"occurred_at":"2026-09-11T09:40:00Z",
     "messages":[{"role":"user","content":"…"},{"role":"assistant","content":"…"}]}
  ],
  "watermark": {"…": "…"},
  "characters": 14210,
  "truncated": false
}
```

- `turns` are the newest turns exactly as they were said, oldest first, with every message and its
  role.
- `segments` are summaries covering the older turns, oldest first. Each gives its level and the range
  of the log it covers.
- `watermark` tells you how far formation has got, so you know what the context cannot include yet.
- `characters` is the size of the result. `truncated` says whether it was cut. Cutting always starts
  from the oldest end.

A person the deployment knows nothing about gets an empty context, not an error.

## What to watch out for

- **Summaries arrive later than turns.** If the worker has not reached part of the history yet, that
  part is handed over word for word and cut to the budget. It is never summarised on demand.
- **The budget is in characters,** not tokens.
- **Erasure reaches summaries.** Erasing a person removes every summary that covered their turns. The
  worker then writes new summaries from what is left.

## Using it from an adapter

The adapters use this for you. When the framework's compaction trigger fires, the adapter asks
`POST /v1/contexts` for the person's history. It then replaces every message before the person's
current one, except system messages, with a single user message, marked untrusted, that carries the
context unchanged. It writes no summary of its own. If the context cannot be fetched, the history
stays as it was, the failure is reported, and the turn carries on.

Each adapter plugs into its framework's own compaction hook. See [Adapters](developers/adapters.md)
for the details per language.

## How it works

### The roll-up

A person's history is their formed turns in log order. The newest eight stay word for word. Behind
them:

- every run of eight turns becomes one **level-1 segment**: a summary a model wrote from those turns,
  stored with the range it covers;
- every run of eight level-1 segments becomes a **level-2 segment**, written from their summaries;
- and so on, up to level six.

Each turn is summarised once per level it climbs, so the work per turn stays constant and the number
of levels grows only logarithmically. `TestTenThousandTurnsCostLinearlyManySummaries` measures it:
ten thousand turns cost 1,426 summaries against a linear bound of 1,428, and a context over them
reads ten segments and eight turns.

A tool call and its results are one group inside one turn. A segment always covers whole turns, so
no segment ever splits a tool call from its results.

### Segments and erasure

A segment is tied to every observation it covers. Erasing a person deletes their segments through
those ties. The worker then finds the gap and writes a new segment from what survived. A run that
runs into an existing segment closes early, so a gap is filled rather than left loose. A segment is
written once and never edited.

### The background pass

After the worker drains a project's backlog, it runs the compaction pass. Each pass writes at most
four segments per project by default, oldest gaps first, one person at a time. A pass that writes
community reports runs beside it, also four per project by default.

Before a model sees the material, anything over 48,000 characters is cut from the oldest turn, and
the cut is counted. The segment still covers the whole range, because its erasure ties must. The
summarising prompt is compiled into the binary ([`compaction.yaml`](../internal/infra/inference/prompts/compaction.yaml)),
runs at temperature zero, and puts the data last. A model failure is counted and does not stop the
pass. A segment another replica already wrote is not an error.

## What is not built

There is no "stateless" mode where an application uploads a list of messages to be compacted. That
would put a model call on the read path, for material the deployment could not tie to a source,
erase or rebuild. To get a history compacted, write it with `observe` and ask for a context.

## How it is tested

- `internal/compaction`: the four planner and assembly tests.
- `internal/infra/pg`: `TestASegmentIsRegisteredToEveryTurnItCoversAndErasureReachesIt`.
- `internal/formation`: `TestTheCompactionPassRollsUpAHistoryOneBoundedCallAtATime` and
  `TestTheDriverRunsTheCompactionPassAfterDraining`.
- `internal/api`: `TestAContextIsTheNewestTurnsVerbatimAndSegmentsOverTheRestUnderABudget`.
- `internal/infra/inference`: the summariser tests. The live inference suite (`make test-inference`)
  includes a compaction case against a real model.

The code is in [`internal/compaction`](../internal/compaction/) (planning and assembly),
[`internal/formation/compaction.go`](../internal/formation/compaction.go) (the pass),
[`internal/infra/pg/segmentstore.go`](../internal/infra/pg/segmentstore.go) (storage) and
[`internal/infra/inference/summariser.go`](../internal/infra/inference/summariser.go) (the model call).
