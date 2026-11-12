<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# The Helm chart

The chart is `deploy/helm/taisce`.

**Highly available by default**: three PostgreSQL instances under the CloudNativePG operator,
two API replicas spread across nodes behind a disruption budget, two workers, the management
surface in-cluster only, the portal off. The compose file is one machine and says so; this is the
shape a deployment has. **No RTO or RPO is claimed**: this is a topology with a failover path, and
the recovery time is a number the failover script measures on the cluster that runs it.

## Install

The operator first, because it is the machinery that knows how to promote a replica:

```sh
kubectl apply --server-side -f https://raw.githubusercontent.com/cloudnative-pg/cloudnative-pg/release-1.27/releases/cnpg-1.27.0.yaml
```

Then the namespace, then the provider key inside it, then the install. That order is not a style:
the key is a Secret you create and a Secret lives in a namespace, so an install that creates the
namespace itself forecloses the only point at which the Secret could exist. A deployment installed
without it comes up, and its workers fail to reach a model until somebody works out why.

```sh
kubectl create namespace taisce
```

```sh
kubectl -n taisce create secret generic model-key --from-literal=apiKey="$YOUR_PROVIDER_KEY"
```

```sh
helm install memory deploy/helm/taisce -n taisce \
  --set inference.endpoint=https://models.example/v1 \
  --set inference.extractorModel=Qwen/Qwen3.8-27B \
  --set inference.allowlist=models.example \
  --set inference.apiKeySecret=model-key \
  --set postgresql.cnpg.backup.objectStore=memory-backups
```

The last line is the one setting with no default: a release names where its backups go, or says it
has none, and one that says neither is refused. [Backups](#backups-say-where-they-go-or-say-there-are-none)
below covers both answers.

No credential exists after the install. Bootstrap provisions the instance and mints nothing, because
a Job's output is a container log, and a cluster ships its logs somewhere this release did not
choose. Mint the first ones yourself — the output is streamed to your terminal and to no log:

```sh
kubectl -n taisce exec deploy/memory-taisce-manage -- /taisce operator issue ops
kubectl -n taisce exec deploy/memory-taisce-manage -- /taisce credential issue owner -project default -access read_write
```

The second is the project owner's, asked for with `read_write` because it is the one that can erase.
Leave `-access` off for a key an agent holds.

`inference.allowlist` names the hosts that may receive somebody's words. Empty permits nothing, so
formation does not start — which is the default, because an allowlist that permits everything when
unset is one forgotten variable from being no allowlist.

### Reaching it

The chart exposes nothing, which is the right default and leaves first contact to you. On a cluster
with no ingress controller — which is most clusters somebody is trying this on — a port-forward is
all it takes, one per surface:

```sh
kubectl -n taisce port-forward svc/memory-taisce-api 8080:8080
```

```sh
kubectl -n taisce port-forward svc/memory-taisce-manage 8081:8081
```

The memory routes are then at `http://localhost:8080` with the project token as a bearer, and the
ops centre at `http://localhost:8081/portal` with the operator token — once `manage.portal=on`,
which it is not by default. `ingress.api` and `ingress.portal` are for publishing either of them
properly, and **What is exposed** below covers that.

Bootstrap prints the first project token and the first operator token once; it is a Job named by the
release revision, so every upgrade runs it again and it applies pending migrations. The serving
pods restart until it has completed on a fresh cluster. Role passwords are generated on first
install and kept (`helm.sh/resource-policy: keep`); an existing secret carrying `dataPassword` and
`controlPassword` is used when `credentials.existingSecret` names it. The provider key is a secret
you create (`inference.apiKeySecret`, key `apiKey`) and is never a value.

**That last command is not optional, and it is not repeatable.** The two tokens exist in the Job's
log and nowhere else, because only their digests are stored. The Job is named by revision, so the
next upgrade takes the previous Job out of the release and Helm deletes it with its log — and the
new revision's bootstrap finds the instance already reachable, mints nothing, and has no token to
print. Changing a replica count is enough. Copy both tokens somewhere before anything else happens.

The database image must provide the `vector` extension; `btree_gist` and `pg_stat_statements` are
contrib modules of every build. The default is the operator's image family that bundles pgvector;
a deployment that builds its own substrate image sets `postgresql.cnpg.image`.

### If a credential was lost

Mint a replacement; do not go looking for the old one. The administrative database connection
outlives every credential, and the management surface is the workload that holds it:

```sh
kubectl -n taisce exec deploy/memory-taisce-manage -- /taisce operator issue replacement
kubectl -n taisce exec deploy/memory-taisce-manage -- /taisce credential issue replacement -project default
```

**Minting does not revoke what was lost.** A token nobody can find is still a token that works for
whoever has it. `/taisce credential list` names what exists and `/taisce credential revoke <id>`
closes it, and doing that is part of recovering rather than an optional tidy-up.

This is why nothing in the chart keeps a token at rest, and why nothing in it prints one. A Secret
holding a token would turn a credential shown for a moment into one that exists for the life of the
deployment, readable by anyone who can read the namespace. A Job printing one is worse: its output is
copied to the cluster's log store as it is written, and kept there under retention and access rules
this release did not choose — which is where the first operator token of an earlier install was
found, after the Job that printed it had been deleted. The two commands above give the token to the
person who asked for it and to nothing else
([D15](../../docs/decisions/D15.md) and [D42](../../docs/decisions/D42.md)).

**A role password is kept and a token is not, and the difference is who has to read it back.** The
deployment itself presents `dataPassword` and `controlPassword` on every connection it opens, so
something it can read has to hold them — which is the Secret **Removing it** below describes. Nobody
in the deployment ever presents a token: the holder does, and the service compares a digest. So
there is nothing a stored token would be read back *for*, and storing it would buy a convenience at
the cost of a credential.

**What this does not cover.** With `manage.enabled=false` there is no workload holding the
administrative connection, so there is nothing to exec into: enable the management surface, mint,
and disable it again. Nothing brings back a token itself — it is gone, which is the property that
makes a stolen database useless for impersonation. And nothing here stops a token being pasted
somewhere that is logged: a terminal with session recording, a shell history, a ticket.

### From a published chart

Each release publishes the chart to `oci://ghcr.io/ensera-ai/charts/taisce`, versioned without the
tag's `v`; it installs the image published beside it, named by the release tag. Pass the same values
as above:

Omitting `--version` installs the newest published chart, which is what you want unless you are
pinning. The namespace and the key come first here too:

```sh
helm install memory oci://ghcr.io/ensera-ai/charts/taisce -n taisce --set inference.apiKeySecret=model-key \
  --set postgresql.cnpg.backup.objectStore=memory-backups
```

Pin with `--version <chart version>` — plain SemVer, the release tag without its `v`. No version is
named in this file on purpose: one written here is one that goes stale and leaves a reader unable to
tell whether it is the current release, an example, or the last version known to work.

Charts 0.3.0 and 0.3.1 ask for their image without the `v` and so name a tag that was never pushed
([D5](../../docs/decisions/D5.md)); installing either needs `--set image.tag=v0.3.1`.

## What this deployment states about itself

Two things the chart used to leave to whatever was underneath it, because a limit nobody chose is
not a limit.

**What each process may open against the database.** `api.memoryPool`, `api.registryPool`,
`worker.memoryPool` and `manage.adminPool` are on every connection string the chart builds. Told
nothing, a pool sizes itself at the number of CPUs the process can see — so the same release
declared 32 connections on a 32-CPU node and 4 on a 4-CPU one, and for the API that is not only a
pool setting: admission bounds concurrent work at one less than the memory pool, half of that for
any one project and half again for any one credential. Three replicas behind one Service admitted
31, 11 and 3 requests at once, and which replica the Service picked decided whether a request was
served or shed. At the defaults each API replica serves eleven at once, five per project, two per
credential.

Work the arithmetic out before scaling: `api.replicas × (memoryPool + registryPool) + worker.replicas
× worker.memoryPool + manage.adminPool + 3` for bootstrap, which must fit `max_connections` with
headroom for operators and migrations. **The chart refuses to render when it does not**, naming both
numbers — every process already refuses to start when its own pool cannot fit the free slots, but
pools fill lazily, so over-declaration is invisible at install and arrives later as connection
refusals on a deployment that came up green.

**How long a lost node holds a project's formation.** `tcp_keepalives_idle` is 60 seconds on the
database the chart declares. Formation holds a session advisory lock for the whole drain of a scope,
and a session lock is released when its session ends and by nothing else — so "the holder has gone"
is a question only the database answers. A process dying answers it in about three seconds, because
the kernel on its node closes the sockets. A node going away answers it only when the keepalive
gives up, and left unset that is the operating system's default: on Linux, about two hours and
eleven minutes, during which every other worker asking for that scope is refused and the project
forms nothing. Raise it on a network where a healthy peer can be silent for longer.

With `postgresql.mode=external` neither applies. The DSNs come from a secret this chart never reads,
so the pool ceilings are yours to put in them, and the keepalives are yours to set on your server.

## Backups: say where they go, or say there are none

A release that says nothing about backups
is refused before anything is applied:

```text
this release declares 3 database instance(s) and no backup. Replicas protect against losing a node
and nothing else ...
```

Three instances and a failover path protect against losing a node. An erasure of the wrong subject,
a migration that goes wrong, a dropped schema and an uninstall are each on every instance within a
second, and none of the three can give them back.

- **`postgresql.cnpg.backup.objectStore`** names an `ObjectStore` (`barmancloud.cnpg.io`) in the
  release's namespace. The cluster then archives its write-ahead log there continuously, and a base
  backup is taken daily at 02:00 UTC and once when the schedule is created.
  `postgresql.cnpg.backup.schedule` changes when. The Barman Cloud plugin must be installed beside
  the operator.
- **`postgresql.cnpg.backup.acknowledgedNone=true`** states that this deployment has no backup, on
  purpose. It renders, and the install notes say so every time.

The `ObjectStore` is yours to create: the bucket, its credentials and how long backups are kept
belong to whoever owns the storage, and a chart that wrote one would have to hold those credentials.

What the chart cannot check, because it is true or false on a cluster and not in a rendering: that
the object store exists, that its credentials work, and that a backup has completed. The install
notes print the two commands that do. And a backup nobody has restored from is a hope — rehearse a
restore into a second cluster before depending on this one.

**A restore brings back what was erased.** Everything erased since the backup was taken is in the
backup, and nothing re-applies an erasure after a restore. Until that changes, keep your own record
of erasure requests and repeat them after restoring.


## Shrinking it, deliberately

```sh
helm install memory deploy/helm/taisce -n taisce -f deploy/helm/values-small.yaml
```

One database instance, one API replica, one worker, a 10Gi volume and a 2Gi memory limit: a shape
that fits a cluster already carrying other work. The default topology asks for 6 CPUs and 750Gi of
reservable capacity across three database instances, which an ordinary cluster does not have spare,
and the default is that way round so nobody deploys the small shape believing it was the large one.
`values-small.yaml` is a file in the repository rather than a paragraph here, so it renders and is
tested.

**Lowering the memory limit is enough on its own.** `shared_buffers`, `effective_cache_size`,
`maintenance_work_mem`, `max_wal_size` and the shared-memory volume are fractions of
`postgresql.cnpg.resources.limits.memory`, so they follow it and cannot disagree with it. They used
to be written beside it, which meant lowering the limit alone left a database configured to reserve
more than its container allowed — it does not start, and nothing said so. State any of them in
`postgresql.cnpg.parameters` to override the derivation; the limit must be written in `Gi` or `Mi`,
and a unit the chart cannot read is refused rather than guessed at.

An existing database instead of the operator's: `postgresql.mode=external` with a secret carrying
`adminDSN`, `memoryDSN`, `registryDSN`, `dataPassword` and `controlPassword`.

## What is exposed

Nothing, until asked. `ingress.api` publishes the memory routes. `ingress.portal` publishes the
portal at `/portal` and is refused unless `manage.portal=on`, so an ingress can never point at the
management surface's door by accident. The management surface itself is a ClusterIP service.

**Give the console a hostname of its own.** The memory hostname is the one agents hold a bearer
token for; a console on the same origin puts project administration behind a name a stolen project
token already knows. On its own hostname the console answers at the bare root — `https://ops.example/`
redirects into `/portal/` — so the address an operator is given is the address that works. Only that
exact path and `/portal` are routed; the management API on the same port is not published. On a
hostname shared with the memory API the root stays the API's and the console is at `/portal`.

### On a Gateway API cluster

Each surface is an `Ingress` by default and an `HTTPRoute` when asked:

```yaml
manage:
  portal: "on"
ingress:
  portal:
    enabled: true
    kind: HTTPRoute
    host: ops.example
    parentRefs:
      - {name: eg, namespace: envoy-gateway-system, sectionName: console-https}
    certificateSecret: console-tls
networkPolicy:
  manage:
    from:
      - namespaceSelector: {matchLabels: {kubernetes.io/metadata.name: envoy-gateway-system}}
```

- `parentRefs` is required. A route attached to no gateway is accepted by the cluster and serves
  nothing, so it is refused at render.
- `certificateSecret` names a secret **in this namespace** that the gateway's listener presents. It
  renders a `ReferenceGrant` letting the gateway's namespace read that one secret. Without a grant
  the listener reports itself unprogrammed, after everything else looks healthy; with a grant that
  names the kind and no object — the one a first example produces — the gateway can read every
  secret in the namespace, the database's passwords included. Leave it empty when the certificate
  lives beside the gateway.
- **The chart does not render the `Gateway` or its listener.** Those belong to whoever owns the
  gateway. The listener's `hostname`, its `tls.certificateRefs` and its `allowedRoutes` are yours to
  write, and the certificate itself is whatever issues certificates on your cluster.
- The gateway's proxy pods run in a namespace the chart cannot derive from `parentRefs` — it is not
  always the namespace the `Gateway` object is in — so `networkPolicy` has to name it, as above.

### Who may connect, inside the cluster

A ClusterIP service is reachable by every pod in the cluster, so the chart also says who may open a
connection, with NetworkPolicy, on by default:

| To | Admitted | Port |
|---|---|---|
| The memory API | pods of this namespace, and whoever `networkPolicy.api.from` names | 8080 |
| The management surface | nobody, until `networkPolicy.manage.from` names somebody | 8081 |
| The workers, the bootstrap Job | nobody; they serve nothing | — |
| The database | this release's pods; its own instances; the operator's namespace (status port only) | 5432, 8000 |

`kubectl exec` and `kubectl port-forward` go through the cluster's API and are not subject to any of
this, which is how an operator reaches a management surface that admits nobody.

**A gateway or ingress controller in a namespace of its own is a stranger until it is named:**

```yaml
networkPolicy:
  api:
    from:
      - podSelector: {}
      - namespaceSelector: {matchLabels: {kubernetes.io/metadata.name: ingress-nginx}}
```

Enabling `ingress.api` or `ingress.portal` without naming where its traffic comes from is refused at
render, because it would otherwise install and time out. A route created outside the chart — a
Gateway API `HTTPRoute`, say — is not something the chart can see: **name its gateway's namespace
before upgrading a deployment that is reached through one**, or the upgrade closes it.

What this does not do: it restricts nothing the pods call, and on a cluster whose network plugin
enforces no NetworkPolicy it restricts nothing at all. The only proof that it is enforced is a
refused connection — from a pod in another namespace, `nc -z -w 4 <release>-taisce-manage.<namespace> 8081`
should time out. If the operator runs somewhere other than `cnpg-system`, say so in
`networkPolicy.database.operatorNamespace`, or the database will run with nobody managing it.

## What an upgrade promises

`helm upgrade` runs the new revision's bootstrap Job, which migrates the schema, and rolls the pods
at the same time. Nothing orders the two, and nothing needs to:

- **A new pod is not ready until the schema is the one it was built for**, and says which version it
  is waiting for.
- **A pod of the previous release keeps serving on the new schema** until the roll replaces it. A
  release's migrations are checked against the release before it; what a release removes is
  something the one before had already stopped using.

That holds from one release to the next. **Upgrade one release at a time.** An upgrade that skips a
release is not promised to roll cleanly: pods two releases old may fail requests until they are
replaced. To skip, scale `api`, `worker` and `manage` to zero for the upgrade.

Going back is the same promise read the other way: the previous release's image runs on the new
schema, so `helm rollback` by one release does not need a restore. The schema itself never goes
backwards, and the promise is about the schema: rows the newer release wrote while it ran are the
one thing the older one was never tested against. [The migrations page](../../docs/postgresql/migrations.md#what-an-upgrade-promises) has
the rule and what it does not cover.

## Upgrading from 0.6

Four things in 0.7 change what an upgrade does. Each is a default that was chosen for a new
install; an existing release has to say what it wants before `helm upgrade`, or the upgrade either
refuses or changes who can reach it.

| Before upgrading | Why | What happens if you do not |
|---|---|---|
| Set `postgresql.cnpg.backup.objectStore`, or `postgresql.cnpg.backup.acknowledgedNone=true` | A release now states where its backups go, or that it has none | The render refuses, and nothing is changed |
| Name your gateway's or ingress controller's namespace in `networkPolicy.api.from` — and in `networkPolicy.manage.from` if it routes to the ops centre | Network policy is on by default, and a controller in a namespace of its own is a stranger until named | The upgrade succeeds and requests through the gateway time out |
| Upgrade the chart and the image together; do not pin `image.tag` to a 0.6 image under a 0.7 chart | The chart's connection strings now verify the database's certificate, and only a 0.7 image re-reads the authority when it is replaced | It works, and stops connecting about ninety days after the pods started |
| Say so in `networkPolicy.database.operatorNamespace` if the CloudNativePG operator is not in `cnpg-system` | The database admits its operator by namespace | The database runs with nobody managing it |

And four things that behave differently afterwards, with nothing to do beforehand:

- **Bootstrap mints no credential on a cluster.** Existing credentials are untouched. A new release
  mints its first ones with the two `kubectl exec` commands the install notes print.
- **An operator credential minted from now on stops working after ninety days**, and says when.
  `-no-expiry` mints a permanent one. Credentials that already exist have no date and keep working.
- **A new project keeps each person's statements separate**; projects that already exist keep
  sharing one world, as they did. `taisce project statements <name> shared|separate` sets it, until
  the project holds a fact.
- **A writer refused for a full backlog is told how long a turn takes to form**, from 1 to 30
  seconds, where it was always told 1.

The schema moves forward by six migrations when the bootstrap Job of the new revision runs. A pod of
the new release is not ready until it has, and says which version it is waiting for.

A `docker compose` deployment has none of the four steps: it has no network policy and no operator
database. Pull the new `compose.yaml`, and the two credential changes above apply.

## Removing it

```sh
helm uninstall memory -n taisce
kubectl delete namespace taisce
```

Both steps, if the deployment is meant to be gone. The uninstall removes everything the chart
created except one Secret, and says so as it goes:

```
These resources were kept due to the resource policy:
[Secret] memory-taisce-credentials
```

That Secret holds `dataPassword` and `controlPassword`, the two role passwords generated on first
install, and it is annotated `helm.sh/resource-policy: keep` deliberately. The reason is not the
uninstall: the generated Secret leaves the release's manifest set the moment
`credentials.existingSecret` is named or `postgresql.mode` becomes `external`, and without the
annotation that upgrade would delete the passwords of a database that is still running.

What the annotation costs is paid at removal. `controlPassword` is the role the write path uses, and
a role's password belongs to the database rather than to the release, so it stays valid against any
database that accepts that role — one restored from a backup of this deployment, or installed again
into the same namespace, where the chart reads the Secret it finds rather than generating new
passwords. An operator who uninstalls and stops there has left a working write credential in the
cluster with nothing currently listening on it, which is the kind nobody thinks to rotate.
`kubectl get all` does not list Secrets, so the obvious check for leftovers does not show it.
Deleting the namespace is what removes it.

Uninstalling in order to reinstall is the case the annotation is for, and it is the first command on
its own. It leaves the passwords behind on purpose; this is a note about knowing which of the two
states a cluster is in, not an argument for removing the annotation.

## The failover measurement

`deploy/helm/failover.sh` builds the image, creates a kind cluster with the operator, installs the
chart, then deletes the primary PostgreSQL pod while polling the API twice a second, and reports how
long the API answered nothing. It runs anywhere kind runs; the number it prints is for that machine
and that cluster and is written into the closing comment of #76 with the machine named, never quoted
as a property of the chart.

### What it measured, once

Five runs on one machine, the primary force-deleted each time from a fresh install of the chart's
default topology (three CloudNativePG instances, two API replicas, no worker, small resources):

| Run | API answered nothing | Kill to answering on the new primary | Samples |
|---|---|---|---|
| 1 | 4.1 s | 6.4 s | 16 at 0.42 s |
| 2 | 2.0 s | 4.4 s | 15 at 0.31 s |
| 3 | 3.3 s | 5.5 s | 15 at 0.39 s |
| 4 | 1.7 s | 4.2 s | 13 at 0.33 s |
| 5 | 3.2 s | 5.6 s | 15 at 0.40 s |

Median 3.2 s of outage and 5.5 s to the promoted instance; the spread is 1.7–4.1 s and 4.2–6.4 s.
The API replicas' restart counts were unchanged across every kill, so a failover of this length costs
no restart and no backoff — the process keeps its port and its pool and reconnects.

**The machine.** Apple M1 Max, 64 GiB, macOS 26.6.2; Docker 29.7.2 inside colima on the macOS
Virtualization framework; kind v0.33.0 running Kubernetes v1.37.0 as three containers; CloudNativePG
1.27. Every component of the cluster shares one laptop's cores and one virtual machine's disk, which
is not the arrangement anybody runs and is exactly why this is a number for this cluster and not a
property of the chart. Read it as evidence that the failover path works and roughly how long it
takes, never as an RTO.

**What it does not measure.** Data loss: nothing was written during the window, so this says nothing
about an RPO. Load: one probe every third of a second is not traffic. A real node failure: the pod
was force-deleted, which is faster than a machine that stops answering and slower than nothing.

Proved by `deploy/helm/chart_test.go`: the defaults render three instances, two replicas, no ingress,
generated and kept passwords, non-root containers, connections that verify the database's
certificate (`deploy/helm/databasetls_test.go`); the shrunk and external
shapes render only when asked; the portal ingress is refused while the portal is off. `deploy/helm/networkpolicy_test.go`
holds who is admitted where, that an emptied list admits nobody, and that an ingress nobody could
arrive through is refused.
