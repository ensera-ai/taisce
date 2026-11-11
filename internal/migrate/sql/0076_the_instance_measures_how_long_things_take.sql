-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0
--
-- The instance measures how long things take, and keeps the totals here.
--
-- ── WHAT WAS MISSING ─────────────────────────────────────────────────────────────────────────
--
-- Nothing recorded a duration: not a request's, not a turn's formation, not how long a turn waited
-- between being stored and being formed. An operator whose agents felt slow had nothing to look at,
-- a latency target could not have been checked had one been agreed, and a process refusing a third
-- of its requests for want of capacity looked, on the scrape, exactly like an idle one.
--
-- ── WHY THE NUMBERS LIVE IN THE DATABASE ─────────────────────────────────────────────────────
--
-- A request is timed in the process that serves it, and a turn in the worker that forms it. The
-- scrape is answered by a third process, the management surface, which is the only door an
-- operator's monitoring is given. So the processes that measure write their totals where the one
-- that reports can read them. The alternative was a scrape endpoint on every process, which means a
-- port on the memory listener answering questions about the deployment's capacity to whoever can
-- reach the port agents use.
--
-- ── WHY TOTALS AND NOT ROWS ──────────────────────────────────────────────────────────────────
--
-- One row per thing measured, holding counts per bucket that only ever rise. A row per request
-- would be a second ledger, growing with traffic, for a question — how are requests distributed —
-- that needs a dozen integers to answer. Each process adds what it has counted every few seconds,
-- in one statement per row, so this is not a write on any request's path.
--
-- Rising totals are what a monitoring system differentiates to get a rate, and they survive a
-- restart because they are here and not in a process. What a crash loses is whatever a process had
-- counted since it last wrote: a few seconds of observations, stated here so nobody reads these as
-- an exact count of requests. The ledger is that.
--
-- ── WHAT A ROW MAY CARRY ─────────────────────────────────────────────────────────────────────
--
-- A kind from a closed set, and a name that is an operation from the surface's own table or a
-- counter this file names. Never a project, a subject, a credential, or anything from memory:
-- these rows become labels on a scrape, and a scrape goes to a system whose access and retention
-- this deployment did not choose. The name's shape is constrained here so that whatever a process
-- writes, nothing that could be read as label syntax can be stored.
--
-- The memory role writes these, which means a compromised memory process can falsify them. They
-- are its own account of its own speed, and nothing is decided from them.
CREATE TABLE {schema}.measurement (
    kind    text     NOT NULL,
    name    text     NOT NULL DEFAULT '',
    -- Observations per bucket, not cumulative: the last element counts what exceeded every bound.
    -- Empty for a counter.
    buckets bigint[] NOT NULL DEFAULT '{}',
    sum_ms  bigint   NOT NULL DEFAULT 0,
    count   bigint   NOT NULL DEFAULT 0,
    PRIMARY KEY (kind, name),
    CONSTRAINT measurement_kind_chk CHECK (kind IN
        ('operation_duration', 'formation_turn_duration', 'formation_lag', 'counter')),
    CONSTRAINT measurement_name_chk CHECK (name ~ '^[a-z][a-z0-9_.]{0,63}$' OR name = ''),
    CONSTRAINT measurement_counts_chk CHECK (sum_ms >= 0 AND count >= 0 AND 0 <= ALL (buckets)),
    CONSTRAINT measurement_counter_chk CHECK (kind <> 'counter' OR (buckets = '{}' AND name <> ''))
);

COMMENT ON TABLE {schema}.measurement IS
    'Rising totals of how long things took, added to by the processes that measured them and read '
    'by the management surface for the scrape. Instance-wide: no row names a project, a subject or '
    'a credential. Not an exact count of anything; the ledger is that.';
