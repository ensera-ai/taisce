<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# The design, read as something an agent and an operator depend on — 2026-11-11

Part of #209. Read against `main` at `e77a234`, with v0.6.1 running on a three-node cluster beside
it. The question was not whether the documents agree with the code. It was what breaks, for whom, and
in what order, if this is given real traffic, real people's words and an operator who was not there
when it was built.

## How to read this

Every finding is labelled.

- **Fact** — a file and a line, a probe that was run, or something observed on the deployment.
- **Inference** — it follows from a fact and was not itself observed.
- **Unverified** — plausible, and nothing here confirms it.

No latency, throughput or capacity figure appears, because none was measured and nothing here is
hardware that can be held still. Where a finding names a ceiling it is one the code states as a
constant, not one a machine produced.

**On independence.** Some of this code was changed by the same hands that reviewed it. The substitute
on offer is that every finding names what to run or read to check it.

**What was already on record** is not repeated: that nothing ranks and a busy entity answers with
whatever hashed low (#153, #156), that anchoring is an exact-name probe with no lexical arm (#157),
that a recall cannot explain itself (#160), that a requirements document loses what it obliges
(#187), and that no performance target has been agreed (#155). They stand, and the ranking below
assumes them.

## The verdict

**The integrity argument is the product, and it holds everywhere except one place that matters.**
Evidence to the byte, the closed vocabulary, the counted erasure and the three database identities
are enforced where a statement cannot get around them, and reading the code did not find a way
around them. The exception is the first finding: in a project whose people do not share a world, a
true fact can be ended by somebody who never saw it, and nothing tells either of them.

**It is not yet operable by somebody who did not build it.** The four things an operator most needs
are the four that are missing: a backup, an upgrade that says what it promises, a duration of
anything, and first credentials that are not in the log store.

**Its ceilings are constants, not hardware.** A replica serves 128 requests a second because a line
says so. A project forms one turn at a time because a loop says so. Neither has been chosen against
a target, because there is no target (#155).

The order to work in is at the end.

## Correctness, as a memory

### 1. One person's statement ends another person's — #210

**Fact.** Reproduced against a real database through `ObservationStore.Append` and
`FactStore.Assert`. Two data subjects in one project each say where a "Sarah" lives:

```
subject "Sarah":    distinct subject entities across alice and bob = 1;
                    alice's fact still current after bob spoke = false
subject "the user": distinct subject entities across alice and bob = 2;
                    alice's fact still current after bob spoke = true
```

A named entity is `(scope, normalized_name)` (`0001_the_spine.sql`), and a single-valued relation
excludes on `(scope, subject_entity_id, predicate, valid, known)` (`0010`, `0032`). Neither carries
who spoke. The person themself is separate; everyone and everything they mention is shared.

**Why it is first.** It is silent — no refusal to count, no receipt, and the citation on the fact
that did the closing is genuine. And it lands in the commonest deployment there is: one project for
one application with many users.

**Corrected after a fix was attempted.** Two things said here at first were wrong. The closure *can*
be found afterwards: a closed fact records what closed it (`superseded_by`, migration `0029`), so
facts ended by a different data subject can be counted. And it is not an oversight: two existing
tests state *"Atlas is in Dublin"* as one data subject and *"Atlas moved to Oslo"* as another, and
require the second to end the first. That is a project as one shared world, where anybody's newer
statement updates a thing — the right answer for a team's workspace.

**So it is a decision, not a defect.** A project that is one shared world wants the later statement
to win whoever made it. A project that holds many people's separate worlds wants each person's
statements to end only their own. The store cannot tell which it is in, and today it assumes the
first, silently.

**Direction.** A project states which it is, each fact carries the account it belongs to, and the
exclusion constraint compares that — so both modes keep the constraint as the backstop. The options
and the parked attempt are on #210.

### 2. A caller cannot ask to be answered after its own write — part of #216

**Fact.** No read takes an offset to wait for; the read handlers carry no such parameter. A worker
with nothing to do looks again after five seconds (`DefaultPolicy.Interval`), and nothing wakes it
when a turn is stored.

**Inference.** An agent that writes a turn and recalls in the same exchange gets memory as it was
before the turn, for at least the model's extraction time and up to five seconds more. `contexts`
covers conversational continuity from raw turns, so this is a gap for facts specifically.

**Direction.** The append notifies and the idle worker listens, with the tick kept as the fallback.
A wait-for-offset on recall is a second step and should follow a latency target, not precede it.

## Security

### 3. The first credentials are in the log store — #211

**Fact.** Bootstrap prints both tokens to standard output (`cmd/taisce/commands.go`), and the chart's
comment argues the Job's deletion takes them with it. On this cluster promtail ships that output to
Loki. Counted without reading:

```
memory-taisce-bootstrap-1-5c676   2
memory-taisce-bootstrap-1-zjvxw   2
taisce-taisce-bootstrap-1-h4zjt   2
```

Two of those pods belong to releases that no longer exist. The third is the running deployment's
operator credential.

**Direction.** Bootstrap mints nothing. The operator mints the first credential through `exec`, whose
output reaches a terminal and no log. Until then: anyone running a release should treat both first
credentials as disclosed, and replace them.

### 4. A stranger can empty the bucket every request spends from — #214

**Fact.** `resolveGrant` takes from a 128-a-second bucket (`admission.go:26`) on its first line and
reads the `Authorization` header six lines later (`api.go:373`, `:379`). Probed in isolation:

```
requests admitted in one instant before refusal: 256; the next request (any credential) admitted: false
offered 1000 requests/second for 10s with one in flight: admitted 1280, which is 128 per second
```

**Inference.** About 130 requests a second with no header, per replica, and callers holding
credentials are refused at the door. No token is needed.

**Direction.** Refuse what cannot be a token before charging for it, and charge the bucket for
failures rather than for requests. Neither gives up anything the security model promises.

### 5. The project boundary is a rule every statement has to remember — #219

**Fact.** One login serves every project; separation is `scope = ANY($1)` across fifty-six store
files; no table enables row-level security; one end-to-end test covers the routes that exist and
nothing checks the next statement written. `roles-and-grants.md:336` rejects row-level security
because *"a role-based policy would have nothing to distinguish between"* — which is true of a
policy keyed on the role and does not consider one keyed on a setting the transaction states.

**Why it has grown.** D34 now sends operators to projects for the separations that matter. What rests
on the convention is more than when the convention was chosen.

**Direction.** Row-level security keyed on a transaction-scoped project, so a forgotten predicate
returns nothing instead of everything. It does not stop a compromised process, and the security page
should keep saying so. Before that, and whatever is decided: a test that walks every statement and
fails on one that reads a scoped table without the scope.

`0001_the_spine.sql:18` still says a project is a *"soft boundary"* across which *"reads are
INTENDED"*. That describes a different system from the one the security page describes.

### 6. Monitoring has to hold the credential that can mint any other — #218

**Fact.** The scrape is registered behind `operated` (`manage.go:146`) and there is one kind of
operator credential. D32 calls the monitoring system a different trust domain, and the scrape
configuration puts the operator credential in it.

**Direction.** A third kind that opens the metrics route and nothing else.

### 7. Nothing separates this namespace from the rest of the cluster — #222

**Fact.** The chart renders no `NetworkPolicy`. From a pod in another namespace on the live
deployment the management port answered `200` for the sign-in page. Every connection string uses
`sslmode=require`, which encrypts and does not verify who answered.

**Direction.** Policies on by default, and `verify-full` against the certificate authority the
database operator already publishes.

### 8. A credential never expires — #223

**Fact.** `control.credential` has `revoked_at` and no expiry; resolution checks nothing about age;
the credential list does not show last use, though the ledger could answer it.

**Direction.** An optional expiry enforced where revocation is, and last use read from the ledger.
A workload identity with no stored key is where rule 8 points, and is a design of its own.

### 9. A generation call has no ceiling on what comes back — #221

**Fact.** `chatRequest` (`extractor.go:314`) carries a model, a temperature, the messages and a
response format. No output limit. The provider's `usage` is not read and no series counts tokens.

**Inference.** Text written by somebody else decides how much the operator pays per message, for up
to 64 messages a turn and six attempts. The extractor refuses what comes back, correctly, after it
has been paid for.

**Direction.** A ceiling per purpose in each prompt's model contract, and tokens counted by purpose.

## Performance

### 10. Nothing measures how long anything takes — #217

**Fact.** No request duration, database time or model-call time exists in a metric or a log line. D32
decided it: *"No durations were added to the read path."* Admission refusals reach a log line and no
series.

**Why it is first in this section.** Every other performance finding here is an inference for
exactly this reason. If #155 were settled tomorrow, no deployment could say whether it met the
answer.

**Direction.** Fixed-bucket histograms over closed sets — operation, model purpose — in the
exposition code that already exists. No dependency, no label from anybody's words.

### 11. A replica serves 128 requests a second because a constant says so — #214

The same bucket as finding 4, read as a ceiling. **Fact:** it is spent by every protected request,
freshness polls included. **Inference:** two replicas give the instance 256 a second for everything,
and a caller polling freshness spends from the allowance its own recalls need.

### 12. Formation is one project at a time, to the bottom — #215

**Fact.** `Driver.Once` walks projects in one loop (`driver.go:146`) and `drain` returns only when
the project is empty (`formation.go:389`). The ledger seal, the retention sweep and notification
delivery run in the same loop after the walk.

**Inference.** With two workers, two continuously busy projects stop a third from forming at all. A
long drain also postpones sealing on that worker, and the seal interval is the window in which a
ledger entry is covered by nothing.

**Direction.** A bounded slice per project per pass, with the walk's order rotated.

### 13. Past 512 turns, a project accepts writes at the speed of a model — #216

**Fact.** The backlog limit is 512 per project and 4096 per instance; past it an observation is
refused with `Retry-After: 1`, whatever the real wait is. The limits are changed by a hand-written
`UPDATE` as the administrative role.

**Inference.** Handing this a body of documents is throttled to extraction speed after the first 512
turns, which is the situation #189 is about to create on purpose.

**Direction.** The limits become operator settings on the management surface, and `Retry-After`
reflects the drain rate.

### 14. Every read is a write, and the table it writes to cannot shrink — #220

**Fact.** Each operation appends to `audit_entry` before it answers (`api.go:140`), freshness
included. Triggers refuse deletion for every identity. Three indexes, no partitions, no retention.
`Verify` recomputes every sealed range from the start.

**Inference.** Reads cannot be served by the two replicas the chart runs, so "move reads off the
primary" is not available as an answer to whatever the first bottleneck turns out to be. The largest
table in a busy instance holds no memory and grows with traffic.

**Direction.** Partition by time and archive sealed ranges with their seals; count cheap reads rather
than list them.

### 15. An append holds the project's lock across 3 + 3·n round trips — #224

**Fact.** `appendTurnTx` takes the watermark lock first and then issues each insert as its own round
trip (`observationstore.go:79`), 195 for a 64-message turn. Each message's text is stored twice, in
`turn_message.content` and `chunk.text`.

**Unverified.** That this is the first thing to break on the write path. The backlog bound and the
model are in front of it for sustained load.

**Direction.** One pipeline for the transaction's statements. Measure before and after, or record
that it waits for #155.

## Operability

### 16. Highly available, and no backup — #212

**Fact.** Nothing in the chart configures a base backup, an archive or a schedule. On the live
deployment `spec.backup` is empty and no backup object exists. `governance.md` states that nothing
re-applies an erasure after a restore; the receipt for an erasure lives in the database being rolled
back and holds no subject.

**Inference.** Everything replicas do not protect against — the wrong erasure, a bad migration, an
uninstall — is unrecoverable on the default install. And a restore silently undoes every erasure made
since the backup, with no record to replay.

**Direction.** The chart renders a backup or refuses unless told there is none. Erasures are recorded
somewhere a restore cannot reach.

### 17. A release exists and an upgrade promises nothing — #213

**Fact.** `migrations.md:274`: *"Taisce has not had its first supported release yet"*, with
mixed-binary operation and zero-downtime upgrades listed as not promised. v0.6.1 is published and
running. The serving binary does not read `schema_migration`; on `helm upgrade` the migration Job and
the rolling Deployments run at once, unordered.

**Direction.** The binary refuses a schema outside the range it was built for, on the readiness path
it already has. What a migration may do between releases is then a decision.

### 18. The deployment did not say it was unhealthy — closed as #206, and the class is open

**Fact.** For the whole life of the release a database replica was replaced every fourteen seconds.
`/health`, `/ready` and every exported series were green. It was found by looking at a pod's age.

**Inference.** The service's signals describe the service. Nothing it exports describes the database
it depends on, beyond connection counts.

**Direction.** Not for the service to learn — availability is a deployment property here. The chart's
notes and the operations page should say which of the database operator's own signals to alert on,
since those are the ones that would have fired.

### 19. A published image keeps its advisory until somebody cuts a release — #225

**Fact.** `make vuln` judges the tree. GO-2026-6859 turned every build red on 2026-11-11 and the pin
moved the same day (#208). v0.6.1's images were built before it and nothing tells whoever runs them.

**Direction.** A scheduled scan of the released artifacts, and a stated patch policy.

### Already filed from operating it

The console's hostname answers nothing at its root (#204), and a cluster on Gateway API cannot
publish either surface from the chart (#205).

## The questions a design is supposed to answer

| Question | Answer found |
|---|---|
| Can one project reach another's data? | Not through the API. Through one forgotten predicate, yes — #219. |
| Can one person affect another's memory inside a project? | Yes, silently — #210. |
| What does a stolen credential reach, and for how long? | One project, or the whole management surface; until somebody revokes it — #223. The first operator credential should be assumed stolen — #211. |
| What breaks first at ten times the load? | Unknown, and unknowable on a running instance — #217. By constants: the 128-a-second bucket on reads (#214), the 512-turn backlog on writes (#216). |
| The model is slow? | Reads are unaffected. Formation falls behind, then writes are refused at the backlog limit, then other projects wait behind the slow one — #215. |
| The database is unavailable? | Not attacked here. Recovery past failover is whatever the operator arranged, which by default is nothing — #212. |
| A message arrives twice? | Answered by the idempotency key and D25. Not re-examined here. |
| A process dies mid-transaction? | An append is one transaction. Formation asserts claim by claim and replays; not attacked here. |
| Can an operator with database access do something invisible? | Yes, and the security page says so. The ledger detects its own rewriting only against a digest kept elsewhere. |
| What does the deployment say when it is unhealthy? | Less than it should — #217, and finding 18. |

**What is hard to copy.** Not the architecture: PostgreSQL, a worker and a closed vocabulary can be
reproduced in a quarter. What would still be better here is that a claim cannot be stored without the
bytes it came from, and a deletion cannot be reported without being counted — both as constraints,
not as code. That is an integrity advantage, and it is the reason finding 1 is ranked above
everything else: it is the one place where the store holds something untrue about what a person said
and the evidence model cannot show it.

## The order to work in

**Now — each one loses or exposes something for every day it waits.**

1. #210 — decide whether a project is one shared world or many separate ones. Until it is decided,
   one person's statement can end another's without a trace.
2. #211 — stop printing credentials to a log; replace the ones already there.
3. #212 — a backup, before there is anything that cannot be re-ingested.
4. #214 — the two-line change that stops a stranger emptying the bucket.

**Next — what makes it runnable by somebody else.**

5. #217 — durations, because 6 to 9 cannot be ranked without them.
6. #213 — the schema gate on readiness, then the upgrade decision.
7. #215 — a bounded slice per project.
8. #221 — a ceiling and a count on every generation call.
9. #218, #223 — a scrape credential, and an expiry.

**Then — decisions that deserve a measurement first.**

10. #155 — the targets. Everything below is ranked by them.
11. #219 — row-level security, after a plan check; the statement lint before it.
12. #220 — the ledger's growth and what is listed against what is counted.
13. #216, #224 — the backlog as an operator setting; the append as one pipeline.
14. #222, #225 — network policy and verified database connections; scanning what is released.

## What was not examined

The adapters and their conformance suite. The portal's pages beyond its headers and session
handling. The MCP route beyond its authentication. Embedding generations, vector layouts and their
plans. The quality of reports and compaction segments. Notification delivery. Erasure and export were
read and not attacked — no attempt was made to find a derived row that an erasure misses, and that is
the review most worth doing next, because the receipt is the claim this system makes that nobody else
does.
