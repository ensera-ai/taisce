-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0

-- A worker that finds a scope held by another replica stands down. That is the mechanism the
-- several-replica topology rests on: each scope is drained under an advisory lock, so a second
-- worker never forms the same turn twice. The count of scopes that were busy was kept in memory for
-- the length of one pass and then discarded, and the one log line carrying it was behind a gate the
-- count was not in — so a pass whose every scope was held formed nothing, errored on nothing, and
-- said nothing at all. The replica doing exactly what the design intends was the silent one.
--
-- On a single-scope instance, which is what the default project makes and what most deployments
-- will be, every replica but one is busy on every pass. So on the most common topology every replica
-- but one was silent by construction, and four workers against one backlog looked identical to one
-- worker and three that were wedged, crash-looping on a schedule that hid it, or pointed at the
-- wrong database. Confirming the property the deployment is sold on meant sampling pg_locks on the
-- primary and reading pg_stat_activity to find which pod held the lock — not an answer an operator
-- should have to reach for, and not available at all to anyone without a psql session.
--
-- So it goes on the row formation already writes, beside what it formed and what failed. A count and
-- a time, on the aggregate row: no scope name, no pod, nothing about anybody's data. The instance
-- page is where somebody asks whether their replicas are working, and this is the answer.
ALTER TABLE {schema}.formation_health
    ADD COLUMN stood_down_count   bigint NOT NULL DEFAULT 0 CHECK (stood_down_count >= 0),
    -- When a pass last stood down on at least one scope. The count alone is a lifetime total, and
    -- the question an operator asks of it is whether the lock is working now.
    ADD COLUMN last_stood_down_at timestamptz;

COMMENT ON COLUMN {schema}.formation_health.stood_down_count IS
    'How many times a pass found a scope held by another replica and stood down. The evidence that '
    'the per-scope lock is serialising several workers rather than that only one of them is alive.';
