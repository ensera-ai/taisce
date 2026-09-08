-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0
-- The CLI/provisioner accepts the same ASCII grammar and byte limit. Validate existing rows
-- atomically; do not silently truncate or rename a project that credentials already reference.
ALTER TABLE {schema}.project DROP CONSTRAINT project_scope_chk;
ALTER TABLE {schema}.project ADD CONSTRAINT project_scope_chk
    CHECK (scope ~ '^[a-z][a-z0-9_]{0,62}$');
