<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Production PostgreSQL specification

This is the starting specification for PostgreSQL 18 serving Taisce. It defines a deployment shape,
resource and connection budgets, settings worth qualifying, and the measurements required before a
setting becomes a production claim. It does not claim a request rate or corpus ceiling: those depend
on the retained corpus, project distribution, embedding count, query mix, storage and inference
provider, and must be measured on the target host.

The Colima profile in [`compose.perf.yaml`](../compose.perf.yaml) is the reproducible qualification
environment. It is useful for comparing settings, catching plan regressions and exercising pool
limits. It does not prove cloud-volume latency, power-loss durability, availability or capacity.
Its host database port is bound to loopback. When using the Make targets, its separate local-only
password can be overridden with `PERF_POSTGRES_PASSWORD`; direct Compose use accepts
`TAISCE_PERF_POSTGRES_PASSWORD`. Changing that password after the volume is initialized requires
recreating the isolated volume or changing the database role password to match. Use a URI-safe value
because the Make target places it in a connection URI.

## The production shape

Run PostgreSQL in its own resource and failure domain when Taisce needs an availability or latency
commitment. A database container on a dedicated Linux VM is a valid shape; a database, API, workers
and local inference model on one VM are one failure domain and one memory-pressure domain. The
single-machine Compose deployment remains suitable for evaluation and controlled-host operation,
not a highly available claim.

The initial database-host qualification floor is:

| Resource | Starting specification | Reason |
|---|---:|---|
| CPU | 8 dedicated vCPU | Leaves useful parallelism for vacuum, checkpoints and HNSW maintenance. |
| RAM | 32 GiB | Gives PostgreSQL and the guest page cache room for vector and traversal indexes. |
| Data volume | Durable SSD, 250 GiB minimum, expandable | Avoids root-disk/image/log contention and leaves migration/reindex headroom. |
| Container shared memory | 4 GiB | Must exceed the qualified HNSW `maintenance_work_mem`. |
| Network | Private low-latency path from API and workers | Pooling cannot remove per-query network latency. |
| Backup | Physical/base backup plus continuous WAL archive outside the VM | A VM or volume loss must not be the loss of the only recovery copy. |

The volume must also satisfy this capacity rule:

```text
volume capacity >= max(250 GiB, 2 × peak live database + 2 × max_wal_size)
```

The second database copy permits a concurrent index rebuild or migration rewrite. WAL and backups
are not allowed to consume that headroom silently. Alert before either the data filesystem or WAL
archive reaches 70% so an index build, checkpoint or retained WAL spike still has room to finish.

Keep inference outside the database VM. A local model can consume all available memory bandwidth
and unified memory while still appearing to be a separate container; database settings cannot make
that contention predictable.

## Durability contract

Production keeps these values:

```text
fsync=on
full_page_writes=on
synchronous_commit=on
autovacuum=on
```

Taisce acknowledges observations, audit entries, freshness state and erasure receipts. Globally
relaxing commit durability would let an acknowledged write disappear after a host failure and would
make those receipts stronger than the storage behind them. Rebuildable projections do not justify a
global exception because their transactions also advance durable lineage and freshness state.

Storage must honor flushes through the VM and hypervisor. Run `pg_test_fsync` against a disposable
file on the exact future WAL filesystem, record the selected `wal_sync_method`, and verify the cloud
or virtualization platform's cache mode. A fast result measures latency; it is not proof that a
write survives a power failure.

## PostgreSQL starting values

These are candidates to qualify, not values to copy onto a smaller host. PostgreSQL's `work_mem` is
available per sort/hash operation and potentially several times per connection; it is deliberately
conservative until statement statistics show a valuable spill.

| Setting | 8 GiB Colima database budget | 32 GiB dedicated host |
|---|---:|---:|
| `shared_buffers` | 2 GiB | 8 GiB |
| `effective_cache_size` | 6 GiB | 22 GiB |
| `work_mem` | 8 MiB | 16 MiB |
| `maintenance_work_mem` | 512 MiB | 2 GiB |
| `autovacuum_work_mem` | 128 MiB | 512 MiB |
| `max_connections` | 60 | derived budget, initially 80 |
| `max_wal_size` | 4 GiB | 16 GiB |
| `min_wal_size` | 1 GiB | 4 GiB |
| `checkpoint_timeout` | 15 minutes | 15 minutes |
| `checkpoint_completion_target` | 0.9 | 0.9 |
| `wal_compression` | on | on |
| `track_io_timing` | on | on |
| `track_wal_io_timing` | on | on |

PostgreSQL recommends roughly 25% of a dedicated host's memory as an initial `shared_buffers` value
and notes that it also depends on the operating-system cache. Increasing `shared_buffers` normally
requires increasing `max_wal_size` so checkpoints can spread the additional dirty-page work. See
[resource consumption](https://www.postgresql.org/docs/18/runtime-config-resource.html) and
[WAL configuration](https://www.postgresql.org/docs/18/wal-configuration.html).

PostgreSQL 18 uses worker-based asynchronous I/O by default. Keep `io_method=worker`, `io_workers=3`,
`effective_io_concurrency=16` and `maintenance_io_concurrency=16` for the first run. Test 32 and 64
only when `pg_stat_io` shows material read stalls on storage that supports concurrent I/O. A larger
number can increase latency when the device is already saturated.

Do not change `random_page_cost`, disable sequential scans, or raise `work_mem` globally to make one
plan look better. Fix statistics and query/index shape first; then qualify a cost change against the
whole workload.

## Connection budget

Each Taisce process opens a memory pool and a registry pool. pgx defaults each pool's maximum to the
greater of four or the process-visible CPU count, so allowing defaults to multiply across replicas
does not create a stable database budget. Production DSNs must set the pool sizes explicitly.

Starting per-process limits are:

| Process | Memory pool | Registry pool | Minimum idle |
|---|---:|---:|---:|
| API | 8–12 | 2 | memory 2, registry 1 |
| Worker | 4 | 1 | 1 in each pool |

Taisce reserves one API memory connection outside admitted HTTP work and admits at most two
concurrent registry lookups. Increasing the API memory pool therefore also increases application
concurrency; it is not merely a cache setting.

Calculate the server limit before changing replica counts:

```text
required client connections =
    API replicas × (API memory max + API registry max)
  + worker replicas × (worker memory max + worker registry max)
  + bootstrap/operator/migration connections

max_connections >= required client connections + 20–30% operational headroom
```

For three API replicas and four workers at 12/2 and 4/1, the application maximum is 62. A server
limit of 80 leaves 18 connections for bootstrap, operator access and transient work. More server
connections are not free: size pools and admission to the measured concurrency target rather than
raising `max_connections` after exhaustion.

Set `pool_max_conn_lifetime` below infrastructure idle/lifetime limits and add
`pool_max_conn_lifetime_jitter` so replicas do not reconnect together. The Colima profile uses 55
minutes plus five minutes of jitter. The supported DSN parameters are documented by
[pgxpool](https://pkg.go.dev/github.com/jackc/pgx/v5/pgxpool).

Do not insert a transaction pooler without a security and correctness review. Runtime privilege
validation currently occurs when pgx opens a physical connection, and transaction pooling changes
which PostgreSQL backend executes later work. Prepared statements and advisory-lock behavior must
also be proved against the selected pooler mode.

## Vacuum, statistics and vector indexes

Observation retention, erasure, supersession and record mutation create dead tuples in the tables
that carry the most important reads. Keep autovacuum enabled and inspect `n_dead_tup`, vacuum time and
query latency before applying lower scale factors to individual high-churn tables. Global aggressive
vacuum settings make every tenant pay for the busiest one.

`chunk` is partitioned by project for source locality and has no vector column or HNSW index.
Message, entity and report vectors are partitioned by immutable embedding generation. Each generation
child owns a directly-created, dimension-specific HNSW index; there is no HNSW index attached to the
partitioned parent. PostgreSQL autovacuum processes leaf partitions but does not automatically
analyze a partitioned parent. Analyze the relevant parent after a large initial load or a substantial
change in partition distribution. See
[routine vacuuming](https://www.postgresql.org/docs/18/routine-vacuuming.html).

The controlled `vector-layout-v1` qualification retained this design. With 128 projects and 13,824
vectors, the ordinary shared layout did not select HNSW for the production-shaped filtered query,
while the generation child did. Removing a generation reclaimed its child immediately with 8 KB of
WAL and no vacuum; filtered alternatives required delete/vacuum work. At 1,000 generations, catalog
storage was 32.8 MB and p95 planning was 4.089 ms. Exact search remains the default because ANN was
slower on the small report corpus and lower candidate bounds lost recall in the mixed corpus. See
[`docs/27-vector-layout-qualification.md`](27-vector-layout-qualification.md) for the complete method,
measurements and limits. Deployment-wide capacity remains #103.

For a large HNSW build or rebuild:

- set `maintenance_work_mem` for that maintenance session rather than raising ordinary query memory;
- keep container shared memory larger than `maintenance_work_mem` for a parallel build;
- use concurrent index operations when writes must continue;
- retain room for both old and new index copies;
- compare approximate recall with an exact search, not latency alone.

The pgvector project documents the build-memory, shared-memory, concurrent-index and recall tradeoffs
in its [performance guidance](https://github.com/pgvector/pgvector#performance).

## Colima qualification workflow

The override is isolated by Compose project name. It therefore uses `taisce-perf_postgres-data`
rather than the normal development volume and publishes PostgreSQL on `55433` and the API on
`18080` by default.

```sh
make perf-config
make perf-up
make perf-reset-stats
make perf-workload
make perf-stats > /tmp/taisce-postgres-profile.txt
make perf-down
```

`perf-workload` runs the database-backed Go packages without coverage instrumentation. It is a
repeatable database regression workload, not a production request-rate simulation. For a capacity
claim, replace it with the agreed HTTP workload and corpus while keeping the reset/snapshot steps.

Run at least three times for each candidate:

1. Recreate the performance volume for the cold-load comparison when the experiment requires one.
2. Start the stack and reset cumulative statistics.
3. Run exactly one workload with no unrelated Colima jobs.
4. Save `perf-stats` output and container CPU, memory and disk telemetry.
5. Repeat a warm run without recreating the volume.
6. Change one class of settings and repeat in the same order.

To delete the isolated performance database deliberately:

```sh
docker compose --project-name taisce-perf -f compose.yaml -f compose.perf.yaml down --volumes
```

That command cannot be undone unless the volume was backed up. Ordinary `make perf-down` preserves
the volume.

## Promotion gates

A candidate moves to production only when all of these are true:

- all database-backed tests pass under the candidate settings;
- target append, recall, citation, export and erasure workloads improve or remain within their
  agreed latency bounds;
- query plans still use the bounded indexes asserted by the plan tests;
- requested checkpoints do not recur during steady load; otherwise increase `max_wal_size` or find
  the WAL source before changing checkpoint timing again;
- the highest-time statements have explained read, temporary-file and WAL costs;
- connection use stays below the calculated budget, with operator headroom available;
- autovacuum keeps up with retention and erasure churn and partition-parent statistics are current;
- disk retains reindex, WAL and recovery headroom;
- a backup restore and the expected crash-recovery path have succeeded on the target platform.

`pg_stat_statements` is already installed and preloaded by Taisce's PostgreSQL image. The profile
enables the timing switches needed for its block-time columns and PostgreSQL 18's `pg_stat_io` WAL
timings. The bounded snapshot is [`scripts/postgres-profile.sql`](../scripts/postgres-profile.sql).
It omits SQL text because PostgreSQL utility statements can retain literal values. Store the report
as operator data and do not return it through the public API.

## Rollback

Keep the last configuration as a complete file, not a list of remembered values. If a candidate
increases latency, memory pressure, checkpoint frequency, vacuum lag or connection refusals, restore
the previous file and restart PostgreSQL. Settings that require restart must be rolled back during a
declared maintenance window or by failing over to a previously qualified instance.

Never roll back by disabling durability. If storage latency is the regression, move or resize the
storage and re-run qualification; do not change what an acknowledged Taisce write means.
