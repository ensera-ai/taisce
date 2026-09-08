-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0

-- Existing credentials retain their established authority. Restriction is explicit at issuance.
ALTER TABLE {schema}.credential ADD COLUMN access text NOT NULL DEFAULT 'read_write'
    CHECK (access IN ('read_only','read_write'));
