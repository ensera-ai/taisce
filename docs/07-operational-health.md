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

For the API, "ready" means it can reach both the memory schema and the credential registry, and
that the schema has been migrated at least as far as the binary was built for. During an upgrade a
new process can start before the migration has committed; it answers `503`, takes no traffic, and
becomes ready on its own when the migration lands. The probe does not say why — it says nothing but
ready or not — so the reason is in the process's log, once when the wait starts and once when it
ends:

```text
not ready: the schema is older than this binary was built for: it is at version 75 and this binary
was built for version 76; waiting for the migration
```

The check runs at most once a second and the result is cached for one second. A probe that arrives while
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
  "stood_down": 118,
  "last_stood_down_at": "2026-09-11T08:15:39Z",
  "failing_projects": 0,
  "database_connections": 18,
  "cluster_connections": 21,
  "max_connections": 100,
  "reserved_connections": 3
}
```

`stood_down` is how many times a worker found a project already being formed by another replica and
waited. It is the evidence that several workers are serialising on the per-project lock rather than
that only one of them is alive, and it stays at zero on a single-replica deployment. `failing_projects`
is how many projects hold a turn that has been tried and did not form.

The report holds aggregate counts only: no subject IDs, project names, observation IDs, message
content or provider errors. Project credentials can't read it. The command gives up after ten
seconds.

## Watch out for

- **A live worker is not a working model.** A worker can report in on time while every turn it tries
  fails and gets parked. One healthy worker also keeps this instance-wide check green while one
  project is stuck: it reports on the instance, and that is all it can report on. Look at `parked`,
  `oldest_pending_at` and `last_failure_at` as well, and at `failing_projects`, which counts the
  projects whose backlog holds a turn that has been tried and did not form — the one number here
  that is per project rather than per instance. The ops centre's overview reads the same count and
  names the projects in its table.
- **A fresh heartbeat doesn't mean an outage is over.** Fix the provider first, then retry parked
  turns with the [ingestion backlog guide](06-ingestion-budget.md).
- **Connection counts are database pressure.** They cover the whole database and cluster, not one
  API process's pool. Subtract `reserved_connections` from `max_connections` to see the real limit.
- **Formation counters are best-effort.** `formed_count` and `failed_attempts` are updated after each
  attempt. They are not a transactional record of every formation, and `failed_attempts` includes
  parsing and extraction failures as well as provider failures. Erasure does not reset them.
- **A total is not a state.** `failed_attempts` only ever rises, so its value says nothing about
  whether anything is failing now. Compare `last_failure_at` with `last_progress_at`: a failure more
  recent than the last turn to form is a failure that has not been recovered from, and a missing
  `last_progress_at` means nothing has ever formed. The ops centre marks its tile on that comparison.
- **Bad input can't take the API down.** A turn that keeps failing parks. It never flips the API's
  readiness.
- **No numbers have been measured.** The one-second cache and the 330-second window are defaults,
  not a measured service level.

## Metrics a monitoring system can scrape

The report above answers a question you asked. Metrics answer the one you are not there to ask. The
management surface serves the exposition format Prometheus and everything compatible with it reads:

```sh
curl -s -H "Authorization: Bearer $TAISCE_OPERATOR_TOKEN" \
  http://127.0.0.1:8081/manage/v1/metrics
```

```
# HELP taisce_formation_backlog Turns stored across every project and not yet formed.
# TYPE taisce_formation_backlog gauge
taisce_formation_backlog 12
```

A scrape presents the operator credential like any other management call. It is on the management
listener, not the memory port, because these are the operator's numbers: on the memory port every
agent holding a project credential could read how far behind the instance is and how much capacity
is left. A project credential, an unknown token and a revoked operator credential all get the same
`401` a stranger gets, and the memory port has no `/metrics` to find.

In Prometheus:

```yaml
scrape_configs:
  - job_name: taisce
    metrics_path: /manage/v1/metrics
    authorization:
      credentials_file: /etc/prometheus/taisce-operator-token
    static_configs:
      - targets: ["taisce-manage:8081"]
```

### What it serves

| Metric | What it is |
|---|---|
| `taisce_formation_backlog` | Turns stored and not yet formed, across every project |
| `taisce_formation_backlog_limit` | Unfinished turns the instance accepts before refusing a write |
| `taisce_formation_parked` | Turns the driver gave up on |
| `taisce_formation_failing_projects` | How many projects hold a turn that has been tried and did not form |
| `taisce_worker_responsive` | `1` when a worker reported inside the heartbeat window, `0` otherwise |
| `taisce_formation_heartbeat_timestamp_seconds` | When a worker last reported, or `NaN` if none ever has |
| `taisce_formation_oldest_pending_timestamp_seconds` | When the oldest unformed turn was stored, or `NaN` if the backlog is empty |
| `taisce_database_connections` | Client sessions open against this instance's database |
| `taisce_cluster_connections` | Client sessions open against the whole database server |
| `taisce_connection_limit` | Sessions the server permits in total |
| `taisce_connection_reserved` | Sessions the server holds back for superuser recovery |
| `taisce_connection_headroom` | Sessions that could still be opened before the server refuses |
| `taisce_operations_allowed` | Operations the ledger recorded as allowed in the recent window |
| `taisce_operations_refused` | Operations the ledger recorded as refused in the recent window |
| `taisce_operations_window_seconds` | How far back the two counts above look |

Every value is read from the same place the health report and the ops centre read it: the
operational snapshot, and the ledger's own counts. Nothing here is measured for the metrics, so a
number on a dashboard cannot disagree with the page you were looking at a moment ago.

Allowed and refused are counted apart because the ratio is the signal. A run of refusals against
steady traffic is what an attack looks like from the outside, and a single total hides it.

`taisce_formation_failing_projects` is a count and no names. The instance's own stamps cannot answer
the question it answers, because any project forming turns moves them while another fails every
attempt — and it is the number the ops centre marks its tile on, so a rule written against it fires
on the same thing the page would have shown you.

The two timestamps are there so you can pick your own staleness window instead of inheriting ours.
`taisce_worker_responsive` has 330 seconds baked into it; `time() - taisce_formation_heartbeat_timestamp_seconds`
has whatever you decide. The same for the backlog: its depth says how much is waiting and
`time() - taisce_formation_oldest_pending_timestamp_seconds` says how long the oldest of it has been
waiting, which is usually the one you want to be woken for.

Both are `NaN` when there is nothing to report — no worker has ever reported, or the backlog is
empty. `NaN` rather than a zero, because a zero is 1970: an empty backlog would read as fifty-six
years behind and fire the rule you wrote. Every comparison against `NaN` is false, so an absent
moment raises nothing.

### What it deliberately does not expose

**No project label, and no label at all.** Every series is an instance-wide aggregate. A scrape does
not stay where it is taken — it goes to a monitoring system whose access and retention are a
different trust domain from an operator token — and a per-project label set would turn every scrape
into an enumeration of who this instance's customers are. Per-project detail stays on the management
API and the ops centre, behind this same credential, where you ask for it one project at a time.
There is no subject, no predicate, no credential identifier and nothing derived from stored
content: the ledger holds no words and neither does this.

**No tracing and no spans.** Metrics only, and no OpenTelemetry dependency in the tree. There is no
settled convention for a memory span to emit against, so anything named now would be renamed later,
and nothing you alert on needs it.

**No durations and no latencies.** Nothing here times anything, because adding timing to the read
path is a cost paid on every call and no latency target has been agreed to justify it. See
[the decision register](01-decisions.md) for what is settled.

### Limits of these numbers

- **The counts are a window, not a total.** `taisce_operations_allowed` and
  `taisce_operations_refused` cover the last `taisce_operations_window_seconds` and go down as well
  as up, which is why neither is named `_total` and both are gauges. Alert on the value; do not take
  a `rate()` of it. They are a window rather than an all-time count because an all-time count is a
  full pass over a ledger that only ever grows, and this endpoint is read by an unattended timer
  forever.
- **`taisce_worker_responsive` carries one threshold, and it is not yours.** It is `1` when a worker
  reported within 330 seconds. Use `taisce_formation_heartbeat_timestamp_seconds` to alert at a
  threshold of your own.
- **A heartbeat is a worker reporting in, not formation having happened.** It is written by the
  driver that runs the passes. A turn that formed without a pass behind it — in a test, say — moves
  the backlog and leaves the worker series exactly where they were.
- **A partial scrape is a failed scrape.** If either the snapshot or the ledger cannot be read, the
  whole scrape answers `503` and nothing is served. A series that quietly stopped being published
  reads as a condition that stopped happening; a target marked down cannot be mistaken for good
  news. The same `503` is the answer from a deployment wired without the operational reader.
- **The scrape is not on the ledger.** Every other management operation is. This one is not: it
  arrives on a timer thousands of times a day, the ledger has no delete, and recording it would grow
  the table whose rows it counts. A refused scrape *is* recorded, under the same sampling budget as
  any other refused authentication.
- **No numbers have been measured.** The five-minute window and the 330-second heartbeat are
  defaults, not a measured service level, and nothing here is a latency or throughput figure.

## How it works

The API checks the memory schema and the credential registry using the same separate database
identities it serves with, so a probe tests the real permissions. Only one check runs at a time, and
a probe that disconnects can't cancel or poison the shared result.

Worker readiness is tied to the worker's own processing loop, not to a separate timer, so a stuck
loop can't keep reporting healthy. The health report reads one instance-level row for the formation
counters and uses partial indexes for the oldest pending turn and the parked count.

The scrape reads the same instance-level row and the same partial indexes, plus one bounded count of
the ledger over its window. The failing-project count is taken from the backlog rather than from a
per-project row: a turn that formed has left the backlog and one the driver gave up on is parked, so
a waiting turn stamped with a failure is work still owed that has already gone wrong once, and the
backlog index holds exactly those rows. It writes the exposition format directly, with no client library: the
format is a name, a space and an integer, and a dependency in the write path's process buys nothing
back. The window's end is pushed five seconds past this process's clock, because a ledger row is
stamped by the database and the two clocks disagree — without the tolerance every scrape would be
missing the operations that had just happened, which are the ones a run of refusals shows up in
first.
