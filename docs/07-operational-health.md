<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Readiness and health

Taisce gives you three ways to see whether it is working: a liveness check, a readiness check, and
an operator health report. Use the first two for your orchestrator's probes, and the third when you
need to know why memory is not forming.

## Liveness and readiness

Every process answers two unauthenticated routes:

- `GET /health` returns `200` while the process can answer at all. It does not touch the database or
  the model.
- `GET /ready` returns `200` with `{"status":"ready"}` when the process can serve, or `503` with
  `{"status":"unavailable"}` when it can't.

Neither route returns counts, errors, versions or configuration, and neither writes to the audit log.

```sh
curl -s http://127.0.0.1:8080/ready
```

```json
{"status":"ready"}
```

For the API, "ready" means it can reach both the memory schema and the credential registry. The
check runs at most once a second and the result is cached for one second. A probe that arrives while
a check is already running gets `503` straight away instead of queueing.

### Require a working worker

By default the API is ready even when no worker is running. That lets you run the API on its own
while formation is paused. To make API readiness also require a live worker somewhere in the
instance, set:

```sh
TAISCE_REQUIRE_FORMATION=true
```

A worker counts as live if it reported in the last 330 seconds (the five-minute formation budget plus
some slack). Stopping every worker therefore fails API readiness only after that window.

### Worker and management listeners

A worker serves only `/health` and `/ready`, on `127.0.0.1:8082` by default. Change it with
`TAISCE_HEALTH_ADDR`, which must be a loopback address. A worker is ready when its own processing
loop reported recently and its database answers. A worker with no model configured stays live but
never becomes ready.

The management process (`TAISCE_ROLE=manage`) has its own listener, set by `TAISCE_MANAGE_ADDR`
(default `127.0.0.1:8081`).

## Probe from inside the container

The image has no shell or curl, so the binary probes itself:

```sh
taisce probe                  # API readiness
taisce probe --live           # API liveness
taisce probe --worker         # worker readiness
taisce probe --worker --live  # worker liveness
taisce probe --manage         # management readiness
taisce probe --manage --live  # management liveness
```

The probe exits `0` on success and non-zero otherwise. It only talks to loopback addresses, never
follows redirects, ignores HTTP proxies and gives up after two seconds. The API address comes from
`TAISCE_ADDR` (default `:8080`); a wildcard listener is probed on loopback.

Compose runs the matching readiness probe as each container's healthcheck (`taisce probe`,
`taisce probe --worker`, `taisce probe --manage`), every 10 seconds with a 3-second timeout and 3
retries. Compose marks a failing container unhealthy, but Docker's restart policy acts on process
exit, not on health, so an unhealthy container is not restarted for you. The Helm chart runs readiness and liveness as separate probes for the API,
worker and management deployments.

## The operator health report

For the detail behind a failing probe, set `TAISCE_ADMIN_DSN` and run:

```sh
taisce health
```

In a terminal it prints a formatted summary. Piped, or with `TAISCE_OUTPUT=json`, it prints one JSON
object:

```json
{
  "pending": 12,
  "pending_limit": 4096,
  "project_pending_limit": 512,
  "parked": 0,
  "oldest_pending_at": "2026-09-11T08:14:03Z",
  "heartbeat_at": "2026-09-11T08:15:40Z",
  "worker_responsive": true,
  "last_progress_at": "2026-09-11T08:15:38Z",
  "last_failure_at": null,
  "formed_count": 5310,
  "failed_attempts": 7,
  "database_connections": 18,
  "cluster_connections": 21,
  "max_connections": 100,
  "reserved_connections": 3
}
```

The report holds aggregate counts only: no subject IDs, project names, observation IDs, message
content or provider errors. Project credentials can't read it. The command gives up after ten
seconds.

## Watch out for

- **A live worker is not a working model.** A worker can report in on time while every turn it tries
  fails and gets parked. One healthy worker also keeps the instance-wide check green while one
  project is stuck. Look at `parked`, `oldest_pending_at` and `last_failure_at` as well.
- **A fresh heartbeat doesn't mean an outage is over.** Fix the provider first, then retry parked
  turns with the [ingestion backlog guide](06-ingestion-budget.md).
- **Connection counts are database pressure.** They cover the whole database and cluster, not one
  API process's pool. Subtract `reserved_connections` from `max_connections` to see the real limit.
- **Formation counters are best-effort.** `formed_count` and `failed_attempts` are updated after each
  attempt. They are not a transactional record of every formation, and `failed_attempts` includes
  parsing and extraction failures as well as provider failures. Erasure does not reset them.
- **Bad input can't take the API down.** A turn that keeps failing parks. It never flips the API's
  readiness.
- **No numbers have been measured.** The one-second cache and the 330-second window are defaults,
  not a measured service level.

## How it works

The API checks the memory schema and the credential registry using the same separate database
identities it serves with, so a probe tests the real permissions. Only one check runs at a time, and
a probe that disconnects can't cancel or poison the shared result.

Worker readiness is tied to the worker's own processing loop, not to a separate timer, so a stuck
loop can't keep reporting healthy. The health report reads one instance-level row for the formation
counters and uses partial indexes for the oldest pending turn and the parked count.
