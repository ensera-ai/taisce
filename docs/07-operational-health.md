<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Readiness and formation health

`GET /health` is process liveness. It always answers 200 while the listener can serve it. It does
not query PostgreSQL or call a provider. `GET /ready` answers 200 (`status: ready`) or 503
(`status: unavailable`). Both are unauthenticated and return no counts, error strings, version,
credentials or configuration. They do not write audit entries.

Readiness checks the memory namespace and the credential table using their separate serving
identities. One check runs at a time, with a one-second total deadline. Success and failure are
cached for one second. Concurrent probes during an uncached check receive 503 immediately;
disconnecting a probe does not cancel or poison the shared check. There is no waiting probe queue.
These are operational defaults, not a measured production service-level objective.

Set `TAISCE_REQUIRE_FORMATION=true` when serving readiness should also require a responsive worker
somewhere in this instance. The default is false, allowing intentionally API-only service while
formation is paused or operated separately. The worker heartbeat must be no more than 330 seconds
old: the normal five-minute turn budget plus scheduling overhead. Stopping all workers therefore
does not immediately fail API readiness; it fails after this detection window and the probe cache.

A responsive worker is not a guarantee that a provider can form every turn. A failed attempt can
leave a responsive worker with parked work, and another responsive worker can keep the fleet check
healthy while one project is stalled. Inspect backlog age, failures and freshness separately.
Poisoned input is not allowed to take the whole API out of service through its readiness signal.

Each worker exposes only `/health` and `/ready` at `127.0.0.1:8081`. `TAISCE_HEALTH_ADDR` may select
another loopback IP/port. Non-loopback binds are refused. It exposes no memory routes. Worker
readiness requires that worker's own processing path to have reported recently and its memory
database to respond; another worker's heartbeat cannot satisfy it. A missing model configuration
leaves the worker live but unready. No independent heartbeat timer can keep a stuck drain healthy.

The image includes its own probe commands:

```sh
taisce probe                  # local API readiness
taisce probe --live           # local API liveness
taisce probe --worker         # local worker readiness
taisce probe --worker --live  # local worker liveness
```

Probes use a two-second HTTP deadline, never follow redirects and never use an HTTP proxy. Their
destination must be loopback. The API address comes from `TAISCE_ADDR`; wildcard listeners map to
loopback for probing. Compose uses readiness with a three-second outer timeout and three retries.
It marks an unavailable container unhealthy; [Docker's restart policy](https://docs.docker.com/engine/containers/start-containers-automatically/)
reacts to process exit rather than restarting a running container merely because it is unhealthy. A future Helm chart must use liveness and
readiness separately; chart delivery/failover testing remains tracked in #76.

For detailed diagnostics, set the explicit operator connection `TAISCE_ADMIN_DSN` and run:

```sh
taisce health
```

This command has a ten-second deadline and returns one JSON aggregate snapshot. It includes pending
turns and limits, parked count, oldest pending timestamp, latest heartbeat/progress/failure, formed
and failed-attempt counters, and database/cluster client-connection counts with the configured
`max_connections`. [Reserved connection slots](https://www.postgresql.org/docs/17/runtime-config-connection.html)
and other databases also consume capacity; this is
database pressure, not a measurement of an API process's pool wait queue.

No subject IDs, project labels, observation IDs, message content or provider errors are selected.
The command requires explicit operator access; project API credentials cannot read these aggregates.
It adds no public metrics endpoint. The oldest-pending query uses a partial timestamp index; parked
count uses the parked index and is subject to the command deadline. Raising ingestion capacity can
increase the work required to count parked turns.

Formation metrics are maintained in one instance row. They count outcomes reported by drivers after
an attempt, and are best-effort operational telemetry rather than a transactional audit of every
formation. Failed recording is logged without error content and does not undo formed memory.
Historical attempts before migration 0027 and direct library workers without a driver are excluded.
The count does not classify every failure as a provider failure: parsing, extraction and other
formation errors can contribute. Erasure does not reset these content-free aggregate counters.

Use the [ingestion runbook](06-ingestion-budget.md) to inspect and recover parked turns. Restore the
provider before retrying them; a fresh heartbeat alone is not evidence that an outage is resolved.
