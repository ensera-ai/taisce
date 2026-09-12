<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# The adapter conformance suite

One suite, expressed once in `cases.json`, run by every adapter against a live deployment. It holds
the rules that are easy to get subtly wrong per language, and it is what stops the behaviour forking
quietly while every adapter's own tests stay green.

## The rules it holds

| Case | Rule |
|---|---|
| `memory_is_one_untrusted_user_message` | Injected memory is exactly one `user` message, marked untrusted, carrying the watermark and the plan. Never `system`, never `assistant`. |
| `recall_failure_is_not_fatal` | When recall is unavailable the agent runs without memory: no memory message, no error raised to the turn, and the turn is still stored. |
| `write_failure_is_never_silent` | When the store is unavailable the failure is surfaced to the application. |
| `store_only_on_success` | A failed turn is not a memory: when the model call fails, nothing is observed. |
| `filters_default_to_external_messages` | Internal tool traffic does not become memory by default: only the person's message and the final reply are observed, each standing alone — an atomic group is a tool exchange, not two unrelated messages the adapter happened to send together. |
| `no_synthetic_message_reaches_observe` | A message the framework wrote, a summary or the injected memory itself, is never observed as though a person said it. |
| `compaction_hands_the_model_the_deployments_context` | When compaction is due the adapter asks the deployment for the subject's context and hands the model exactly that: one untrusted `user` message with the segments and the verbatim turns unchanged, in place of the history the framework held. The person's message stays; nothing synthetic is observed. |
| `context_failure_is_not_fatal` | When the context cannot be fetched the history stays as the framework held it: no memory message, no error raised to the turn, and the turn is still stored. |
| `a_retried_turn_is_stored_once` | A turn the application retries is one memory: the observation's idempotency key is derived from the turn — the subject, the run and the messages — so the retry carries the same key and the deployment folds it into what it already holds. |
| `a_turn_retried_a_second_later_is_stored_once` | A retry that comes a second or more after the first attempt is still one memory, whatever time the adapter stamps on it. |

Compaction is not in the suite yet: it is a server operation and its adapter
seams do not exist; the case joins when the operation does.

## What is real

The deployment is real. The runner seeds each case's facts through `POST /v1/records/assert` under a
data subject of its own, reads the stored offset from `GET /v1/freshness` before and after the turn,
and stands a recording proxy between the driver and the deployment. The proxy forwards everything and
records it; for the cases that need it, it answers `503` to one operation, which is what a deployment
behind a load balancer answers when it is down and what an adapter must survive. Nothing about the
deployment is mocked. The model is stubbed inside the driver, because the suite is about what the
adapter hands the model and what it stores afterwards.

## The driver protocol

A driver is the adapter under test wrapped in a loop. The runner starts it once and exchanges one
JSON line per turn on its stdin and stdout: an instruction in, a report out. Stderr is the driver's
own.

Instruction:

```json
{"case": "memory_is_one_untrusted_user_message",
 "api": "http://127.0.0.1:53211", "token": "tsk_…", "data_subject_id": "…",
 "turn": {"history": [{"role": "user", "content": "…"}, {"role": "assistant", "content": "…"}],
          "user": "Where does Marta work?",
          "tool_calls": [{"call": "…", "result": "…"}],
          "synthetic": [{"role": "assistant", "content": "…"}],
          "assistant": "At Ensera.",
          "model_failure": false,
          "compact": false,
          "repeat": 1}}
```

The driver configures the adapter with `api` and `token`, loads `turn.history` into the framework's
own session as earlier turns the application kept, runs one agent turn for `data_subject_id` in
which the person says `turn.user`, the framework generates the listed tool calls and results and
the listed synthetic messages, and the stubbed model replies `turn.assistant` or fails when
`model_failure` is set. When `compact` is set the driver arms the adapter's compaction seam with a
trigger the history trips, in the framework's own vocabulary, so the turn runs with compaction
due. When `repeat` is above one the runner sends the same instruction that many times, as an
application retrying would, and the driver is not told which it is. It then answers:

```json
{"model_messages": [{"role": "user", "content": "taisce-memory/v1 untrusted\n{…}", "untrusted": true},
                    {"role": "user", "content": "Where does Marta work?"}],
 "fatal": false, "observed": true, "store_error": ""}
```

`model_messages` is every message the adapter handed the model, in order, with the adapter's own
untrusted mark where it set one. `fatal` is whether the turn raised an error to the application.
`observed` is whether the adapter attempted to store the turn. `store_error` is the error the adapter
surfaced when storing failed, and empty otherwise; an adapter whose store failed and whose
`store_error` is empty has failed silently.

The reference driver is `taisce conformance --serve-reference`; an adapter's driver reproduces its
behaviour, not its code.

## The memory message

Every adapter injects the same message: one `user` message whose content is the line
`taisce-memory/v1 untrusted`, a newline, and a JSON document:

```json
{"watermark": {"stored": 12, "formed": 11, "parked": 0},
 "plan": {"controls": {…}, "degraded": [], "reach": {…}},
 "facts": [ … the recall's facts, unchanged … ],
 "reports": [ … ], "passages": [ … ]}
```

`watermark` is `GET /v1/freshness` as read beside the recall; `plan` is what the recall did: its
effective `controls`, the surfaces it named `degraded`, and its `reach`. `facts`, `reports` and
`passages` are the recall's arrays unchanged. When the recall returns nothing, nothing is injected.
The message is marked untrusted by whatever means the framework has for it, and it is never stored.

A compaction hands the model the same kind of message with a context in it, from `POST
/v1/contexts`:

```json
{"watermark": {"stored": 12, "formed": 12, "parked": 0},
 "plan": {"characters": 140, "truncated": false},
 "segments": [ … the context's segments, unchanged … ],
 "turns": [ … the context's verbatim turns, unchanged … ]}
```

It replaces every message before the person's current one, except system messages, and nothing
else is added: the adapter writes no summary of its own. A case that exercises this states the
context the proxy answers (`context` in the case), because a segment is written by the worker's
pass from a model's summary, which a conformance run has no model for; the deployment's own side of
the operation is proved by the service's tests, and the adapter's obligation is fidelity to the
answer. `expect.model_carries` and `expect.model_lacks` name what must and must not reach the model
as messages of their own. A context the proxy refuses (`faults.context`) leaves the history as the
framework held it.

## Running it

```sh
TAISCE_TOKEN=… taisce conformance --api https://memory.example --driver "python -m myadapter.conformance"
TAISCE_TOKEN=… taisce conformance --api https://memory.example --reference
```

The credential must be write-enabled for one project. The run prints one verdict per case and a
summary, and exits non-zero when any case fails or the driver cannot be used. `--reference` runs the
adapter this suite ships with, to prove the deployment and the suite before an adapter is blamed.

## How the suite is proved

`internal/conformance` runs the reference through the suite against a live deployment and expects
every case to pass; runs thirteen deliberately broken variants and expects each to fail exactly the case
that holds its rule; and runs the reference over the subprocess protocol. A suite that has never
been seen to fail has asserted nothing.
