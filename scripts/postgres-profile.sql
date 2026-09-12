-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0

-- One bounded, content-free PostgreSQL performance snapshot. Run as the operator identity after a
-- measured workload. Query text is deliberately omitted: parameterized statements normally contain
-- placeholders, but utility statements can retain credentials or other literal values.

\pset pager off
\pset null '(null)'

\echo '== configuration =='
SELECT name, current_setting(name) AS value, source
FROM pg_settings
WHERE name = ANY (ARRAY[
    'autovacuum', 'autovacuum_work_mem', 'checkpoint_completion_target', 'checkpoint_timeout',
    'effective_cache_size', 'effective_io_concurrency', 'io_method', 'io_workers',
    'maintenance_io_concurrency', 'maintenance_work_mem', 'max_connections', 'max_wal_size',
    'min_wal_size', 'shared_buffers', 'synchronous_commit', 'track_io_timing',
    'track_wal_io_timing', 'wal_compression', 'work_mem'
])
ORDER BY name;

\echo '== database =='
SELECT datname,
       pg_size_pretty(pg_database_size(datname)) AS size,
       xact_commit, xact_rollback, deadlocks,
       blks_read, blks_hit,
       round(100.0 * blks_hit / nullif(blks_hit + blks_read, 0), 2) AS cache_hit_percent,
       temp_files, pg_size_pretty(temp_bytes) AS temp_bytes,
       stats_reset
FROM pg_stat_database
WHERE datname = current_database();

\echo '== connections =='
SELECT coalesce(state, backend_type) AS state, count(*) AS connections
FROM pg_stat_activity
WHERE datname = current_database() OR backend_type <> 'client backend'
GROUP BY coalesce(state, backend_type)
ORDER BY connections DESC, state;

\echo '== checkpoints =='
SELECT num_timed, num_requested, num_done,
       round(write_time::numeric, 1) AS write_ms,
       round(sync_time::numeric, 1) AS sync_ms,
       pg_size_pretty(buffers_written * current_setting('block_size')::bigint) AS bytes_written,
       stats_reset
FROM pg_stat_checkpointer;

\echo '== io =='
SELECT backend_type, object, context,
       pg_size_pretty(coalesce(read_bytes, 0)) AS read_bytes,
       round(coalesce(read_time, 0)::numeric, 1) AS read_ms,
       pg_size_pretty(coalesce(write_bytes, 0)) AS write_bytes,
       round(coalesce(write_time, 0)::numeric, 1) AS write_ms,
       fsyncs,
       round(coalesce(fsync_time, 0)::numeric, 1) AS fsync_ms,
       evictions
FROM pg_stat_io
WHERE coalesce(read_bytes, 0) + coalesce(write_bytes, 0) + coalesce(fsyncs, 0) > 0
ORDER BY coalesce(read_time, 0) + coalesce(write_time, 0) + coalesce(fsync_time, 0) DESC,
         backend_type, object, context;

\echo '== largest relations =='
SELECT schemaname, relname,
       pg_size_pretty(pg_total_relation_size(relid)) AS total_size,
       pg_size_pretty(pg_relation_size(relid)) AS table_size,
       pg_size_pretty(pg_indexes_size(relid)) AS index_size,
       n_live_tup, n_dead_tup,
       last_autoanalyze, last_autovacuum
FROM pg_stat_user_tables
ORDER BY pg_total_relation_size(relid) DESC
LIMIT 20;

\echo '== top statements by total execution time =='
SELECT queryid,
       calls,
       round(total_exec_time::numeric, 1) AS total_exec_ms,
       round(mean_exec_time::numeric, 2) AS mean_exec_ms,
       rows,
       shared_blks_read,
       shared_blks_hit,
       temp_blks_written,
       pg_size_pretty(wal_bytes::bigint) AS wal_bytes
FROM pg_stat_statements
WHERE dbid = (SELECT oid FROM pg_database WHERE datname = current_database())
ORDER BY total_exec_time DESC
LIMIT 20;

\echo '== active index builds =='
SELECT pid, command, phase, lockers_total, lockers_done, blocks_total, blocks_done,
       tuples_total, tuples_done
FROM pg_stat_progress_create_index
ORDER BY pid;
