-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0

ALTER TABLE {schema}.audit_entry DROP CONSTRAINT audit_operation_chk;
ALTER TABLE {schema}.audit_entry ADD CONSTRAINT audit_operation_chk CHECK (operation IN (
    'observe','recall','erase','freshness','export',
    'project.create','project.suspend','project.resume',
    'credential.issue','credential.revoke','authenticate','formation.unpark'
));

-- Keyset paging must not sort all parked observations before returning a bounded page.
DROP INDEX {schema}.observation_parked_idx;
CREATE INDEX observation_parked_idx ON {schema}.observation(scope,log_offset)
    WHERE parked_at IS NOT NULL;
