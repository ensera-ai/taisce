<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Production PostgreSQL

This page is a starting specification for running PostgreSQL 18 under Taisce in production. It
covers the host shape, the durability settings you must keep, a first set of tuning values, how to
size connections, and how to test a change before you roll it out.

It does not tell you how many requests per second or how large a corpus a given host will carry.
That depends on your data, your projects, your embeddings, your query mix, your storage and your
model provider. Measure it on the host you plan to use.

## What you need

Give PostgreSQL its own machine or VM when you need a promise about availability or latency. A
database container on a dedicated Linux VM is fine. A database, API, workers and a local model on
one VM share one failure domain and one pool of memory, so a spike in one starves the others. The
single-machine Compose deployment is good for evaluation and for a host you control. It is not
highly available.

A starting floor for the database host:

| Resource | Starting point | Why |
|---|---:|---|
| CPU | 8 dedicated vCPU | Room for vacuum, checkpoints and vector-index maintenance alongside queries. |
| RAM | 32 GiB | Room for PostgreSQL and the page cache to hold vector and traversal indexes. |
| Data volume | Durable SSD, 250 GiB minimum, expandable | Keeps data off the root disk and leaves room for migrations and reindexing. |
| Container shared memory | 4 GiB | Must be larger than `maintenance_work_mem` for parallel vector-index builds. |
| Network | Private, low-latency path from API and workers | A pool cannot hide network latency on every query. |
| Backup | Base backup plus continuous WAL archive, stored off the VM | Losing the VM must not mean losing the only copy. |

Size the volume with this rule:

```text
volume capacity >= max(250 GiB, 2 × peak live database + 2 × max_wal_size)
```

The second copy of the database is room for a concurrent index rebuild or a migration that rewrites
a table. Do not let WAL or backups eat into it. Alert when the data filesystem or the WAL archive
reaches 70%, so an index build or a WAL spike still has room to finish.

Keep your model off the database VM. A local model can use all the memory bandwidth on a host even
when it runs in a separate container, and no database setting makes that predictable.

## Settings you must keep

Leave these on in production:

```text
fsync=on
full_page_writes=on
synchronous_commit=on
autovacuum=on
```

Taisce acknowledges observations, audit entries, freshness state and erasure receipts. If you relax
commit durability, an acknowledged write can vanish after a host failure, and a receipt would claim
more than the storage behind it can deliver. This holds for rebuildable data too, because the same
transactions also move durable lineage and freshness state.

Your storage must honour flushes all the way through the VM and hypervisor. Run `pg_test_fsync`
against a scratch file on the exact filesystem that will hold WAL, record the `wal_sync_method` you
chose, and check the cache mode of your cloud or virtualisation platform. A fast result measures
latency. It does not prove a write survives a power cut.

## Starting values to test

These are values to test, not values to copy onto a smaller host. `work_mem` applies to each sort
or hash step, and one connection can use it several times, so it starts low until your statement
statistics show a spill worth fixing.

| Setting | 8 GiB Colima budget | 32 GiB dedicated host |
|---|---:|---:|
| `shared_buffers` | 2 GiB | 8 GiB |
| `effective_cache_size` | 6 GiB | 22 GiB |
| `work_mem` | 8 MiB | 16 MiB |
| `maintenance_work_mem` | 512 MiB | 2 GiB |
| `autovacuum_work_mem` | 128 MiB | 512 MiB |
| `max_connections` | 60 | from your connection budget, initially 80 |
| `max_wal_size` | 4 GiB | 16 GiB |
| `min_wal_size` | 1 GiB | 4 GiB |
| `checkpoint_timeout` | 15 minutes | 15 minutes |
| `checkpoint_completion_target` | 0.9 | 0.9 |
| `wal_compression` | on | on |
| `track_io_timing` | on | on |
| `track_wal_io_timing` | on | on |

PostgreSQL suggests about 25% of a dedicated host's memory as a first `shared_buffers` value. When
you raise `shared_buffers`, raise `max_wal_size` too, so checkpoints can spread the extra work. See
PostgreSQL's pages on [resource consumption](https://www.postgresql.org/docs/18/runtime-config-resource.html)
and [WAL configuration](https://www.postgresql.org/docs/18/wal-configuration.html).

PostgreSQL 18 uses worker-based asynchronous I/O by default. Start with `io_method=worker`,
`io_workers=3`, `effective_io_concurrency=16` and `maintenance_io_concurrency=16`. Try 32 or 64 only
when `pg_stat_io` shows real read stalls on storage that handles concurrent I/O. A bigger number can
make things slower on a device that is already busy.

Do not change `random_page_cost`, turn off sequential scans, or raise `work_mem` for everyone to make
one query plan look better. Fix statistics and the query or index first. Then test any cost change
against the whole workload.

## Sizing connections

Each Taisce process opens two pools: one for memory and one for the credential registry. By default
pgx sizes each pool at four or the number of CPUs the process sees, whichever is larger. Across
several replicas those defaults add up to a number nobody chose, so set pool sizes explicitly in
every production connection string.

Starting limits per process:

| Process | Memory pool | Registry pool | Minimum idle |
|---|---:|---:|---:|
| API | 8–12 | 2 | memory 2, registry 1 |
| Worker | 4 | 1 | 1 in each pool |

The API keeps one memory connection back from HTTP work, and allows at most two credential lookups
at once. So a bigger API memory pool also lets more requests run at the same time. It is not only a
cache setting.

Work out the server limit before you change replica counts:

```text
required client connections =
    API replicas × (API memory max + API registry max)
  + worker replicas × (worker memory max + worker registry max)
  + bootstrap, operator and migration connections

max_connections >= required client connections + 20–30% headroom
```

For three API replicas and four workers at 12/2 and 4/1, the application needs at most 62
connections. A server limit of 80 leaves 18 for bootstrap, operators and short-lived work. Extra
server connections cost memory, so size pools to the concurrency you have measured instead of
raising `max_connections` after you run out.

### A process that does not fit refuses to start

At startup, each process asks PostgreSQL how many connection slots exist and how many are taken. If
its own pool cannot fit, it exits with both numbers:

```text
memory pool: the deployment's connection demand exceeds the server's slots: this process's pool may
open 12 connections, and the server has 4 of 12 left (max_connections 80,
superuser_reserved_connections 3, 73 client backends already connected)
```

The check counts every client connection on the server, not just this database or this role. A
pooler, a monitoring session and an operator's `psql` all use the same slots. Nothing needs to know
how many sibling processes exist: the process that starts when the slots are gone is the one that
refuses. The check is in [`internal/migrate/connectionbound.go`](../internal/migrate/connectionbound.go).

Two processes starting at the same moment can both see room, so the server can still run out. When a
write hits that, the API answers `503 no_database_capacity` with `Retry-After: 1` instead of an
internal error. The condition is temporary, and the observation's idempotency key makes the retry
safe.

`taisce health` prints `cluster_connections`, `max_connections` and `reserved_connections` in its
JSON output, so you can read the numbers instead of working them out. The headroom is
`max_connections − reserved_connections − cluster_connections`. The portal shows that headroom
directly, and marks it as tight when it falls to ten connections or a tenth of `max_connections`,
whichever is larger.

### Pool lifetimes

Set `pool_max_conn_lifetime` below any idle or lifetime limit in your network, and add
`pool_max_conn_lifetime_jitter` so replicas do not all reconnect at once. The Colima profile uses 55
minutes plus up to five minutes of jitter. The full list of connection-string parameters is in the
[pgxpool documentation](https://pkg.go.dev/github.com/jackc/pgx/v5/pgxpool).

### Transaction poolers

Do not put a transaction pooler in front of Taisce without reviewing it first. Taisce checks the
privileges of a connection when pgx opens it, and a transaction pooler changes which PostgreSQL
backend runs later work. Prepared statements and advisory locks also have to be proven against the
pooler mode you pick.

## Vacuum, statistics and vector indexes

Retention, erasure, supersession and record changes leave dead rows in the tables that carry the
most important reads. Keep autovacuum on. Look at `n_dead_tup`, vacuum time and query latency before
you lower scale factors on individual busy tables. Aggressive vacuum settings for the whole server
make every project pay for the busiest one.

How vectors are laid out:

- `chunk` is partitioned by project, for locality. It has no vector column and no HNSW index.
- Message, entity and report vectors are partitioned by embedding generation.
- Each generation's partition owns its own HNSW index, built for that generation's dimension. The
  partitioned parent has no HNSW index.

PostgreSQL's autovacuum processes leaf partitions but does not analyze a partitioned parent. Run
`ANALYZE` on the parent after a large first load or a big change in how rows spread across
partitions. See PostgreSQL's page on [routine vacuuming](https://www.postgresql.org/docs/18/routine-vacuuming.html).

A controlled test kept this layout. With 128 projects and 13,824 vectors, a single shared table did
not use HNSW for a filtered query shaped like production, while the generation partition did.
Dropping a generation freed its partition at once, with 8 KB of WAL and no vacuum. The alternatives
needed a delete and a vacuum. At 1,000 generations the catalog took 32.8 MB and p95 planning time was
4.089 ms. Exact search stays the default, because approximate search was slower on the small report
corpus and lower candidate bounds lost results on the mixed corpus. The method and all the numbers
are in [Vector layout, 2026-09-10](27-vector-layout-qualification.md). Capacity across a whole
deployment has not been measured yet.

For a large HNSW build or rebuild:

- set `maintenance_work_mem` for that session only, not for ordinary queries;
- keep container shared memory above `maintenance_work_mem` for a parallel build;
- use concurrent index operations when writes must keep going;
- leave room for both the old and new index;
- compare approximate results with exact search, not just latency.

pgvector's [performance guidance](https://github.com/pgvector/pgvector#performance) covers build
memory, shared memory, concurrent builds and recall.

## Testing a change on Colima

The Colima profile in [`compose.perf.yaml`](../compose.perf.yaml) is a repeatable test environment.
Use it to compare settings, catch query-plan regressions and exercise pool limits. It cannot tell
you about cloud-volume latency, power-loss durability, availability or capacity.

It runs as a separate Compose project, `taisce-perf`, with its own volume
(`taisce-perf_postgres-data`). PostgreSQL listens on `55433` and the API on `18080`, both on
loopback only. The Make targets take its local-only password from `PERF_POSTGRES_PASSWORD`; plain
Compose takes `TAISCE_PERF_POSTGRES_PASSWORD`. Use a value that is safe in a URI, because the Make
targets put it in a connection string. If you change the password after the volume exists, recreate
the volume or change the role's password to match.

```sh
make perf-config
make perf-up
make perf-reset-stats
make perf-workload
make perf-stats > /tmp/taisce-postgres-profile.txt
make perf-down
```

`perf-workload` runs the database-backed Go test packages without coverage. It is a repeatable
regression workload, not a simulation of production traffic. For a capacity claim, swap in your own
HTTP workload and corpus and keep the reset and snapshot steps.

Run each candidate at least three times:

1. Recreate the volume if you need a cold-start comparison.
2. Start the stack and reset statistics.
3. Run one workload, with nothing else running on Colima.
4. Save the `perf-stats` output and the container's CPU, memory and disk figures.
5. Repeat warm, without recreating the volume.
6. Change one group of settings and repeat in the same order.

`make perf-down` keeps the volume. To delete the test database on purpose:

```sh
docker compose --project-name taisce-perf -f compose.yaml -f compose.perf.yaml down --volumes
```

You cannot undo that unless you backed the volume up.

## Before a setting goes to production

Promote a candidate only when all of these hold:

- every database-backed test passes with the candidate settings;
- append, recall, citation, export and erasure improve, or stay within the latency you agreed;
- query plans still use the indexes the plan tests expect;
- requested checkpoints do not keep happening under steady load (if they do, raise `max_wal_size`
  or find where the WAL comes from before touching checkpoint timing again);
- you can explain the read, temp-file and WAL cost of the slowest statements;
- connection use stays under your budget, with room left for operators;
- autovacuum keeps up with retention and erasure, and partition-parent statistics are current;
- the disk still has room for reindexing, WAL and recovery;
- a backup restore and a crash recovery have worked on the target platform.

Taisce's PostgreSQL image already installs and preloads `pg_stat_statements`. The profile turns on
the timing switches its block-time columns need, plus PostgreSQL 18's WAL timings in `pg_stat_io`.
The snapshot script is [`scripts/postgres-profile.sql`](../scripts/postgres-profile.sql). It leaves
out SQL text, because utility statements can keep literal values. Treat the report as operator data
and never serve it through the public API.

## Rolling back

Keep the previous configuration as a whole file, not a list of values you remember. If a candidate
makes latency, memory pressure, checkpoint frequency, vacuum lag or connection refusals worse,
restore the previous file and restart PostgreSQL. Roll back settings that need a restart in a planned
maintenance window, or by failing over to an instance you have already tested.

Never roll back by turning off durability. If storage latency is the problem, move or resize the
storage and test again. Do not change what an acknowledged write means.
