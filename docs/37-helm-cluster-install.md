<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# The Helm chart meets a cluster — 2026-11-08

The chart says it is highly available by default. Until this run, the evidence for that was
`deploy/helm/chart_test.go` and `deploy/helm/roles_test.go`, which assert over rendered manifests.
They prove the YAML declares three database instances, two API replicas and a disruption budget.
They cannot prove that the image pulls, that the storage binds, that bootstrap finishes before the
serving pods stop restarting, that a value left empty is one an operator can supply, or that the
advisory lock behind the several-replicas claim holds when four processes contend for one backlog.

This is the first time the published chart has been installed anywhere. It records what the install
took, what the journey did, and what the availability tests showed.

## What this means for you

- **The chart installs and the journey runs.** Version 0.5.1 from the published registry came up on a
  three-node cluster, bootstrapped, served memory, accepted turns and answered recalls, and the ops
  centre signed in and reported on the instance.
- **Four defects stood between the documentation and a working install**, and none of them could have
  been caught by a test that reads rendered YAML: #101, #102, #103 and #104. Two more came out of the
  availability tests: #105 and #106, and a seventh came out of removing the install again: #108.
- **One of them loses data an operator cannot get back.** `helm upgrade` deletes the bootstrap Job,
  and with it the only copy of the first operator and project credentials (#102). Changing a replica
  count is enough to trigger it, and it happened during this run.
- **The per-scope advisory lock serialises formation, and this is now measured rather than reasoned
  about.** Four workers contending for one scope: never more than one holder, and holding the lock
  from outside the deployment stopped formation across all four and released it again.
- **The availability claim is not observable from inside the deployment that makes it.** A replica
  that finds the scope busy says nothing, anywhere. See #105.
- **No latency, throughput or capacity number appears here.** The cluster was carrying unrelated work
  throughout and nothing was held still. Setup timings are quoted as setup timings and nothing else.

## The cluster

Three nodes, k3s v1.36.5, with 32, 12 and 4 CPUs and 131, 63 and 32 GiB of memory. It was running
roughly forty other namespaces of unrelated work at the time, which is why this document contains no
performance figure. Storage is node-local only (`local-path`, the default class). CloudNativePG 1.30.1
was already installed cluster-wide. There is no ingress controller.

Nothing cluster-scoped was created for this run. The install went into one new namespace.

## What the install took

| | |
|---|---|
| `helm install` returned | 7 s |
| Database reported three of three instances ready | 3 min 39 s after the install began |
| Everything serving | the same moment |
| Scaling to three API replicas and four workers | 16 s |

Those are setup timings on a cluster that was busy with other things. They say the install completes
in minutes rather than hours. They say nothing else.

The serving pods restart while the database comes up, which the chart's own notes predict and which
looked, from `kubectl get pods`, exactly like a broken install for the first three minutes:
`CrashLoopBackOff` on every API, worker and management pod with
`memory database unreachable: ... connect: connection refused`. It resolves itself.

### What the documentation does not contain

Four things, each now an issue of its own.

**The install order the README gives cannot be followed** (#104). The provider key is a Secret the
operator creates, named by `inference.apiKeySecret`; a Secret lives in a namespace; and the install
command creates the namespace as part of the install. There is no point in that sequence at which the
Secret can exist. The namespace has to be made first.

**The default database resources will not schedule** (#103). Three instances at 2 CPUs, 8 GiB and a
250 GiB volume each is more reservable capacity than an ordinary cluster has spare, and the
documented way to shrink the deployment changes only the instance and replica counts. Lowering
`resources` alone leaves `shared_buffers: 8GB` configured against a smaller container, and the
database does not start. The install needed five PostgreSQL parameters rewritten by hand alongside the
resource block.

**The chart sets no connection pool size** (#101), so each pod's pool ceiling is the CPU count of the
node it landed on. This is the one worth describing in full.

**Nothing says how to reach what was installed** (#104). The chart exposes nothing by default, which
is right, and on a cluster with no ingress controller there is no documented way to make first contact
with either the memory API or the ops centre.

### The pool nobody set

Every DSN the chart builds omits `pool_max_conns`. A pool with no size defaults to the machine's CPU
count, so a pod's connection ceiling is decided by the scheduler. Each process logs what it asked for
as it starts, and on a cluster whose nodes have 32, 12 and 4 CPUs the answer was:

| Pod | Node CPUs | memory pool | registry pool |
|---|---|---|---|
| api | 32 | 32 | 32 |
| api | 12 | 12 | 12 |
| api | 4 | 4 | 4 |
| worker ×2 | 32 | 32 | — |
| worker ×2 | 12 | 12 | — |

The compose deployment sets these deliberately — eight memory and two registry connections for the
API, four memory for a worker — because the arithmetic has to add up against one database. This
install declared 132 against the chart's own `max_connections: 200`, and the same release on a
cluster of 64-CPU nodes would declare 384.

**Two things follow, and the quieter one is worse.** The loud one is that the ceilings can exceed what
the database has. The quiet one is that the API's admission gate is derived from the memory pool:
concurrent work is bounded at `min(64, pool − 1)`, half that for any one project and half again for
any one credential. So the three API replicas behind one Service admitted **31, 11 and 3** concurrent
requests. Which replica the Service picks decides whether a request is served or shed, and nothing in
the deployment says so.

Neither is visible from inside. Each process checks its own ceiling against the connections currently
free and refuses to start if it is short — but pools fill lazily, so every pod started cleanly while
reporting 185 to 196 connections free and actually using 11. Nothing ever compares the sum of the
declared ceilings to anything. The ops centre shows the same actual figure: `Connections 1 of 200`,
`Headroom 196 sessions`, on a deployment that had declared 132.

*Inference, not measured:* that the failure therefore arrives under load as connection refusals rather
than as a pod that will not start. Confirming it needs a load that fills the pools, which a shared
cluster cannot honestly produce.

### The install, as it actually ran

```sh
kubectl create namespace taisce
kubectl -n taisce create secret generic model-key --from-file=apiKey=/dev/stdin   # key on stdin
helm install memory oci://ghcr.io/ensera-ai/charts/taisce --version 0.5.1 -n taisce -f cluster.yaml
```

with `cluster.yaml` carrying the shrunk instance size, the inference endpoint, model, allowlist and
the name of that Secret, and leaving the topology at the chart's default. The chart resolved its image
to `ghcr.io/ensera-ai/taisce:v0.5.1` and pulled it without a credential; the `v` prefix D5 describes is
correct in this chart.

## What the journey did

| Step | Result |
|---|---|
| `GET /health`, `GET /ready` | 200 |
| `POST /v1/observations` | 201, 512 turns across several batches, none refused |
| `GET /v1/freshness` | 200, reporting stored and formed offsets and the parked count |
| `POST /v1/recalls` | 200, answering `named_nothing_known` honestly over an empty fact set |
| Ops centre | signs in on an operator credential and reports the instance |

**Formation did not run, and this is a property of the run rather than of the chart.** The model key
available for this exercise was revoked, so every extraction attempt returned 401 and every turn
failed to form. That makes the numbers above a test of the write path, the read path, the admission
gate, the watermark and the ops centre, and not a test of extraction.

It also exercised two behaviours worth recording, because both are correct and neither is obvious:

- **A turn that cannot form is parked rather than left to freeze the scope.** After six attempts with
  growing backoff the first turn was parked, and the formed watermark advanced past it. The backlog
  behind it kept being attempted.
- **The failure is recorded on the turn.** `memory.observation` carried
  `formation_attempts` and the provider's own refusal text, which is where an operator can find out
  that a key was rejected.

In this release the workers log nothing at all about it. The ops centre does: it showed a growing
`Failed attempts` count and a backlog `behind by` the whole log, which is how the cause was found.

## The availability tests

The claim under test is the one the chart makes for itself: several replicas are a deployment
decision rather than a code change, because each scope is drained under an advisory lock. All three tests
ran with three API replicas and four workers, all four workers contending for one scope.

### Test one: how many workers hold the lock at once

`pg_locks` on the primary was sampled for the lock whose key is `hashtext('memory/default')`, 420
times over thirteen minutes, while the four workers drained a backlog of several hundred turns.

| | |
|---|---|
| Samples with the lock held by nobody | 260 |
| Samples with exactly one granted holder | 159 |
| Samples with more than one granted holder | **0** |
| Distinct backends that held it granted | 11: ten belonging to worker pods, one the measuring session |

**The lock was never granted to two sessions at once.** It moved between workers rather than being
monopolised by one, which is what a lock taken per drain and released at the end of it should look
like. The ten worker backends span five pods — the four the deployment started with, and the
replacement for the one killed in the third test.

One sample of the 420 returned two rows, and it is worth saying what it was rather than rounding it
away. At 08:50:14 the sample read `1219:true, 4687:false`: a worker holding the lock granted, and a
second session waiting. The waiting session was the measuring session, a tick before it acquired the
lock for the second test below. It waits because it asks with `pg_advisory_lock`, which queues. The
workers ask with `pg_try_advisory_lock`, which does not: a worker that loses is refused immediately
and moves on to the next scope. No sample ever showed a worker waiting.

The losers are visible in `pg_stat_activity` too. At a moment when one worker held the lock, two of
the other three had `SELECT pg_try_advisory_lock(hashtext($1)::bigint)` as their last statement and
were idle: they had asked, been refused, and stopped.

Sampling shows what happened; it cannot show what would have happened. That is what the second test
is for.

### Test two: take the lock away from them

A session outside the deployment took the same lock with `pg_advisory_lock(-1388333988)` and held it
for 90 seconds, while a fresh backlog sat waiting and all four workers polled for work every five
seconds. Formation was measured as the total of `formation_attempts` across every turn, which rises
once per extraction attempt whether the attempt succeeds or fails.

| Window | Formation attempts in it |
|---|---|
| 20 s before, workers free | 143 |
| **70 s with the lock held by the outside session** | **0** |
| 30 s after it was released | 350 |

**Not one attempt was made by any of the four workers for as long as something else held the lock,
and they resumed within seconds of its release.** That is the claim demonstrated rather than
inferred: formation is gated on this lock, every worker honours the same key, and no worker has a
path around it.

It also rules out the reading that test one invited — that one worker happened to be doing all the
work while the others were idle for some unrelated reason. They were not idle. They were refused.

### Test three: kill a worker while it is forming

The worker holding the scope lock was identified from `pg_locks` joined to `pg_stat_activity`, and
its pod was force-deleted with no grace period — an abrupt death rather than a shutdown, so the
process never ran its deferred unlock.

| | |
|---|---|
| Lock holder | worker `…-sgcwx` on the 32-CPU node, backend pid 1218 |
| Turns stored before the kill | 512 |
| Replacement pod created | 3 s after the kill |
| Lock held by a different backend | 3 s after the kill |
| Backends left behind by the dead pod | 0 |
| Turns stored after | 512 |

**The dead worker's lock was released promptly and formation never stopped.** The deferred unlock in
the drain loop did not run — the process was killed — so the release came from the database noticing
the connection had gone. That is the mechanism that matters, because it is the one that works when
the process has no chance to tidy up.

No turn was lost and none was left in a state nothing would pick up: the turn the dead worker held
stays unformed with its attempt recorded, which is the same state as a turn whose extraction failed,
and the backlog is drained oldest-first regardless of which worker drains it.

**What this test does not cover, and it is the case high availability exists for.** The pod died but
its node did not. When a process dies, the kernel on its node closes its sockets, so the database
learns at once. When a *node* fails — power, kernel panic, a severed network — nothing closes
anything, and the database is left with a backend whose peer will never speak again.

How long that backend holds the scope lock is decided by TCP keepalives, and this deployment sets
none: `tcp_keepalives_idle`, `tcp_keepalives_interval` and `tcp_keepalives_count` are all `0` in the
installed cluster, which means the operating system's defaults, and the database container reports
those as 7200, 75 and 9 — about two hours and eleven minutes before a dead peer is given up on.

*This is inference from two measured settings, not a measured outcome.* Confirming it needs a node
taken away or a network partition imposed, neither of which belongs on a cluster carrying somebody
else's work. It is recorded as #106 because the lock has no lease and no timeout of its own, so
nothing else bounds it — and because the three replicas that would stand in for the lost worker would
each be refused the lock, and would say nothing about it (#105).

### What could not be tested

- **That two workers do not assert the same fact twice.** The lock test shows serialisation; proving
  no duplication requires turns that actually form, which requires a model this run did not have.
  Formation is the half of the write path that reaches a model, and it was never exercised.
- **Anything about how long failover takes, or how much it costs.** The cluster was shared throughout.
  A failover measurement is only meaningful on hardware that can be held still.
- **Loss of a node, as opposed to loss of a pod.** A pod was deleted; no machine was stopped.
- **That the database survives losing the node its volume is on.** Storage here is node-local, so a
  database instance cannot be rescheduled elsewhere and find its data. That is a property of this
  cluster, not of the chart, and it means the three instances protect against a process failing and a
  pod being evicted, not against a disk going away.
- **Whether the over-declared connection pools of #101 actually exhaust the database.** That needs a
  load that fills them.

## What was installed, and what removing it took

For the length of this run the release `memory` held namespace `taisce`: three database instances,
three API replicas, four workers, the management surface with the portal on, one Secret holding the
provider key, and one `toolbox` pod used to drive the journey from inside the cluster. Nothing
outside that namespace was created or changed, and nothing cluster-scoped was needed — the
CloudNativePG operator was already present, and the cluster's image policy applies only to
namespaces that opt in by label, which this one did not.

It was removed at the end of the run. `helm uninstall memory -n taisce` reported one resource kept:

```
These resources were kept due to the resource policy:
[Secret] memory-taisce-credentials
```

So `helm.sh/resource-policy: keep` on the role passwords is now observed behaviour rather than a
reading of the template. Deleting the namespace afterwards took that Secret, the provider key, the
`toolbox` pod and the three volumes with it, and no `PersistentVolume` survived — this cluster's
default storage class reclaims on delete.

That makes the uninstall and the removal two different things, and only one of them is in the chart's
documentation. An operator who uninstalls in order to reinstall is protected by the kept Secret,
which is what the annotation is for. An operator who uninstalls in order to be rid of the deployment
is left holding the database role passwords in a namespace with no database in it, and `kubectl get
all` does not list Secrets, so the usual check for leftovers does not show them. The gap is in the
documentation rather than the template — the annotation should stay — and it is #108.

The cluster was verified unchanged after the removal: every other namespace present, the six Helm
releases that were there before still deployed, the five CloudNativePG clusters belonging to other
workloads all healthy, both cluster image policies untouched at the age they had before this run, and
all three nodes `Ready`.

## The network policy meets a cluster — 2026-11-12

The chart's policies (D49) are a request to the cluster's network plugin, and a rendering cannot
show that anything honours it. This run applied them to the same three-node cluster, which runs
k3s with its built-in policy controller, in a namespace created for the purpose and removed
afterwards. The deployed instance was not touched.

**What was applied.** The chart's own rendering of the database and the policies for a release
named `np`, two instances, into namespace `taisce-np-check`. Beside it, four pods: one labelled as
the release's API listening on 8080, one labelled as its management surface listening on 8081, one
unlabelled neighbour in the same namespace, and one stranger in a second namespace. The listeners
were stand-ins, because the question is who the network lets through, not what answers.

**The database came up through the policy.** Both instances became ready and the cluster reported
healthy, with the second instance on a different node from the first and `pg_stat_replication`
showing it streaming. So the three callers the database policy names are sufficient: the operator
reached the status port, the joining instance cloned the primary, and replication crossed nodes.

**Who was let through.**

| From | To | With the policies | With them removed |
|---|---|---|---|
| neighbour, same namespace | API, 8080 | connects | — |
| stranger, other namespace | API, 8080 | refused | connects |
| neighbour, same namespace | management, 8081 | refused | connects |
| stranger, other namespace | management, 8081 | refused | connects |
| the release's own API pod | management, 8081 | refused | — |
| the release's API and management pods | database, 5432 | connects | — |
| neighbour, same namespace | database, 5432 | refused | connects |
| stranger, other namespace | database, 5432 | refused | connects |
| any of the three | database status port, 8000 | refused | — |
| `kubectl port-forward` | management, 8081 | answers | — |

"Refused" is a connection attempt that did not complete within four seconds. The last column is the
control: the same probes, a few seconds after deleting the three policies, to show that it was the
policies refusing and not something else about the cluster.

**What was not tested.** A failover or a switchover with the policies in place — the cluster became
healthy, and nobody then took the primary away. The backup plugin, which this run did not configure.
A cluster with a different network plugin. The published image: the listeners were stand-ins, so
this says nothing about the service's own behaviour behind the policy, only about who reaches it.

## The database's certificate is verified — 2026-11-12

The chart's connection strings now say `sslmode=verify-full` against the authority the operator
publishes (D55). Two things were read on the cluster before that could be the default, and one was
checked after.

**How long the authority lives.** The secret `<cluster>-ca` of the running release:

```
subject=OU=taisce, CN=taisce-taisce-db
notBefore=Nov 10 10:40:38 2026 GMT
notAfter=Feb  8 10:40:38 2027 GMT
```

Ninety days. A process that read it once at start would refuse every new connection from the day
it expired, which is why the service reads it again for each connection.

**That verification is available and means something** (2026-11-11, with `psql` in a pod):

```
verify-full, the cluster's authority      FATAL: password authentication failed   (verified, then asked for a password)
verify-full, another authority            SSL error: certificate verify failed
verify-full, right authority, wrong name  server certificate for "taisce-taisce-db-rw" (and 11 other names) does not match host name
```

**The mount and the connection string, exactly as the chart renders them** (2026-11-12). A pod
running as the service's own user, given the volume the chart gives a workload and the chart's
connection string with a password that was wrong on purpose:

```
== what the pod is given, as uid 65532
ca.crt
== the chart's connection string, with a password that is wrong on purpose
psql: error: connection to server at "taisce-taisce-db-rw" (10.43.120.79), port 5432 failed:
FATAL:  password authentication failed for user "taisce_data"
```

One file, readable by that user, and a connection that got as far as being asked who it was — which
is past the certificate. The signing key, which is in the same secret, is not in the pod. The four
workloads rendered for the running release's shape were accepted by a server-side dry run; nothing
on the cluster was changed.

**What was not tested here.** The service itself connecting this way on a cluster: the image that
re-reads the authority is not published yet, so the client above was `psql`. The service's own
behaviour — including that it follows a replaced authority and refuses the old one — is held by
`TestANewConnectionIsVerifiedAgainstTheAuthorityAsItIsNow`, against a real database, with the real
driver. Nobody waited ninety days.
