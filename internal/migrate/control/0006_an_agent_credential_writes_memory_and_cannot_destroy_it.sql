-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0
--
-- A third memory authority, between reading and everything (D26). An agent credential does what an
-- agent process needs — saves turns, recalls, corrects, stores artifacts — and cannot erase a person
-- or purge an entity, the two operations that destroy data with no way back. The key most likely to
-- leak is the one inside a process that reads text other people wrote, and that is the key that
-- should not be able to empty a project.
--
-- Existing rows keep the authority they were issued with; nothing here narrows a key somebody is
-- already using. The column default moves to the narrowest authority that still writes, so a row
-- inserted without naming one gets the one that cannot destroy anything.

ALTER TABLE {schema}.credential DROP CONSTRAINT IF EXISTS credential_access_check;
ALTER TABLE {schema}.credential
    ADD CONSTRAINT credential_access_check CHECK (access IN ('read_only', 'agent', 'read_write'));
ALTER TABLE {schema}.credential ALTER COLUMN access SET DEFAULT 'agent';
