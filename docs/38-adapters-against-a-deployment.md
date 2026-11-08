<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Every adapter against one deployment — 2026-11-08

Each adapter repository holds tests that need no deployment, and one suite that does. The
deployment-free tests prove each adapter against its own idea of what a deployment answers. The
conformance suite proves them against a deployment that answers for itself — one suite, expressed
once, run by every adapter.

Until this run the suite had not been run against an installation of the published chart. Every
adapter's own tests were green, and so was the suite in the places it is run during development.
What had never been established is that five adapter drivers, in three languages, agree with a
deployment that was installed from the published artifact rather than built for the occasion.

## What this means for you

- **All five adapters pass all ten cases against the published chart.** Two Python drivers, two Java
  drivers, one .NET driver, and the reference driver first to prove the deployment and the suite
  before any adapter could be blamed. Sixty case results, no failures.
- **The suite passed with nothing formed.** The deployment had no reachable model throughout, so no
  turn was ever formed. The suite does not notice, because it stubs the model inside each driver and
  seeds facts directly. That is the right design for a suite about adapters, and it means a green
  conformance run says nothing about whether extraction works.
- **One operation does not do what the contract says it does.** `POST /v1/contexts` returns only
  turns that have been formed, so a subject whose turns are stored but not yet formed gets an empty
  context — and an adapter's compaction replaces the history the framework held with that empty
  answer. The response says `truncated: false` and `characters: 0`, so nothing in it reports that
  history was dropped. This is #111, and the suite cannot catch it: its compaction case takes a
  canned context from the recording proxy rather than from the deployment.
- **Two refusals were exactly right.** A passage search refused with `503 passage_unavailable`
  rather than answering from an index that was never built, and a recall narrowed to one person said
  in `degraded` that reports were withheld and why. Neither had to be inferred from an empty result.

## The deployment the adapters met

Version 0.5.1 of the chart, pulled from `oci://ghcr.io/ensera-ai/charts/taisce`, installed into a
namespace of its own on a three-node cluster that was carrying unrelated work throughout. Three
database instances under the CloudNativePG operator, two API replicas, two workers, the management
surface with the portal on. `helm install --wait` returned in 2 minutes 10 seconds.

Nothing about the deployment was adjusted for the adapters. The runner reached it over a forwarded
port, which is how an application outside the cluster reaches a service that publishes no ingress.

**No model was reachable.** The credential available for the configured endpoint is not valid, which
was established in the install run that preceded this one and is unchanged. The consequence for this
run is bounded and worth stating precisely: formation never ran, so `formed` stayed at zero for
every turn stored, and anything downstream of a model — extraction, embeddings, segment summaries —
did not happen. The conformance suite is unaffected by design, because it stubs the model in the
driver and seeds its facts through `POST /v1/records/assert`, which writes them without a model.

## What ran

| Driver | Language | Framework seam | Cases | Failed |
|---|---|---|---|---|
| `taisce conformance --reference` | Go | the reference the suite ships | 10 | 0 |
| `python -m taisce_agent_framework.conformance` | Python | a context provider | 10 | 0 |
| `python -m taisce_langgraph.conformance` | Python | agent middleware | 10 | 0 |
| `scripts/conformance.sh langchain4j` | Java | a chat-memory seam | 10 | 0 |
| `scripts/conformance.sh spring-ai` | Java | an advisor | 10 | 0 |
| `scripts/conformance.sh` | .NET | a context provider | 10 | 0 |

The reference run took ten seconds and the .NET run eighteen, of which seven were its build. The
Java runs are dominated by a Maven install of the whole repository before the driver starts. These
are run times on a shared cluster, quoted so a reader knows what to expect of the command, and they
are not latencies: nothing was held still and nothing here measures the deployment's speed.

Running the reference first is worth the ten seconds. It separates "this adapter disagrees with the
suite" from "this deployment or this suite is wrong", and those two have very different fixes.

## What the suite does not reach

The suite is about what an adapter hands the model and what it stores afterwards, so three things it
deliberately does not exercise stayed unexercised, and a green run should not be read as covering
them:

- **The model.** Stubbed in every driver. No adapter's handling of a real model's output was tested.
- **A context the deployment actually assembled.** The compaction case states the context the
  recording proxy answers, because a segment is written by a worker's pass over a model's summary
  and a conformance run has no model. The adapter's obligation in that case is fidelity to an
  answer, and fidelity is what was proved. Whether the deployment's own answer is the one the
  contract describes is a different question, and #111 is what happens when you ask it.
- **Formation, and everything after it.** Nothing was formed, so no recall in this run returned a
  fact that formation produced. The facts the suite needs it seeds directly.

## The client journey, and the operation it found

The suite exercises adapters. To exercise the deployment through the same client the adapters are
built on, one journey was run directly against it: store a turn under a subject of its own, read the
freshness watermark, ask for that subject's context, recall, and search passages.

The context came back empty:

```json
{"segments": [], "turns": [], "characters": 0, "truncated": false,
 "watermark": {"scope": "default", "stored": 59, "formed": 0, "parked": 0}}
```

The turn was stored moments earlier, under that subject, and its text was in the store. Asked
directly:

```
data_subject_id | log_offset | unformed | parked | message_rows | text_chars
sample-8bfa6c8e |         58 | t        | f      |            2 |         67
```

Two message rows and sixty-seven characters of the person's own words, attributed to the subject,
returned by no part of the context. The reason is one predicate: the query behind a context selects
`formed_at IS NOT NULL AND parked_at IS NULL`.

Both halves of that matter, and the second is worse. A turn that has not been formed yet is
temporarily invisible, and formation is asynchronous by design, so the window is however far behind
the workers are. A turn that has been parked — six failed formation attempts, after which the
watermark advances past it — is invisible permanently. In the install run that preceded this one,
261 of 512 turns parked while the model was unreachable; a compaction in that state hands the model
almost nothing and reports no loss.

What makes this a defect rather than a choice is that the contract describes the other behaviour.
The response's own documentation says a context is the newest turns verbatim with segments standing
in for the rest, and the client says what the deployment has not rolled up yet comes back verbatim.
A reader has every reason to think "not rolled up yet" includes "not formed yet" — and an adapter
built on that reading replaces real history with nothing. The fix might be the query or it might be
the contract, and that is the decision #111 asks for.

## The retry check, run alone

A turn stored three times under one idempotency key, with nothing else writing to the deployment:

```
stored before : 66
stored after  : 67   (three attempts at one turn)
moved by      : 1
same id       : True
same offset   : True
```

This was measured twice. The first attempt ran while another adapter's suite was still storing
turns, and the watermark it read had moved by two — not because the retry was stored twice, but
because something else was writing. The figure above is from a run with the deployment to itself,
and it is the one to read. A watermark is a property of the deployment, not of the caller, and a
measurement of one taken while anything else is writing is a measurement of both.

## What refused, and refused well

**A passage search.** `503 passage_unavailable: passage search is unavailable for the configured
model generation`. Passage search reads an embedding generation, and no generation exists without a
model. The alternative — answering from nothing and returning an empty list — would be
indistinguishable from a search that found nothing, and a caller would quote an index that was never
built.

**Reports, under a subject.** A recall narrowed to one person returned `degraded:
["reports_withheld_for_subject"]`. A report summarises a community of entities across everybody a
project holds and records nobody's words as its source, so it cannot honestly be narrowed to one
person. The recall said which surface did not run and the answer carries the reason, rather than
leaving a caller to infer it from a shorter answer.

Both of these are the fail-closed default doing its job, and both are visible in the answer rather
than only in a log.

## What could not be tested

- **Anything that needs a model.** Extraction, embeddings, segment summaries, and therefore any
  recall that would answer from a formed fact. The adapters' handling of a real model is untested
  here.
- **The head-to-head the .NET repository ships.** It needs a live deployment with a forming worker
  and an OpenAI-compatible endpoint for chat and embeddings. Without a model it cannot run, and
  nothing about it was exercised.
- **Any statement about speed.** The cluster was shared throughout. The run times above are for a
  reader deciding how long to wait, and are not a property of the deployment or of any adapter.
- **Whether the adapters agree under load.** One driver at a time, one turn at a time, which is what
  the suite does. Concurrency between adapters was not exercised.

## What is still installed

The release `memory` in namespace `taisce`: three database instances, two API replicas, two
workers, the management surface, one Secret holding the provider key, and the Secret holding the
role passwords. Nothing outside that namespace was created or changed. Removing it is `helm
uninstall memory -n taisce` followed by deleting the namespace, and both steps are needed.
