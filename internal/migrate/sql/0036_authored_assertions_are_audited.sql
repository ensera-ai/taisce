-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0

-- Independent authored assertions use curated source input and namespaced observation retry
-- receipts. They have their own audit operation so creation cannot be mistaken for a correction.
ALTER TABLE {schema}.audit_entry DROP CONSTRAINT audit_operation_chk;
ALTER TABLE {schema}.audit_entry ADD CONSTRAINT audit_operation_chk CHECK (operation IN (
    'observe','recall','erase','freshness','export','citation.resolve','record.list','record.history','record.retract','record.correct','record.assert',
    'project.create','project.suspend','project.resume','credential.issue','credential.revoke','authenticate','formation.unpark','formation.recover'
));
