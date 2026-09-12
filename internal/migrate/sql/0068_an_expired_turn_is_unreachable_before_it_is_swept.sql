-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0

-- Expiry meant "gone once a sweep reaches it". An operator who sets a retention expects it to mean
-- unreachable, and artifacts already behaved that way: every artifact read carries
-- `retention_until > now()`. Conversation memory carried no such filter, so an expired turn's facts
-- answered recalls until the hourly pass got to that project, 500 observations at a time.
--
-- The deadline is put on the fact itself rather than reached through its evidence on every read. A
-- fact is on the critical path of every recall; an EXISTS from each fact to a supporting
-- observation is a join per row per hop, where a column test is free. The fact already had the
-- column — recovery carried it — and nothing ever wrote it.
--
-- A fact's identity includes the turn it came from, so it has one supporting observation and takes
-- that turn's deadline. Written as the latest over its evidence, and null when one of them is kept
-- indefinitely, so evidence restored or shared later keeps the longest-lived support.
UPDATE {schema}.fact f
   SET retention_until = s.deadline
  FROM (
    SELECT e.scope, e.fact_id,
           CASE WHEN bool_or(o.retention_until IS NULL) THEN NULL ELSE max(o.retention_until) END AS deadline
      FROM {schema}.fact_evidence e
      JOIN {schema}.observation o ON o.observation_id = e.source_observation_id AND o.scope = e.scope
     GROUP BY e.scope, e.fact_id
  ) s
 WHERE f.scope = s.scope AND f.fact_id = s.fact_id
   AND f.retention_until IS DISTINCT FROM s.deadline;

-- The reads filter on it, so the rows with a deadline are the ones worth indexing: in a project
-- that keeps everything indefinitely the index is empty and costs nothing.
CREATE INDEX fact_retention_idx ON {schema}.fact (scope, retention_until)
    WHERE retention_until IS NOT NULL;

COMMENT ON COLUMN {schema}.fact.retention_until IS
    'When this fact stops answering, taken from the latest deadline of the observations supporting '
    'it, or null when one of them is kept indefinitely. The sweep deletes it afterwards; this is '
    'what makes it unreachable in between.';
