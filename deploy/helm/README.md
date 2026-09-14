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
helm install memory deploy/helm/taisce -n taisce --create-namespace \
  --set inference.endpoint=https://models.example/v1 \
  --set inference.extractorModel=Qwen/Qwen3.8-27B \
  --set inference.allowlist=models.example
kubectl -n taisce logs job/memory-taisce-bootstrap-1 | grep token
```

Bootstrap prints the first project token and the first operator token once; it is a Job named by the
release revision, so every upgrade runs it again and it applies pending migrations. The serving
pods restart until it has completed on a fresh cluster. Role passwords are generated on first
install and kept (`helm.sh/resource-policy: keep`); an existing secret carrying `dataPassword` and
`controlPassword` is used when `credentials.existingSecret` names it. The provider key is a secret
you create (`inference.apiKeySecret`, key `apiKey`) and is never a value.

The database image must provide the `vector` extension; `btree_gist` and `pg_stat_statements` are
contrib modules of every build. The default is the operator's image family that bundles pgvector;
a deployment that builds its own substrate image sets `postgresql.cnpg.image`.

### From a published chart

Each release publishes the chart to `oci://ghcr.io/ensera-ai/charts/taisce`, versioned without the
tag's `v`; it installs the image published beside it, named by the release tag. Pass the same values
as above:

```sh
helm install memory oci://ghcr.io/ensera-ai/charts/taisce --version 0.4.0 -n taisce --create-namespace
```

Charts 0.3.0 and 0.3.1 ask for their image without the `v` and so name a tag that was never pushed
([D5](../../docs/01-decisions.md)); installing either needs `--set image.tag=v0.3.1`.

## Shrinking it, deliberately

```sh
helm install memory deploy/helm/taisce --set postgresql.cnpg.instances=1 --set api.replicas=1 --set worker.replicas=1
```

That is a single point of failure in every tier and the chart renders it as asked. The default is
the other way round so that nobody deploys the small shape believing it was the large one.

An existing database instead of the operator's: `postgresql.mode=external` with a secret carrying
`adminDSN`, `memoryDSN`, `registryDSN`, `dataPassword` and `controlPassword`.

## What is exposed

Nothing, until asked. `ingress.api` publishes the memory routes. `ingress.portal` publishes the
portal at `/portal` and is refused unless `manage.portal=on`, so an ingress can never point at the
management surface's door by accident. The management surface itself is a ClusterIP service.

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
generated and kept passwords, non-root containers, TLS to the database; the shrunk and external
shapes render only when asked; the portal ingress is refused while the portal is off.
