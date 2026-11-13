<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# What a requirements document keeps — 2026-11-13

The vocabulary and the extraction prompt were written for conversation. This measurement asks what
they keep of a document that is none of that: a request for proposals, made of obligations, dates,
criteria and terms. It was run before and after one change to the prompt.

## What this means for you

- **Before the change, a timetable was lost whole.** Every date in the document was refused as not
  current, because the day had not come. 20 of 38 listed items were kept.
- **After it, 29 to 34 of 38 were kept**, over four runs: every date, and 16 of 17 obligations in
  the run that was read in full.
- **Evaluation weights are not kept.** "The technical criterion has a weight of fifty percent" has
  no relation in the vocabulary to be stored as. An operator who loads such documents adds one.
- **A person with two roles loses the first.** `has_role` holds one value for a subject, so "chair
  of the panel" closed "procurement lead" in every run. The closed fact is kept in history; a recall
  answers with the second role only.
- **One thing under two names is two entities.** "The Authority" and "the Northern Health Authority"
  were stored apart, and a question that names one walks only that one's facts.
- **A question that names nothing gets nothing** from the walk. "List the security requirements" is
  a question for the report and passage surfaces, which were off.

This is one authored document, one model and one prompt. It is not a rate for requirements
documents in general, and it is not a speed.

## Conditions

| | |
|---|---|
| Date | 2026-11-13 |
| Extraction model | `deepseek-flash` at `api.deepseek.com` |
| Prompt | `extraction.yaml` before and after the guidance on deadlines, schedules and obligations |
| Vocabulary | the shipped one, with nothing added |
| Embedding model | none; the semantic surfaces were off |
| Document | written for this measurement; 8 sections, 2.5 kB, in the test file |
| How it was stored | one turn per section, one message each, role `tool`, no data subject |
| Runs | 1 before the change, 4 after |

**The document is not a real tender.** It was written to read like one, and the list of what it
contains — 38 items — was written by the same author before anything was run. That is a bias: the
document may be easier or harder than a real one in ways its author cannot see. A listed item counts
as kept when one stored fact holds every one of its words in its statement, quote, subject or
object. That is crude, and it is the same for every run.

## Run it yourself

```sh
make test-inference   # or, for this measurement alone:
go test -tags inference -count=1 -v ./internal/infra/inference -run '^TestWhatARequirementsDocumentKeeps$'
```

[`liverequirements_test.go`](../internal/infra/inference/liverequirements_test.go) holds the
document, the list and the harness. It sends the document through the path a deployment's would
take — stored, then formed by the worker with the configured model — and reads back what the
database holds. It asserts only that the measurement ran.

## What was kept

| Kind | Listed | Before | After (first run) |
|---|---|---|---|
| Plain statements | 10 | 6 | 8 |
| Dates | 4 | 0 | 4 |
| Events that happened | 1 | 1 | 1 |
| Obligations | 17 | 11 | 16 |
| Criteria and weights | 4 | 1 | 1 |
| Terms | 2 | 1 | 1 |
| **All** | **38** | **20** | **31** |

The four runs after the change kept 31, 34, 29 and 34. Facts stored: 24 before; 36, 36, 32 and 37
after.

**Refusals.** Before: 4, all `not_current`, and all four were the document's dates — two deadlines
and two scheduled events. After: 0, 0, 1 and 0. Nothing was refused as `not_asserted` or by the
vocabulary in any run.

**A section that produced nothing.** Before the change, the five functional requirements of
section 4 — "The contractor shall provide single sign-on", and four like it — produced no fact and
no refusal. The model proposed nothing for them. After, the section produced five facts in every
run.

## What changed in the prompt

Two paragraphs in the section on tense. A deadline or a schedule says what the date is, and that
holds now although the day has not come: it is reported as present, with the thing that is due or
scheduled as its subject. An obligation states a duty that binds now, whether or not it has been
done: it is reported as present on whoever is bound.

Nothing was admitted that the rule refused before for a reason that still holds. A relation that
has not started is still refused: the regression corpus keeps "I start at Acme in June" out, and
gained a case that a plan to move is still not a residence. The corpus passed 22 of 22 after the
change, three attempts a case ([D68](decisions/D68.md)).

## What was stored wrongly

Read by hand in the first run after the change, 36 facts:

- **One false fact.** "The contractor shall provide a support desk located in Ireland" was stored
  as the obligation, correctly, and also as `located_in(a support desk, Ireland)` — a desk that does
  not exist yet, stated as being somewhere.
- **Two loose ones.** "The Authority operates forty-two clinics" became `responsible_for`. "Shortlisted
  bidders will present on 20 April 2027" became `scheduled_for` with the bidders as its subject,
  where the presentations are what is scheduled.
- **One true fact closed**, below.

## A single-valued relation and two true values

In every run, before and after, `has_role(Dervla Quinn, procurement lead)` from section 2 was
closed by `has_role(Dervla Quinn, chair of the panel)` from section 7. Both are true. One closure
in each of five runs, and it was the wrong one each time.

What the finding feared for dates did not happen. With the thing that is due as the subject, the
two deadlines are `due_on(Questions from bidders, …)` and `due_on(Proposals, …)`: two subjects, two
facts, and neither closes the other ([D69](decisions/D69.md)).

## One thing under several names

From the last run, by the name an entity is stored under:

| Stored as | Current facts it is the subject of |
|---|---|
| `the contractor` | 9 |
| `the service` | 5 |
| `the authority` | 2 |
| `the northern health authority` | 0 as subject; the object of `works_at` |
| `bidder` | 2 |

In an earlier run the same contractor was stored as both `the contractor` and `contractor`. The
name probe is exact, so each is a separate place to start a walk.

## What a question gets back

Five questions against the last run, asked for the `tool` source the document was stored under:

| Question | Anchored | Facts |
|---|---|---|
| When are proposals due? | `Proposals` | 1, the deadline |
| What must the contractor do? | `The contractor` | 9, not cut |
| What is the service required to do? | `The service` | 5, not cut |
| What is Dervla Quinn's role? | nothing | 0 |
| List the security requirements. | nothing | 0 |

The fourth was a defect and is fixed: a name in the possessive was looked up with its `'s`
([#284](https://github.com/ensera-ai/taisce/issues/284)). The fifth names nothing, and the walk has
nowhere to start.

A recall with the default sources returns none of the document: it was stored as a tool's words,
and the default is what a person said themselves.

## What this does not tell you

- **A rate.** One document, written by the person who scored it.
- **Anything about another model.** One model, one endpoint.
- **What a hub looks like.** The busiest entity had nine facts; the fanout cap is 64.
- **What the semantic surfaces would have returned** for the question that named nothing.
- **Latency, throughput or cost.** None was measured.
