-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0

ALTER TABLE {schema}.audit_entry DROP CONSTRAINT audit_operation_chk;
ALTER TABLE {schema}.audit_entry ADD CONSTRAINT audit_operation_chk CHECK (operation IN (
    'observe','recall','erase','freshness','export','citation.resolve',
    'project.create','project.suspend','project.resume',
    'credential.issue','credential.revoke','authenticate','formation.unpark'
));
