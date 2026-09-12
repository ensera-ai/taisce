-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0

-- The scheduler probes for one due source per project. A sweep then reads a bounded page in
-- deadline order. Neither depends on the project's current policy or scans all retained sources.
CREATE INDEX observation_scope_expiry_idx
ON {schema}.observation(scope,retention_until,log_offset)
WHERE retention_until IS NOT NULL;
