-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0

-- One aggregate row for the instance, never a row or metric label per subject/project/process.
CREATE TABLE {schema}.formation_health (
    singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
    heartbeat_at timestamptz,
    last_progress_at timestamptz,
    last_failure_at timestamptz,
    formed_count bigint NOT NULL DEFAULT 0 CHECK(formed_count>=0),
    failed_attempts bigint NOT NULL DEFAULT 0 CHECK(failed_attempts>=0)
);
INSERT INTO {schema}.formation_health(singleton) VALUES(true);

CREATE INDEX observation_pending_age_idx ON {schema}.observation(ingested_at)
    WHERE kind='turn' AND formed_at IS NULL;
